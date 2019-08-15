package catalog

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/reflection"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	health "github.com/operator-framework/operator-registry/pkg/api/grpc_health_v1"
	opserver "github.com/operator-framework/operator-registry/pkg/server"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

func server(store registry.Query, port int) (func(), func()) {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		logrus.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()

	api.RegisterRegistryServer(s, opserver.NewRegistryServer(store))
	health.RegisterHealthServer(s, opserver.NewHealthServer())
	reflection.Register(s)
	serve := func() {
		if err := s.Serve(lis); err != nil {
			logrus.Fatalf("failed to serve: %v", err)
		}
	}

	stop := func() {
		s.Stop()
	}

	return serve, stop
}

func TestResolveNamespaceMemoryUsage(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	testNamespace := "testNamespace"

	port := 50051
	catalogSourceName := "catalog"
	catalogSource := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      catalogSourceName,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: v1alpha1.SourceTypeGrpc,
			Address:    "127.0.0.1:50051",
		},
	}

	type fields struct {
		clientOptions     []clientfake.Option
		sourcesLastUpdate metav1.Time
		resolveSteps      []*v1alpha1.Step
		resolveSubs       []*v1alpha1.Subscription
		resolveErr        error
		existingOLMObjs   []k8sruntime.Object
		existingObjects   []k8sruntime.Object
	}
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name              string
		fields            fields
		args              args
		wantErr           error
		wantInstallPlan   *v1alpha1.InstallPlan
		wantSubscriptions []*v1alpha1.Subscription
	}{
		{
			name: "NoStatus/NoCurrentCSV/FoundInCatalog",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []k8sruntime.Object{
					catalogSource,
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
				resolveSteps: []*v1alpha1.Step{
					{
						Resolving: "csv.v.1",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:    v1alpha1.GroupName,
							Version:  v1alpha1.GroupVersion,
							Kind:     v1alpha1.ClusterServiceVersionKind,
							Name:     "csv.v.1",
							Manifest: "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "csv.v.1",
							State:      "SubscriptionStateAtLatest",
						},
					},
				},
			},
			args: args{
				obj: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          catalogSourceName,
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
					},
				},
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "csv.v.1",
						State:      v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated: now,
					},
				},
			},
			wantInstallPlan: &v1alpha1.InstallPlan{
				Spec: v1alpha1.InstallPlanSpec{
					ClusterServiceVersionNames: []string{
						"csv.v.1",
					},
					Approval: v1alpha1.ApprovalAutomatic,
					Approved: true,
				},
				Status: v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseInstalling,
					CatalogSources: []string{
						"src",
					},
					Plan: []*v1alpha1.Step{
						{
							Resolving: "csv.v.1",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:    v1alpha1.GroupName,
								Version:  v1alpha1.GroupVersion,
								Kind:     v1alpha1.ClusterServiceVersionKind,
								Name:     "csv.v.1",
								Manifest: "{}",
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			o, err := NewFakeOperator(ctx, testNamespace, []string{testNamespace}, withClock(clockFake), withClientObjs(tt.fields.existingOLMObjs...), withK8sObjs(tt.fields.existingObjects...), withFakeClientOptions(tt.fields.clientOptions...))
			require.NoError(t, err)
			o.reconciler = &fakes.FakeRegistryReconcilerFactory{
				ReconcilerForSourceStub: func(source *v1alpha1.CatalogSource) reconciler.RegistryReconciler {
					return &fakes.FakeRegistryReconciler{
						EnsureRegistryServerStub: func(source *v1alpha1.CatalogSource) error {
							return nil
						},
					}
				},
			}
			o.sourcesLastUpdate = tt.fields.sourcesLastUpdate
			// Wire CatalogSources
			operatorsFactory := externalversions.NewSharedInformerFactoryWithOptions(o.client, 1*time.Second, externalversions.WithNamespace(testNamespace))
			catsrcInformer := operatorsFactory.Operators().V1alpha1().CatalogSources()
			catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/catsrcs", testNamespace))
			o.catsrcQueueSet = queueinformer.NewEmptyResourceQueueSet()
			o.catsrcQueueSet.Set(testNamespace, catsrcQueue)
			catsrcQueueInformer, err := queueinformer.NewQueueInformer(
				ctx,
				queueinformer.WithMetricsProvider(metrics.NewMetricsCatalogSource(o.client)),
				queueinformer.WithLogger(o.logger),
				queueinformer.WithQueue(catsrcQueue),
				queueinformer.WithInformer(catsrcInformer.Informer()),
				queueinformer.WithSyncer(queueinformer.LegacySyncHandler(o.syncCatalogSources).ToSyncerWithDelete(o.handleCatSrcDeletion)),
			)
			require.NoError(t, err)
			err = o.RegisterQueueInformer(catsrcQueueInformer)
			require.NoError(t, err)

			o.resolver = resolver.NewOperatorsV1alpha1Resolver(o.lister)

			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}
			o.RunInformers(ctx)

			require.True(t, cache.WaitForCacheSync(ctx.Done(), o.HasSynced))
			store := &fakes.FakeQuery{
				ListPackagesStub: func(context.Context) ([]string, error) {
					return []string{"test"}, nil
				},
			}
			serve, stop := server(store, port)
			go serve()
			defer stop()

			c, err := o.sources.Add(resolver.CatalogKey{Name: catalogSourceName, Namespace: testNamespace}, catalogSource.Spec.Address)
			require.NoError(t, err)

			err = wait.Poll(1*time.Second, 2*time.Minute, func() (done bool, err error) {
				state := c.Conn.GetState()
				fmt.Println(state)
				if state != connectivity.Ready {
					fmt.Println("waiting...")
					return false, nil
				}
				return true, nil
			})
			require.NoError(t, err)

			repro := func() {
				o.syncResolvingNamespace(namespace)
			}

			repro()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			alloc := m.HeapObjects

			for i := 0; i < 100000; i++ {
				repro()
			}
			runtime.GC()
			runtime.ReadMemStats(&m)

			require.True(t, m.HeapObjects < alloc, "%d MiB should be less than %d MiB", m.HeapObjects/1024/1024, alloc/1024/1024)
		})
	}
}
