package bundle

import (
	"context"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net"
	"os/exec"
)

const (
	registryImage    = "registry:2.7.1"
	localFQDN        = "localhost:5000"
	defaultLocalPort = 5000
	containerPort    = 5000
)

var RegistryName = "registry"

func CreateDockerRegistry(client operatorclient.ClientInterface, namespace string) (string, error) {
	registry := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RegistryName,
			Namespace: namespace,
			Labels:    map[string]string{"name": RegistryName},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  RegistryName,
					Image: registryImage,
					Ports: []corev1.ContainerPort{
						{
							HostPort:      int32(defaultLocalPort),
							ContainerPort: int32(containerPort),
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RegistryName,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"name": RegistryName},
			Ports: []corev1.ServicePort{
				{
					Port: int32(defaultLocalPort),
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

func DeleteDockerRegistry(client operatorclient.ClientInterface, namespace string) {
	_ = client.KubernetesInterface().CoreV1().Pods(namespace).Delete(context.TODO(), RegistryName, metav1.DeleteOptions{})
	_ = client.KubernetesInterface().CoreV1().Services(namespace).Delete(context.TODO(), RegistryName, metav1.DeleteOptions{})
}

// port-forward registry pod port 5000 for local test
// port-forwarding ends when registry pod is deleted: no need for explicit port-forward stop
// Note: https://github.com/kubernetes/kubernetes/issues/89208: local ports bound by port-forward are not released till the cluster is reset
// deleting pods alone does not fix this behaviour.
func RegistryPortForward(namespace string) error {
	cmd := exec.Command("kubectl", "-n", namespace, "port-forward", RegistryName, "5000:5000")
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}
	return nil
}

// best effort attempt to return a free local port. Fall back to default local port 5000 in case of error
func nextAvailableLocalPort() int {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return defaultLocalPort
	}
	_ = l.Close()
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return defaultLocalPort
	}
	portnum, err := net.DefaultResolver.LookupPort(context.Background(), "tcp", port)
	if err != nil {
		return defaultLocalPort
	}

	return portnum
}
