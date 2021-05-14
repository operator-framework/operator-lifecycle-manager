package install

// See kubernetes/pkg/kubectl/rollout_status.go

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const TimedOutReason = "ProgressDeadlineExceeded"

// Status returns a message describing deployment status, and a bool value indicating if the status is considered done.
func DeploymentStatus(deployment *appsv1.Deployment) (string, bool, error) {
	if deployment.Generation <= deployment.Status.ObservedGeneration {
		// check if deployment has timed out
		cond := getDeploymentCondition(deployment.Status, appsv1.DeploymentProgressing)
		if cond != nil && cond.Reason == TimedOutReason {
			return "", false, fmt.Errorf("deployment %q exceeded its progress deadline", deployment.Name)
		}
		// not all replicas are up yet
		if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d out of %d new replicas have been updated...\n", deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas), false, nil
		}
		// waiting for old replicas to be cleaned up
		if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d old replicas are pending termination...\n", deployment.Status.Replicas-deployment.Status.UpdatedReplicas), false, nil
		}
		if c := getDeploymentCondition(deployment.Status, appsv1.DeploymentAvailable); c == nil || c.Status != corev1.ConditionTrue {
			msg := fmt.Sprintf("deployment %q missing condition %q", deployment.Name, appsv1.DeploymentAvailable)
			if c != nil {
				msg = fmt.Sprintf("deployment %q not available: %s", deployment.Name, c.Message)
			}
			return fmt.Sprintf("Waiting for rollout to finish: %s\n", msg), false, nil
		}
		// deployment is finished
		return fmt.Sprintf("deployment %q successfully rolled out\n", deployment.Name), true, nil
	}
	return fmt.Sprintf("Waiting for deployment spec update to be observed...\n"), false, nil
}

func getDeploymentCondition(status appsv1.DeploymentStatus, condType appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}
