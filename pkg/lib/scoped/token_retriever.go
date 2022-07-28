package scoped

import (
	"context"
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BearerTokenRetriever retrieves bearer token from a service account.
type BearerTokenRetriever struct {
	kubeclient operatorclient.ClientInterface
	logger     logrus.FieldLogger
}

// Retrieve returns the bearer token for API access from a given service account reference.
func (r *BearerTokenRetriever) Retrieve(reference *corev1.ObjectReference) (token string, err error) {
	logger := r.logger.WithFields(logrus.Fields{
		"sa":         reference.Name,
		"namespace":  reference.Namespace,
		logFieldName: logFieldValue,
	})

	sa, err := r.kubeclient.KubernetesInterface().CoreV1().ServiceAccounts(reference.Namespace).Get(context.TODO(), reference.Name, metav1.GetOptions{})
	if err != nil {
		return
	}

	secret, err := getAPISecret(logger, r.kubeclient, sa)
	if err != nil {
		err = fmt.Errorf("error occurred while retrieving API secret associated with the service account sa=%s/%s - %v", sa.GetNamespace(), sa.GetName(), err)
		return
	}

	if secret == nil {
		err = fmt.Errorf("the service account does not have any API secret sa=%s/%s", sa.GetNamespace(), sa.GetName())
		return
	}

	token = string(secret.Data[corev1.ServiceAccountTokenKey])
	if token == "" {
		err = fmt.Errorf("the secret does not have any API token sa=%s/%s secret=%s", sa.GetNamespace(), sa.GetName(), secret.GetName())
	}

	return
}

func getAPISecret(logger logrus.FieldLogger, kubeclient operatorclient.ClientInterface, sa *corev1.ServiceAccount) (APISecret *corev1.Secret, err error) {
	seList, err := kubeclient.KubernetesInterface().CoreV1().Secrets(sa.GetNamespace()).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		logger.Errorf("unable to retrieve list of secrets in the namespace %s - %v", sa.GetNamespace(), err)
		return nil, err
	}
	secrets := filterSecretsBySAName(sa.Name, seList)

	for _, ref := range sa.Secrets {
		if _, ok := secrets[ref.Name]; !ok {
			logger.Warnf("skipping secret %s: secret not found", ref.Name)
			continue
		}
	}

	for _, secret := range secrets {
		// Validate that this is a token for API access.
		if !IsServiceAccountToken(secret, sa) {
			logger.Warnf("skipping secret %s: not token secret", secret.Name)
			continue
		}
		// The first eligible secret that has an API access token is returned.
		APISecret = secret
		break
	}

	return
}

// filterSecretsBySAName returna a maps of secrets that are associated with a
// specific ServiceAccount via annotations kubernetes.io/service-account.name
func filterSecretsBySAName(saName string, secrets *corev1.SecretList) map[string]*corev1.Secret {
	secretMap := make(map[string]*corev1.Secret)
	for _, ref := range secrets.Items {
		annotations := ref.GetAnnotations()
		value := annotations[corev1.ServiceAccountNameKey]
		if value == saName {
			secretMap[ref.Name] = &ref
		}
	}

	return secretMap
}
