package provider

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/fakes"
)

func TestRefreshCacheConcurrency(t *testing.T) {
	for _, workers := range []int{1, 4, 8} {
		for _, scenario := range []string{"complete", "package-error", "bundle-error", "cache-error", "cancel"} {
			t.Run(fmt.Sprintf("%d/%s", workers, scenario), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				p, err := NewFakeRegistryProvider(ctx, nil, nil, "global")
				require.NoError(t, err)
				t.Cleanup(p.refreshQueue.ShutDown)
				p.packageRefreshSlots = semaphore.NewWeighted(int64(workers))
				if scenario == "cache-error" {
					p.cache = failingCache{p.cache}
				}
				const packages = 24
				var active, maximum atomic.Int32
				entered := make(chan struct{}, packages*2)
				release := make(chan struct{})
				done := make(chan error, 2)
				for _, name := range []string{"first", "second"} {
					if scenario == "cancel" {
						require.NoError(t, p.cache.Add(&operators.PackageManifest{
							ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: "global"},
							Status:     operators.PackageManifestStatus{CatalogSource: name, CatalogSourceNamespace: "global"},
						}))
					}
					client := &fakes.FakeRegistryClient{}
					bundles := &fakes.FakeRegistry_ListBundlesClient{}
					bundles.RecvReturns(nil, io.EOF)
					client.ListBundlesReturns(bundles, nil)
					stream := &fakes.FakeRegistry_ListPackagesClient{}
					for i := 0; i < packages; i++ {
						stream.RecvReturnsOnCall(i, &api.PackageName{Name: fmt.Sprintf("pkg-%d", i)}, nil)
					}
					stream.RecvReturnsOnCall(packages, nil, io.EOF)
					client.ListPackagesReturns(stream, nil)
					client.GetPackageCalls(func(ctx context.Context, req *api.GetPackageRequest, _ ...grpc.CallOption) (*api.Package, error) {
						n := active.Add(1)
						defer active.Add(-1)
						for old := maximum.Load(); n > old; old = maximum.Load() {
							if maximum.CompareAndSwap(old, n) {
								break
							}
						}
						entered <- struct{}{}
						select {
						case <-ctx.Done():
							return nil, ctx.Err()
						case <-release:
						}
						if scenario == "package-error" && req.Name == "pkg-0" {
							return nil, fmt.Errorf("package failure")
						}
						return &api.Package{Name: req.Name, DefaultChannelName: "stable", Channels: []*api.Channel{{Name: "stable"}, {Name: "preview"}}}, nil
					})
					client.GetBundleForChannelCalls(func(_ context.Context, req *api.GetBundleInChannelRequest, _ ...grpc.CallOption) (*api.Bundle, error) {
						if scenario == "bundle-error" && req.PkgName == "pkg-0" {
							return nil, fmt.Errorf("bundle failure")
						}
						return &api.Bundle{CsvJson: `{"metadata":{"name":"test.v1"}}`}, nil
					})
					go func() {
						done <- p.refreshCache(ctx, &registryClient{RegistryClient: client, catsrc: catalogSource(name, "global")})
					}()
				}
				for i := 0; i < workers; i++ {
					select {
					case <-entered:
					case <-ctx.Done():
						t.Fatal("workers did not start")
					}
				}
				select {
				case <-entered:
					t.Fatal("refresh exceeded provider concurrency bound")
				case <-time.After(100 * time.Millisecond):
				}
				if scenario == "cancel" {
					cancel()
				} else {
					close(release)
				}
				for i := 0; i < 2; i++ {
					select {
					case err := <-done:
						if scenario == "cancel" {
							require.ErrorIs(t, err, context.Canceled)
						} else {
							require.NoError(t, err)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("refresh failed to drain workers")
					}
				}
				require.LessOrEqual(t, maximum.Load(), int32(workers))
				require.Zero(t, active.Load())
				require.True(t, p.packageRefreshSlots.TryAcquire(int64(workers)), "all slots must be released")
				p.packageRefreshSlots.Release(int64(workers))
				if scenario != "cancel" {
					want := packages * 2
					if scenario != "complete" {
						want -= 2
					}
					require.Len(t, p.cache.List(), want, "every valid package must be cached")
				} else {
					require.Len(t, p.cache.List(), 2, "cancellation must not garbage collect existing packages")
				}
			})
		}
	}
}

type failingCache struct{ cache.Indexer }

func (c failingCache) Add(obj interface{}) error {
	if obj.(*operators.PackageManifest).Name == "pkg-0" {
		return fmt.Errorf("cache failure")
	}
	return c.Indexer.Add(obj)
}
