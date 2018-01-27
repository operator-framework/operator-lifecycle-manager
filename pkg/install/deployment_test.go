package install

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	v1beta1rbac "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client/clientfakes"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func testDeployment(name, namespace string, mockOwnerMeta metav1.ObjectMeta) v1beta1.Deployment {
	testDeploymentLabels := map[string]string{"alm-owner-name": mockOwnerMeta.Name, "alm-owner-namespace": mockOwnerMeta.Namespace}

	deployment := v1beta1.Deployment{
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

func testRules(name string) []v1beta1rbac.PolicyRule {
	return []v1beta1rbac.PolicyRule{
		{
			Verbs:     []string{"list", "delete"},
			APIGroups: []string{name},
			Resources: []string{"pods"},
		},
	}
}

func TestInstallStrategyDeploymentInstallDeployments(t *testing.T) {
	var (
		namespace     = "alm-test-deployment"
		mockOwnerName = "clusterserviceversion-owner"
		mockOwnerMeta = metav1.ObjectMeta{
			Name:      mockOwnerName,
			Namespace: namespace,
		}
		mockOwnerRefs = []metav1.OwnerReference{{
			Name: mockOwnerName,
		}}
	)

	type inputs struct {
		strategyDeploymentSpecs []StrategyDeploymentSpec
	}
	type setup struct {
		existingDeployments []*v1beta1.Deployment
	}
	type createOrUpdateMock struct {
		expectedDeployment v1beta1.Deployment
		returnError        error
	}
	tests := []struct {
		description         string
		inputs              inputs
		setup               setup
		createOrUpdateMocks []createOrUpdateMock
		output              error
	}{
		{
			description: "updates/creates correctly",
			inputs: inputs{
				strategyDeploymentSpecs: []StrategyDeploymentSpec{
					{
						Name: "test-deployment-1",
						Spec: v1beta1.DeploymentSpec{},
					},
					{
						Name: "test-deployment-2",
						Spec: v1beta1.DeploymentSpec{},
					},
					{
						Name: "test-deployment-3",
						Spec: v1beta1.DeploymentSpec{},
					},
				},
			},
			setup: setup{
				existingDeployments: []*v1beta1.Deployment{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-deployment-1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-deployment-3",
						},
						Spec: v1beta1.DeploymentSpec{
							Paused: false, // arbitrary spec difference
						},
					},
				},
			},
			createOrUpdateMocks: []createOrUpdateMock{
				{
					expectedDeployment: v1beta1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-1",
							Namespace:       namespace,
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"alm-owner-name":      mockOwnerName,
								"alm-owner-namespace": namespace,
							},
						},
					},
					returnError: nil,
				},
				{
					expectedDeployment: v1beta1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-2",
							Namespace:       namespace,
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"alm-owner-name":      mockOwnerName,
								"alm-owner-namespace": namespace,
							},
						},
					},
					returnError: nil,
				},
				{
					expectedDeployment: v1beta1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-3",
							Namespace:       namespace,
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"alm-owner-name":      mockOwnerName,
								"alm-owner-namespace": namespace,
							},
						},
					},
					returnError: nil,
				},
			},
			output: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)

			for i, m := range tt.createOrUpdateMocks {
				fakeClient.CreateDeploymentReturns(nil, m.returnError)
				defer func(i int, expectedDeployment v1beta1.Deployment) {
					dep := fakeClient.CreateOrUpdateDeploymentArgsForCall(i)
					require.Equal(t, expectedDeployment, *dep)
				}(i, m.expectedDeployment)
			}

			installer := &StrategyDeploymentInstaller{
				strategyClient: fakeClient,
				ownerRefs:      mockOwnerRefs,
				ownerMeta:      mockOwnerMeta,
			}
			result := installer.installDeployments(tt.inputs.strategyDeploymentSpecs)
			assert.Equal(t, tt.output, result)
		})
	}
}

func TestInstallStrategyDeploymentInstallPermissions(t *testing.T) {
	namespace := "alm-test-deployment"

	mockOwnerName := "clusterserviceversion-owner"
	generateRoleName := "clusterserviceversion-owner-role-"

	mockOwnerMeta := metav1.ObjectMeta{
		Name:      mockOwnerName,
		Namespace: namespace,
	}
	mockOwnerRefs := []metav1.OwnerReference{{
		Name: mockOwnerName,
	}}

	serviceAccountName1 := "alm-sa-1"
	serviceAccountName2 := "alm-sa-2"

	ensuredServiceAccountName1 := "ensured-alm-sa-1"
	ensuredServiceAccountName2 := "ensured-alm-sa-2"

	ruleName1 := "alm-rule-1"
	ruleName2 := "alm-rule-2"

	testRules1 := testRules(ruleName1)
	testRules2 := testRules(ruleName2)

	roleName1 := "alm-role-1"
	roleName2 := "alm-role-2"

	generateRoleBindingName1 := "alm-role-1-ensured-alm-sa-1-rolebinding-"
	generateRoleBindingName2 := "alm-role-2-ensured-alm-sa-2-rolebinding-"

	testError := errors.New("test error")

	type inputs struct {
		strategyPermissions []StrategyDeploymentPermissions
	}
	type mock struct {
		expectedRole      *v1beta1rbac.Role
		createdRole       *v1beta1rbac.Role
		roleCreationError error

		expectedServiceAccount    *corev1.ServiceAccount
		ensuredServiceAccount     *corev1.ServiceAccount
		ensureServiceAccountError error

		expectedRoleBinding      *v1beta1rbac.RoleBinding
		roleBindingCreationError error
	}
	tests := []struct {
		description string

		inputs inputs
		mocks  []mock
		output error
	}{
		{
			description: "no permissions is no-op",
			inputs: inputs{
				[]StrategyDeploymentPermissions{},
			},
			mocks:  []mock{},
			output: nil,
		},
		{
			description: "creates roles, SAs, and rolebindings for multiple permissions",
			inputs: inputs{
				[]StrategyDeploymentPermissions{
					{serviceAccountName1, testRules1}, {serviceAccountName2, testRules2},
				},
			},
			mocks: []mock{
				{
					expectedRole: &v1beta1rbac.Role{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleName,
							OwnerReferences: mockOwnerRefs,
						},
						Rules: testRules1,
					},
					createdRole: &v1beta1rbac.Role{ObjectMeta: metav1.ObjectMeta{
						Name: roleName1,
					}},
					roleCreationError: nil,

					expectedServiceAccount: &corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name:            serviceAccountName1,
							OwnerReferences: mockOwnerRefs,
						},
					},
					ensuredServiceAccount: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
						Name: ensuredServiceAccountName1,
					}},
					ensureServiceAccountError: nil,

					expectedRoleBinding: &v1beta1rbac.RoleBinding{
						RoleRef: v1beta1rbac.RoleRef{
							Kind:     "Role",
							Name:     roleName1,
							APIGroup: v1beta1rbac.GroupName},
						Subjects: []v1beta1rbac.Subject{{
							Kind:      "ServiceAccount",
							Name:      serviceAccountName1,
							Namespace: namespace,
						}},
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleBindingName1,
							OwnerReferences: mockOwnerRefs,
						},
						TypeMeta: metav1.TypeMeta{Kind: "", APIVersion: ""},
					},
					roleBindingCreationError: nil,
				},
				{
					expectedRole: &v1beta1rbac.Role{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleName,
							OwnerReferences: mockOwnerRefs,
						},
						Rules: testRules2,
					},
					createdRole: &v1beta1rbac.Role{ObjectMeta: metav1.ObjectMeta{
						Name: roleName2,
					}},
					roleCreationError: nil,

					expectedServiceAccount: &corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name:            serviceAccountName2,
							OwnerReferences: mockOwnerRefs,
						},
					},
					ensuredServiceAccount: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
						Name: ensuredServiceAccountName2,
					}},
					ensureServiceAccountError: nil,

					expectedRoleBinding: &v1beta1rbac.RoleBinding{
						RoleRef: v1beta1rbac.RoleRef{
							Kind:     "Role",
							Name:     roleName2,
							APIGroup: v1beta1rbac.GroupName},
						Subjects: []v1beta1rbac.Subject{{
							Kind:      "ServiceAccount",
							Name:      serviceAccountName2,
							Namespace: namespace,
						}},
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleBindingName2,
							OwnerReferences: mockOwnerRefs,
						},
						TypeMeta: metav1.TypeMeta{Kind: "", APIVersion: ""},
					},
					roleBindingCreationError: nil,
				},
			},
			output: nil,
		},
		{
			description: "handles errors creating roles",
			inputs: inputs{
				[]StrategyDeploymentPermissions{
					{serviceAccountName1, testRules1}, {serviceAccountName2, testRules2},
				},
			},
			mocks: []mock{
				{
					expectedRole: &v1beta1rbac.Role{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleName,
							OwnerReferences: mockOwnerRefs,
						},
						Rules: testRules1,
					},
					createdRole:       nil,
					roleCreationError: testError,

					expectedServiceAccount:    nil,
					ensuredServiceAccount:     nil,
					ensureServiceAccountError: nil,

					expectedRoleBinding:      nil,
					roleBindingCreationError: nil,
				},
			},
			output: testError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {

			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)

			for i, m := range tt.mocks {
				fakeClient.CreateRoleReturns(m.createdRole, m.roleCreationError)
				defer func() {
					require.Equal(t, m.expectedRole, fakeClient.CreateRoleArgsForCall(i))
				}()

				if m.expectedServiceAccount != nil {
					fakeClient.EnsureServiceAccountReturns(m.ensuredServiceAccount, m.ensureServiceAccountError)
					defer func() {
						require.Equal(t, 2, fakeClient.EnsureServiceAccountCallCount())
						sa := fakeClient.EnsureServiceAccountArgsForCall(1)
						require.Equal(t, m.expectedServiceAccount, sa)
					}()
				}
				if m.expectedRoleBinding != nil {
					fakeClient.CreateRoleBindingReturns(nil, m.roleBindingCreationError)
					defer func() {
						require.Equal(t, 2, fakeClient.CreateRoleBindingCallCount())
						require.Equal(t, m.expectedRoleBinding, fakeClient.CreateRoleBindingArgsForCall(1))
					}()
				}
			}
			installer := &StrategyDeploymentInstaller{
				strategyClient: fakeClient,
				ownerRefs:      mockOwnerRefs,
				ownerMeta:      mockOwnerMeta,
			}
			result := installer.installPermissions(tt.inputs.strategyPermissions)
			assert.Equal(t, tt.output, result)

		})
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
			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
			strategy := strategy(tt.numExpected, namespace, mockOwnerMeta)
			for i, p := range strategy.Permissions {
				if i < tt.numMockServiceAccounts {
					t.Logf("mocking %s true", p.ServiceAccountName)

					fakeClient.GetServiceAccountByNameReturnsOnCall(i, testServiceAccount(p.ServiceAccountName, mockOwnerMeta), nil)
					defer func() {
						require.Equal(t, p.ServiceAccountName, fakeClient.GetServiceAccountByNameArgsForCall(i))
					}()
				}
				if i == tt.numMockServiceAccounts {
					t.Logf("mocking %s false", p.ServiceAccountName)
					fakeClient.GetServiceAccountByNameReturnsOnCall(i, nil, apierrors.NewNotFound(schema.GroupResource{}, p.ServiceAccountName))
					defer func() {
						require.Equal(t, p.ServiceAccountName, fakeClient.GetServiceAccountByNameArgsForCall(i))
					}()
				}

				serviceAccount := testServiceAccount(p.ServiceAccountName, mockOwnerMeta)

				fakeClient.EnsureServiceAccountReturnsOnCall(i, serviceAccount, nil)
				fakeClient.CreateRoleReturnsOnCall(i, &v1beta1rbac.Role{Rules: p.Rules}, nil)
				fakeClient.CreateRoleBindingReturnsOnCall(i, &v1beta1rbac.RoleBinding{}, nil)
				defer func(call int, rules []v1beta1rbac.PolicyRule) {
					require.Equal(t, rules, fakeClient.CreateRoleArgsForCall(call).Rules)
				}(i, p.Rules)
			}

			var mockedDeps []*v1beta1.Deployment
			for i := 1; i <= tt.numMockDeployments; i++ {
				dep := testDeployment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta)
				dep.Spec = v1beta1.DeploymentSpec{Paused: true} // arbitrary

				mockedDeps = append(mockedDeps, &dep)
			}
			if tt.numMockServiceAccounts == tt.numExpected {
				t.Log("mocking dep check")
				// if all serviceaccounts exist then we check if deployments exist
				var depNames []string
				for i := 1; i <= tt.numExpected; i++ {
					depNames = append(depNames, fmt.Sprintf("alm-dep-%d", i))
				}

				fakeClient.FindAnyDeploymentsMatchingNamesReturns(mockedDeps, nil)
				defer func() {
					require.Equal(t, 1, fakeClient.FindAnyDeploymentsMatchingNamesCallCount())
					require.Equal(t, depNames, fakeClient.FindAnyDeploymentsMatchingNamesArgsForCall(0))
				}()
			}

			for i := 1; i <= len(strategy.DeploymentSpecs); i++ {
				deployment := testDeployment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwnerMeta)
				fakeClient.CreateDeploymentReturnsOnCall(i-1, &deployment, nil)
			}

			installer := NewStrategyDeploymentInstaller(fakeClient, mockOwnerMeta, nil)

			installed, err := installer.CheckInstalled(strategy)
			if tt.numMockServiceAccounts == tt.numExpected && tt.numMockDeployments == tt.numExpected {
				require.NoError(t, err)
				require.True(t, installed)
			} else {
				require.False(t, installed)
				require.Error(t, err)
			}
			assert.NoError(t, installer.Install(strategy))
		})
	}
}

type BadStrategy struct{}

func (b *BadStrategy) GetStrategyName() string {
	return "bad"
}

func TestNewStrategyDeploymentInstaller(t *testing.T) {
	mockOwnerMeta := metav1.ObjectMeta{
		Name:      "clusterserviceversion-owner",
		Namespace: "ns",
	}
	fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
	strategy := NewStrategyDeploymentInstaller(fakeClient, mockOwnerMeta, nil)
	require.Implements(t, (*StrategyInstaller)(nil), strategy)
	require.Error(t, strategy.Install(&BadStrategy{}))
	installed, err := strategy.CheckInstalled(&BadStrategy{})
	require.False(t, installed)
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
		description             string
	}{
		{
			checkServiceAccountErr: fmt.Errorf("couldn't query serviceaccount"),
			description:            "ErrorCheckingForServiceAccount",
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
			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
			strategy := strategy(1, namespace, mockOwnerMeta)
			installer := NewStrategyDeploymentInstaller(fakeClient, mockOwnerMeta, nil)

			skipInstall := tt.checkServiceAccountErr != nil

			fakeClient.GetServiceAccountByNameReturns(testServiceAccount(strategy.Permissions[0].ServiceAccountName, mockOwnerMeta), tt.checkServiceAccountErr)
			defer func() {
				require.Equal(t, strategy.Permissions[0].ServiceAccountName, fakeClient.GetServiceAccountByNameArgsForCall(0))
			}()

			if tt.checkServiceAccountErr == nil {
				dep := testDeployment("alm-dep-1", namespace, mockOwnerMeta)
				fakeClient.FindAnyDeploymentsMatchingNamesReturns(
					[]*v1beta1.Deployment{
						&dep,
					}, nil,
				)
				defer func() {
					require.Equal(t, []string{dep.Name}, fakeClient.FindAnyDeploymentsMatchingNamesArgsForCall(0))
				}()
			}

			installed, err := installer.CheckInstalled(strategy)

			if skipInstall {
				require.Error(t, err)
				require.False(t, installed)
				return
			} else {
				require.True(t, installed)
				require.NoError(t, err)
			}

			fakeClient.CreateRoleReturns(&v1beta1rbac.Role{Rules: strategy.Permissions[0].Rules}, tt.createRoleErr)
			defer func() {
				require.Equal(t, strategy.Permissions[0].Rules, fakeClient.CreateRoleArgsForCall(0).Rules)
			}()

			if tt.createRoleErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}

			serviceAccount := testServiceAccount(strategy.Permissions[0].ServiceAccountName, mockOwnerMeta)

			fakeClient.EnsureServiceAccountReturns(serviceAccount, tt.createServiceAccountErr)
			defer func() {
				require.Equal(t, serviceAccount, fakeClient.EnsureServiceAccountArgsForCall(0))
			}()

			if tt.createServiceAccountErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}
			fakeClient.CreateRoleBindingReturns(&v1beta1rbac.RoleBinding{}, tt.createRoleBindingErr)

			if tt.createRoleBindingErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}
			deployment := testDeployment("alm-dep-1", namespace, mockOwnerMeta)
			fakeClient.CreateOrUpdateDeploymentReturns(&deployment, tt.createDeploymentErr)
			defer func() {
				require.Equal(t, &deployment, fakeClient.CreateOrUpdateDeploymentArgsForCall(0))
			}()

			if tt.createDeploymentErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
				return
			}
		})
	}
}
