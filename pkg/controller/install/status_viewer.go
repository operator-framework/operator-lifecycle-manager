package install

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
		progressing := getDeploymentCondition(deployment.Status, appsv1.DeploymentProgressing)
		if progressing != nil && progressing.Reason == TimedOutReason {
			return "", false, fmt.Errorf("deployment %q exceeded its progress deadline", deployment.Name)
		}

		if deployment.Status.Replicas > deployment.Status.UpdatedReplicas {
			return fmt.Sprintf("deployment %q waiting for %d outdated replica(s) to be terminated", deployment.Name, deployment.Status.Replicas-deployment.Status.UpdatedReplicas), false, nil
		}

		if available := getDeploymentCondition(deployment.Status, appsv1.DeploymentAvailable); available == nil || available.Status != corev1.ConditionTrue {
			msg := fmt.Sprintf("missing condition %q", appsv1.DeploymentAvailable)
			if available != nil {
				msg = available.Message
			}
			return fmt.Sprintf("deployment %q not available: %s", deployment.Name, msg), false, nil
		}

		return fmt.Sprintf("deployment %q is up-to-date and available", deployment.Name), true, nil
	}
	return fmt.Sprintf("waiting for spec update of deployment %q to be observed...", deployment.Name), false, nil
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
