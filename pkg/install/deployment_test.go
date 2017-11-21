package install

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1beta1extensions "k8s.io/api/extensions/v1beta1"
	v1beta1rbac "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func testDeployment(name, namespace string, mockOwnerMeta metav1.ObjectMeta) v1beta1extensions.Deployment {
	testDeploymentLabels := map[string]string{"alm-owner-name": mockOwnerMeta.Name, "alm-owner-namespace": mockOwnerMeta.Namespace}

	deployment := v1beta1extensions.Deployment{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1alpha1.SchemeGroupVersion.String(),
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
			APIVersion:         v1alpha1.SchemeGroupVersion.String(),
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
	var deploymentSpecs = []StrategyDeploymentSpec{}
	var permissions = []StrategyDeploymentPermissions{}
	for i := 1; i <= n; i++ {
		dep := testDeployment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta)
		spec := StrategyDeploymentSpec{Name: dep.GetName(), Spec: dep.Spec}
		deploymentSpecs = append(deploymentSpecs, spec)
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
		Name:      "clusterserviceversion-owner",
		Namespace: namespace,
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
			mockClient := NewMockInstallStrategyDeploymentInterface(ctrl)
			strategy := strategy(tt.numExpected, namespace, mockOwnerMeta)
			for i, p := range strategy.Permissions {
				if i < tt.numMockServiceAccounts {
					t.Logf("mocking %s true", p.ServiceAccountName)
					mockClient.EXPECT().
						GetServiceAccountByName(p.ServiceAccountName).
						Return(testServiceAccount(p.ServiceAccountName, mockOwnerMeta), nil)
				}
				if i == tt.numMockServiceAccounts {
					t.Logf("mocking %s false", p.ServiceAccountName)
					mockClient.EXPECT().
						GetServiceAccountByName(p.ServiceAccountName).
						Return(nil, apierrors.NewNotFound(schema.GroupResource{}, p.ServiceAccountName))
				}

				serviceAccount := testServiceAccount(p.ServiceAccountName, mockOwnerMeta)
				mockClient.EXPECT().EnsureServiceAccount(serviceAccount).Return(serviceAccount, nil)
				mockClient.EXPECT().
					CreateRole(MatchesRoleRules(p.Rules)).
					Return(&v1beta1rbac.Role{Rules: p.Rules}, nil)
				mockClient.EXPECT().CreateRoleBinding(gomock.Any()).Return(&v1beta1rbac.RoleBinding{}, nil)
			}
			if tt.numMockServiceAccounts == tt.numExpected {
				t.Log("mocking dep check")
				// if all serviceaccounts exist then we check if deployments exist
				mockedDeps := []v1beta1extensions.Deployment{}
				for i := 1; i <= tt.numMockDeployments; i++ {
					mockedDeps = append(mockedDeps, testDeployment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta))
				}
				mockClient.EXPECT().GetOwnedDeployments(mockOwnerMeta).Return(&v1beta1extensions.DeploymentList{Items: mockedDeps}, nil)
			}

			for i := range make([]int, len(strategy.DeploymentSpecs)) {
				deployment := testDeployment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta)
				mockClient.EXPECT().
					CreateDeployment(&deployment).
					Return(&deployment, nil)
			}

			installer := &StrategyDeploymentInstaller{
				strategyClient: mockClient,
				ownerMeta:      mockOwnerMeta,
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

type BadStrategy struct{}

func (b *BadStrategy) GetStrategyName() string {
	return "bad"
}

func TestNewStrategyDeploymentInstaller(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockOwnerMeta := metav1.ObjectMeta{
		Name:      "clusterserviceversion-owner",
		Namespace: "ns",
	}
	mockClient := NewMockInstallStrategyDeploymentInterface(ctrl)
	strategy := NewStrategyDeploymentInstaller(mockClient, mockOwnerMeta)
	require.Implements(t, (*StrategyInstaller)(nil), strategy)
	require.Error(t, strategy.Install(&BadStrategy{}))
	_, err := strategy.CheckInstalled(&BadStrategy{})
	require.Error(t, err)
}

func TestInstallStrategyDeploymentCheckInstallErrors(t *testing.T) {
	namespace := "alm-test-deployment"
	mockOwnerMeta := metav1.ObjectMeta{
		Name:      "clusterserviceversion-owner",
		Namespace: namespace,
	}

	tests := []struct {
		createRoleErr           error
		createRoleBindingErr    error
		createServiceAccountErr error
		createDeploymentErr     error
		checkServiceAccountErr  error
		checkDeploymentErr      error
		description             string
	}{
		{
			checkServiceAccountErr: fmt.Errorf("couldn't query serviceaccount"),
			description:            "ErrorCheckingForServiceAccount",
		},
		{
			checkDeploymentErr: fmt.Errorf("couldn't query deployments"),
			description:        "ErrorCheckingForDeployments",
		},
		{
			createRoleErr: fmt.Errorf("error creating role"),
			description:   "ErrorCreatingRole",
		},
		{
			createServiceAccountErr: fmt.Errorf("error creating serviceaccount"),
			description:             "ErrorCreatingServiceAccount",
		},
		{
			createRoleBindingErr: fmt.Errorf("error creating rolebinding"),
			description:          "ErrorCreatingRoleBinding",
		},
		{
			createDeploymentErr: fmt.Errorf("error creating deployment"),
			description:         "ErrorCreatingDeployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := NewMockInstallStrategyDeploymentInterface(ctrl)
			strategy := strategy(1, namespace, mockOwnerMeta)
			installer := &StrategyDeploymentInstaller{
				strategyClient: mockClient,
				ownerMeta:      mockOwnerMeta,
			}

			skipInstall := tt.checkDeploymentErr != nil || tt.checkServiceAccountErr != nil

			mockClient.EXPECT().
				GetServiceAccountByName(strategy.Permissions[0].ServiceAccountName).
				Return(testServiceAccount(strategy.Permissions[0].ServiceAccountName, mockOwnerMeta), tt.checkServiceAccountErr)
			if tt.checkServiceAccountErr == nil {
				mockClient.EXPECT().
					GetOwnedDeployments(mockOwnerMeta).
					Return(
						&v1beta1extensions.DeploymentList{
							Items: []v1beta1extensions.Deployment{
								testDeployment("alm-dep", namespace, mockOwnerMeta),
							},
						}, tt.checkDeploymentErr)
			}

			installed, err := installer.CheckInstalled(strategy)

			if skipInstall {
				require.False(t, installed)
				require.Error(t, err)
				return
			} else {
				require.True(t, installed)
				require.NoError(t, err)
			}

			mockClient.EXPECT().
				CreateRole(MatchesRoleRules(strategy.Permissions[0].Rules)).
				Return(&v1beta1rbac.Role{Rules: strategy.Permissions[0].Rules}, tt.createRoleErr)

			if tt.createRoleErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}

			serviceAccount := testServiceAccount(strategy.Permissions[0].ServiceAccountName, mockOwnerMeta)
			mockClient.EXPECT().EnsureServiceAccount(serviceAccount).Return(serviceAccount, tt.createServiceAccountErr)

			if tt.createServiceAccountErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}

			mockClient.EXPECT().CreateRoleBinding(gomock.Any()).Return(&v1beta1rbac.RoleBinding{}, tt.createRoleBindingErr)

			if tt.createRoleBindingErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}

			deployment := testDeployment("alm-dep", namespace, mockOwnerMeta)
			mockClient.EXPECT().
				CreateDeployment(&deployment).
				Return(&deployment, tt.createDeploymentErr)

			if tt.createDeploymentErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}
		})
	}
}
