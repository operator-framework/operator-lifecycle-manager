package e2e

import (
	"context"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os/exec"
)

// This module contains helper functions for copying images and creating image registries
// Use for tests that require manipulating images or use of custom container registries

const (
	registryImage = "registry:2.7.1"
	registryName  = "registry"
	localFQDN     = "localhost:5000"
)

func createDockerRegistry(client operatorclient.ClientInterface, namespace string) (string, error) {
	registry := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registryName,
			Namespace: namespace,
			Labels:    map[string]string{"name": registryName},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  registryName,
					Image: registryImage,
					Ports: []corev1.ContainerPort{
						{
							HostPort:      int32(5000),
							ContainerPort: int32(5000),
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      registryName,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"name": registryName},
			Ports: []corev1.ServicePort{
				{
					Port: int32(5000),
				},
			},
		},
	}

	_, err := client.KubernetesInterface().CoreV1().Pods(namespace).Create(context.TODO(), registry, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("creating test registry: %s", err)
	}

	_, err = client.KubernetesInterface().CoreV1().Services(namespace).Create(context.TODO(), svc, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("creating test registry: %s", err)
	}

	return localFQDN, nil
}

func deleteDockerRegistry(client operatorclient.ClientInterface, namespace string) {
	_ = client.KubernetesInterface().CoreV1().Pods(namespace).Delete(context.TODO(), registryName, metav1.DeleteOptions{})
	_ = client.KubernetesInterface().CoreV1().Services(namespace).Delete(context.TODO(), registryName, metav1.DeleteOptions{})
}

// port-forward registry pod port 5000 for local test
// port-forwarding ends when registry pod is deleted: no need for explicit port-forward stop
func registryPortForward(namespace string) error {
	cmd := exec.Command("kubectl", "-n", namespace, "port-forward", "registry", "5000:5000")
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}
	return nil
}
