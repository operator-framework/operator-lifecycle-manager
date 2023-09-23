package olm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coreos/go-semver/semver"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/sirupsen/logrus"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (a *Operator) minKubeVersionStatus(name string, minKubeVersion string) (met bool, statuses []v1alpha1.RequirementStatus) {
	if minKubeVersion == "" {
		return true, nil
	}

	status := v1alpha1.RequirementStatus{
		Group:   "operators.coreos.com",
		Version: "v1alpha1",
		Kind:    "ClusterServiceVersion",
		Name:    name,
	}

	// Retrieve server k8s version
	serverVersionInfo, err := a.opClient.KubernetesInterface().Discovery().ServerVersion()
	if err != nil {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = "Server version discovery error"
		met = false
		statuses = append(statuses, status)
		return
	}

	serverVersion, err := semver.NewVersion(strings.Split(strings.TrimPrefix(serverVersionInfo.String(), "v"), "-")[0])
	if err != nil {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = "Server version parsing error"
		met = false
		statuses = append(statuses, status)
		return
	}

	csvVersionInfo, err := semver.NewVersion(strings.TrimPrefix(minKubeVersion, "v"))
	if err != nil {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = "CSV version parsing error"
		met = false
		statuses = append(statuses, status)
		return
	}

	if csvVersionInfo.Compare(*serverVersion) > 0 {
		status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
		status.Message = fmt.Sprintf("CSV version requirement not met: minKubeVersion (%s) > server version (%s)", minKubeVersion, serverVersion.String())
		met = false
		statuses = append(statuses, status)
		return
	}

	status.Status = v1alpha1.RequirementStatusReasonPresent
	status.Message = fmt.Sprintf("CSV minKubeVersion (%s) less than server version (%s)", minKubeVersion, serverVersionInfo.String())
	met = true
	statuses = append(statuses, status)
	return
}

func (a *Operator) requirementStatus(strategyDetailsDeployment *v1alpha1.StrategyDetailsDeployment, csv *v1alpha1.ClusterServiceVersion) (met bool, statuses []v1alpha1.RequirementStatus) {
	ownedCRDNames := make(map[string]bool)
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		ownedCRDNames[owned.Name] = true
	}

	crdDescs := csv.GetAllCRDDescriptions()
	ownedAPIServiceDescs := csv.GetOwnedAPIServiceDescriptions()
	requiredAPIServiceDescs := csv.GetRequiredAPIServiceDescriptions()
	requiredNativeAPIs := csv.Spec.NativeAPIs
	met = true

	// Check for CRDs
	for _, r := range crdDescs {
		status := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1",
			Kind:    "CustomResourceDefinition",
			Name:    r.Name,
		}

		// check if CRD exists - this verifies group, version, and kind, so no need for GVK check via discovery
		crd, err := a.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), r.Name, metav1.GetOptions{})
		if err != nil {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			status.Message = "CRD is not present"
			a.logger.Debugf("Setting 'met' to false, %v with status %v, with err: %v", r.Name, status, err)
			met = false
			statuses = append(statuses, status)
			continue
		}

		if others := othersInstalledAlongside(crd, csv, a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(csv.GetNamespace())); len(others) > 0 && ownedCRDNames[crd.Name] {
			status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
			status.Message = fmt.Sprintf("CRD installed alongside other CSV(s): %s", strings.Join(others, ", "))
			met = false
			statuses = append(statuses, status)
			continue
		}

		served := false
		for _, version := range crd.Spec.Versions {
			if version.Name == r.Version {
				if version.Served {
					served = true
				}
				break
			}
		}

		if !served {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			status.Message = "CRD version not served"
			a.logger.Debugf("Setting 'met' to false, %v with status %v, CRD version %v not found", r.Name, status, r.Version)
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if CRD has successfully registered with k8s API
		established := false
		namesAccepted := false
		for _, cdt := range crd.Status.Conditions {
			switch cdt.Type {
			case apiextensionsv1.Established:
				if cdt.Status == apiextensionsv1.ConditionTrue {
					established = true
				}
			case apiextensionsv1.NamesAccepted:
				if cdt.Status == apiextensionsv1.ConditionTrue {
					namesAccepted = true
				}
			}
		}

		if established && namesAccepted {
			status.Status = v1alpha1.RequirementStatusReasonPresent
			status.Message = "CRD is present and Established condition is true"
			status.UUID = string(crd.GetUID())
			statuses = append(statuses, status)
		} else {
			status.Status = v1alpha1.RequirementStatusReasonNotAvailable
			status.Message = "CRD is present but the Established condition is False (not available)"
			met = false
			a.logger.Debugf("Setting 'met' to false, %v with status %v, established=%v, namesAccepted=%v", r.Name, status, established, namesAccepted)
			statuses = append(statuses, status)
		}
	}

	// Check for required API services
	for _, r := range requiredAPIServiceDescs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   "apiregistration.k8s.io",
			Version: "v1",
			Kind:    "APIService",
			Name:    name,
		}

		// Check if GVK exists
		if ok, err := a.isGVKRegistered(r.Group, r.Version, r.Kind); !ok || err != nil {
			status.Status = "NotPresent"
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if APIService is registered
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(name)
		if err != nil {
			status.Status = "NotPresent"
			met = false
			statuses = append(statuses, status)
			continue
		}

		// Check if API is available
		if !install.IsAPIServiceAvailable(apiService) {
			status.Status = "NotPresent"
			met = false
		} else {
			status.Status = "Present"
			status.UUID = string(apiService.GetUID())
		}
		statuses = append(statuses, status)
	}

	// Check owned API services
	for _, r := range ownedAPIServiceDescs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   "apiregistration.k8s.io",
			Version: "v1",
			Kind:    "APIService",
			Name:    name,
		}

		found := false
		for _, spec := range strategyDetailsDeployment.DeploymentSpecs {
			if spec.Name == r.DeploymentName {
				status.Status = "DeploymentFound"
				statuses = append(statuses, status)
				found = true
				break
			}
		}

		if !found {
			status.Status = "DeploymentNotFound"
			statuses = append(statuses, status)
			met = false
		}
	}

	for _, r := range requiredNativeAPIs {
		name := fmt.Sprintf("%s.%s", r.Version, r.Group)
		status := v1alpha1.RequirementStatus{
			Group:   r.Group,
			Version: r.Version,
			Kind:    r.Kind,
			Name:    name,
		}

		if ok, err := a.isGVKRegistered(r.Group, r.Version, r.Kind); !ok || err != nil {
			status.Status = v1alpha1.RequirementStatusReasonNotPresent
			status.Message = "Native API does not exist"
			met = false
			statuses = append(statuses, status)
			continue
		} else {
			status.Status = v1alpha1.RequirementStatusReasonPresent
			status.Message = "Native API exists"
			statuses = append(statuses, status)
			continue
		}
	}

	return
}

// permissionStatus checks whether the given CSV's RBAC requirements are met in its namespace
func (a *Operator) permissionStatus(strategyDetailsDeployment *v1alpha1.StrategyDetailsDeployment, targetNamespace string, csv *v1alpha1.ClusterServiceVersion) (bool, []v1alpha1.RequirementStatus, error) {
	statusesSet := map[string]v1alpha1.RequirementStatus{}

	checkPermissions := func(permissions []v1alpha1.StrategyDeploymentPermissions, namespace string) (bool, error) {
		met := true
		for _, perm := range permissions {
			saName := perm.ServiceAccountName
			a.logger.Debugf("perm.ServiceAccountName: %s", saName)

			var status v1alpha1.RequirementStatus
			if stored, ok := statusesSet[saName]; !ok {
				status = v1alpha1.RequirementStatus{
					Group:      "",
					Version:    "v1",
					Kind:       "ServiceAccount",
					Name:       saName,
					Status:     v1alpha1.RequirementStatusReasonPresent,
					Dependents: []v1alpha1.DependentStatus{},
				}
			} else {
				status = stored
			}

			// Ensure the ServiceAccount exists
			sa, err := a.opClient.GetServiceAccount(csv.GetNamespace(), perm.ServiceAccountName)
			if err != nil {
				met = false
				status.Status = v1alpha1.RequirementStatusReasonNotPresent
				status.Message = "Service account does not exist"
				statusesSet[saName] = status
				continue
			}
			// Check SA's ownership
			if ownerutil.IsOwnedByKind(sa, v1alpha1.ClusterServiceVersionKind) && !ownerutil.IsOwnedBy(sa, csv) {
				met = false
				status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
				status.Message = "Service account is owned by another ClusterServiceVersion"
				statusesSet[saName] = status
				continue
			}

			// Check if PolicyRules are satisfied
			if a.informersFiltered {
				// we don't hold the whole set of RBAC in memory, so we can't use the authorizer:
				// check for rules we would have created ourselves first
				var err error
				var permissionMet bool
				if namespace == metav1.NamespaceAll {
					permissionMet, err = permissionsPreviouslyCreated[*rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding](
						perm, csv,
						a.lister.RbacV1().ClusterRoleLister().List, a.lister.RbacV1().ClusterRoleBindingLister().List,
					)
				} else {
					permissionMet, err = permissionsPreviouslyCreated[*rbacv1.Role, *rbacv1.RoleBinding](
						perm, csv,
						a.lister.RbacV1().RoleLister().List, a.lister.RbacV1().RoleBindingLister().List,
					)
				}
				if err != nil {
					return false, err
				}
				if permissionMet {
					// OLM previously made all the permissions we need, exit early
					for _, rule := range perm.Rules {
						dependent := v1alpha1.DependentStatus{
							Group:   "rbac.authorization.k8s.io",
							Kind:    "PolicyRule",
							Version: "v1",
							Status:  v1alpha1.DependentStatusReasonSatisfied,
						}
						marshalled, err := json.Marshal(rule)
						if err != nil {
							dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
							dependent.Message = "rule unmarshallable"
							status.Dependents = append(status.Dependents, dependent)
							continue
						}

						var scope string
						if namespace == metav1.NamespaceAll {
							scope = "cluster"
						} else {
							scope = "namespaced"
						}
						dependent.Message = fmt.Sprintf("%s rule:%s", scope, marshalled)
						status.Dependents = append(status.Dependents, dependent)
					}
					continue
				}
			}
			// if we have not filtered our informers or if we were unable to detect the correct permissions, we have
			// no choice but to page in the world and see if the user pre-created permissions for this CSV
			ruleChecker := a.getRuleChecker()(csv)
			for _, rule := range perm.Rules {
				dependent := v1alpha1.DependentStatus{
					Group:   "rbac.authorization.k8s.io",
					Kind:    "PolicyRule",
					Version: "v1",
				}

				marshalled, err := json.Marshal(rule)
				if err != nil {
					dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
					dependent.Message = "rule unmarshallable"
					status.Dependents = append(status.Dependents, dependent)
					continue
				}

				var scope string
				if namespace == metav1.NamespaceAll {
					scope = "cluster"
				} else {
					scope = "namespaced"
				}
				dependent.Message = fmt.Sprintf("%s rule:%s", scope, marshalled)

				satisfied, err := ruleChecker.RuleSatisfied(sa, namespace, rule)
				if err != nil {
					return false, err
				} else if !satisfied {
					met = false
					dependent.Status = v1alpha1.DependentStatusReasonNotSatisfied
					status.Status = v1alpha1.RequirementStatusReasonPresentNotSatisfied
					status.Message = "Policy rule not satisfied for service account"
				} else {
					dependent.Status = v1alpha1.DependentStatusReasonSatisfied
				}

				status.Dependents = append(status.Dependents, dependent)
			}

			statusesSet[saName] = status
		}

		return met, nil
	}

	permMet, err := checkPermissions(strategyDetailsDeployment.Permissions, targetNamespace)
	if err != nil {
		return false, nil, err
	}
	clusterPermMet, err := checkPermissions(strategyDetailsDeployment.ClusterPermissions, metav1.NamespaceAll)
	if err != nil {
		return false, nil, err
	}

	statuses := []v1alpha1.RequirementStatus{}
	for key, status := range statusesSet {
		a.logger.WithField("key", key).WithField("status", status).Tracef("appending permission status")
		statuses = append(statuses, status)
	}

	return permMet && clusterPermMet, statuses, nil
}

func permissionsPreviouslyCreated[T, U metav1.Object](
	permission v1alpha1.StrategyDeploymentPermissions,
	csv *v1alpha1.ClusterServiceVersion,
	listRoles func(labels.Selector) ([]T, error),
	listBindings func(labels.Selector) ([]U, error),
) (bool, error) {
	// first, find the (cluster)role
	ruleHash, err := resolver.PolicyRuleHashLabelValue(permission.Rules)
	if err != nil {
		return false, fmt.Errorf("failed to hash permission rules: %w", err)
	}
	roleSelectorMap := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	roleSelectorMap[resolver.ContentHashLabelKey] = ruleHash
	roleSelectorSet := labels.Set{}
	for key, value := range roleSelectorMap {
		roleSelectorSet[key] = value
	}
	roleSelector := labels.SelectorFromSet(roleSelectorSet)
	roles, err := listRoles(roleSelector)
	if err != nil {
		return false, err
	}

	if len(roles) == 0 {
		return false, nil
	}

	// then, find the (cluster)rolebinding, if we found the role
	bindingHash, err := resolver.RoleReferenceAndSubjectHashLabelValue(rbacv1.RoleRef{
		Kind:     "Role",
		Name:     roles[0].GetName(),
		APIGroup: rbacv1.GroupName,
	},
		[]rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      permission.ServiceAccountName,
			Namespace: csv.GetNamespace(),
		}},
	)
	if err != nil {
		return false, fmt.Errorf("failed to hash binding content: %w", err)
	}
	bindingSelectorMap := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	bindingSelectorMap[resolver.ContentHashLabelKey] = bindingHash
	bindingSelectorSet := labels.Set{}
	for key, value := range bindingSelectorMap {
		bindingSelectorSet[key] = value
	}
	bindingSelector := labels.SelectorFromSet(bindingSelectorSet)
	bindings, err := listBindings(bindingSelector)
	return len(roles) > 0 && len(bindings) > 0, err
}

// requirementAndPermissionStatus returns the aggregate requirement and permissions statuses for the given CSV
func (a *Operator) requirementAndPermissionStatus(csv *v1alpha1.ClusterServiceVersion) (bool, []v1alpha1.RequirementStatus, error) {
	allReqStatuses := []v1alpha1.RequirementStatus{}
	// Use a StrategyResolver to unmarshal
	strategyResolver := install.StrategyResolver{}
	strategy, err := strategyResolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return false, nil, err
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return false, nil, fmt.Errorf("could not cast install strategy as type %T", strategyDetailsDeployment)
	}

	// Check kubernetes version requirement between CSV and server
	minKubeMet, minKubeStatus := a.minKubeVersionStatus(csv.GetName(), csv.Spec.MinKubeVersion)
	if minKubeStatus != nil {
		allReqStatuses = append(allReqStatuses, minKubeStatus...)
	}

	reqMet, reqStatuses := a.requirementStatus(strategyDetailsDeployment, csv)
	allReqStatuses = append(allReqStatuses, reqStatuses...)

	permMet, permStatuses, err := a.permissionStatus(strategyDetailsDeployment, csv.GetNamespace(), csv)
	if err != nil {
		return false, nil, err
	}

	// Aggregate requirement and permissions statuses
	statuses := append(allReqStatuses, permStatuses...)
	met := minKubeMet && reqMet && permMet
	if !met {
		a.logger.WithField("minKubeMet", minKubeMet).WithField("reqMet", reqMet).WithField("permMet", permMet).Debug("permissions/requirements not met")
	}

	return met, statuses, nil
}

func (a *Operator) isGVKRegistered(group, version, kind string) (bool, error) {
	logger := a.logger.WithFields(logrus.Fields{
		"group":   group,
		"version": version,
		"kind":    kind,
	})

	gv := metav1.GroupVersion{Group: group, Version: version}
	resources, err := a.opClient.KubernetesInterface().Discovery().ServerResourcesForGroupVersion(gv.String())
	if err != nil {
		logger.WithField("err", err).Info("could not query for GVK in api discovery")
		return false, err
	}

	for _, r := range resources.APIResources {
		if r.Kind == kind {
			return true, nil
		}
	}

	logger.Info("couldn't find GVK in api discovery")
	return false, nil
}

// othersInstalledAlongside returns the names of all
// ClusterServiceVersions alongside which the given object was
// installed, that are not the named CSV and are directly or
// transitively replaced by the named CSV.
func othersInstalledAlongside(o metav1.Object, target *v1alpha1.ClusterServiceVersion, lister listersv1alpha1.ClusterServiceVersionNamespaceLister) []string {
	csvsByName := make(map[string]*v1alpha1.ClusterServiceVersion)
	for _, nn := range (alongside.Annotator{}).FromObject(o) {
		if nn.Namespace != target.GetNamespace() {
			continue
		}
		if nn.Name == target.GetName() {
			return nil
		}
		csv, err := lister.Get(nn.Name)
		if err != nil || csv.IsCopied() {
			continue
		}
		csvsByName[csv.GetName()] = csv
	}

	replacees := make(map[string]string)
	for current, csv := range csvsByName {
		if _, ok := csvsByName[csv.Spec.Replaces]; ok {
			replacees[current] = csv.Spec.Replaces
		}
	}
	if target.Spec.Replaces != "" {
		replacees[target.GetName()] = target.Spec.Replaces
	}

	ancestors := make(map[string]struct{})
	for current := target.GetName(); current != ""; {
		replacee, ok := replacees[current]
		if ok {
			ancestors[replacee] = struct{}{}
		}
		delete(replacees, current) // avoid cycles
		current = replacee
	}

	var names []string
	for each := range csvsByName {
		if _, ok := ancestors[each]; ok && each != target.GetName() {
			names = append(names, each)
		}
	}
	return names
}
