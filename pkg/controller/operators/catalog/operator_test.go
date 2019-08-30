package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/grpc"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
)

type mockTransitioner struct {
	err error
}

var _ installPlanTransitioner = &mockTransitioner{}

func (m *mockTransitioner) ResolvePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func (m *mockTransitioner) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func TestTransitionInstallPlan(t *testing.T) {
	errMsg := "transition test error"
	err := errors.New(errMsg)
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())

	installed := &v1alpha1.InstallPlanCondition{
		Type:   v1alpha1.InstallPlanInstalled,
		Status: corev1.ConditionTrue,
	}
	failed := &v1alpha1.InstallPlanCondition{
		Type:    v1alpha1.InstallPlanInstalled,
		Status:  corev1.ConditionFalse,
		Reason:  v1alpha1.InstallPlanReasonComponentFailed,
		Message: errMsg,
	}

	tests := []struct {
		initial    v1alpha1.InstallPlanPhase
		transError error
		approval   v1alpha1.Approval
		approved   bool
		expected   v1alpha1.InstallPlanPhase
		condition  *v1alpha1.InstallPlanCondition
	}{
		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseComplete, installed},
		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseComplete, installed},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseFailed, failed},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseFailed, failed},

		{v1alpha1.InstallPlanPhaseRequiresApproval, nil, v1alpha1.ApprovalManual, false, v1alpha1.InstallPlanPhaseRequiresApproval, nil},
		{v1alpha1.InstallPlanPhaseRequiresApproval, nil, v1alpha1.ApprovalManual, true, v1alpha1.InstallPlanPhaseInstalling, nil},
	}
	for _, tt := range tests {
		// Create a plan in the provided initial phase.
		plan := &v1alpha1.InstallPlan{
			Spec: v1alpha1.InstallPlanSpec{
				Approval: tt.approval,
				Approved: tt.approved,
			},
			Status: v1alpha1.InstallPlanStatus{
				Phase:      tt.initial,
				Conditions: []v1alpha1.InstallPlanCondition{},
			},
		}

		// Create a transitioner that returns the provided error.
		transitioner := &mockTransitioner{tt.transError}

		// Attempt to transition phases.
		out, _ := transitionInstallPlanState(logrus.New(), transitioner, *plan, now)

		// Assert that the final phase is as expected.
		require.Equal(t, tt.expected, out.Status.Phase)

		// Assert that the condition set is as expected
		if tt.condition == nil {
			require.Equal(t, 0, len(out.Status.Conditions))
		} else {
			require.Equal(t, 1, len(out.Status.Conditions))
			require.Equal(t, tt.condition.Type, out.Status.Conditions[0].Type)
			require.Equal(t, tt.condition.Status, out.Status.Conditions[0].Status)
			require.Equal(t, tt.condition.Reason, out.Status.Conditions[0].Reason)
			require.Equal(t, tt.condition.Message, out.Status.Conditions[0].Message)
		}
	}
}

func TestEnsureCRDVersions(t *testing.T) {
	mainCRDPlural := "ins-main-abcde"

	currentVersions := []v1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1alpha1",
			Served:  true,
			Storage: true,
		},
	}

	addedVersions := []v1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1alpha1",
			Served:  true,
			Storage: false,
		},
		{
			Name:    "v1alpha2",
			Served:  true,
			Storage: true,
		},
	}

	missingVersions := []v1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1alpha2",
			Served:  true,
			Storage: true,
		},
	}

	tests := []struct {
		name            string
		oldCRD          v1beta1.CustomResourceDefinition
		newCRD          v1beta1.CustomResourceDefinition
		expectedFailure bool
	}{
		{
			name: "existing versions are present",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = ""
				oldCRD.Spec.Versions = currentVersions
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = ""
				newCRD.Spec.Versions = addedVersions
				return newCRD
			}(),
			expectedFailure: false,
		},
		{
			name: "missing versions in new CRD",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = ""
				oldCRD.Spec.Versions = currentVersions
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = ""
				newCRD.Spec.Versions = missingVersions
				return newCRD
			}(),
			expectedFailure: true,
		},
		{
			name: "missing version in new CRD",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = "v1alpha1"
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = "v1alpha2"
				return newCRD
			}(),
			expectedFailure: true,
		},
		{
			name: "existing version is present in new CRD's versions",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = "v1alpha1"
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = ""
				newCRD.Spec.Versions = addedVersions
				return newCRD
			}(),
			expectedFailure: false,
		},
	}

	for _, tt := range tests {
		err := ensureCRDVersions(&tt.oldCRD, &tt.newCRD)
		if tt.expectedFailure {
			require.Error(t, err)
		}
	}
}

func TestRemoveDeprecatedStoredVersions(t *testing.T) {
	mainCRDPlural := "ins-main-test"

	currentVersions := []v1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1alpha1",
			Served:  true,
			Storage: false,
		},
		{
			Name:    "v1alpha2",
			Served:  true,
			Storage: true,
		},
	}

	newVersions := []v1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1alpha2",
			Served:  true,
			Storage: false,
		},
		{
			Name:    "v1beta1",
			Served:  true,
			Storage: true,
		},
	}

	crdStatusStoredVersions := v1beta1.CustomResourceDefinitionStatus{
		StoredVersions: []string{},
	}

	tests := []struct {
		name           string
		oldCRD         v1beta1.CustomResourceDefinition
		newCRD         v1beta1.CustomResourceDefinition
		expectedResult []string
	}{
		{
			name: "only one stored version exists",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = ""
				oldCRD.Spec.Versions = currentVersions
				oldCRD.Status = crdStatusStoredVersions
				oldCRD.Status.StoredVersions = []string{"v1alpha1"}
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = ""
				newCRD.Spec.Versions = newVersions
				return newCRD
			}(),
			expectedResult: nil,
		},
		{
			name: "multiple stored versions with one deprecated version",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = ""
				oldCRD.Spec.Versions = currentVersions
				oldCRD.Status.StoredVersions = []string{"v1alpha1", "v1alpha2"}
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = ""
				newCRD.Spec.Versions = newVersions
				return newCRD
			}(),
			expectedResult: []string{"v1alpha2"},
		},
		{
			name: "multiple stored versions with all deprecated version",
			oldCRD: func() v1beta1.CustomResourceDefinition {
				oldCRD := crd(mainCRDPlural)
				oldCRD.Spec.Version = ""
				oldCRD.Spec.Versions = currentVersions
				oldCRD.Status.StoredVersions = []string{"v1alpha1", "v1alpha3"}
				return oldCRD
			}(),
			newCRD: func() v1beta1.CustomResourceDefinition {
				newCRD := crd(mainCRDPlural)
				newCRD.Spec.Version = ""
				newCRD.Spec.Versions = newVersions
				return newCRD
			}(),
			expectedResult: nil,
		},
	}

	for _, tt := range tests {
		resultCRD := removeDeprecatedStoredVersions(&tt.oldCRD, &tt.newCRD)
		require.Equal(t, tt.expectedResult, resultCRD)
	}
}

func TestExecutePlan(t *testing.T) {
	namespace := "ns"

	tests := []struct {
		testName string
		in       *v1alpha1.InstallPlan
		want     []runtime.Object
		err      error
	}{
		{
			testName: "NoSteps",
			in:       installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling),
			want:     []runtime.Object{},
			err:      nil,
		},
		{
			testName: "MultipleSteps",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "Service",
							Name:                   "service",
							Manifest:               toManifest(service("service", namespace)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "operators.coreos.com",
							Version:                "v1alpha1",
							Kind:                   "ClusterServiceVersion",
							Name:                   "csv",
							Manifest:               toManifest(csv("csv", namespace, nil, nil)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{service("service", namespace), csv("csv", namespace, nil, nil)},
			err:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			op, err := NewFakeOperator(ctx, namespace, []string{namespace}, withClientObjs(tt.in))
			require.NoError(t, err)

			err = op.ExecutePlan(tt.in)
			require.Equal(t, tt.err, err)

			for _, obj := range tt.want {
				var err error
				var fetched runtime.Object
				switch o := obj.(type) {
				case *appsv1.Deployment:
					fetched, err = op.opClient.GetDeployment(namespace, o.GetName())
				case *rbacv1.ClusterRole:
					fetched, err = op.opClient.GetClusterRole(o.GetName())
				case *rbacv1.Role:
					fetched, err = op.opClient.GetRole(namespace, o.GetName())
				case *rbacv1.ClusterRoleBinding:
					fetched, err = op.opClient.GetClusterRoleBinding(o.GetName())
				case *rbacv1.RoleBinding:
					fetched, err = op.opClient.GetRoleBinding(namespace, o.GetName())
				case *corev1.ServiceAccount:
					fetched, err = op.opClient.GetServiceAccount(namespace, o.GetName())
				case *corev1.Service:
					fetched, err = op.opClient.GetService(namespace, o.GetName())
				case *v1alpha1.ClusterServiceVersion:
					fetched, err = op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(o.GetName(), metav1.GetOptions{})
				default:
					require.Failf(t, "couldn't find expected object", "%#v", obj)
				}

				require.NoError(t, err, "couldn't fetch %s %v", namespace, obj)
				require.EqualValues(t, obj, fetched)
			}
		})
	}
}

func TestSyncCatalogSources(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())

	configmapCatalog := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cool-catalog",
			Namespace: "cool-namespace",
			UID:       types.UID("catalog-uid"),
		},
		Spec: v1alpha1.CatalogSourceSpec{
			ConfigMap:  "cool-configmap",
			SourceType: v1alpha1.SourceTypeInternal,
		},
	}
	grpcCatalog := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cool-catalog",
			Namespace: "cool-namespace",
			UID:       types.UID("catalog-uid"),
			Labels:    map[string]string{"olm.catalogSource": "cool-catalog"},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Image:      "catalog-image",
			SourceType: v1alpha1.SourceTypeGrpc,
		},
	}
	tests := []struct {
		testName       string
		namespace      string
		catalogSource  *v1alpha1.CatalogSource
		k8sObjs        []runtime.Object
		configMap      *corev1.ConfigMap
		expectedStatus *v1alpha1.CatalogSourceStatus
		expectedObjs   []runtime.Object
		expectedError  error
	}{
		{
			testName:  "CatalogSourceWithInvalidSourceType",
			namespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					ConfigMap:  "cool-configmap",
					SourceType: "nope",
				},
			},
			k8sObjs: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "cool-configmap",
						Namespace:       "cool-namespace",
						UID:             types.UID("configmap-uid"),
						ResourceVersion: "resource-version",
					},
					Data: fakeConfigMapData(),
				},
			},
			expectedStatus: nil,
			expectedError:  fmt.Errorf("no reconciler for source type nope"),
		},
		{
			testName:      "CatalogSourceWithBackingConfigMap",
			namespace:     "cool-namespace",
			catalogSource: configmapCatalog,
			k8sObjs: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "cool-configmap",
						Namespace:       "cool-namespace",
						UID:             types.UID("configmap-uid"),
						ResourceVersion: "resource-version",
					},
					Data: fakeConfigMapData(),
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
					LastUpdateTime: now,
				},
				RegistryServiceStatus: nil,
			},
			expectedError: nil,
		},
		{
			testName:  "CatalogSourceUpdatedByDifferentCatalogOperator",
			namespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					ConfigMap:  "cool-configmap",
					SourceType: v1alpha1.SourceTypeConfigmap,
				},
				Status: v1alpha1.CatalogSourceStatus{
					ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
						Name:            "cool-configmap",
						Namespace:       "cool-namespace",
						UID:             types.UID("configmap-uid"),
						ResourceVersion: "resource-version",
						LastUpdateTime:  now,
					},
					RegistryServiceStatus: nil,
				},
			},
			k8sObjs: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "cool-configmap",
						Namespace:       "cool-namespace",
						UID:             types.UID("configmap-uid"),
						ResourceVersion: "resource-version",
					},
					Data: fakeConfigMapData(),
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
					LastUpdateTime:  now,
				},
				RegistryServiceStatus: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
					CreatedAt:        now,
				},
			},
			expectedError: nil,
		},
		{
			testName:      "CatalogSourceWithMissingConfigMap",
			namespace:     "cool-namespace",
			catalogSource: configmapCatalog,
			k8sObjs: []runtime.Object{
				&corev1.ConfigMap{},
			},
			expectedStatus: nil,
			expectedError:  errors.New("failed to get catalog config map cool-configmap: configmap \"cool-configmap\" not found"),
		},
		{
			testName:      "CatalogSourceWithGrpcImage",
			namespace:     "cool-namespace",
			catalogSource: grpcCatalog,
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				RegistryServiceStatus: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
					CreatedAt:        now,
				},
			},
			expectedError: nil,
			expectedObjs: []runtime.Object{
				pod(*grpcCatalog),
			},
		},
		{
			testName:      "CatalogSourceWithGrpcImage/EnsuresCorrectImage",
			namespace:     "cool-namespace",
			catalogSource: grpcCatalog,
			k8sObjs: []runtime.Object{
				pod(v1alpha1.CatalogSource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cool-catalog",
						Namespace: "cool-namespace",
						UID:       types.UID("catalog-uid"),
						Labels:    map[string]string{"olm.catalogSource": "cool-catalog"},
					},
					Spec: v1alpha1.CatalogSourceSpec{
						Image:      "old-image",
						SourceType: v1alpha1.SourceTypeGrpc,
					},
				}),
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				RegistryServiceStatus: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
					CreatedAt:        now,
				},
			},
			expectedError: nil,
			expectedObjs: []runtime.Object{
				pod(*grpcCatalog),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			// Create existing objects
			clientObjs := []runtime.Object{tt.catalogSource}

			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			op, err := NewFakeOperator(ctx, tt.namespace, []string{tt.namespace}, withClock(clockFake), withClientObjs(clientObjs...), withK8sObjs(tt.k8sObjs...))
			require.NoError(t, err)

			// Run sync
			err = op.syncCatalogSources(tt.catalogSource)
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
			} else {
				require.NoError(t, err)
			}

			// Get updated catalog and check status
			updated, err := op.client.OperatorsV1alpha1().CatalogSources(tt.catalogSource.GetNamespace()).Get(tt.catalogSource.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, updated)

			if tt.expectedStatus != nil {
				require.NotEmpty(t, updated.Status)
				require.Equal(t, *tt.expectedStatus, updated.Status)

				if tt.catalogSource.Spec.ConfigMap != "" {
					configMap, err := op.opClient.KubernetesInterface().CoreV1().ConfigMaps(tt.catalogSource.GetNamespace()).Get(tt.catalogSource.Spec.ConfigMap, metav1.GetOptions{})
					require.NoError(t, err)
					require.True(t, ownerutil.EnsureOwner(configMap, updated))
				}
			}

			for _, o := range tt.expectedObjs {
				switch o.(type) {
				case *corev1.Pod:
					t.Log("verifying pod")
					pods, err := op.opClient.KubernetesInterface().CoreV1().Pods(tt.catalogSource.Namespace).List(metav1.ListOptions{})
					require.NoError(t, err)
					require.Len(t, pods.Items, 1)

					// set the name to the generated name
					o.(*corev1.Pod).SetName(pods.Items[0].GetName())
					require.EqualValues(t, o, &pods.Items[0])
				}
			}
		})
	}
}

func TestCompetingCRDOwnersExist(t *testing.T) {

	testNamespace := "default"
	tests := []struct {
		name              string
		csv               *v1alpha1.ClusterServiceVersion
		existingCRDOwners map[string][]string
		expectedErr       error
		expectedResult    bool
	}{
		{
			name:              "NoCompetingOwnersExist",
			csv:               csv("turkey", testNamespace, []string{"feathers"}, nil),
			existingCRDOwners: nil,
			expectedErr:       nil,
			expectedResult:    false,
		},
		{
			name: "OnlyCompetingWithSelf",
			csv:  csv("turkey", testNamespace, []string{"feathers"}, nil),
			existingCRDOwners: map[string][]string{
				"feathers": {"turkey"},
			},
			expectedErr:    nil,
			expectedResult: false,
		},
		{
			name: "CompetingOwnersExist",
			csv:  csv("turkey", testNamespace, []string{"feathers"}, nil),
			existingCRDOwners: map[string][]string{
				"feathers": {"seagull"},
			},
			expectedErr:    nil,
			expectedResult: true,
		},
		{
			name: "CompetingOwnerExistsOnSecondCRD",
			csv:  csv("turkey", testNamespace, []string{"feathers", "beak"}, nil),
			existingCRDOwners: map[string][]string{
				"milk": {"cow"},
				"beak": {"squid"},
			},
			expectedErr:    nil,
			expectedResult: true,
		},
		{
			name: "MoreThanOneCompetingOwnerExists",
			csv:  csv("turkey", testNamespace, []string{"feathers"}, nil),
			existingCRDOwners: map[string][]string{
				"feathers": {"seagull", "turkey"},
			},
			expectedErr:    olmerrors.NewMultipleExistingCRDOwnersError([]string{"seagull", "turkey"}, "feathers", testNamespace),
			expectedResult: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			competing, err := competingCRDOwnersExist(testNamespace, tt.csv, tt.existingCRDOwners)

			// Assert the error is as expected
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			require.Equal(t, competing, tt.expectedResult)
		})
	}
}

func fakeConfigMapData() map[string]string {
	data := make(map[string]string)
	yaml, err := yaml.Marshal([]v1beta1.CustomResourceDefinition{crd("fake-crd")})
	if err != nil {
		return data
	}

	data["customResourceDefinitions"] = string(yaml)
	return data
}

// fakeOperatorConfig is the configuration for a fake operator.
type fakeOperatorConfig struct {
	clock         utilclock.Clock
	clientObjs    []runtime.Object
	k8sObjs       []runtime.Object
	extObjs       []runtime.Object
	regObjs       []runtime.Object
	clientOptions []clientfake.Option
	logger        *logrus.Logger
	resolver      resolver.Resolver
	reconciler    reconciler.RegistryReconcilerFactory
}

// fakeOperatorOption applies an option to the given fake operator configuration.
type fakeOperatorOption func(*fakeOperatorConfig)

func withResolver(res resolver.Resolver) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.resolver = res
	}
}

func withReconciler(rec reconciler.RegistryReconcilerFactory) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.reconciler = rec
	}
}

func withClock(clock utilclock.Clock) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.clock = clock
	}
}

func withClientObjs(clientObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.clientObjs = clientObjs
	}
}

func withK8sObjs(k8sObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.k8sObjs = k8sObjs
	}
}

func extObjs(extObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.extObjs = extObjs
	}
}

func withFakeClientOptions(options ...clientfake.Option) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.clientOptions = options
	}
}

// NewFakeOperator creates a new operator using fake clients.
func NewFakeOperator(ctx context.Context, namespace string, watchedNamespaces []string, fakeOptions ...fakeOperatorOption) (*Operator, error) {
	// Apply options to default config
	config := &fakeOperatorConfig{
		logger:   logrus.StandardLogger(),
		clock:    utilclock.RealClock{},
		resolver: &fakes.FakeResolver{},
	}
	for _, option := range fakeOptions {
		option(config)
	}

	// Create client fakes
	clientFake := fake.NewReactionForwardingClientsetDecorator(config.clientObjs, config.clientOptions...)
	opClientFake := operatorclient.NewClient(k8sfake.NewSimpleClientset(config.k8sObjs...), apiextensionsfake.NewSimpleClientset(config.extObjs...), apiregistrationfake.NewSimpleClientset(config.regObjs...))

	// Create operator namespace
	_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
	if err != nil {
		return nil, err
	}

	wakeupInterval := 5 * time.Minute
	lister := operatorlister.NewLister()
	var sharedInformers []cache.SharedIndexInformer
	for _, ns := range watchedNamespaces {
		if ns != namespace {
			_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
			if err != nil {
				return nil, err
			}
		}

		// Create informers and register listers
		operatorsFactory := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(ns))
		catsrcInformer := operatorsFactory.Operators().V1alpha1().CatalogSources()
		subInformer := operatorsFactory.Operators().V1alpha1().Subscriptions()
		ipInformer := operatorsFactory.Operators().V1alpha1().InstallPlans()
		csvInformer := operatorsFactory.Operators().V1alpha1().ClusterServiceVersions()
		sharedInformers = append(sharedInformers, catsrcInformer.Informer(), subInformer.Informer(), ipInformer.Informer(), csvInformer.Informer())

		lister.OperatorsV1alpha1().RegisterCatalogSourceLister(ns, catsrcInformer.Lister())
		lister.OperatorsV1alpha1().RegisterSubscriptionLister(ns, subInformer.Lister())
		lister.OperatorsV1alpha1().RegisterInstallPlanLister(ns, ipInformer.Lister())
		lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(ns, csvInformer.Lister())

		factory := informers.NewSharedInformerFactoryWithOptions(opClientFake.KubernetesInterface(), wakeupInterval, informers.WithNamespace(ns))
		roleInformer := factory.Rbac().V1().Roles()
		roleBindingInformer := factory.Rbac().V1().RoleBindings()
		serviceAccountInformer := factory.Core().V1().ServiceAccounts()
		serviceInformer := factory.Core().V1().Services()
		podInformer := factory.Core().V1().Pods()
		configMapInformer := factory.Core().V1().ConfigMaps()
		sharedInformers = append(sharedInformers, roleInformer.Informer(), roleBindingInformer.Informer(), serviceAccountInformer.Informer(), serviceInformer.Informer(), podInformer.Informer(), configMapInformer.Informer())

		lister.RbacV1().RegisterRoleLister(ns, roleInformer.Lister())
		lister.RbacV1().RegisterRoleBindingLister(ns, roleBindingInformer.Lister())
		lister.CoreV1().RegisterServiceAccountLister(ns, serviceAccountInformer.Lister())
		lister.CoreV1().RegisterServiceLister(ns, serviceInformer.Lister())
		lister.CoreV1().RegisterPodLister(ns, podInformer.Lister())
		lister.CoreV1().RegisterConfigMapLister(ns, configMapInformer.Lister())

	}

	logger := logrus.New()

	// Create the new operator
	queueOperator, err := queueinformer.NewOperator(opClientFake.KubernetesInterface().Discovery())
	for _, informer := range sharedInformers {
		queueOperator.RegisterInformer(informer)
	}

	op := &Operator{
		Operator:  queueOperator,
		clock:     config.clock,
		logger:    config.logger,
		opClient:  opClientFake,
		client:    clientFake,
		lister:    lister,
		namespace: namespace,
		nsResolveQueue: workqueue.NewNamedRateLimitingQueue(
			workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 1000*time.Second),
				// 1 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(1), 100)},
			), "resolver"),
		resolver:              config.resolver,
		reconciler:            config.reconciler,
		clientAttenuator:      scoped.NewClientAttenuator(logger, &rest.Config{}, opClientFake, clientFake),
		serviceAccountQuerier: scoped.NewUserDefinedServiceAccountQuerier(logger, clientFake),
		catsrcQueueSet:         queueinformer.NewEmptyResourceQueueSet(),
	}
	op.sources = grpc.NewSourceStore(config.logger, 1*time.Second, 5*time.Second, op.syncSourceState)
	if op.reconciler == nil {
		op.reconciler = reconciler.NewRegistryReconcilerFactory(lister, op.opClient, "test:pod", op.now)
	}

	op.RunInformers(ctx)
	op.sources.Start(ctx)

	if ok := cache.WaitForCacheSync(ctx.Done(), op.HasSynced); !ok {
		return nil, fmt.Errorf("failed to wait for caches to sync")
	}

	return op, nil
}

func installPlan(name, namespace string, phase v1alpha1.InstallPlanPhase, names ...string) *v1alpha1.InstallPlan {
	return &v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: names,
		},
		Status: v1alpha1.InstallPlanStatus{
			Phase: phase,
			Plan:  []*v1alpha1.Step{},
		},
	}
}

func withSteps(plan *v1alpha1.InstallPlan, steps []*v1alpha1.Step) *v1alpha1.InstallPlan {
	plan.Status.Plan = steps
	return plan
}

func csv(name, namespace string, owned, required []string) *v1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, name := range required {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}
	if len(requiredCRDDescs) == 0 {
		requiredCRDDescs = nil
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, name := range owned {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}
	if len(ownedCRDDescs) == 0 {
		ownedCRDDescs = nil
	}

	return &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
	}
}

func crd(name string) v1beta1.CustomResourceDefinition {
	return v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   name + "group",
			Version: "v1",
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

func service(name, namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func toManifest(obj runtime.Object) string {
	raw, _ := json.Marshal(obj)
	return string(raw)
}

func pod(s v1alpha1.CatalogSource) *corev1.Pod {
	pod := reconciler.Pod(&s, "registry-server", s.Spec.Image, s.GetLabels(), 5, 10)
	ownerutil.AddOwner(pod, &s, false, false)
	return pod
}
