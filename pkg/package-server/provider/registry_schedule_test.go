package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/connectivity"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	registrygrpc "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/grpc"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

type scheduledRefresh struct {
	key   types.NamespacedName
	delay time.Duration
}
type recordingRefreshQueue struct {
	workqueue.TypedRateLimitingInterface[types.NamespacedName]
	items []scheduledRefresh
}

func (q *recordingRefreshQueue) AddAfter(key types.NamespacedName, delay time.Duration) {
	q.items = append(q.items, scheduledRefresh{key, delay})
}

func TestRefreshSchedule(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q := &recordingRefreshQueue{}
	samples := []float64{0.1, 0.9, 0.5, 0.25, 0.75}
	i := 0
	p := &RegistryProvider{ctx: ctx, refreshQueue: q, refreshInterval: 5 * time.Minute, refreshJitter: 0.2, randomFloat: func() float64 { n := samples[i]; i++; return n }}
	first, second := registry.CatalogKey{Name: "first", Namespace: "global"}, registry.CatalogKey{Name: "second", Namespace: "global"}
	p.enqueueRefresh(first, true)
	p.enqueueRefresh(second, true)
	p.enqueueRefresh(first, true)
	p.syncSourceState(registrygrpc.SourceState{Key: first, State: connectivity.Ready})
	require.Equal(t, []time.Duration{6 * time.Second, 54 * time.Second, 30 * time.Second, 7500 * time.Millisecond}, []time.Duration{q.items[0].delay, q.items[1].delay, q.items[2].delay, q.items[3].delay})
	require.Equal(t, types.NamespacedName{Namespace: "global", Name: "second"}, q.items[1].key)
	p.refreshJitter = 0
	p.enqueueRefresh(first, false)
	require.Zero(t, q.items[4].delay)
	cancel()
	p.enqueueRefresh(first, true)
	require.Len(t, q.items, 5, "shutdown must not admit new delayed work")
	require.Equal(t, 5*time.Minute, cacheTimeout, "RPC deadline must not change")
}

func TestRefreshQueueShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := NewFakeRegistryProvider(ctx, nil, nil, "global")
	require.NoError(t, err)
	t.Cleanup(p.refreshQueue.ShutDown)
	p.refreshJitter = 0.2
	p.randomFloat = func() float64 { return 0.9 }
	p.enqueueRefresh(registry.CatalogKey{Name: "catalog"}, true)
	p.refreshQueue.ShutDown()
	_, stopped := p.refreshQueue.Get()
	require.True(t, stopped, "shutdown must not wait for delayed work")
}

func TestRefreshProviderConfiguration(t *testing.T) {
	for _, workers := range []int{1, 4, 8, 128} {
		op, err := queueinformer.NewOperator(k8sfake.NewClientset().Discovery())
		require.NoError(t, err)
		//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
		p, err := NewRegistryProvider(context.Background(), fake.NewSimpleClientset(), op, 5*time.Minute, "global", workers, 0.2)
		require.NoError(t, err)
		t.Cleanup(p.refreshQueue.ShutDown)
		require.True(t, p.packageRefreshSlots.TryAcquire(int64(workers)))
		require.False(t, p.packageRefreshSlots.TryAcquire(1), "constructor must enforce selected capacity")
		p.packageRefreshSlots.Release(int64(workers))
		require.Equal(t, 0.2, p.refreshJitter)
		require.Equal(t, 5*time.Minute, p.refreshInterval)
	}
}
