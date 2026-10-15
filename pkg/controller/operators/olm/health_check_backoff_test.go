package olm

import (
	"context"
	"testing"
	"time"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
)

type delayedQueueItem struct {
	key   types.NamespacedName
	delay time.Duration
}

type recordingCSVQueue struct {
	workqueue.TypedRateLimitingInterface[types.NamespacedName]
	added   []types.NamespacedName
	delayed []delayedQueueItem
}

func (q *recordingCSVQueue) Add(key types.NamespacedName) {
	q.added = append(q.added, key)
}

func (q *recordingCSVQueue) AddAfter(key types.NamespacedName, delay time.Duration) {
	q.delayed = append(q.delayed, delayedQueueItem{key: key, delay: delay})
}

func TestCSVHealthCheckBackoffIsExponentialAndCapped(t *testing.T) {
	backoff := newCSVHealthCheckBackoff()
	key := types.NamespacedName{Namespace: "operators", Name: "sample"}
	otherKey := types.NamespacedName{Namespace: "operators", Name: "other"}
	want := []time.Duration{
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		80 * time.Second,
		160 * time.Second,
		5 * time.Minute,
		5 * time.Minute,
	}

	for _, expected := range want {
		require.Equal(t, expected, backoff.next(key))
	}
	require.Equal(t, 10*time.Second, backoff.next(otherKey), "backoff should be isolated per CSV")

	backoff.forget(key)
	require.Equal(t, 10*time.Second, backoff.next(key), "forget should reset the retry sequence")
}

func TestRequeueAfterStrategyErrorDelaysOnlyDeploymentWaits(t *testing.T) {
	queue := &recordingCSVQueue{}
	queueSet := queueinformer.NewEmptyResourceQueueSet()
	queueSet.Set("operators", queue)
	op := &Operator{
		csvQueueSet:        queueSet,
		healthCheckBackoff: newCSVHealthCheckBackoff(),
	}
	csv := &v1alpha1.ClusterServiceVersion{ObjectMeta: metav1.ObjectMeta{
		Namespace: "operators",
		Name:      "sample",
	}}
	waiting := install.StrategyError{Reason: install.StrategyErrReasonWaiting, Message: "deployment is not ready"}

	require.NoError(t, op.requeueAfterStrategyError(csv, waiting))
	require.Empty(t, queue.added, "health waits must not use immediate queue.Add")
	require.Equal(t, []delayedQueueItem{{
		key:   types.NamespacedName{Namespace: "operators", Name: "sample"},
		delay: 10 * time.Second,
	}}, queue.delayed)

	require.NoError(t, op.requeueAfterStrategyError(csv, waiting))
	require.Equal(t, 20*time.Second, queue.delayed[1].delay)

	other := install.StrategyError{Reason: install.StrategyErrDeploymentUpdated, Message: "deployment spec changed"}
	require.NoError(t, op.requeueAfterStrategyError(csv, other))
	require.Equal(t, []types.NamespacedName{{Namespace: "operators", Name: "sample"}}, queue.added)

	require.NoError(t, op.requeueAfterStrategyError(csv, waiting))
	require.Equal(t, 10*time.Second, queue.delayed[2].delay, "non-waiting errors should reset health backoff")
}

func TestUpdateInstallStatusResetsHealthBackoffWhenDeploymentIsHealthy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	op, err := NewFakeOperator(ctx, withNamespaces("health-backoff-test"))
	require.NoError(t, err)
	op.recorder = &record.FakeRecorder{Events: make(chan string, 10)}

	csv := &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{Name: "sample", Namespace: "health-backoff-test"},
		Status: v1alpha1.ClusterServiceVersionStatus{
			Phase:   v1alpha1.CSVPhaseFailed,
			Reason:  v1alpha1.CSVReasonComponentUnhealthy,
			Message: "deployment is not ready",
		},
	}
	key := types.NamespacedName{Namespace: csv.GetNamespace(), Name: csv.GetName()}
	require.Equal(t, 10*time.Second, op.healthCheckBackoff.next(key))
	require.Equal(t, 20*time.Second, op.healthCheckBackoff.next(key))

	err = op.updateInstallStatus(
		csv,
		NewTestInstaller(nil, nil),
		&v1alpha1.StrategyDetailsDeployment{},
		v1alpha1.CSVPhaseFailed,
		v1alpha1.CSVReasonComponentUnhealthy,
	)
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, op.healthCheckBackoff.next(key), "healthy checks should reset the retry sequence")
}

func TestCSVDeletionForgetsHealthBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	op, err := NewFakeOperator(ctx, withNamespaces("health-backoff-test"))
	require.NoError(t, err)
	csv := &v1alpha1.ClusterServiceVersion{ObjectMeta: metav1.ObjectMeta{
		Name:      "sample",
		Namespace: "health-backoff-test",
	}}
	key := types.NamespacedName{Namespace: csv.GetNamespace(), Name: csv.GetName()}
	op.healthCheckBackoff.next(key)
	require.Equal(t, 20*time.Second, op.healthCheckBackoff.next(key))

	op.handleClusterServiceVersionDeletion(csv)

	require.Equal(t, 10*time.Second, op.healthCheckBackoff.next(key), "deleted CSVs should not retain retry state")
}
