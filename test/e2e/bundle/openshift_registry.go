package bundle

import (
	"context"
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	BuilderServiceAccount               = "builder"
	OpenshiftAnnotationTokenSecretName  = "openshift.io/token-secret.name"
	OpenshiftAnnotationTokenSecretValue = "openshift.io/token-secret.value"
)

func OpenshiftRegistryAuth(client operatorclient.ClientInterface, namespace string) (string, error) {
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

	user := annotations[OpenshiftAnnotationTokenSecretName]
	pass := annotations[OpenshiftAnnotationTokenSecretValue]

	return fmt.Sprint(user, ":", pass), nil
}
