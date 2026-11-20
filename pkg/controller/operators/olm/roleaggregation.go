package olm

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

// roleAggregationPolicy is the internal form of the OlmConfig roleAggregation
// feature, with the enableAPIs allowlist as a set for O(1) lookups. The zero
// value is the legacy policy (aggregation enabled for every API).
type roleAggregationPolicy struct {
	disabled bool
	// enabledAPIs are the apis (group+kind) that stay aggregated while
	// aggregation is disabled. Matching is version-independent.
	enabledAPIs sets.Set[operatorsv1.GroupKind]
}

// newRoleAggregationPolicy converts an OlmConfig's roleAggregation feature
// into its internal form. A nil config or missing features yield the legacy
// policy.
func newRoleAggregationPolicy(config *operatorsv1.OLMConfig) roleAggregationPolicy {
	if config == nil || config.Spec.Features == nil {
		return roleAggregationPolicy{}
	}

	roleAggregation := config.Spec.Features.RoleAggregation
	return roleAggregationPolicy{
		disabled:    roleAggregation.Disable,
		enabledAPIs: sets.New(roleAggregation.EnableAPIs...),
	}
}

// isEnabledFor reports whether aggregation is enabled for the given group
// and kind: every API when not disabled, otherwise only allowlisted ones.
func (p roleAggregationPolicy) isEnabledFor(group, kind string) bool {
	if !p.disabled {
		return true
	}
	return p.enabledAPIs.Has(operatorsv1.GroupKind{Group: group, Kind: kind})
}

// roleAggregationPolicy reads the OlmConfig singleton from the informer
// cache and converts it to its internal form, defaulting to the legacy
// policy when the singleton is not found. OLMConfig reconciliation requeues
// non-copied CSVs whose existing provided-API roles have aggregation labels
// that do not match the policy. CSV resyncs also apply the current policy.
func (a *Operator) roleAggregationPolicy() (roleAggregationPolicy, error) {
	olmConfig, err := a.informersByNamespace[corev1.NamespaceAll].OLMConfigInformer.Lister().Get("cluster")
	if err != nil {
		if apierrors.IsNotFound(err) {
			return roleAggregationPolicy{}, nil
		}
		return roleAggregationPolicy{}, err
	}

	return newRoleAggregationPolicy(olmConfig), nil
}

// csvRoleAggregationNeedsSync checks only the policy-controlled aggregation
// labels on existing provided-API roles. Missing roles are created by normal
// CSV reconciliation, and CRD discovery roles always aggregate regardless of
// the policy. Assessment errors are returned only if no mismatch is found:
// one confirmed mismatch is enough to require CSV reconciliation.
func (a *Operator) csvRoleAggregationNeedsSync(csv *v1alpha1.ClusterServiceVersion, policy roleAggregationPolicy) (bool, error) {
	var errs []error
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		_, group, ok := strings.Cut(owned.Name, ".")
		if !ok {
			errs = append(errs, fmt.Errorf("invalid CRD name %q", owned.Name))
			continue
		}
		key := opregistry.APIKey{Group: group, Version: owned.Version, Kind: owned.Kind}
		needsSync, err := a.providedAPIRoleAggregationNeedsSync(owned.Name, key, policy)
		if needsSync {
			return true, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
		key := opregistry.APIKey{Group: owned.Group, Version: owned.Version, Kind: owned.Kind}
		needsSync, err := a.providedAPIRoleAggregationNeedsSync(owned.Name, key, policy)
		if needsSync {
			return true, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return false, utilerrors.NewAggregate(errs)
}

func (a *Operator) providedAPIRoleAggregationNeedsSync(name string, key opregistry.APIKey, policy roleAggregationPolicy) (bool, error) {
	aggregate := policy.isEnabledFor(key.Group, key.Kind)
	var errs []error
	for _, suffix := range Suffices {
		role, err := a.lister.RbacV1().ClusterRoleLister().Get(fmt.Sprintf("%s-%s-%s", name, key.Version, suffix))
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		aggregationLabel, err := aggregationLabelFromAPIKey(key, suffix)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, label := range []string{fmt.Sprintf("rbac.authorization.k8s.io/aggregate-to-%s", suffix), aggregationLabel} {
			value, present := role.Labels[label]
			if (aggregate && value != "true") || (!aggregate && present) {
				return true, nil
			}
		}
	}
	return false, utilerrors.NewAggregate(errs)
}
