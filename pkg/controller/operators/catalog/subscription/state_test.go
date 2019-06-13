package subscription

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
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
					Status: v1alpha1.SubscriptionStatus{},
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
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.NoCatalogSourcesFound, "dependency resolution requires at least one catalogsource", &now),
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
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &now),
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
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
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
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
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
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.UnhealthyCatalogSourceFound, "targeted catalogsource ns/cs-0 unhealthy", &earlier),
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
							catalogHealth("ns", "cs-0", &now, true),
							catalogHealth("ns", "cs-1", &now, true),
						},
						Conditions: []v1alpha1.SubscriptionCondition{
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.CatalogSourcesUpdated, "all available catalogsources are healthy", &now),
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
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
							unhealthyCondition(corev1.ConditionTrue, v1alpha1.CatalogSourcesUpdated, "targeted catalogsource ns/cs-0 missing", &now),
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
							unhealthyCondition(corev1.ConditionFalse, v1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy", &earlier),
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
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := tt.fields.existingObjs.fakeClientset(t).OperatorsV1alpha1().Subscriptions(tt.fields.namespace)
			transitioned, err := tt.fields.state.UpdateHealth(tt.args.now, fakeClient, tt.args.catalogHealth...)
			require.Equal(t, tt.want.err, err)
			require.EqualValues(t, tt.want.transitioned.Subscription(), transitioned.Subscription())

			if tt.want.transitioned != nil {
				require.Equal(t, tt.want.terminal, transitioned.Terminal())
			}
		})
	}
}

func unhealthyCondition(status corev1.ConditionStatus, reason, message string, time *metav1.Time) v1alpha1.SubscriptionCondition {
	return v1alpha1.SubscriptionCondition{
		Type:               v1alpha1.SubscriptionCatalogSourcesUnhealthy,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time,
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
