package olm

import (
	"time"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
)

const (
	healthCheckBackoffBaseDelay = 10 * time.Second
	healthCheckBackoffMaxDelay  = 5 * time.Minute
)

// csvHealthCheckBackoff tracks repeated transient Deployment readiness failures
// independently for each CSV.
type csvHealthCheckBackoff struct {
	rateLimiter workqueue.TypedRateLimiter[types.NamespacedName]
}

func newCSVHealthCheckBackoff() *csvHealthCheckBackoff {
	return &csvHealthCheckBackoff{
		rateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[types.NamespacedName](
			healthCheckBackoffBaseDelay,
			healthCheckBackoffMaxDelay,
		),
	}
}

func (b *csvHealthCheckBackoff) next(key types.NamespacedName) time.Duration {
	return b.rateLimiter.When(key)
}

func (b *csvHealthCheckBackoff) forget(key types.NamespacedName) {
	b.rateLimiter.Forget(key)
}

func (a *Operator) requeueAfterStrategyError(csv *v1alpha1.ClusterServiceVersion, strategyErr error) error {
	key := types.NamespacedName{Namespace: csv.GetNamespace(), Name: csv.GetName()}
	if install.ReasonForError(strategyErr) == install.StrategyErrReasonWaiting {
		return a.csvQueueSet.RequeueAfter(csv.GetNamespace(), csv.GetName(), a.healthCheckBackoff.next(key))
	}

	a.healthCheckBackoff.forget(key)
	return a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName())
}
