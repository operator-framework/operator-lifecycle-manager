package e2e

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	insecure              = "--insecure-policy=true"
	skopeo                = "skopeo"
	debug                 = "--debug"
	skipTLS               = "--dest-tls-verify=false"
	skipCreds             = "--dest-no-creds=true"
	destCreds             = "--dest-creds="
	v2format              = "--format=v2s2"
	skopeoImage           = "quay.io/olmtest/skopeo:0.1.40"
	BuilderServiceAccount = "builder"
)

func openshiftRegistryAuth(client operatorclient.ClientInterface, namespace string) (string, error) {
	sa, err := client.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Get(context.TODO(), BuilderServiceAccount, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	secretName := sa.ImagePullSecrets[0].Name
	secret, err := client.KubernetesInterface().CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	annotations := secret.Annotations
	if annotations == nil {
		return "", fmt.Errorf("annotations not present on builder secret")
	}

	user := annotations["openshift.io/token-secret.name"]
	pass := annotations["openshift.io/token-secret.value"]

	return fmt.Sprint(user, ":", pass), nil
}

func skopeoCopyCmd(newImage, newTag, oldImage, oldTag, auth string) []string {
	newImageName := fmt.Sprint(newImage, newTag)
	oldImageName := fmt.Sprint(oldImage, oldTag)

	var creds string
	if auth == "" {
		creds = skipCreds
	} else {
		creds = fmt.Sprint(destCreds, auth)
	}

	cmd := []string{debug, insecure, "copy", skipTLS, v2format, creds, oldImageName, newImageName}

	return cmd
}

func createSkopeoPod(client operatorclient.ClientInterface, args []string, namespace string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      skopeo,
			Namespace: namespace,
			Labels:    map[string]string{"name": skopeo},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  skopeo,
					Image: skopeoImage,
					Args:  args,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			// ServiceAccountName: "builder",
		},
	}

	_, err := client.KubernetesInterface().CoreV1().Pods(namespace).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func deleteSkopeoPod(client operatorclient.ClientInterface, namespace string) error {
	err := client.KubernetesInterface().CoreV1().Pods(namespace).Delete(context.TODO(), skopeo, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}

func skopeoLocalCopy(newImage, newTag string, oldImage, oldTag string) (string, error) {
	newImageName := fmt.Sprint(newImage, newTag)
	oldImageName := fmt.Sprint(oldImage, oldTag)
	cmd := exec.Command(skopeo, debug, insecure, "copy", skipTLS, v2format, skipCreds, oldImageName, newImageName)

	out, err := cmd.Output()
	fmt.Println(string(out))
	if err != nil {
		return "", fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}

	return newImageName, nil
}
