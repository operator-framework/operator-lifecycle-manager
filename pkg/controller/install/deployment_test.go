package install

import (
	"fmt"
	"testing"
	"context"
	gomock "github.com/golang/mock/gomock"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/operators/v1"
	clientfakes "github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers/wrappersfakes"
	clientv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/labels"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// operatorGroupNamespaceLister implements the OperatorGroupLister interface.
type fake_operatorGroupNamespaceLister struct {
	name string
	ret  []*v1.OperatorGroup
}

// List lists all OperatorGroups in the indexer.
func (s *fake_operatorGroupNamespaceLister) List(selector k8slabels.Selector) (ret []*v1.OperatorGroup, err error) {
	s.ret = make([]*v1.OperatorGroup, 1)
	og := new(v1.OperatorGroup)
	s.ret[0] = og
	return s.ret, nil
}

// OperatorGroups returns an object that can list and get OperatorGroups.
func (s *fake_operatorGroupNamespaceLister) Get(name string) (*v1.OperatorGroup, error) {
	return nil, nil
}

// operatorGroupLister implements the OperatorGroupLister interface.
type fake_operatorGroupLister struct {
	name string
	operatorGroupNamespaceLister clientv1.OperatorGroupNamespaceLister
}

// List lists all OperatorGroups in the indexer.
func (s *fake_operatorGroupLister) List(selector k8slabels.Selector) (ret []*v1.OperatorGroup, err error) {
	return nil, nil
}

// OperatorGroups returns an object that can list and get OperatorGroups.
func (s *fake_operatorGroupLister) OperatorGroups(namespace string) clientv1.OperatorGroupNamespaceLister {
	s.operatorGroupNamespaceLister = new(fake_operatorGroupNamespaceLister)
	return s.operatorGroupNamespaceLister
}

func testDeployment(name, namespace string, mockOwner ownerutil.Owner) appsv1.Deployment {
	testDeploymentLabels := map[string]string{"olm.owner": mockOwner.GetName(), "olm.owner.namespace": mockOwner.GetNamespace(), "olm.owner.kind": "ClusterServiceVersion"}

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
					Controller:         &ownerutil.NotController,
					BlockOwnerDeletion: &ownerutil.DontBlockOwnerDeletion,
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
			Controller:         &ownerutil.NotController,
			BlockOwnerDeletion: &ownerutil.DontBlockOwnerDeletion,
		},
	})
	return serviceAccount
}

func strategy(n int, namespace string, mockOwner ownerutil.Owner) *v1alpha1.StrategyDetailsDeployment {
	var deploymentSpecs = []v1alpha1.StrategyDeploymentSpec{}
	var permissions = []v1alpha1.StrategyDeploymentPermissions{}
	for i := 1; i <= n; i++ {
		dep := testDeployment(fmt.Sprintf("olm-dep-%d", i), namespace, mockOwner)
		spec := v1alpha1.StrategyDeploymentSpec{Name: dep.GetName(), Spec: dep.Spec}
		deploymentSpecs = append(deploymentSpecs, spec)
		serviceAccount := testServiceAccount(fmt.Sprintf("olm-sa-%d", i), mockOwner)
		permissions = append(permissions, v1alpha1.StrategyDeploymentPermissions{
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
	return &v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: deploymentSpecs,
		Permissions:     permissions,
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
// TODO - add namespae to fake				Namespace: "olm-test-deployment",
			},
		}
		mockOwnerRefs = []metav1.OwnerReference{{
			APIVersion:         v1alpha1.ClusterServiceVersionAPIVersion,
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               mockOwner.GetName(),
			UID:                mockOwner.UID,
			Controller:         &ownerutil.NotController,
			BlockOwnerDeletion: &ownerutil.DontBlockOwnerDeletion,
		}}
		expectedRevisionHistoryLimit = int32(1)
		defaultRevisionHistoryLimit  = int32(10)
	)

	type inputs struct {
		strategyDeploymentSpecs []v1alpha1.StrategyDeploymentSpec
	}
	type setup struct {
		existingDeployments []*appsv1.Deployment
	}
	type createOrUpdateMock struct {
		expectedDeployment appsv1.Deployment
		returnError        error
	}
	type label struct {
		key   string
		value string
	}
	tests := []struct {
		description         string
		inputs              inputs
		setup               setup
		createOrUpdateMocks []createOrUpdateMock
		output              error
		customLabel         label
	}{
		{
			description: "updates/creates correctly",
			inputs: inputs{
				strategyDeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
					{
						Name: "test-deployment-1",
						Spec: appsv1.DeploymentSpec{
							RevisionHistoryLimit: &defaultRevisionHistoryLimit,
						},
					},
					{
						Name: "test-deployment-2",
						Spec: appsv1.DeploymentSpec{
							RevisionHistoryLimit: nil,
						},
					},
					{
						Name: "test-deployment-3",
						Spec: appsv1.DeploymentSpec{},
					},
					{
						Name:  "test-deployment-4",
						Spec:  appsv1.DeploymentSpec{},
						Label: k8slabels.Set{"custom-label": "custom-label-value"},
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
								"olm.owner":           mockOwner.GetName(),
								"olm.owner.namespace": mockOwner.GetNamespace(),
							},
						},
						Spec: appsv1.DeploymentSpec{
							RevisionHistoryLimit: &expectedRevisionHistoryLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Annotations: map[string]string{},
								},
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
								"olm.owner":           mockOwner.GetName(),
								"olm.owner.namespace": mockOwner.GetNamespace(),
							},
						},
						Spec: appsv1.DeploymentSpec{
							RevisionHistoryLimit: &expectedRevisionHistoryLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Annotations: map[string]string{},
								},
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
								"olm.owner":           mockOwner.GetName(),
								"olm.owner.namespace": mockOwner.GetNamespace(),
							},
						},
						Spec: appsv1.DeploymentSpec{
							RevisionHistoryLimit: &expectedRevisionHistoryLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Annotations: map[string]string{},
								},
							},
						},
					},
					returnError: nil,
				},
				{
					expectedDeployment: appsv1.Deployment{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-4",
							Namespace:       mockOwner.GetNamespace(),
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"olm.owner":           mockOwner.GetName(),
								"olm.owner.namespace": mockOwner.GetNamespace(),
								"custom-label":        "custom-label-value",
							},
						},
						Spec: appsv1.DeploymentSpec{
							RevisionHistoryLimit: &expectedRevisionHistoryLimit,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Annotations: map[string]string{},
								},
							},
						},
					},
					returnError: nil,
				},
			},
			output: nil,
			customLabel: label {
				key:   "custom-label",
				value: "custom-label-value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockOpClient := operatorclientmocks.NewMockClientInterface(ctrl)

			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
			fakeOperatorLister := new(operatorlisterfakes.FakeOperatorLister)
			fakeOperatorsV1Lister := new(operatorlisterfakes.FakeOperatorsV1Lister)
			fakeOperatorsV1Lister.OperatorGroupListerReturns(new(fake_operatorGroupLister))
			fakeOperatorLister.OperatorsV1Returns(fakeOperatorsV1Lister)
			fake := fake.NewSimpleClientset()
			for i, m := range tt.createOrUpdateMocks {
				fakeClient.CreateDeploymentReturns(nil, m.returnError)
				fakeClient.GetOpListerReturns(fakeOperatorLister)
				fakeClient.GetOpClientReturns(mockOpClient)
				mockOpClient.EXPECT().KubernetesInterface().Return(fake)
				mockOpClient.EXPECT().KubernetesInterface().Return(fake)

				defer func(i int, expectedDeployment appsv1.Deployment) {
					dep := fakeClient.CreateOrUpdateDeploymentArgsForCall(i)
					expectedDeployment.Spec.Template.Annotations = map[string]string{}
					require.Equal(t, expectedDeployment.OwnerReferences, dep.OwnerReferences)
					for labelKey, labelValue := range expectedDeployment.Labels {
						require.Contains(t, dep.GetLabels(), labelKey)
						require.Equal(t, dep.Labels[labelKey], labelValue)
					}
					require.Equal(t, expectedDeployment.Spec.RevisionHistoryLimit, dep.Spec.RevisionHistoryLimit)
				}(i, m.expectedDeployment)
			}
			fake_certResources := make([]certResource, 1)
			fake_certResources[0] = &webhookDescriptionWithCAPEM{v1alpha1.WebhookDescription{Type: v1alpha1.ValidatingAdmissionWebhook}, []byte{}}
			installer := &StrategyDeploymentInstaller{
				strategyClient: fakeClient,
				owner:          &mockOwner,
				webhookDescriptions: fake_certResources,
			}
			result := installer.installDeployments(tt.inputs.strategyDeploymentSpecs)
			assert.Equal(t, tt.output, result)
			got, _ := fake.AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{})
			assert.Equal(t, tt.customLabel.value, got.Items[0].ObjectMeta.Labels[tt.customLabel.key])
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
	strategy := NewStrategyDeploymentInstaller(fakeClient, map[string]string{"test": "annotation"}, &mockOwner, nil, nil, nil, nil)
	require.Implements(t, (*StrategyInstaller)(nil), strategy)
	require.Error(t, strategy.Install(&BadStrategy{}))
	installed, err := strategy.CheckInstalled(&BadStrategy{})
	require.False(t, installed)
	require.Error(t, err)
}

func TestInstallStrategyDeploymentCheckInstallErrors(t *testing.T) {
	namespace := "olm-test-deployment"

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

	mockOwnerLabel := ownerutil.CSVOwnerSelector(&mockOwner)

	tests := []struct {
		createDeploymentErr error
		description         string
	}{
		{
			createDeploymentErr: fmt.Errorf("error creating deployment"),
			description:         "ErrorCreatingDeployment",
		},
	}

	revisionHistoryLimit := int32(1)
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
			strategy := strategy(1, namespace, &mockOwner)
			installer := NewStrategyDeploymentInstaller(fakeClient, map[string]string{"test": "annotation"}, &mockOwner, nil, nil, nil, nil)

			dep := testDeployment("olm-dep-1", namespace, &mockOwner)
			dep.Spec.Template.SetAnnotations(map[string]string{"test": "annotation"})
			dep.Spec.RevisionHistoryLimit = &revisionHistoryLimit
			dep.SetLabels(labels.CloneAndAddLabel(dep.ObjectMeta.GetLabels(), DeploymentSpecHashLabelKey, HashDeploymentSpec(dep.Spec)))
			dep.Status.Conditions = append(dep.Status.Conditions, appsv1.DeploymentCondition{
				Type:   appsv1.DeploymentAvailable,
				Status: corev1.ConditionTrue,
			})
			fakeClient.FindAnyDeploymentsMatchingLabelsReturns(
				[]*appsv1.Deployment{
					&dep,
				}, nil,
			)
			defer func() {
				require.Equal(t, mockOwnerLabel, fakeClient.FindAnyDeploymentsMatchingLabelsArgsForCall(0))
			}()

			installed, err := installer.CheckInstalled(strategy)
			require.NoError(t, err)
			require.True(t, installed)

			deployment := testDeployment("olm-dep-1", namespace, &mockOwner)
			deployment.Spec.Template.SetAnnotations(map[string]string{"test": "annotation"})
			deployment.Spec.RevisionHistoryLimit = &revisionHistoryLimit
			deployment.SetLabels(labels.CloneAndAddLabel(dep.ObjectMeta.GetLabels(), DeploymentSpecHashLabelKey, HashDeploymentSpec(deployment.Spec)))
			fakeClient.CreateOrUpdateDeploymentReturns(&deployment, tt.createDeploymentErr)
			defer func() {
				require.Equal(t, &deployment, fakeClient.CreateOrUpdateDeploymentArgsForCall(0))
			}()

			if tt.createDeploymentErr != nil {
				err := installer.Install(strategy)
				require.Error(t, err)
			}
		})
	}
}

func TestInstallStrategyDeploymentCleanupDeployments(t *testing.T) {
	var (
		mockOwner = v1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.ClusterServiceVersionKind,
				APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "clusterserviceversion-owner",
				Namespace: "olm-test-deployment",
			},
		}
		mockOwnerRefs = []metav1.OwnerReference{{
			APIVersion:         v1alpha1.ClusterServiceVersionAPIVersion,
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               mockOwner.GetName(),
			UID:                mockOwner.UID,
			Controller:         &ownerutil.NotController,
			BlockOwnerDeletion: &ownerutil.DontBlockOwnerDeletion,
		}}
	)

	type inputs struct {
		strategyDeploymentSpecs []v1alpha1.StrategyDeploymentSpec
	}
	type setup struct {
		existingDeployments []*appsv1.Deployment
		returnError         error
	}
	type cleanupMock struct {
		deletedDeploymentName string
		returnError           error
	}
	tests := []struct {
		description string
		inputs      inputs
		setup       setup
		cleanupMock cleanupMock
		output      error
	}{
		{
			description: "cleanup successfully",
			inputs: inputs{
				strategyDeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
					{
						Name: "test-deployment-1",
						Spec: appsv1.DeploymentSpec{},
					},
				},
			},
			setup: setup{
				existingDeployments: []*appsv1.Deployment{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-2",
							Namespace:       mockOwner.GetNamespace(),
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"olm.owner":           mockOwner.GetName(),
								"olm.owner.namespace": mockOwner.GetNamespace(),
							},
						},
					},
				},
				returnError: nil,
			},
			cleanupMock: cleanupMock{
				deletedDeploymentName: "test-deployment-2",
				returnError:           nil,
			},
			output: nil,
		},
		{
			description: "cleanup unsuccessfully as no orphaned deployments found",
			inputs: inputs{
				strategyDeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
					{
						Name: "test-deployment-1",
						Spec: appsv1.DeploymentSpec{},
					},
				},
			},
			setup: setup{
				existingDeployments: []*appsv1.Deployment{},
				returnError:         fmt.Errorf("error getting deployments"),
			},
			cleanupMock: cleanupMock{
				deletedDeploymentName: "",
				returnError:           nil,
			},
			output: fmt.Errorf("error getting deployments"),
		},
		{
			description: "cleanup unsuccessfully as unable to look up orphaned deployments",
			inputs: inputs{
				strategyDeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
					{
						Name: "test-deployment-1",
						Spec: appsv1.DeploymentSpec{},
					},
				},
			},
			setup: setup{
				existingDeployments: []*appsv1.Deployment{},
				returnError:         fmt.Errorf("error unable to look up orphaned deployments"),
			},
			cleanupMock: cleanupMock{
				deletedDeploymentName: "",
				returnError:           nil,
			},
			output: fmt.Errorf("error unable to look up orphaned deployments"),
		},
		{
			description: "cleanup unsuccessfully as unable to delete deployments",
			inputs: inputs{
				strategyDeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
					{
						Name: "test-deployment-1",
						Spec: appsv1.DeploymentSpec{},
					},
				},
			},
			setup: setup{
				existingDeployments: []*appsv1.Deployment{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-deployment-2",
							Namespace:       mockOwner.GetNamespace(),
							OwnerReferences: mockOwnerRefs,
							Labels: map[string]string{
								"olm.owner":           mockOwner.GetName(),
								"olm.owner.namespace": mockOwner.GetNamespace(),
							},
						},
					},
				},
				returnError: nil,
			},
			cleanupMock: cleanupMock{
				deletedDeploymentName: "",
				returnError:           fmt.Errorf("error unable to delete deployments"),
			},
			output: fmt.Errorf("error unable to delete deployments"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			fakeClient := new(clientfakes.FakeInstallStrategyDeploymentInterface)
			installer := &StrategyDeploymentInstaller{
				strategyClient: fakeClient,
				owner:          &mockOwner,
			}

			fakeClient.FindAnyDeploymentsMatchingLabelsReturns(
				tt.setup.existingDeployments, tt.setup.returnError,
			)

			fakeClient.DeleteDeploymentReturns(tt.cleanupMock.returnError)

			if tt.setup.returnError == nil && tt.cleanupMock.returnError == nil {
				defer func() {
					deletedDep := fakeClient.DeleteDeploymentArgsForCall(0)
					require.Equal(t, tt.cleanupMock.deletedDeploymentName, deletedDep)
				}()
			}

			result := installer.cleanupOrphanedDeployments(tt.inputs.strategyDeploymentSpecs)
			assert.Equal(t, tt.output, result)
		})
	}
}
