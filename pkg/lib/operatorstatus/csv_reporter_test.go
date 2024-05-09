package operatorstatus

import (
	"testing"
	"time"

	"github.com/blang/semver/v4"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilclock "k8s.io/utils/clock/testing"
)

func TestGetNewStatus(t *testing.T) {
	fakeClock := utilclock.NewFakeClock(time.Now())

	tests := []struct {
		name     string
		existing *configv1.ClusterOperatorStatus
		context  *csvEventContext
		expected *configv1.ClusterOperatorStatus
	}{
		// A CSV is being worked on. It has not succeeded or failed yet.
		{
			name: "WithCSVInProgress",
			context: &csvEventContext{
				Name:           "foo",
				CurrentDeleted: false,
				Current: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "foo-namespace",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version.OperatorVersion{
							Version: semver.Version{
								Major: 1, Minor: 0, Patch: 0,
							},
						},
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase:   v1alpha1.CSVPhasePending,
						Reason:  v1alpha1.CSVReasonWaiting,
						Message: "Progressing towards 1.0.0",
					},
				},
			},

			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorDegraded,
						Status:             configv1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "Safe to upgrade",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						Message:            "Working toward 1.0.0",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				Versions: []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{
					{
						Group:     "",
						Resource:  "namespaces",
						Namespace: "",
						Name:      "foo-namespace",
					},
					{
						Group:     v1alpha1.GroupName,
						Resource:  clusterServiceVersionResource,
						Namespace: "foo-namespace",
						Name:      "foo",
					},
				},
			},
		},

		{
			name: "WithCSVInProgressAlreadyAvailableTrue",
			context: &csvEventContext{
				Name:           "foo",
				CurrentDeleted: false,
				Current: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "foo-namespace",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version.OperatorVersion{
							Version: semver.Version{
								Major: 1, Minor: 0, Patch: 0,
							},
						},
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase:   v1alpha1.CSVPhasePending,
						Reason:  v1alpha1.CSVReasonWaiting,
						Message: "Progressing towards 1.0.0",
					},
				},
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorAvailable,
						Status:             configv1.ConditionTrue,
						Message:            "test message",
						Reason:             "test reason",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
			},

			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorAvailable,
						Status:             configv1.ConditionTrue,
						Message:            "test message",
						Reason:             "test reason",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorDegraded,
						Status:             configv1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "Safe to upgrade",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						Message:            "Working toward 1.0.0",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				RelatedObjects: []configv1.ObjectReference{
					{
						Group:     "",
						Resource:  "namespaces",
						Namespace: "",
						Name:      "foo-namespace",
					},
					{
						Group:     v1alpha1.GroupName,
						Resource:  clusterServiceVersionResource,
						Namespace: "foo-namespace",
						Name:      "foo",
					},
				},
			},
		},

		{
			name: "WithCSVInProgressAlreadyAvailableFalse",
			context: &csvEventContext{
				Name:           "foo",
				CurrentDeleted: false,
				Current: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "foo-namespace",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version.OperatorVersion{
							Version: semver.Version{
								Major: 1, Minor: 0, Patch: 0,
							},
						},
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase:   v1alpha1.CSVPhasePending,
						Reason:  v1alpha1.CSVReasonWaiting,
						Message: "Progressing towards 1.0.0",
					},
				},
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorAvailable,
						Status:             configv1.ConditionFalse,
						Message:            "test message",
						Reason:             "test reason",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
			},

			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorAvailable,
						Status:             configv1.ConditionFalse,
						Message:            "test message",
						Reason:             "test reason",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorDegraded,
						Status:             configv1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "Safe to upgrade",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						Message:            "Working toward 1.0.0",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				RelatedObjects: []configv1.ObjectReference{
					{
						Group:     "",
						Resource:  "namespaces",
						Namespace: "",
						Name:      "foo-namespace",
					},
					{
						Group:     v1alpha1.GroupName,
						Resource:  clusterServiceVersionResource,
						Namespace: "foo-namespace",
						Name:      "foo",
					},
				},
			},
		},

		{
			name: "WithCSVSuccessfullyInstalled",
			context: &csvEventContext{
				Name:           "foo",
				CurrentDeleted: false,
				Current: &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "foo",
						Namespace: "foo-namespace",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Version: version.OperatorVersion{
							Version: semver.Version{
								Major: 1, Minor: 0, Patch: 0,
							},
						},
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					},
				},
			},

			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorDegraded,
						Status:             configv1.ConditionFalse,
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "Safe to upgrade",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorAvailable,
						Status:             configv1.ConditionTrue,
						Message:            "ClusterServiceVersion foo-namespace/foo observed in phase Succeeded",
						Reason:             "ClusterServiceVersionSucceeded",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
					{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionFalse,
						Message:            "Deployed version 1.0.0",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				Versions: []configv1.OperandVersion{
					{
						Name:    "operator",
						Version: "snapshot",
					},
					{
						Name:    "foo",
						Version: "1.0.0",
					},
				},
				RelatedObjects: []configv1.ObjectReference{
					{
						Group:     "",
						Resource:  "namespaces",
						Namespace: "",
						Name:      "foo-namespace",
					},
					{
						Group:     v1alpha1.GroupName,
						Resource:  clusterServiceVersionResource,
						Namespace: "foo-namespace",
						Name:      "foo",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reporter := &csvStatusReporter{
				clock:          fakeClock,
				releaseVersion: "snapshot",
			}

			err := ownerutil.InferGroupVersionKind(tt.context.Current)
			require.NoError(t, err)

			statusWant := tt.expected
			statusGot := reporter.GetNewStatus(tt.existing, tt.context)

			assert.Equal(t, statusWant, statusGot)
		})
	}
}
