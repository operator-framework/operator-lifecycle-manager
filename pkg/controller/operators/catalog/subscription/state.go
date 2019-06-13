package subscription

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	clientv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/pkg/errors"
)

// SubscriptionState describes subscription states.
type SubscriptionState interface {
	kubestate.State

	isSubscriptionState()
	setSubscription(*v1alpha1.Subscription)

	Subscription() *v1alpha1.Subscription
	Add() SubscriptionExistsState
	Update() SubscriptionExistsState
	Delete() SubscriptionDeletedState
}

// SubscriptionExistsState describes subscription states in which the subscription exists on the cluster.
type SubscriptionExistsState interface {
	SubscriptionState

	isSubscriptionExistsState()
}

// SubscriptionAddedState describes subscription states in which the subscription was added to cluster.
type SubscriptionAddedState interface {
	SubscriptionExistsState

	isSubscriptionAddedState()
}

// SubscriptionUpdatedState describes subscription states in which the subscription was updated in the cluster.
type SubscriptionUpdatedState interface {
	SubscriptionExistsState

	isSubscriptionUpdatedState()
}

// SubscriptionDeletedState describes subscription states in which the subscription no longer exists and was deleted from the cluster.
type SubscriptionDeletedState interface {
	SubscriptionState

	isSubscriptionDeletedState()
}

// CatalogHealthState describes subscription states that represent a subscription with respect to catalog health.
type CatalogHealthState interface {
	SubscriptionState

	isCatalogHealthState()

	// UpdateHealth transitions the CatalogHealthState to another CatalogHealthState based on the given subscription catalog health.
	// The state's underlying subscription may be updated on the cluster. If the subscription is updated, the resulting state will contain the updated version.
	UpdateHealth(now *metav1.Time, client clientv1alpha1.SubscriptionInterface, health ...v1alpha1.SubscriptionCatalogHealth) (CatalogHealthState, error)
}

// CatalogHealthKnownState describes subscription states in which all relevant catalog health is known.
type CatalogHealthKnownState interface {
	CatalogHealthState

	isCatalogHealthKnownState()
}

// CatalogHealthyState describes subscription states in which all relevant catalogs are known to be healthy.
type CatalogHealthyState interface {
	CatalogHealthKnownState

	isCatalogHealthyState()
}

// CatalogUnhealthyState describes subscription states in which at least one relevant catalog is known to be unhealthy.
type CatalogUnhealthyState interface {
	CatalogHealthKnownState

	isCatalogUnhealthyState()
}

type subscriptionState struct {
	kubestate.State

	subscription *v1alpha1.Subscription
}

func (s *subscriptionState) isSubscriptionState() {}

func (s *subscriptionState) setSubscription(sub *v1alpha1.Subscription) {
	s.subscription = sub
}

func (s *subscriptionState) Subscription() *v1alpha1.Subscription {
	return s.subscription
}

func (s *subscriptionState) Add() SubscriptionExistsState {
	return &subscriptionAddedState{
		SubscriptionExistsState: &subscriptionExistsState{
			SubscriptionState: s,
		},
	}
}

func (s *subscriptionState) Update() SubscriptionExistsState {
	return &subscriptionUpdatedState{
		SubscriptionExistsState: &subscriptionExistsState{
			SubscriptionState: s,
		},
	}
}

func (s *subscriptionState) Delete() SubscriptionDeletedState {
	return &subscriptionDeletedState{
		SubscriptionState: s,
	}
}

func NewSubscriptionState(sub *v1alpha1.Subscription) SubscriptionState {
	return &subscriptionState{
		State:        kubestate.NewState(),
		subscription: sub,
	}
}

type subscriptionExistsState struct {
	SubscriptionState
}

func (*subscriptionExistsState) isSubscriptionExistsState() {}

type subscriptionAddedState struct {
	SubscriptionExistsState
}

func (c *subscriptionAddedState) isSubscriptionAddedState() {}

type subscriptionUpdatedState struct {
	SubscriptionExistsState
}

func (c *subscriptionUpdatedState) isSubscriptionUpdatedState() {}

type subscriptionDeletedState struct {
	SubscriptionState
}

func (c *subscriptionDeletedState) isSubscriptionDeletedState() {}

type catalogHealthState struct {
	SubscriptionExistsState
}

func (c *catalogHealthState) isCatalogHealthState() {}

func (c *catalogHealthState) UpdateHealth(now *metav1.Time, client clientv1alpha1.SubscriptionInterface, catalogHealth ...v1alpha1.SubscriptionCatalogHealth) (CatalogHealthState, error) {
	in := c.Subscription()
	out := in.DeepCopy()

	healthSet := make(map[types.UID]v1alpha1.SubscriptionCatalogHealth, len(catalogHealth))
	healthy := true
	missingTargeted := true

	cond := out.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
	for _, h := range catalogHealth {
		ref := h.CatalogSourceRef
		healthSet[ref.UID] = h
		healthy = healthy && h.Healthy

		if ref.Namespace == in.Spec.CatalogSourceNamespace && ref.Name == in.Spec.CatalogSource {
			missingTargeted = false
			if !h.Healthy {
				cond.Message = fmt.Sprintf("targeted catalogsource %s/%s unhealthy", ref.Namespace, ref.Name)
			}
		}
	}

	var known CatalogHealthKnownState
	switch {
	case missingTargeted:
		healthy = false
		cond.Message = fmt.Sprintf("targeted catalogsource %s/%s missing", in.Spec.CatalogSourceNamespace, in.Spec.CatalogSource)
		fallthrough
	case !healthy:
		cond.Status = corev1.ConditionTrue
		cond.Reason = v1alpha1.UnhealthyCatalogSourceFound
		known = &catalogUnhealthyState{
			CatalogHealthKnownState: &catalogHealthKnownState{
				CatalogHealthState: c,
			},
		}
	default:
		cond.Status = corev1.ConditionFalse
		cond.Reason = v1alpha1.AllCatalogSourcesHealthy
		cond.Message = "all available catalogsources are healthy"
		known = &catalogHealthyState{
			CatalogHealthKnownState: &catalogHealthKnownState{
				CatalogHealthState: c,
			},
		}
	}

	// Check for changes in CatalogHealth
	update := true
	switch numNew, numOld := len(healthSet), len(in.Status.CatalogHealth); {
	case numNew > numOld:
		cond.Reason = v1alpha1.CatalogSourcesAdded
	case numNew < numOld:
		cond.Reason = v1alpha1.CatalogSourcesDeleted
	case numNew == 0 && numNew == numOld:
		healthy = false
		cond.Reason = v1alpha1.NoCatalogSourcesFound
		cond.Message = "dependency resolution requires at least one catalogsource"
	case numNew == numOld:
		// Check against existing subscription
		for _, oldHealth := range in.Status.CatalogHealth {
			uid := oldHealth.CatalogSourceRef.UID
			if newHealth, ok := healthSet[uid]; !ok || !newHealth.Equals(oldHealth) {
				cond.Reason = v1alpha1.CatalogSourcesUpdated
				break
			}
		}

		fallthrough
	default:
		update = false
	}

	if !update && cond.Equals(in.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)) {
		utilruntime.HandleError(errors.New("nothing has changed, returning same state"))
		// Nothing to do, transition to self
		return known, nil
	}

	cond.LastTransitionTime = now
	out.Status.LastUpdated = *now
	out.Status.SetCondition(cond)
	out.Status.CatalogHealth = catalogHealth

	updated, err := client.UpdateStatus(out)
	if err != nil {
		// Error occurred, transition to self
		return c, err
	}

	// Inject updated subscription into the state
	known.setSubscription(updated)

	return known, nil
}

func NewCatalogHealthState(s SubscriptionExistsState) CatalogHealthState {
	return &catalogHealthState{
		SubscriptionExistsState: s,
	}
}

type catalogHealthKnownState struct {
	CatalogHealthState
}

func (c *catalogHealthKnownState) isCatalogHealthKnownState() {}

func (c *catalogHealthKnownState) CatalogHealth() []v1alpha1.SubscriptionCatalogHealth {
	return c.Subscription().Status.CatalogHealth
}

type catalogHealthyState struct {
	CatalogHealthKnownState
}

func (c *catalogHealthyState) isCatalogHealthyState() {}

type catalogUnhealthyState struct {
	CatalogHealthKnownState
}

func (c *catalogUnhealthyState) isCatalogUnhealthyState() {}
