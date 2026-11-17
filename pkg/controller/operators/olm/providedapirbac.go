package olm

import (
	"encoding/json"
	"fmt"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	// RoleAggregationPropertyType is the property type that opts a bundle out
	// of OLM's automatic aggregation of its provided APIs into the default
	// user-facing admin/edit/view ClusterRoles.
	//
	// The signal is carried as a CSV property rather than a CSV spec field
	// because properties survive installation on OLM versions that do not
	// understand them: the resolver projects the full property list onto the
	// on-cluster CSV as the operatorframework.io/properties annotation, and
	// the raw olm.properties CSV annotation round-trips verbatim. CSV
	// metadata annotations are exempt from structural schema pruning (unlike
	// unknown spec fields), so the intent persists across OLM upgrades and is
	// acted on by the periodic CSV resync without requiring a reinstall.
	RoleAggregationPropertyType = "olm.rbac.role-aggregation"

	// olmPropertiesAnnotationKey is the CSV annotation that carries
	// bundle-level properties for bundles that were not resolved from a
	// property-aware catalog. Unlike the resolver projection above, its value
	// is a bare JSON list of properties.
	olmPropertiesAnnotationKey = "olm.properties"
)

// roleAggregationPropertyValue is the value schema of the
// olm.rbac.role-aggregation property.
type roleAggregationPropertyValue struct {
	Disabled bool `json:"disabled"`
}

// reconcileProvidedAPIRBAC reconciles the ClusterRoles that aggregate access
// to the APIs provided by csv into the default user-facing admin/edit/view
// ClusterRoles.
//
// The decision is made per provided API and folded over every active CSV
// providing it: if any provider carries the role-aggregation opt-out, the
// shared roles are deleted rather than ensured. The roles OLM generates are
// GVK-scoped (their names, aggregation labels and CRD ownerReference are all
// derived from the API alone, not from the CSV), and the permission they
// grant is the union over all providers, so a per-CSV decision would both
// leak the permission (an unflagged provider re-creates what a flagged
// provider deletes) and flap the shared objects on every resync. Because the
// fold is a pure function of the CSV informer cache, every sync computes the
// same decision for the same cluster state: independent (jittered) resync
// timing cannot oscillate, and the decision only changes when the set of
// active providers or their signals change.
//
// Owned APIServices keep the legacy per-CSV ensure behavior; extending the
// fold to them is a mechanical follow-up.
func (a *Operator) reconcileProvidedAPIRBAC(csv *v1alpha1.ClusterServiceVersion) error {
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		crd, err := a.lister.APIExtensionsV1().CustomResourceDefinitionLister().Get(owned.Name)
		if err != nil {
			return fmt.Errorf("crd %q not found: %s", owned.Name, err.Error())
		}
		crd.SetGroupVersionKind(apiextensionsv1.SchemeGroupVersion.WithKind("customresourcedefinition"))
		vetoed, err := a.roleAggregationVetoed(owned.Name)
		if err != nil {
			return err
		}
		if vetoed {
			if err := a.deleteProvidedAPIClusterRoles(crd, owned); err != nil {
				return err
			}
			continue
		}
		if err := a.ensureCRDProvidedAPIClusterRoles(crd, owned); err != nil {
			return err
		}
	}
	for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
		svcName := strings.Join([]string{owned.Version, owned.Group}, ".")
		svc, err := a.lister.APIRegistrationV1().APIServiceLister().Get(svcName)
		if err != nil {
			return fmt.Errorf("apiservice %q not found: %s", svcName, err.Error())
		}
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)
		key := opregistry.APIKey{Group: owned.Group, Version: owned.Version, Kind: owned.Kind}
		for suffix, verbs := range VerbsForSuffix {
			if err := a.ensureProvidedAPIClusterRole(namePrefix, suffix, verbs, owned.Group, owned.Name, nil, svc, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureCRDProvidedAPIClusterRoles ensures the aggregated ClusterRoles for a
// single owned CRD (extracted from the legacy per-CSV ensure loop). crd is
// partial metadata for the CRD, fetched by the caller.
func (a *Operator) ensureCRDProvidedAPIClusterRoles(crd *metav1.PartialObjectMetadata, owned v1alpha1.CRDDescription) error {
	nameGroupPair := strings.SplitN(owned.Name, ".", 2) // -> etcdclusters etcd.database.coreos.com
	if len(nameGroupPair) != 2 {
		return fmt.Errorf("invalid parsing of name '%v', got %v", owned.Name, nameGroupPair)
	}
	plural := nameGroupPair[0]
	group := nameGroupPair[1]
	namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)
	key := opregistry.APIKey{Group: group, Version: owned.Version, Kind: owned.Kind, Plural: plural}
	for suffix, verbs := range VerbsForSuffix {
		if err := a.ensureProvidedAPIClusterRole(namePrefix, suffix, verbs, group, plural, nil, crd, key); err != nil {
			return err
		}
	}
	return a.ensureProvidedAPIClusterRole(namePrefix+"crd", ViewSuffix, []string{"get"}, "apiextensions.k8s.io", "customresourcedefinitions", []string{owned.Name}, crd, key)
}

// roleAggregationVetoed computes the per-API decision: aggregation for a
// provided API is vetoed when ANY active CSV providing the CRD carries the
// role-aggregation opt-out. Most-restrictive-wins is the only coherent
// policy because the aggregated permission is the union over all providers:
// an opt-out that can be outvoted by another provider of the same API is an
// opt-out that does not work.
//
// The fold lists CSVs through the union lister and therefore requires
// cluster-wide CSV visibility. The default olm-operator configuration
// watches all namespaces; namespace-scoped deployments see only the watched
// subset and must not rely on this behavior.
func (a *Operator) roleAggregationVetoed(crdName string) (bool, error) {
	all, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().List(labels.Everything())
	if err != nil {
		return false, fmt.Errorf("listing CSVs to evaluate role aggregation for crd %q: %s", crdName, err.Error())
	}
	for _, csv := range all {
		if !csvIsActiveProviderOf(csv, crdName) {
			continue
		}
		if csvDisablesRoleAggregation(csv) {
			return true, nil
		}
	}
	return false, nil
}

// csvIsActiveProviderOf reports whether csv is an active provider of the
// given CRD: Succeeded (the phase in which OLM reconciles provided-API
// RBAC), not a copied CSV (copies are status mirrors that are never
// reconciled), not deleting, and listing the CRD as owned.
func csvIsActiveProviderOf(csv *v1alpha1.ClusterServiceVersion, crdName string) bool {
	if csv == nil || csv.DeletionTimestamp != nil || csv.IsCopied() {
		return false
	}
	if csv.Status.Phase != v1alpha1.CSVPhaseSucceeded {
		return false
	}
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		if owned.Name == crdName {
			return true
		}
	}
	return false
}

// csvDisablesRoleAggregation reports whether the CSV carries the
// role-aggregation opt-out in either properties annotation. It is
// fail-closed with respect to the signal itself: a well-formed
// olm.rbac.role-aggregation property with disabled=true vetoes, regardless
// of which annotation carries it; malformed annotations are ignored (they
// are advisory input, not authority).
func csvDisablesRoleAggregation(csv *v1alpha1.ClusterServiceVersion) bool {
	annotations := csv.GetAnnotations()
	if len(annotations) == 0 {
		return false
	}

	// Resolver projection: {"properties":[{"type":...,"value":{...}}]}
	if raw, ok := annotations[projection.PropertiesAnnotationKey]; ok && raw != "" {
		if props, err := projection.PropertyListFromPropertiesAnnotation(raw); err == nil {
			for _, prop := range props {
				if prop.Type == RoleAggregationPropertyType && roleAggregationPropertyDisables(prop.Value) {
					return true
				}
			}
		}
	}

	// Raw CSV annotation: [{"type":...,"value":{...}}]. The value is a raw
	// JSON value, matching operator-registry's encoding of this annotation
	// when deriving bundle properties (pkg/registry/parse.go, types.go).
	if raw, ok := annotations[olmPropertiesAnnotationKey]; ok && raw != "" {
		var props []struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal([]byte(raw), &props); err == nil {
			for _, prop := range props {
				if prop.Type == RoleAggregationPropertyType && roleAggregationPropertyDisables(string(prop.Value)) {
					return true
				}
			}
		}
	}

	return false
}

// roleAggregationPropertyDisables parses the JSON value of an
// olm.rbac.role-aggregation property.
func roleAggregationPropertyDisables(value string) bool {
	var v roleAggregationPropertyValue
	return json.Unmarshal([]byte(value), &v) == nil && v.Disabled
}

// deleteProvidedAPIClusterRoles deletes the aggregated ClusterRoles OLM
// generates for a single owned CRD. Deletion is how an opted-out provider
// (or, after an OLM upgrade, the resync of an already-installed opted-out
// CSV) closes the aggregation: the kube-apiserver's ClusterRole aggregation
// controller removes the rules from the default admin/edit/view roles as
// soon as the labeled source roles are gone.
func (a *Operator) deleteProvidedAPIClusterRoles(crd *metav1.PartialObjectMetadata, owned v1alpha1.CRDDescription) error {
	namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)
	for _, name := range []string{
		namePrefix + AdminSuffix,
		namePrefix + EditSuffix,
		namePrefix + ViewSuffix,
		namePrefix + "crd" + ViewSuffix,
	} {
		if err := a.deleteProvidedAPIClusterRole(name, crd); err != nil {
			return err
		}
	}
	return nil
}

// deleteProvidedAPIClusterRole deletes an OLM-generated aggregated
// ClusterRole by its deterministic name, but only when it carries the OLM
// signature (owned by the CRD and labeled olm.managed), so out-of-band
// roles that merely collide with the name are left untouched.
func (a *Operator) deleteProvidedAPIClusterRole(name string, api ownerutil.Owner) error {
	existing, err := a.lister.RbacV1().ClusterRoleLister().Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !ownerutil.IsOwnedBy(existing, api) || existing.Labels[install.OLMManagedLabelKey] != install.OLMManagedLabelValue {
		// Not a role OLM generated for this API: a name collision with an
		// out-of-band role. Leave it alone.
		a.logger.WithField("clusterrole", name).Debug("skipping deletion of non-OLM-managed clusterrole")
		return nil
	}
	a.logger.Infof("deleting cluster role %s: role aggregation opted out for its provided api", name)
	return a.opClient.DeleteClusterRole(name, &metav1.DeleteOptions{})
}
