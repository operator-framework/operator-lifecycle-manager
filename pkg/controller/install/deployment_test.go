package install

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	clientfakes "github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers/wrappersfakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

var (
	Controller         = false
	BlockOwnerDeletion = false
)

func testDeployment(name, namespace string, mockOwner ownerutil.Owner) appsv1.Deployment {
	testDeploymentLabels := map[string]string{"alm-owner-name": mockOwner.GetName(), "alm-owner-namespace": mockOwner.GetNamespace()}

	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1alpha1.SchemeGroupVersion.String(),
					Kind:               v1alpha1.ClusterServiceVersionKind,
					Name:               mockOwner.GetName(),
					UID:                mockOwner.GetUID(),
					Controller:         &Controller,
					BlockOwnerDeletion: &BlockOwnerDeletion,
				},
			},
			Labels: testDeploymentLabels,
		},
	}
	return deployment
}

func testServiceAccount(name string, mockOwner ownerutil.Owner) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{}
	serviceAccount.SetName(name)
	serviceAccount.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         v1alpha1.SchemeGroupVersion.String(),
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               mockOwner.GetName(),
			UID:                mockOwner.GetUID(),
			Controller:         &Controller,
			BlockOwnerDeletion: &BlockOwnerDeletion,
		},
	})
	return serviceAccount
}

func strategy(n int, namespace string, mockOwner ownerutil.Owner) *StrategyDetailsDeployment {
	var deploymentSpecs = []StrategyDeploymentSpec{}
	var permissions = []StrategyDeploymentPermissions{}
	for i := 1; i <= n; i++ {
		dep := testDeployment(fmt.Sprintf("alm-dep-%d", i), namespace, mockOwner)
		spec := StrategyDeploymentSpec{Name: dep.GetName(), Spec: dep.Spec}
		deploymentSpecs = append(deploymentSpecs, spec)
		serviceAccount := testServiceAccount(fmt.Sprintf("alm-sa-%d", i), mockOwner)
		permissions = append(permissions, StrategyDeploymentPermissions{
			ServiceAccountName: serviceAccount.Name,
			Rules: []rbacv1.PolicyRule{
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

func testRules(name string) []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			Verbs:     []string{"list", "delete"},
			APIGroups: []string{name},
			Resources: []string{"pods"},
		},
	}
}

func TestInstallStrategyDeploymentInstallDeployments(t *testing.T) {
	var (
		mockOwner = v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clusterserviceversion-owner",
				Namespace: "alm-test-deployment",
			},
		}
		mockOwnerRefs = []metav1.OwnerReference{{
			APIVersion:         v1alpha1.ClusterServiceVersionAPIVersion,
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               mockOwner.GetName(),
			UID:                mockOwner.UID,
			Controller:         &Controller,
			BlockOwnerDeletion: &BlockOwnerDeletion,
		}}
	)

	type inputs struct {
		strategyDeploymentSpecs []StrategyDeploymentSpec
	}
	type setup struct {
		existingDeployments []*appsv1.Deployment
	}
	type createOrUpdateMock struct {
		expectedDeployment appsv1.Deployment
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
						Spec: appsv1.DeploymentSpec{},
					},
					{
						Name: "test-deployment-2",
						Spec: appsv1.DeploymentSpec{},
					},
					{
						Name: "test-deployment-3",
						Spec: appsv1.DeploymentSpec{},
					},
				},
			},
			setup: setup{
				existingDeployments: []*appsv1.Deployment{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-deployment-1",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "test-deployment-3",
						},
						Spec: appsv1.DeploymentSpec{
							Paused: false, // arbitrary spec difference
						},
					},
				},
			},
			createOrUpdateMocks: []createOrUpdateMock{
				{
					expectedDeployment: appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-1",
							Namespace:       mockOwner.GetNamespace(),
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"alm-owner-name":      mockOwner.GetName(),
								"alm-owner-namespace": mockOwner.GetNamespace(),
							},
						},
					},
					returnError: nil,
				},
				{
					expectedDeployment: appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-2",
							Namespace:       mockOwner.GetNamespace(),
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"alm-owner-name":      mockOwner.GetName(),
								"alm-owner-namespace": mockOwner.GetNamespace(),
							},
						},
					},
					returnError: nil,
				},
				{
					expectedDeployment: appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-3",
							Namespace:       mockOwner.GetNamespace(),
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"alm-owner-name":      mockOwner.GetName(),
								"alm-owner-namespace": mockOwner.GetNamespace(),
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
				defer func(i int, expectedDeployment appsv1.Deployment) {
					dep := fakeClient.CreateOrUpdateDeploymentArgsForCall(i)
					require.Equal(t, expectedDeployment, *dep)
				}(i, m.expectedDeployment)
			}

			installer := &StrategyDeploymentInstaller{
				strategyClient: fakeClient,
				owner:          &mockOwner,
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

	mockOwner := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      mockOwnerName,
			Namespace: namespace,
		},
	}
	mockOwnerRefs := []metav1.OwnerReference{{
		APIVersion:         v1alpha1.ClusterServiceVersionAPIVersion,
		Kind:               v1alpha1.ClusterServiceVersionKind,
		Name:               mockOwner.GetName(),
		UID:                mockOwner.UID,
		Controller:         &Controller,
		BlockOwnerDeletion: &BlockOwnerDeletion,
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
		expectedRole      *rbacv1.Role
		createdRole       *rbacv1.Role
		roleCreationError error

		expectedServiceAccount    *corev1.ServiceAccount
		ensuredServiceAccount     *corev1.ServiceAccount
		ensureServiceAccountError error

		expectedRoleBinding      *rbacv1.RoleBinding
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
					expectedRole: &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleName,
							OwnerReferences: mockOwnerRefs,
						},
						Rules: testRules1,
					},
					createdRole: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
						Name: roleName1,
					}},
					roleCreationError: nil,

					expectedServiceAccount: &corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name: serviceAccountName1,
						},
					},
					ensuredServiceAccount: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
						Name:            ensuredServiceAccountName1,
						OwnerReferences: mockOwnerRefs,
					}},
					ensureServiceAccountError: nil,

					expectedRoleBinding: &rbacv1.RoleBinding{
						RoleRef: rbacv1.RoleRef{
							Kind:     "Role",
							Name:     roleName1,
							APIGroup: rbacv1.GroupName},
						Subjects: []rbacv1.Subject{{
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
					expectedRole: &rbacv1.Role{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName:    generateRoleName,
							OwnerReferences: mockOwnerRefs,
						},
						Rules: testRules2,
					},
					createdRole: &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
						Name: roleName2,
					}},
					roleCreationError: nil,

					expectedServiceAccount: &corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name: serviceAccountName2,
						},
					},
					ensuredServiceAccount: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
						Name:            ensuredServiceAccountName2,
						OwnerReferences: mockOwnerRefs,
					}},
					ensureServiceAccountError: nil,

					expectedRoleBinding: &rbacv1.RoleBinding{
						RoleRef: rbacv1.RoleRef{
							Kind:     "Role",
							Name:     roleName2,
							APIGroup: rbacv1.GroupName},
						Subjects: []rbacv1.Subject{{
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
					expectedRole: &rbacv1.Role{
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
						sa, owner := fakeClient.EnsureServiceAccountArgsForCall(1)
						require.Equal(t, m.expectedServiceAccount, sa)
						require.Equal(t, &mockOwner, owner)
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
				owner:          &mockOwner,
			}
			result := installer.installPermissions(tt.inputs.strategyPermissions)
			assert.Equal(t, tt.output, result)

		})
	}
}

type BadStrategy struct{}

func (b *BadStrategy) GetStrategyName() string {
	return "bad"
}

func TestNewStrategyDeploymentInstaller(t *testing.T) {
	mockOwner := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clusterserviceversion-owner",
			Namespace: "ns",
		},
	}
	fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
	strategy := NewStrategyDeploymentInstaller(fakeClient, &mockOwner, nil)
	require.Implements(t, (*StrategyInstaller)(nil), strategy)
	require.Error(t, strategy.Install(&BadStrategy{}))
	installed, err := strategy.CheckInstalled(&BadStrategy{})
	require.False(t, installed)
	require.Error(t, err)
}

func TestInstallStrategyDeploymentCheckInstallErrors(t *testing.T) {
	namespace := "alm-test-deployment"

	mockOwner := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "clusterserviceversion-owner",
			Namespace: namespace,
		},
	}

	tests := []struct {
		createDeploymentErr    error
		checkServiceAccountErr error
		description            string
	}{
		{
			checkServiceAccountErr: fmt.Errorf("couldn't query serviceaccount"),
			description:            "ErrorCheckingForServiceAccount",
		},
		{
			createDeploymentErr: fmt.Errorf("error creating deployment"),
			description:         "ErrorCreatingDeployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
			strategy := strategy(1, namespace, &mockOwner)
			installer := NewStrategyDeploymentInstaller(fakeClient, &mockOwner, nil)

			skipInstall := tt.checkServiceAccountErr != nil

			fakeClient.GetServiceAccountByNameReturns(testServiceAccount(strategy.Permissions[0].ServiceAccountName, &mockOwner), tt.checkServiceAccountErr)
			defer func() {
				require.Equal(t, strategy.Permissions[0].ServiceAccountName, fakeClient.GetServiceAccountByNameArgsForCall(0))
			}()

			if tt.checkServiceAccountErr == nil {
				dep := testDeployment("alm-dep-1", namespace, &mockOwner)
				fakeClient.FindAnyDeploymentsMatchingNamesReturns(
					[]*appsv1.Deployment{
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
			}

			require.True(t, installed)
			require.NoError(t, err)

			deployment := testDeployment("alm-dep-1", namespace, &mockOwner)
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
