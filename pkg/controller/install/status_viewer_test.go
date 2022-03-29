package install

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDeploymentStatusViewerStatus(t *testing.T) {
	tests := []struct {
		generation int64
		status     appsv1.DeploymentStatus
		err        error
		msg        string
		done       bool
	}{
		{
			status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentProgressing,
						Reason: TimedOutReason,
					},
				},
			},
			err:  fmt.Errorf("deployment \"foo\" exceeded its progress deadline"),
			done: false,
		},
		{
			status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentProgressing,
						Reason: "NotTimedOut",
					},
					{
						Type:   appsv1.DeploymentAvailable,
						Status: corev1.ConditionTrue,
					},
				},
			},
			msg:  "deployment \"foo\" is up-to-date and available",
			done: true,
		},
		{
			generation: 1,
			status: appsv1.DeploymentStatus{
				ObservedGeneration: 0,
			},
			msg:  "waiting for spec update of deployment \"foo\" to be observed...",
			done: false,
		},
		{
			status: appsv1.DeploymentStatus{
				Replicas:        5,
				UpdatedReplicas: 3,
			},
			msg:  "deployment \"foo\" waiting for 2 outdated replica(s) to be terminated",
			done: false,
		},
		{
			status: appsv1.DeploymentStatus{},
			msg:    fmt.Sprintf("deployment \"foo\" not available: missing condition %q", appsv1.DeploymentAvailable),
			done:   false,
		},
		{
			status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionFalse,
						Message: "test message",
					},
				},
			},
			msg:  "deployment \"foo\" not available: test message",
			done: false,
		},
		{
			status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionUnknown,
						Message: "test message",
					},
				},
			},
			msg:  "deployment \"foo\" not available: test message",
			done: false,
		},
		{
			status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentAvailable,
						Status: corev1.ConditionTrue,
					},
				},
			},
			msg:  "deployment \"foo\" is up-to-date and available",
			done: true,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("%d", i+1), func(t *testing.T) {
			d := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "bar",
					Name:       "foo",
					Generation: test.generation,
				},
				Status: test.status,
			}
			msg, done, err := DeploymentStatus(d)
			assert := assert.New(t)
			if test.err == nil {
				assert.NoError(err)
			} else {
				assert.EqualError(err, test.err.Error())
			}
			assert.Equal(test.done, done)
			assert.Equal(test.msg, msg)
		})
	}
}
