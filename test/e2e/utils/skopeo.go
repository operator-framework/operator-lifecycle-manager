package utils

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	corev1 "k8s.io/api/core/v1"
)

func (r *Registry) skopeoCopy(newImage, newTag, oldImage, oldTag string) error {
	skopeoArgs := e2e.SkopeoCopyCmd(newImage, newTag, oldImage, "", r.Auth)
	c := ctx.Ctx().KubeClient()

	err := e2e.CreateSkopeoPod(c, skopeoArgs, r.Namespace)
	if err != nil {
		return fmt.Errorf("error creating skopeo pod: %s", err)
	}

	// wait for skopeo pod to exit successfully
	awaitPod(c, r.Namespace, e2e.Skopeo, func(pod *corev1.Pod) bool {
		return pod.Status.Phase == corev1.PodSucceeded
	})

	err = e2e.DeleteSkopeoPod(c, r.Namespace)
	if err != nil {
		return fmt.Errorf("error deleting skopeo pod: %s", err)
	}
	return nil
}
