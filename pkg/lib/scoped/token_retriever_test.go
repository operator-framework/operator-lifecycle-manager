package scoped

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const serviceAccountName = "foo"

func TestFilterSecretsBySAName(t *testing.T) {
	tests := []struct {
		name              string
		secrets           *corev1.SecretList
		wantedSecretNames []string
	}{
		{
			name: "NoSecretFound",
			secrets: &corev1.SecretList{
				Items: []corev1.Secret{
					*newSecret("aSecret"),
					*newSecret("someSecret"),
					*newSecret("zSecret"),
				},
			},
			wantedSecretNames: []string{},
		},
		{
			name: "FirstSecretFound",
			secrets: &corev1.SecretList{
				Items: []corev1.Secret{
					*newSecret("aSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
					*newSecret("someSecret"),
					*newSecret("zSecret"),
				},
			},
			wantedSecretNames: []string{"aSecret"},
		},
		{
			name: "SecondSecretFound",
			secrets: &corev1.SecretList{
				Items: []corev1.Secret{
					*newSecret("aSecret"),
					*newSecret("someSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
					*newSecret("zSecret"),
				},
			},
			wantedSecretNames: []string{"someSecret"},
		},
		{
			name: "ThirdSecretFound",
			secrets: &corev1.SecretList{
				Items: []corev1.Secret{
					*newSecret("aSecret"),
					*newSecret("someSecret"),
					*newSecret("zSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
				},
			},
			wantedSecretNames: []string{"zSecret"},
		},
		{
			name: "TwoSecretsFound",
			secrets: &corev1.SecretList{
				Items: []corev1.Secret{
					*newSecret("aSecret"),
					*newSecret("someSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
					*newSecret("zSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
				},
			},
			wantedSecretNames: []string{"someSecret", "zSecret"},
		},
		{
			name: "AllSecretsFound",
			secrets: &corev1.SecretList{
				Items: []corev1.Secret{
					*newSecret("aSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
					*newSecret("someSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
					*newSecret("zSecret", withAnnotations(map[string]string{corev1.ServiceAccountNameKey: serviceAccountName})),
				},
			},
			wantedSecretNames: []string{"aSecret", "someSecret", "zSecret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterSecretsBySAName(serviceAccountName, tt.secrets)
			require.Equal(t, len(tt.wantedSecretNames), len(got))
			for _, wantedSecretName := range tt.wantedSecretNames {
				require.NotNil(t, got[wantedSecretName])
				require.Equal(t, wantedSecretName, got[wantedSecretName].GetName())
			}
		})
	}
}

type secretOption func(*corev1.Secret)

func withAnnotations(annotations map[string]string) secretOption {
	return func(s *corev1.Secret) {
		s.SetAnnotations(annotations)
	}
}

func newSecret(name string, opts ...secretOption) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
