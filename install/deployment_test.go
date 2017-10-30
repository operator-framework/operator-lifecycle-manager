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

	"reflect"

	"github.com/coreos-inc/alm/apis"
	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
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

func strategy(n int, namespace string, mockOwnerMeta metav1.ObjectMeta) *StrategyDetailsDeployment {
	var deploymentSpecs = []v1beta1extensions.DeploymentSpec{}
	var permissions = []StrategyDeploymentPermissions{}
	for i := 1; i <= n; i++ {
		deploymentSpecs = append(deploymentSpecs, testDepoyment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta).Spec)
		serviceAccount := testServiceAccount(fmt.Sprintf("alm-sa-%d", i), mockOwnerMeta)
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
	}
	return &StrategyDetailsDeployment{
		DeploymentSpecs: deploymentSpecs,
		Permissions:     permissions,
	}
}

func TestInstallStrategyDeployment(t *testing.T) {
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

	tests := []struct {
		numMockServiceAccounts int
		numMockDeployments     int
		numExpected            int
		description            string
	}{
		{
			numMockServiceAccounts: 0,
			numMockDeployments:     0,
			numExpected:            1,
			description:            "NoServiceAccount/NoDeployment/Require1,1",
		},
		{
			numMockServiceAccounts: 1,
			numMockDeployments:     1,
			numExpected:            1,
			description:            "1ServiceAccount/1Deployment/Require1,1",
		},
		{
			numMockServiceAccounts: 0,
			numMockDeployments:     1,
			numExpected:            1,
			description:            "0ServiceAccount/1Deployment/Require1,1",
		},
		{
			numMockServiceAccounts: 1,
			numMockDeployments:     0,
			numExpected:            1,
			description:            "1ServiceAccount/0Deployment/Require1,1",
		},
		{
			numMockServiceAccounts: 3,
			numMockDeployments:     3,
			numExpected:            3,
			description:            "3ServiceAccount/3Deployment/Require3,3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockClient := client.NewMockInstallStrategyDeploymentInterface(ctrl)
			strategy := strategy(tt.numExpected, namespace, mockOwnerMeta)
			for i, p := range strategy.Permissions {
				if i < tt.numMockServiceAccounts {
					t.Logf("mocking %s true", p.ServiceAccountName)
					mockClient.EXPECT().
						CheckServiceAccount(p.ServiceAccountName).
						Return(true, nil)
				}
				if i == tt.numMockServiceAccounts {
					t.Logf("mocking %s false", p.ServiceAccountName)
					mockClient.EXPECT().
						CheckServiceAccount(p.ServiceAccountName).
						Return(false, nil)
				}

				serviceAccount := testServiceAccount(p.ServiceAccountName, mockOwnerMeta)
				mockClient.EXPECT().GetOrCreateServiceAccount(serviceAccount).Return(serviceAccount, nil)
				mockClient.EXPECT().
					CreateRole(MatchesRoleRules(p.Rules)).
					Return(&v1beta1rbac.Role{Rules: p.Rules}, nil)
				mockClient.EXPECT().CreateRoleBinding(gomock.Any()).Return(&v1beta1rbac.RoleBinding{}, nil)
			}
			if tt.numMockServiceAccounts == tt.numExpected {
				t.Log("mocking dep check")
				// if all serviceaccounts exist then we check if deployments exist
				mockClient.EXPECT().CheckOwnedDeployments(mockOwnerMeta, strategy.DeploymentSpecs).Return(tt.numMockDeployments == len(strategy.DeploymentSpecs), nil)
			}

			for i := range make([]int, len(strategy.DeploymentSpecs)) {
				deployment := testDepoyment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta)
				mockClient.EXPECT().
					CreateDeployment(&deployment).
					Return(&deployment, nil)
			}

			installer := &StrategyDeploymentInstaller{
				strategyClient: mockClient,
				ownerMeta:      mockOwnerMeta,
				ownerType:      mockOwnerType,
			}
			installed, err := installer.CheckInstalled(strategy)
			if tt.numMockServiceAccounts == tt.numExpected && tt.numMockDeployments == tt.numExpected {
				require.True(t, installed)
			} else {
				require.False(t, installed)
			}
			assert.NoError(t, err)
			assert.NoError(t, installer.Install(strategy))

			ctrl.Finish()
		})
	}
}
