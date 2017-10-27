package install

import (
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/api/core/v1"
	v1beta1extensions "k8s.io/api/extensions/v1beta1"
	v1beta1rbac "k8s.io/api/rbac/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis"
	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
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

func testServiceAccount(name string, mockOwnerMeta metav1.ObjectMeta) *v1.ServiceAccount {
	serviceAccount := &v1.ServiceAccount{}
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

func TestInstallStrategyDeployment(t *testing.T) {

	// Cases to test:
	// no service account, no deployment, expect 1
	// no service account, deployment, expect 1
	// service account, deployment, expect 1
	// < n service accounts, deployments
	// service accounts, <n deployments
	// < n service accounts, <n deployments
	// n service accounts, n deployments

	namespace := "alm-test-deployment"
	mockOwnerMeta := metav1.ObjectMeta{
		Name:         "clusterserviceversion-owner",
		Namespace:    namespace,
		GenerateName: fmt.Sprintf("%s-", namespace),
	}
	mockOwnerType := metav1.TypeMeta{
		Kind:       "kind",
		APIVersion: "APIString",
	}

	deployment := testDepoyment("alm-test", namespace, mockOwnerMeta)
	serviceAccount := testServiceAccount("test-sa", mockOwnerMeta)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := client.NewMockInstallStrategyDeploymentInterface(ctrl)

	mockClient.EXPECT().
		CheckServiceAccount(serviceAccount.Name).
		Return(false, nil)
	mockClient.EXPECT().CreateRoleBinding(gomock.Any()).Return(&v1beta1rbac.RoleBinding{}, nil)
	mockClient.EXPECT().CreateRole(gomock.Any()).Return(&v1beta1rbac.Role{}, nil)
	mockClient.EXPECT().GetOrCreateServiceAccount(serviceAccount).Return(serviceAccount, nil)
	mockClient.EXPECT().
		CreateDeployment(&deployment).
		Return(&deployment, nil)

	deployInstallStrategy := &StrategyDetailsDeployment{
		DeploymentSpecs: []v1beta1extensions.DeploymentSpec{deployment.Spec},
		Permissions: []StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccount.Name,
				Rules: []v1beta1rbac.PolicyRule{
					{
						Verbs:     []string{"list", "delete"},
						APIGroups: []string{""},
						Resources: []string{"pods"},
					},
				},
			},
		},
	}

	installer := &StrategyDeploymentInstaller{
		strategyClient: mockClient,
		ownerMeta:      mockOwnerMeta,
		ownerType:      mockOwnerType,
	}
	installed, err := installer.CheckInstalled(deployInstallStrategy)
	assert.False(t, installed)
	assert.NoError(t, err)
	assert.NoError(t, installer.Install(deployInstallStrategy))
}
