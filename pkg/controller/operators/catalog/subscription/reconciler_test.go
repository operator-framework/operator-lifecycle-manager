package subscription

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	versionedfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	registryreconciler "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	olmfakes "github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

func TestCatalogHealthReconcile(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	earlier := metav1.NewTime(now.Add(-time.Minute))
	nowFunc := func() *metav1.Time { return &now }

	type fields struct {
		config *fakeReconcilerConfig
	}
	type args struct {
		in kubestate.State
	}
	type want struct {
		err error
		out kubestate.State
	}

	tests := []struct {
		description string
		fields      fields
		args        args
		want        want
	}{
		{
			description: "ExistsToUnhealthy/NoCatalogs",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					// registryReconcilerFactory: fakeRegistryReconcilerFactory(true, nil),
					globalCatalogNamespace: "global",
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "default",
								},
								Spec: &v1alpha1.SubscriptionSpec{},
							},
						},
					},
				},
			},
			args: args{
				in: newSubscriptionExistsState(
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: "default",
						},
						Spec:   &v1alpha1.SubscriptionSpec{},
						Status: v1alpha1.SubscriptionStatus{},
					},
				),
			},
			want: want{
				out: newCatalogUnhealthyState(
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: "default",
						},
						Spec: &v1alpha1.SubscriptionSpec{},
						Status: v1alpha1.SubscriptionStatus{
							Conditions: []v1alpha1.SubscriptionCondition{
								{
									Type:               v1alpha1.SubscriptionCatalogSourcesUnhealthy,
									Status:             corev1.ConditionTrue,
									Reason:             v1alpha1.NoCatalogSourcesFound,
									Message:            "dependency resolution requires at least one catalogsource",
									LastTransitionTime: &now,
								},
							},
							LastUpdated: now,
						},
					},
				),
			},
		},
		{
			description: "ExistsToUnhealthy/Catalogs/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now:                       nowFunc,
					registryReconcilerFactory: fakeRegistryReconcilerFactory(false, nil),
					globalCatalogNamespace:    "global",
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							catalogSource("ns", "cs-0"),
							catalogSource("ns", "cs-1"),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Spec: &v1alpha1.SubscriptionSpec{
									CatalogSourceNamespace: "ns",
									CatalogSource:          "cs-0",
								},
								Status: v1alpha1.SubscriptionStatus{
									CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
										catalogHealth("ns", "cs-0", &earlier, false),
										catalogHealth("ns", "cs-1", &earlier, false),
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
									},
									LastUpdated: earlier,
								},
							},
						},
					},
				},
			},
			args: args{
				in: newSubscriptionExistsState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, false),
							catalogHealth("ns", "cs-1", &earlier, false),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newCatalogUnhealthyState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, false),
							catalogHealth("ns", "cs-1", &earlier, false),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "ExistsToHealthy/Catalogs/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now:                       nowFunc,
					registryReconcilerFactory: fakeRegistryReconcilerFactory(true, nil),
					globalCatalogNamespace:    "ns",
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							catalogSource("ns", "cs-0"),
							catalogSource("ns", "cs-1"),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Spec: &v1alpha1.SubscriptionSpec{
									CatalogSourceNamespace: "ns",
									CatalogSource:          "cs-0",
								},
								Status: v1alpha1.SubscriptionStatus{
									CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
										catalogHealth("ns", "cs-0", &earlier, true),
										catalogHealth("ns", "cs-1", &earlier, true),
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
									},
									LastUpdated: earlier,
								},
							},
						},
					},
				},
			},
			args: args{
				in: newSubscriptionExistsState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, true),
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newCatalogHealthyState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, true),
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "ExistsToHealthy/Catalogs/Changes/GlobalAdded",
			fields: fields{
				config: &fakeReconcilerConfig{
					now:                       nowFunc,
					registryReconcilerFactory: fakeRegistryReconcilerFactory(true, nil),
					globalCatalogNamespace:    "global",
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							catalogSource("ns", "cs-0"),
							catalogSource("ns", "cs-1"),
							catalogSource("global", "cs-g"),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Spec: &v1alpha1.SubscriptionSpec{
									CatalogSourceNamespace: "ns",
									CatalogSource:          "cs-0",
								},
								Status: v1alpha1.SubscriptionStatus{
									CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
										catalogHealth("ns", "cs-0", &earlier, true),
										catalogHealth("ns", "cs-1", &earlier, true),
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
									},
									LastUpdated: earlier,
								},
							},
						},
					},
				},
			},
			args: args{
				in: newSubscriptionExistsState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, true),
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newCatalogHealthyState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &now, true),
							catalogHealth("ns", "cs-1", &now, true),
							catalogHealth("global", "cs-g", &now, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.CatalogSourcesAdded, "all available catalogsources are healthy", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			rec := newFakeCatalogHealthReconciler(ctx, t, tt.fields.config)

			out, err := rec.Reconcile(ctx, tt.args.in)
			require.Equal(t, tt.want.err, err)
			require.Equal(t, reflect.TypeOf(tt.want.out), reflect.TypeOf(out))

			// Ensure the client's view of the subscription matches the typestate's
			sub := out.(SubscriptionState).Subscription()
			clusterSub, err := rec.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).Get(sub.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, sub, clusterSub)
		})
	}
}

func fakeRegistryReconcilerFactory(healthy bool, err error) *olmfakes.FakeRegistryReconcilerFactory {
	return &olmfakes.FakeRegistryReconcilerFactory{
		ReconcilerForSourceStub: func(*v1alpha1.CatalogSource) registryreconciler.RegistryReconciler {
			return &olmfakes.FakeRegistryReconciler{
				CheckRegistryServerStub: func(*v1alpha1.CatalogSource) (bool, error) {
					return healthy, err
				},
			}
		},
	}
}

type existingObjs struct {
	clientObjs []runtime.Object
}

func (e existingObjs) fakeClientset(t *testing.T) *versionedfake.ReactionForwardingClientsetDecorator {
	return versionedfake.NewReactionForwardingClientsetDecorator(e.clientObjs, clientfake.WithSelfLinks(t))
}

type fakeReconcilerConfig struct {
	now                       func() *metav1.Time
	registryReconcilerFactory registryreconciler.RegistryReconcilerFactory
	globalCatalogNamespace    string
	subscriptionNamespace     string
	existingObjs              existingObjs
}

func newFakeCatalogHealthReconciler(ctx context.Context, t *testing.T, config *fakeReconcilerConfig) *catalogHealthReconciler {
	fakeClient := config.existingObjs.fakeClientset(t)
	versionedFactory := externalversions.NewSharedInformerFactoryWithOptions(fakeClient, time.Minute)
	catalogInformer := versionedFactory.Operators().V1alpha1().CatalogSources()
	lister := operatorlister.NewLister()
	lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catalogInformer.Lister())

	rec := &catalogHealthReconciler{
		now:                       config.now,
		client:                    fakeClient,
		catalogLister:             lister.OperatorsV1alpha1().CatalogSourceLister(),
		registryReconcilerFactory: config.registryReconcilerFactory,
		globalCatalogNamespace:    config.globalCatalogNamespace,
	}

	versionedFactory.Start(ctx.Done())
	versionedFactory.WaitForCacheSync(ctx.Done())

	return rec
}

// Helper functions to shortcut to a particular state.
// They should not be used outside of testing.

func newSubscriptionExistsState(sub *v1alpha1.Subscription) SubscriptionExistsState {
	return &subscriptionExistsState{
		SubscriptionState: NewSubscriptionState(sub),
	}
}

func newCatalogHealthState(sub *v1alpha1.Subscription) CatalogHealthState {
	return &catalogHealthState{
		SubscriptionExistsState: newSubscriptionExistsState(sub),
	}
}

func newCatalogHealthKnownState(sub *v1alpha1.Subscription) CatalogHealthKnownState {
	return &catalogHealthKnownState{
		CatalogHealthState: newCatalogHealthState(sub),
	}
}

func newCatalogHealthyState(sub *v1alpha1.Subscription) CatalogHealthyState {
	return &catalogHealthyState{
		CatalogHealthKnownState: newCatalogHealthKnownState(sub),
	}
}

func newCatalogUnhealthyState(sub *v1alpha1.Subscription) CatalogUnhealthyState {
	return &catalogUnhealthyState{
		CatalogHealthKnownState: newCatalogHealthKnownState(sub),
	}
}

// Helper functions for generating OLM resources.

func catalogSource(namespace, name string) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			SelfLink: clientfake.BuildSelfLink(v1alpha1.SchemeGroupVersion.String(), "catalogsources", namespace, name),
			UID:       types.UID(name),
		},
	}
}
