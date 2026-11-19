package olm

import (
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
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
// policy when the singleton is not found. Convergence to a policy change
// rides CSV resyncs: each sync re-applies the current policy to the roles
// for its provided APIs via label equality.
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
