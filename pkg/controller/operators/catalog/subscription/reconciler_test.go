package subscription

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
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
					now:                    nowFunc,
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
			description: "ExistsToUnhealthy/InvalidCatalogSpec",
			fields: fields{
				config: &fakeReconcilerConfig{
					now:                    nowFunc,
					globalCatalogNamespace: "global",
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							func() *v1alpha1.CatalogSource {
								cs := catalogSource("ns", "cs-0")
								cs.Spec = v1alpha1.CatalogSourceSpec{
									SourceType: v1alpha1.SourceTypeGrpc,
								}
								cs.Status = v1alpha1.CatalogSourceStatus{
									Reason:  v1alpha1.CatalogSourceSpecInvalidError,
									Message: fmt.Sprintf("image and address unset: at least one must be set for sourcetype: %s", v1alpha1.SourceTypeGrpc),
								}

								return cs
							}(),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Spec: &v1alpha1.SubscriptionSpec{
									CatalogSourceNamespace: "ns",
									CatalogSource:          "cs-0",
								},
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
							Namespace: "ns",
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSourceNamespace: "ns",
							CatalogSource:          "cs-0",
						},
					},
				),
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
							catalogHealth("ns", "cs-0", &now, false),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.CatalogSourcesAdded, "targeted catalogsource ns/cs-0 unhealthy", &now),
						},
						LastUpdated: now,
					},
				}),
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
										catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
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
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
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
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
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
										catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
							catalogSource("global", "cs-g"),
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
										catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
							catalogHealth("global", "cs-g", &now, true),
							catalogHealth("ns", "cs-0", &now, true),
							catalogHealth("ns", "cs-1", &now, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.CatalogSourcesAdded, "all available catalogsources are healthy", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "HealthyToUnhealthy/InvalidCatalogSpec",
			fields: fields{
				config: &fakeReconcilerConfig{
					now:                    nowFunc,
					globalCatalogNamespace: "global",
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							func() *v1alpha1.CatalogSource {
								cs := catalogSource("ns", "cs-0")
								cs.Spec = v1alpha1.CatalogSourceSpec{
									SourceType: v1alpha1.SourceTypeGrpc,
								}
								cs.Status = v1alpha1.CatalogSourceStatus{
									Reason:  v1alpha1.CatalogSourceSpecInvalidError,
									Message: fmt.Sprintf("image and address unset: at least one must be set for sourcetype: %s", v1alpha1.SourceTypeGrpc),
								}

								return cs
							}(),
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
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
									},
									LastUpdated: earlier,
								},
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
							Namespace: "ns",
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSourceNamespace: "ns",
							CatalogSource:          "cs-0",
						},
						Status: v1alpha1.SubscriptionStatus{
							CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
								catalogHealth("ns", "cs-0", &earlier, true),
							},
							Conditions: []v1alpha1.SubscriptionCondition{
								catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
							},
							LastUpdated: earlier,
						},
					},
				),
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
							catalogHealth("ns", "cs-0", &now, false),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.CatalogSourcesUpdated, "targeted catalogsource ns/cs-0 unhealthy", &now),
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
			require.Equal(t, tt.want.out, out)

			// Ensure the client's view of the subscription matches the typestate's
			sub := out.(SubscriptionState).Subscription()
			clusterSub, err := rec.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).Get(context.TODO(), sub.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, sub, clusterSub)
		})
	}
}

func TestInstallPlanReconcile(t *testing.T) {
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
			description: "SubscriptionExistsToNoInstallPlanReferenced/NoConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
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
				}),
			},
			want: want{
				out: newNoInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
		},
		{
			description: "CatalogHealthyToNoInstallPlanReferenced/MixedConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
										catalogHealth("ns", "cs-0", &earlier, true),
										catalogHealth("ns", "cs-1", &earlier, true),
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
									},
									LastUpdated: earlier,
								},
							},
						},
					},
				},
			},
			args: args{
				in: newCatalogHealthyState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, true),
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: &noInstallPlanReferencedState{
					InstallPlanState: &installPlanState{
						SubscriptionExistsState: newCatalogHealthyState(&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
									catalogHealth("ns", "cs-0", &earlier, true),
									catalogHealth("ns", "cs-1", &earlier, true),
								},
								Conditions: []v1alpha1.SubscriptionCondition{
									catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
								},
								LastUpdated: earlier,
							},
						}),
					},
				},
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanNotFound/NoConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanNotFound/Conditions/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "CatalogHealthyToInstallPlanNotFound/MixedConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
										catalogHealth("ns", "cs-0", &earlier, true),
										catalogHealth("ns", "cs-1", &earlier, true),
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
									},
									LastUpdated: earlier,
								},
							},
						},
					},
				},
			},
			args: args{
				in: newCatalogHealthyState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
							catalogHealth("ns", "cs-0", &earlier, true),
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: &installPlanMissingState{
					InstallPlanKnownState: &installPlanKnownState{
						InstallPlanReferencedState: &installPlanReferencedState{
							InstallPlanState: &installPlanState{
								SubscriptionExistsState: newCatalogHealthyState(&v1alpha1.Subscription{
									ObjectMeta: metav1.ObjectMeta{
										Name:      "sub",
										Namespace: "ns",
									},
									Status: v1alpha1.SubscriptionStatus{
										InstallPlanRef: &corev1.ObjectReference{
											Namespace: "ns",
											Name:      "ip",
										},
										CatalogHealth: []v1alpha1.SubscriptionCatalogHealth{
											catalogHealth("ns", "cs-0", &earlier, true),
											catalogHealth("ns", "cs-1", &earlier, true),
										},
										Conditions: []v1alpha1.SubscriptionCondition{
											catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
											planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &now),
										},
										LastUpdated: now,
									},
								}),
							},
						},
					},
				},
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanPending/NotYetReconciled/NoConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							installPlan("ns", "ip"),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanNotYetReconciled), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanPending/Planning/NoConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhasePlanning,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhasePlanning), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanPending/Planning/Conditions/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhasePlanning,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhasePlanning), "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhasePlanning), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhasePlanning), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanPending/Installing/NoConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseInstalling,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanPending/RequiresApproval/NoConditions/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseRequiresApproval,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanPending/Installing/Conditions/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseInstalling,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanFailed/Failed/NoProjectedReason/Conditions/Installing/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseFailed,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, v1alpha1.InstallPlanFailed, "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanFailed/Failed/ProjectedReason/Conditions/Installing/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseFailed,
									Conditions: []v1alpha1.InstallPlanCondition{
										{
											Type:   v1alpha1.InstallPlanInstalled,
											Status: corev1.ConditionFalse,
											Reason: v1alpha1.InstallPlanReasonComponentFailed,
										},
									},
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanFailed/Failed/ProjectedReason/Conditions/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseFailed,
									Conditions: []v1alpha1.InstallPlanCondition{
										{
											Type:   v1alpha1.InstallPlanInstalled,
											Status: corev1.ConditionFalse,
											Reason: v1alpha1.InstallPlanReasonComponentFailed,
										},
									},
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanInstalled/Conditions/Installing/Changes",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseComplete,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
									},
									Conditions: []v1alpha1.SubscriptionCondition{
										planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanInstalledState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "SubscriptionExistsToInstallPlanInstalled/NoConditions/NoChanges",
			fields: fields{
				config: &fakeReconcilerConfig{
					now: nowFunc,
					existingObjs: existingObjs{
						clientObjs: []runtime.Object{
							withInstallPlanStatus(
								installPlan("ns", "ip"),
								&v1alpha1.InstallPlanStatus{
									Phase: v1alpha1.InstallPlanPhaseComplete,
								},
							),
							&v1alpha1.Subscription{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "sub",
									Namespace: "ns",
								},
								Status: v1alpha1.SubscriptionStatus{
									InstallPlanRef: &corev1.ObjectReference{
										Namespace: "ns",
										Name:      "ip",
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
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
			want: want{
				out: newInstallPlanInstalledState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			rec := newFakeInstallPlanReconciler(ctx, t, tt.fields.config)

			out, err := rec.Reconcile(ctx, tt.args.in)
			require.Equal(t, tt.want.err, err)
			require.Equal(t, tt.want.out, out)

			// Ensure the client's view of the subscription matches the typestate's
			sub := out.(SubscriptionState).Subscription()
			clusterSub, err := rec.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).Get(context.TODO(), sub.GetName(), metav1.GetOptions{})
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

func newFakeInstallPlanReconciler(ctx context.Context, t *testing.T, config *fakeReconcilerConfig) *installPlanReconciler {
	fakeClient := config.existingObjs.fakeClientset(t)
	versionedFactory := externalversions.NewSharedInformerFactoryWithOptions(fakeClient, time.Minute)
	ipInformer := versionedFactory.Operators().V1alpha1().InstallPlans()
	lister := operatorlister.NewLister()
	lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, ipInformer.Lister())

	rec := &installPlanReconciler{
		now:               config.now,
		client:            fakeClient,
		installPlanLister: lister.OperatorsV1alpha1().InstallPlanLister(),
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

func newNoInstallPlanReferencedState(sub *v1alpha1.Subscription) NoInstallPlanReferencedState {
	return &noInstallPlanReferencedState{
		InstallPlanState: newInstallPlanState(newSubscriptionExistsState(sub)),
	}
}

func newInstallPlanReferencedState(sub *v1alpha1.Subscription) InstallPlanReferencedState {
	return &installPlanReferencedState{
		InstallPlanState: newInstallPlanState(newSubscriptionExistsState(sub)),
	}
}

func newInstallPlanKnownState(sub *v1alpha1.Subscription) InstallPlanKnownState {
	return &installPlanKnownState{
		InstallPlanReferencedState: newInstallPlanReferencedState(sub),
	}
}

func newInstallPlanMissingState(sub *v1alpha1.Subscription) InstallPlanMissingState {
	return &installPlanMissingState{
		InstallPlanKnownState: newInstallPlanKnownState(sub),
	}
}

func newInstallPlanPendingState(sub *v1alpha1.Subscription) InstallPlanPendingState {
	return &installPlanPendingState{
		InstallPlanKnownState: newInstallPlanKnownState(sub),
	}
}

func newInstallPlanFailedState(sub *v1alpha1.Subscription) InstallPlanFailedState {
	return &installPlanFailedState{
		InstallPlanKnownState: newInstallPlanKnownState(sub),
	}
}

func newInstallPlanInstalledState(sub *v1alpha1.Subscription) InstallPlanInstalledState {
	return &installPlanInstalledState{
		InstallPlanKnownState: newInstallPlanKnownState(sub),
	}
}

// Helper functions for generating OLM resources.

func catalogSource(namespace, name string) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			SelfLink:  clientfake.BuildSelfLink(v1alpha1.SchemeGroupVersion.String(), "catalogsources", namespace, name),
			UID:       types.UID(name),
		},
	}
}

func installPlan(namespace, name string) *v1alpha1.InstallPlan {
	return &v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			SelfLink:  clientfake.BuildSelfLink(v1alpha1.SchemeGroupVersion.String(), "installplans", namespace, name),
			UID:       types.UID(name),
		},
	}
}

func withInstallPlanStatus(plan *v1alpha1.InstallPlan, status *v1alpha1.InstallPlanStatus) *v1alpha1.InstallPlan {
	if plan == nil {
		plan = &v1alpha1.InstallPlan{}
	}
	if status == nil {
		status = &v1alpha1.InstallPlanStatus{}
	}
	plan.Status = *status

	return plan
}
