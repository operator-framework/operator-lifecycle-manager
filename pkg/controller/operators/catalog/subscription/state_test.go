package subscription

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestUpdateHealth(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	earlier := metav1.NewTime(now.Add(-time.Minute))

	type fields struct {
		existingObjs existingObjs
		namespace    string
		state        CatalogHealthState
	}
	type args struct {
		now           *metav1.Time
		catalogHealth []v1alpha1.SubscriptionCatalogHealth
	}
	type want struct {
		transitioned CatalogHealthState
		terminal     bool
		err          error
	}

	tests := []struct {
		description string
		fields      fields
		args        args
		want        want
	}{
		{
			description: "CatalogHealthState/NoCatalogSources/NoConditions/Unhealthy/ConditionsAdded",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
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
				namespace: "ns",
				state: newCatalogHealthState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
				}),
			},
			args: args{
				now: &now,
			},
			want: want{
				transitioned: newCatalogUnhealthyState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSourceNamespace: "ns",
						CatalogSource:          "cs-0",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.NoCatalogSourcesFound, "dependency resolution requires at least one catalogsource", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "CatalogHealthState/CatalogSources/NoConditions/Unhealthy/CatalogsAdded",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
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
				namespace: "ns",
				state: newCatalogHealthState(&v1alpha1.Subscription{
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
						},
					},
				}),
			},
			args: args{
				now: &now,
				catalogHealth: []v1alpha1.SubscriptionCatalogHealth{
					catalogHealth("ns", "cs-0", &now, false),
				},
			},
			want: want{
				transitioned: newCatalogUnhealthyState(&v1alpha1.Subscription{
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
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "CatalogHealthState/CatalogSources/Conditions/Unhealthy/NoChanges",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
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
									catalogHealth("ns", "cs-1", &earlier, true),
								},
								Conditions: []v1alpha1.SubscriptionCondition{
									catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newCatalogHealthState(&v1alpha1.Subscription{
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
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				catalogHealth: []v1alpha1.SubscriptionCatalogHealth{
					catalogHealth("ns", "cs-0", &now, false),
					catalogHealth("ns", "cs-1", &now, true),
				},
			},
			want: want{
				transitioned: newCatalogUnhealthyState(&v1alpha1.Subscription{
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
							catalogHealth("ns", "cs-1", &earlier, true),
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
			description: "CatalogHealthState/CatalogSources/Conditions/Unhealthy/ToHealthy",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
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
									catalogHealth("ns", "cs-1", &earlier, true),
								},
								Conditions: []v1alpha1.SubscriptionCondition{
									catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newCatalogHealthState(&v1alpha1.Subscription{
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
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				catalogHealth: []v1alpha1.SubscriptionCatalogHealth{
					catalogHealth("ns", "cs-0", &now, true),
					catalogHealth("ns", "cs-1", &now, true),
				},
			},
			want: want{
				transitioned: newCatalogHealthyState(&v1alpha1.Subscription{
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
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.CatalogSourcesUpdated, "all available catalogsources are healthy", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "CatalogHealthState/CatalogSources/Conditions/MissingTargeted/Healthy/ToUnhealthy",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
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
				namespace: "ns",
				state: newCatalogHealthState(&v1alpha1.Subscription{
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
							catalogHealth("ns", "cs-1", &earlier, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				catalogHealth: []v1alpha1.SubscriptionCatalogHealth{
					catalogHealth("ns", "cs-1", &now, true),
					catalogHealth("global", "cs-g", &now, true),
				},
			},
			want: want{
				transitioned: newCatalogUnhealthyState(&v1alpha1.Subscription{
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
							catalogHealth("ns", "cs-1", &now, true),
							catalogHealth("global", "cs-g", &now, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							catalogUnhealthyCondition(corev1.ConditionTrue, v1alpha1.CatalogSourcesUpdated, "targeted catalogsource ns/cs-0 missing", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "CatalogHealthState/CatalogSources/Conditions/Healthy/NoChanges",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
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
				namespace: "ns",
				state: newCatalogHealthState(&v1alpha1.Subscription{
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
			args: args{
				now: &now,
				catalogHealth: []v1alpha1.SubscriptionCatalogHealth{
					catalogHealth("ns", "cs-0", &now, true),
					catalogHealth("ns", "cs-1", &now, true),
				},
			},
			want: want{
				transitioned: newCatalogHealthyState(&v1alpha1.Subscription{
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
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := tt.fields.existingObjs.fakeClientset(t).OperatorsV1alpha1().Subscriptions(tt.fields.namespace)
			transitioned, err := tt.fields.state.UpdateHealth(tt.args.now, fakeClient, tt.args.catalogHealth...)
			require.Equal(t, tt.want.err, err)
			require.Equal(t, tt.want.transitioned, transitioned)

			if tt.want.transitioned != nil {
				require.Equal(t, tt.want.terminal, transitioned.Terminal())

				// Ensure the client's view of the subscription matches the typestate's
				sub := transitioned.(SubscriptionState).Subscription()
				clusterSub, err := fakeClient.Get(context.TODO(), sub.GetName(), metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, sub, clusterSub)
			}

		})
	}
}

func TestCheckReference(t *testing.T) {
	type fields struct {
		state InstallPlanState
	}
	type want struct {
		transitioned InstallPlanState
		terminal     bool
	}

	tests := []struct {
		description string
		fields      fields
		want        want
	}{
		{
			description: "NoReference/FromInstallPlanState/ToNoInstallPlanReferencedState",
			fields: fields{
				state: newInstallPlanState(newSubscriptionExistsState(
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: "ns",
						},
					},
				)),
			},
			want: want{
				transitioned: newNoInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
				terminal: false,
			},
		},
		{
			description: "NoReference/FromNoInstallPlanReferencedState/ToNoInstallPlanReferencedState",
			fields: fields{
				state: newNoInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
			want: want{
				transitioned: newNoInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
				terminal: false,
			},
		},
		{
			description: "NoReference/FromInstallPlanReferencedState/ToNoInstallPlanReferencedState",
			fields: fields{
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
			want: want{
				transitioned: newNoInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
				terminal: false,
			},
		},
		{
			description: "Reference/FromInstallPlanState/ToInstallPlanReferencedState",
			fields: fields{
				state: newInstallPlanState(newSubscriptionExistsState(
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
						},
					},
				)),
			},
			want: want{
				transitioned: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
					},
				}),
				terminal: false,
			},
		},
		{
			description: "Reference/FromInstallPlanReferencedState/ToInstallPlanReferencedState",
			fields: fields{
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
					},
				}),
			},
			want: want{
				transitioned: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
					},
				}),
				terminal: false,
			},
		},
		{
			description: "Reference/FromNoInstallPlanReferencedState/ToInstallPlanReferencedState",
			fields: fields{
				state: newNoInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
					},
				}),
			},
			want: want{
				transitioned: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						InstallPlanRef: &corev1.ObjectReference{
							Namespace: "ns",
							Name:      "ip",
						},
					},
				}),
				terminal: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			transitioned := tt.fields.state.CheckReference()
			require.Equal(t, tt.want.transitioned, transitioned)
			require.Equal(t, tt.want.terminal, transitioned.Terminal())
		})
	}
}

func TestInstallPlanNotFound(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	earlier := metav1.NewTime(now.Add(-time.Minute))

	type fields struct {
		existingObjs existingObjs
		namespace    string
		state        InstallPlanReferencedState
	}
	type args struct {
		now *metav1.Time
	}
	type want struct {
		transitioned InstallPlanReferencedState
		terminal     bool
		err          error
	}
	tests := []struct {
		description string
		fields      fields
		args        args
		want        want
	}{
		{
			description: "InstallPlanReferencedState/NoConditions/ToInstallPlanMissingState/Update",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
			},
			want: want{
				transitioned: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/ToInstallPlanMissingState/NoUpdate",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
			},
			want: want{
				transitioned: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanMissingState/Conditions/ToInstallPlanMissingState/NoUpdate",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
			},
			want: want{
				transitioned: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/ToInstallPlanMissingState/Update/RemovesFailedAndPending",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
									planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
									planPendingCondition(corev1.ConditionTrue, "", "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &earlier),
							planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
							planPendingCondition(corev1.ConditionTrue, "", "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
			},
			want: want{
				transitioned: newInstallPlanMissingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, v1alpha1.ReferencedInstallPlanNotFound, "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := tt.fields.existingObjs.fakeClientset(t).OperatorsV1alpha1().Subscriptions(tt.fields.namespace)
			transitioned, err := tt.fields.state.InstallPlanNotFound(tt.args.now, fakeClient)
			require.Equal(t, tt.want.err, err)
			require.Equal(t, tt.want.transitioned, transitioned)

			if tt.want.transitioned != nil {
				require.Equal(t, tt.want.terminal, transitioned.Terminal())

				// Ensure the client's view of the subscription matches the typestate's
				sub := transitioned.(SubscriptionState).Subscription()
				clusterSub, err := fakeClient.Get(context.TODO(), sub.GetName(), metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, sub, clusterSub)
			}
		})
	}
}

func TestCheckInstallPlanStatus(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	earlier := metav1.NewTime(now.Add(-time.Minute))

	type fields struct {
		existingObjs existingObjs
		namespace    string
		state        InstallPlanReferencedState
	}
	type args struct {
		now    *metav1.Time
		status *v1alpha1.InstallPlanStatus
	}
	type want struct {
		transitioned InstallPlanReferencedState
		terminal     bool
		err          error
	}
	tests := []struct {
		description string
		fields      fields
		args        args
		want        want
	}{
		{
			description: "InstallPlanReferencedState/NoConditions/InstallPlanNotYetReconciled/ToInstallPlanPendingState/Update",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now:    &now,
				status: &v1alpha1.InstallPlanStatus{},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/InstallPlanNotYetReconciled/ToInstallPlanPendingState/NoUpdate",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now:    &now,
				status: &v1alpha1.InstallPlanStatus{},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanPendingState/Conditions/InstallPlanNotYetReconciled/ToInstallPlanPendingState/NoUpdate",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now:    &now,
				status: &v1alpha1.InstallPlanStatus{},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/InstallPlanNotYetReconciled/ToInstallPlanPendingState/Update/RemovesFailedAndMissing",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planMissingCondition(corev1.ConditionTrue, "", "", &earlier),
									planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
									planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, "", "", &earlier),
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &earlier),
							planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now:    &now,
				status: &v1alpha1.InstallPlanStatus{},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, v1alpha1.InstallPlanNotYetReconciled, "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/NoConditions/RequiresApproval/ToInstallPlanPendingState/Update",
			fields: fields{
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
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseRequiresApproval,
				},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/RequiresApproval/ToInstallPlanPendingState/NoUpdate",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseRequiresApproval,
				},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/RequiresApproval/ToInstallPlanPendingState/Update/RemovesMissingAndFailed",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planMissingCondition(corev1.ConditionTrue, "", "", &earlier),
									planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &earlier),
									planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, "", "", &earlier),
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &earlier),
							planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseRequiresApproval,
				},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseRequiresApproval), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/NoConditions/Installing/ToInstallPlanPendingState/Update",
			fields: fields{
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
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseInstalling,
				},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/Installing/ToInstallPlanPendingState/NoUpdate",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseInstalling,
				},
			},
			want: want{
				transitioned: newInstallPlanPendingState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planPendingCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanPhaseInstalling), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/NoConditions/Failed/ToInstallPlanFailedState/Update/NoReason",
			fields: fields{
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
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseFailed,
				},
			},
			want: want{
				transitioned: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanFailed), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/Failed/ToInstallPlanFailedState/NoUpdate/NoReason",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanFailed), "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanFailed), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseFailed,
				},
			},
			want: want{
				transitioned: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanFailed), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/NoConditions/Failed/ToInstallPlanFailedState/Update/InstallPlanReasonComponentFailed",
			fields: fields{
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
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseFailed,
					Conditions: []v1alpha1.InstallPlanCondition{
						{
							Type:   v1alpha1.InstallPlanInstalled,
							Status: corev1.ConditionFalse,
							Reason: v1alpha1.InstallPlanReasonComponentFailed,
						},
					},
				},
			},
			want: want{
				transitioned: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &now),
						},
						LastUpdated: now,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/Failed/ToInstallPlanFailedState/NoUpdate/InstallPlanReasonComponentFailed",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseFailed,
					Conditions: []v1alpha1.InstallPlanCondition{
						{
							Type:   v1alpha1.InstallPlanInstalled,
							Status: corev1.ConditionFalse,
							Reason: v1alpha1.InstallPlanReasonComponentFailed,
						},
					},
				},
			},
			want: want{
				transitioned: newInstallPlanFailedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planFailedCondition(corev1.ConditionTrue, string(v1alpha1.InstallPlanReasonComponentFailed), "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
		},
		{
			description: "InstallPlanReferencedState/Conditions/Installed/ToInstallPlanInstalledState/Update/RemovesMissingPendingAndFailed",
			fields: fields{
				existingObjs: existingObjs{
					clientObjs: []runtime.Object{
						&v1alpha1.Subscription{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "sub",
								Namespace: "ns",
							},
							Status: v1alpha1.SubscriptionStatus{
								Conditions: []v1alpha1.SubscriptionCondition{
									planMissingCondition(corev1.ConditionTrue, "", "", &earlier),
									planPendingCondition(corev1.ConditionTrue, "", "", &earlier),
									planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
								},
								LastUpdated: earlier,
							},
						},
					},
				},
				namespace: "ns",
				state: newInstallPlanReferencedState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						Conditions: []v1alpha1.SubscriptionCondition{
							planMissingCondition(corev1.ConditionTrue, "", "", &earlier),
							planPendingCondition(corev1.ConditionTrue, "", "", &earlier),
							planFailedCondition(corev1.ConditionTrue, "", "", &earlier),
						},
						LastUpdated: earlier,
					},
				}),
			},
			args: args{
				now: &now,
				status: &v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseComplete,
				},
			},
			want: want{
				transitioned: newInstallPlanInstalledState(&v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: "ns",
					},
					Status: v1alpha1.SubscriptionStatus{
						LastUpdated: now,
					},
				}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := tt.fields.existingObjs.fakeClientset(t).OperatorsV1alpha1().Subscriptions(tt.fields.namespace)
			transitioned, err := tt.fields.state.CheckInstallPlanStatus(tt.args.now, fakeClient, tt.args.status)
			require.Equal(t, tt.want.err, err)
			require.Equal(t, tt.want.transitioned, transitioned)

			if tt.want.transitioned != nil {
				require.Equal(t, tt.want.terminal, transitioned.Terminal())

				// Ensure the client's view of the subscription matches the typestate's
				sub := transitioned.(SubscriptionState).Subscription()
				clusterSub, err := fakeClient.Get(context.TODO(), sub.GetName(), metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, sub, clusterSub)
			}
		})
	}
}

func catalogHealth(namespace, name string, lastUpdated *metav1.Time, healthy bool) v1alpha1.SubscriptionCatalogHealth {
	return v1alpha1.SubscriptionCatalogHealth{
		CatalogSourceRef: &corev1.ObjectReference{
			Kind:       v1alpha1.CatalogSourceKind,
			Namespace:  namespace,
			Name:       name,
			UID:        types.UID(name),
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		LastUpdated: lastUpdated,
		Healthy:     healthy,
	}
}

func subscriptionCondition(conditionType v1alpha1.SubscriptionConditionType, status corev1.ConditionStatus, reason, message string, time *metav1.Time) v1alpha1.SubscriptionCondition {
	return v1alpha1.SubscriptionCondition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time,
	}
}

func catalogUnhealthyCondition(status corev1.ConditionStatus, reason, message string, time *metav1.Time) v1alpha1.SubscriptionCondition {
	return subscriptionCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy, status, reason, message, time)
}

func planMissingCondition(status corev1.ConditionStatus, reason, message string, time *metav1.Time) v1alpha1.SubscriptionCondition {
	return subscriptionCondition(v1alpha1.SubscriptionInstallPlanMissing, status, reason, message, time)
}

func planFailedCondition(status corev1.ConditionStatus, reason, message string, time *metav1.Time) v1alpha1.SubscriptionCondition {
	return subscriptionCondition(v1alpha1.SubscriptionInstallPlanFailed, status, reason, message, time)
}

func planPendingCondition(status corev1.ConditionStatus, reason, message string, time *metav1.Time) v1alpha1.SubscriptionCondition {
	return subscriptionCondition(v1alpha1.SubscriptionInstallPlanPending, status, reason, message, time)
}
