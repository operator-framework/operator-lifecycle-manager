package olm

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// roleAggregationPolicy is the internal form of the OlmConfig
// roleAggregation feature, converted from the on-cluster object before any
// use: the enableAPIs allowlist is stored as a set so that API lookups are
// O(1). The zero value is the legacy policy (aggregation enabled for every
// API).
type roleAggregationPolicy struct {
	// disabled records whether role aggregation is disabled (the
	// default-deny gate). When false, aggregation is enabled for every API.
	disabled bool
	// enabledAPIs is the set of apis (group+kind) that remain aggregated
	// while aggregation is otherwise disabled. An empty set disables
	// aggregation for every API. Matching is version-independent: the
	// aggregated permission applies to the resource as a whole.
	enabledAPIs sets.Set[operatorsv1.GroupKind]
}

// newRoleAggregationPolicy converts the roleAggregation feature of an
// OlmConfig into its internal form. A nil config or missing features convert
// to the legacy policy; the zero value of the roleAggregation stanza (unset
// or disable=false with no enableAPIs) is itself the legacy policy.
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

// isEnabledFor reports whether role aggregation is enabled for the API
// identified by the given group and kind: every API when aggregation is not
// disabled, and otherwise only the allowlisted apis. Matching is
// version-independent: an entry matches every served version of the kind.
func (p roleAggregationPolicy) isEnabledFor(group, kind string) bool {
	if !p.disabled {
		return true
	}
	return p.enabledAPIs.Has(operatorsv1.GroupKind{Group: group, Kind: kind})
}

// roleAggregationPolicy fetches the OlmConfig singleton and returns its
// roleAggregation feature already converted into the internal form, so the
// conversion (and its O(1) lookup set) is available to every consumer from
// the earliest possible point. Defaults to the legacy policy when the
// singleton cannot be found.
func (a *Operator) roleAggregationPolicy() (roleAggregationPolicy, error) {
	olmConfig, err := a.client.OperatorsV1().OLMConfigs().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		// Default to no policy (legacy behavior) if olmConfig singleton cannot be found
		if apierrors.IsNotFound(err) {
			return roleAggregationPolicy{}, nil
		}
		return roleAggregationPolicy{}, err
	}

	return newRoleAggregationPolicy(olmConfig), nil
}
