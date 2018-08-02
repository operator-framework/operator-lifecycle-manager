package install

import (
	"testing"

	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDeploymentStatusViewerStatus(t *testing.T) {
	tests := []struct {
		generation   int64
		specReplicas int32
		status       apps.DeploymentStatus
		msg          string
		done         bool
	}{
		{
			generation:   0,
			specReplicas: 1,
			status: apps.DeploymentStatus{
				ObservedGeneration:  1,
				Replicas:            1,
				UpdatedReplicas:     0,
				AvailableReplicas:   1,
				UnavailableReplicas: 0,
			},

			msg:  "Waiting for rollout to finish: 0 out of 1 new replicas have been updated...\n",
			done: false,
		},
		{
			generation:   1,
			specReplicas: 1,
			status: apps.DeploymentStatus{
				ObservedGeneration:  1,
				Replicas:            2,
				UpdatedReplicas:     1,
				AvailableReplicas:   2,
				UnavailableReplicas: 0,
			},

			msg:  "Waiting for rollout to finish: 1 old replicas are pending termination...\n",
			done: false,
		},
		{
			generation:   1,
			specReplicas: 2,
			status: apps.DeploymentStatus{
				ObservedGeneration:  1,
				Replicas:            2,
				UpdatedReplicas:     2,
				AvailableReplicas:   1,
				UnavailableReplicas: 1,
			},

			msg:  "Waiting for rollout to finish: 1 of 2 updated replicas are available...\n",
			done: false,
		},
		{
			generation:   1,
			specReplicas: 2,
			status: apps.DeploymentStatus{
				ObservedGeneration:  1,
				Replicas:            2,
				UpdatedReplicas:     2,
				AvailableReplicas:   2,
				UnavailableReplicas: 0,
			},

			msg:  "deployment \"foo\" successfully rolled out\n",
			done: true,
		},
		{
			generation:   2,
			specReplicas: 2,
			status: apps.DeploymentStatus{
				ObservedGeneration:  1,
				Replicas:            2,
				UpdatedReplicas:     2,
				AvailableReplicas:   2,
				UnavailableReplicas: 0,
			},

			msg:  "Waiting for deployment spec update to be observed...\n",
			done: false,
		},
	}

	for _, test := range tests {
		d := &apps.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "bar",
				Name:       "foo",
				UID:        "8764ae47-9092-11e4-8393-42010af018ff",
				Generation: test.generation,
			},
			Spec: apps.DeploymentSpec{
				Replicas: &test.specReplicas,
			},
			Status: test.status,
		}
		msg, done, err := DeploymentStatus(d)
		if err != nil {
			t.Fatalf("DeploymentStatusViewer.Status(): %v", err)
		}
		if done != test.done || msg != test.msg {
			t.Errorf("DeploymentStatusViewer.Status() for deployment with generation %d, %d replicas specified, and status %+v returned %q, %t, want %q, %t",
				test.generation,
				test.specReplicas,
				test.status,
				msg,
				done,
				test.msg,
				test.done,
			)
		}
	}
}
