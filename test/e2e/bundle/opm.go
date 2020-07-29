package bundle

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	opmPodName = "index-builder-opm-podman"
	opmImage   = "quay.io/olmtest/opm-index-builder:1.12.7"
	shCommand  = "/bin/sh"
	shFlag     = "-c"
)

func (r *RegistryClient) runIndexBuilderPod(argString string) error {
	cmd := []string{shCommand, shFlag, argString}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opmPodName,
			Namespace: r.namespace,
			Labels:    map[string]string{"name": opmPodName},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    opmPodName,
					Image:   opmImage,
					Command: cmd,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			// ServiceAccountName: "builder",
		},
	}

	_, err := r.client.KubernetesInterface().CoreV1().Pods(r.namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create index builder pod: %v", err)
	}

	_, _ = awaitPod(r.client, r.namespace, opmPodName, func(pod *corev1.Pod) bool {
		return pod.Status.Phase == corev1.PodSucceeded
	})

	err = r.client.KubernetesInterface().CoreV1().Pods(r.namespace).Delete(context.TODO(), opmPodName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete index builder pod: %v", err)
	}
	return nil
}
