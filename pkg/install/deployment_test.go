package install

import (
	"fmt"
	"reflect"
	"testing"

	client "github.com/coreos-inc/alm/pkg/client"
	opClient "github.com/coreos-inc/operator-client/pkg/client"

	"github.com/golang/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	v1beta1extensions "k8s.io/api/extensions/v1beta1"
	v1beta1rbac "k8s.io/api/rbac/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/coreos-inc/alm/pkg/apis"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/stretchr/testify/require"
)

func testDepoyment(name, namespace string, mockOwnerMeta metav1.ObjectMeta) v1beta1extensions.Deployment {
	testDeploymentLabels := map[string]string{"alm-owner-name": mockOwnerMeta.Name, "alm-owner-namespace": mockOwnerMeta.Namespace}

	deployment := v1beta1extensions.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: fmt.Sprintf("%s-", mockOwnerMeta.Name),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         apis.GroupName,
					Kind:               v1alpha1.ClusterServiceVersionKind,
					Name:               mockOwnerMeta.GetName(),
					UID:                mockOwnerMeta.UID,
					Controller:         &Controller,
					BlockOwnerDeletion: &BlockOwnerDeletion,
				},
			},
			Labels: testDeploymentLabels,
		},
	}
	return deployment
}

func testServiceAccount(name string, mockOwnerMeta metav1.ObjectMeta) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{}
	serviceAccount.SetName(name)
	serviceAccount.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         apis.GroupName,
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               mockOwnerMeta.GetName(),
			UID:                mockOwnerMeta.UID,
			Controller:         &Controller,
			BlockOwnerDeletion: &BlockOwnerDeletion,
		},
	})
	return serviceAccount
}

type RoleMatcher struct{ rules []v1beta1rbac.PolicyRule }

func MatchesRoleRules(rules []v1beta1rbac.PolicyRule) gomock.Matcher {
	return &RoleMatcher{rules}
}

func (e *RoleMatcher) Matches(x interface{}) bool {
	role, ok := x.(*v1beta1rbac.Role)
	if !ok {
		return false
	}
	return reflect.DeepEqual(role.Rules, e.rules)
}

func (e *RoleMatcher) String() string {
	return "matches expected rules"
}

func strategy(n int, sourceNamespace string, targetNamespace string, mockOwnerMeta metav1.ObjectMeta) *StrategyDetailsDeployment {
	var deploymentSpecs = []v1beta1extensions.DeploymentSpec{}
	var permissions = []StrategyDeploymentPermissions{}
	var secrets = []client.SecretReference{}

	serviceAccount := testServiceAccount(fmt.Sprintf("alm-sa-%d", 1), mockOwnerMeta)
	permissions = append(permissions, StrategyDeploymentPermissions{
		ServiceAccountName: serviceAccount.Name,
		Rules: []v1beta1rbac.PolicyRule{
			{
				Verbs:     []string{"list", "delete"},
				APIGroups: []string{""},
				Resources: []string{"pods"},
			},
		},
	})

	for i := 1; i <= n; i++ {
		deploymentSpecs = append(deploymentSpecs, testDepoyment(fmt.Sprintf("alm-dep-%d", i), targetNamespace, mockOwnerMeta).Spec)
		secrets = append(secrets, client.SecretReference{fmt.Sprintf("alm-secret-%d", i), sourceNamespace})
	}
	return &StrategyDetailsDeployment{
		DeploymentSpecs: deploymentSpecs,
		Permissions:     permissions,
		Secrets:         secrets,
	}
}

type BadStrategy struct{}

func (b *BadStrategy) GetStrategyName() string {
	return "bad"
}

func TestNewStrategyDeploymentInstaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockOwnerMeta := metav1.ObjectMeta{
		Name:         "clusterserviceversion-owner",
		Namespace:    "ns",
		GenerateName: fmt.Sprintf("%s-", "ns"),
	}
	mockClient := NewMockInstallStrategyDeploymentInterface(ctrl)
	strategy := NewStrategyDeploymentInstaller(mockClient, mockOwnerMeta)
	require.Implements(t, (*StrategyInstaller)(nil), strategy)
	require.Error(t, strategy.Install(&BadStrategy{}))
	_, err := strategy.CheckInstalled(&BadStrategy{})
	require.Error(t, err)
}

func NewMockNamespaceClient(ctrl *gomock.Controller, currentNamespaces []corev1.Namespace) (*opClient.MockInterface, kubernetes.Interface) {
	mockClient := opClient.NewMockInterface(ctrl)
	fakeKubernetesInterface := fake.NewSimpleClientset(&corev1.NamespaceList{Items: currentNamespaces})
	return mockClient, fakeKubernetesInterface
}

func namespaceObj(name string) corev1.Namespace {
	return corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
}

func TestInstallSuccessfully(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	namespaceName := "somenamespace"
	targetNamespaceName := "targetnamespace"

	mockClient, fakeKubernetesClient := NewMockNamespaceClient(ctrl, []corev1.Namespace{corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
		},
	}, corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNamespaceName,
		},
	}})

	mockClient.EXPECT().KubernetesInterface().Return(fakeKubernetesClient).MaxTimes(100)

	// Add the secrets.
	for _, secretName := range []string{"alm-secret-1", "alm-secret-2"} {
		fakeKubernetesClient.CoreV1().Secrets(namespaceName).Create(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespaceName,
				Name:      secretName,
			},
		})
	}

	mockOwnerMeta := metav1.ObjectMeta{
		Name:         "clusterserviceversion-owner",
		Namespace:    targetNamespaceName,
		GenerateName: fmt.Sprintf("%s-", targetNamespaceName),
	}

	client := client.NewInstallStrategyDeploymentClient(mockClient, targetNamespaceName)
	installer := NewStrategyDeploymentInstaller(client, mockOwnerMeta)
	strategy := strategy(2, namespaceName, targetNamespaceName, mockOwnerMeta)

	// Expect the deployments are created.
	mockClient.EXPECT().CreateDeployment(gomock.Any()).MaxTimes(2).Return(&v1beta1extensions.Deployment{}, nil)

	err := installer.Install(strategy)
	require.Nil(t, err, "Expected no error in success test")

	// Ensure the secrets were created.
	for _, secretName := range []string{"alm-secret-1", "alm-secret-2"} {
		found, err := fakeKubernetesClient.CoreV1().Secrets(namespaceName).Get(secretName, metav1.GetOptions{})
		require.Nil(t, err, "Missing expected secret `%s`", secretName)
		require.NotNil(t, found, "Missing expected secret `%s`", secretName)
	}
}

func TestInstallWithIssue(t *testing.T) {
	tests := []struct {
		name                  string
		strategyResourceCount int
		secrets               []string
		expectedError         string
	}{
		{
			name: "missing secret",
			strategyResourceCount: 1,
			secrets:               []string{},
			expectedError:         `secrets "alm-secret-1" not found`,
		},

		{
			name: "missing second secret",
			strategyResourceCount: 2,
			secrets:               []string{"alm-secret-1"},
			expectedError:         `secrets "alm-secret-2" not found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			namespaceName := "somenamespace"
			targetNamespaceName := "targetnamespace"

			mockClient, fakeKubernetesClient := NewMockNamespaceClient(ctrl, []corev1.Namespace{corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespaceName,
				},
			}, corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: targetNamespaceName,
				},
			}})

			mockClient.EXPECT().KubernetesInterface().Return(fakeKubernetesClient).MaxTimes(100)

			// Add the secrets.
			for _, secretName := range tt.secrets {
				fakeKubernetesClient.CoreV1().Secrets(namespaceName).Create(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespaceName,
						Name:      secretName,
					},
				})
			}

			mockOwnerMeta := metav1.ObjectMeta{
				Name:         "clusterserviceversion-owner",
				Namespace:    targetNamespaceName,
				GenerateName: fmt.Sprintf("%s-", targetNamespaceName),
			}

			client := client.NewInstallStrategyDeploymentClient(mockClient, targetNamespaceName)
			installer := NewStrategyDeploymentInstaller(client, mockOwnerMeta)
			strategy := strategy(tt.strategyResourceCount, namespaceName, targetNamespaceName, mockOwnerMeta)
			err := installer.Install(strategy)
			require.Equal(t, tt.expectedError, err.Error(), "Mismatch in expected error in test `%s`", tt.name)
		})
	}
}
