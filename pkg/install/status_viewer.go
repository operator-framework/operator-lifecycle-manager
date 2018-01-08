package install

// See kubernetes/pkg/kubectl/rollout_status.go

import (
	"fmt"

	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
)

const TimedOutReason = "ProgressDeadlineExceeded"

// Status returns a message describing deployment status, and a bool value indicating if the status is considered done.
func DeploymentStatus(deployment *extensionsv1beta1.Deployment) (string, bool, error) {
	if deployment.Generation <= deployment.Status.ObservedGeneration {
		cond := getDeploymentCondition(deployment.Status, extensionsv1beta1.DeploymentProgressing)
		if cond != nil && cond.Reason == TimedOutReason {
			return "", false, fmt.Errorf("deployment %q exceeded its progress deadline", deployment.Name)
		}
		if deployment.Spec.Replicas != nil && deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d out of %d new replicas have been updated...\n", deployment.Status.UpdatedReplicas, *deployment.Spec.Replicas), false, nil
		}
		if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d old replicas are pending termination...\n", deployment.Status.Replicas-deployment.Status.UpdatedReplicas), false, nil
		}
		if deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("Waiting for rollout to finish: %d of %d updated replicas are available...\n", deployment.Status.AvailableReplicas, deployment.Status.UpdatedReplicas), false, nil
		}
		return fmt.Sprintf("deployment %q successfully rolled out\n", deployment.Name), true, nil
	}
	return fmt.Sprintf("Waiting for deployment spec update to be observed...\n"), false, nil
}

func getDeploymentCondition(status extensionsv1beta1.DeploymentStatus, condType extensionsv1beta1.DeploymentConditionType) *extensionsv1beta1.DeploymentCondition {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == condType {
			return &c
		}
	}
	return nil
}
