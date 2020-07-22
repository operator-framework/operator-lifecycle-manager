package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
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

func TestSyncInstallPlanUnhappy(t *testing.T) {
	namespace := "ns"

	tests := []struct {
		testName string
		in       *v1alpha1.InstallPlan
		err      error
	}{
		{
			testName: "NoStatus",
			in:       installPlan("p", namespace, v1alpha1.InstallPlanPhaseNone),
			err:      nil,
		},
		{
			// This checks that installplans are not applied when no operatorgroup is present
			testName: "HasSteps/NoOperatorGroup",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ServiceAccount",
							Name:                   "sa",
							Manifest: toManifest(t, serviceAccount("sa", namespace, "",
								objectReference("init secret"))),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			err:  fmt.Errorf("attenuated service account query failed - no operator group found that is managing this namespace"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			op, err := NewFakeOperator(ctx, namespace, []string{namespace}, withClientObjs(tt.in))
			require.NoError(t, err)

			err = op.syncInstallPlans(tt.in)
			require.Equal(t, tt.err, err)
		})
	}
}

func TestExecutePlan(t *testing.T) {
	namespace := "ns"

	tests := []struct {
		testName string
		in       *v1alpha1.InstallPlan
		extObjs  []runtime.Object
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
							Manifest:               toManifest(t, service("service", namespace)),
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
							Manifest:               toManifest(t, csv("csv", namespace, nil, nil)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{service("service", namespace), csv("csv", namespace, nil, nil)},
			err:  nil,
		},
		{
			testName: "CreateServiceAccount",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ServiceAccount",
							Name:                   "sa",
							Manifest: toManifest(t, serviceAccount("sa", namespace, "",
								objectReference("init secret"))),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{serviceAccount("sa", namespace, "", objectReference("init secret"))},
			err:  nil,
		},
		{
			testName: "CreateConfigMap",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ConfigMap",
							Name:                   "cfg",
							Manifest:               toManifest(t, configMap("cfg", namespace)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{configMap("cfg", namespace)},
			err:  nil,
		},
		{
			testName: "CreateSecretFromBundle",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "BundleSecret",
							Name:                   "s",
							Manifest:               toManifest(t, secret("s", namespace)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{secret("s", namespace)},
			err:  nil,
		},
		{
			testName: "DoesNotCreateSecretNotFromBundle",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "Secret",
							Name:                   "s",
							Manifest:               toManifest(t, secret("s", namespace)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{},
			err:  fmt.Errorf("secret s does not exist - secrets \"s\" not found"),
		},
		{
			testName: "UpdateServiceAccountWithSameFields",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ServiceAccount",
							Name:                   "sa",
							Manifest: toManifest(t, serviceAccount("sa", namespace, "name",
								objectReference("init secret"))),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ServiceAccount",
							Name:                   "sa",
							Manifest:               toManifest(t, serviceAccount("sa", namespace, "name", nil)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{serviceAccount("sa", namespace, "name", objectReference("init secret"))},
			err:  nil,
		},
		{
			testName: "UpdateServiceAccountWithDiffFields",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ServiceAccount",
							Name:                   "sa",
							Manifest: toManifest(t, serviceAccount("sa", namespace, "old_name",
								objectReference("init secret"))),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "",
							Version:                "v1",
							Kind:                   "ServiceAccount",
							Name:                   "sa",
							Manifest:               toManifest(t, serviceAccount("sa", namespace, "new_name", nil)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			want: []runtime.Object{serviceAccount("sa", namespace, "new_name", objectReference("init secret"))},
			err:  nil,
		},
		{
			testName: "DynamicResourcesAreOwnerReferencedToCSV",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resolving: "csv",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "operators.coreos.com",
							Version:                "v1alpha1",
							Kind:                   "ClusterServiceVersion",
							Name:                   "csv",
							Manifest:               toManifest(t, csv("csv", namespace, nil, nil)),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
					{
						Resolving: "csv",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "monitoring.coreos.com",
							Version:                "v1",
							Kind:                   "PrometheusRule",
							Name:                   "rule",
							Manifest:               toManifest(t, decodeFile(t, "./testdata/prometheusrule.cr.yaml", &unstructured.Unstructured{})),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			extObjs: []runtime.Object{decodeFile(t, "./testdata/prometheusrule.crd.yaml", &apiextensionsv1beta1.CustomResourceDefinition{})},
			want: []runtime.Object{
				csv("csv", namespace, nil, nil),
				modify(t, decodeFile(t, "./testdata/prometheusrule.cr.yaml", &unstructured.Unstructured{}),
					withNamespace(namespace),
					withOwner(csv("csv", namespace, nil, nil)),
				),
			},
			err: nil,
		},
		{
			testName: "V1CRDResourceIsCreated",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "crdv1"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "apiextensions.k8s.io",
							Version:                "v1",
							Kind:                   crdKind,
							Name:                   "crd",
							Manifest: toManifest(t,
								&apiextensionsv1.CustomResourceDefinition{
									TypeMeta: metav1.TypeMeta{
										Kind:       "CustomResourceDefinition",
										APIVersion: "apiextensions.k8s.io/v1", // v1 CRD version of API
									},
									ObjectMeta: metav1.ObjectMeta{Name: "test"},
									Spec:       apiextensionsv1.CustomResourceDefinitionSpec{},
								}),
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				}),
			want: []runtime.Object{
				&apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{Name: "test"},
					TypeMeta: metav1.TypeMeta{
						Kind:       "CustomResourceDefinition",
						APIVersion: "apiextensions.k8s.io/v1", // v1 CRD version of API
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			op, err := NewFakeOperator(ctx, namespace, []string{namespace}, withClientObjs(tt.in), withExtObjs(tt.extObjs...))
			require.NoError(t, err)

			err = op.ExecutePlan(tt.in)
			require.Equal(t, tt.err, err)

			getOpts := metav1.GetOptions{}
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
				case *corev1.Secret:
					fetched, err = op.opClient.GetSecret(namespace, o.GetName())
				case *corev1.Service:
					fetched, err = op.opClient.GetService(namespace, o.GetName())
				case *corev1.ConfigMap:
					fetched, err = op.opClient.GetConfigMap(namespace, o.GetName())
				case *apiextensionsv1beta1.CustomResourceDefinition:
					fetched, err = op.opClient.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), o.GetName(), getOpts)
				case *apiextensionsv1.CustomResourceDefinition:
					fetched, err = op.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), o.GetName(), getOpts)
				case *v1alpha1.ClusterServiceVersion:
					fetched, err = op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), o.GetName(), getOpts)
				case *unstructured.Unstructured:
					// Get the resource from the GVK
					gvk := o.GroupVersionKind()
					var r metav1.APIResource
					r, err = op.apiresourceFromGVK(gvk)
					require.NoError(t, err)

					gvr := schema.GroupVersionResource{
						Group:    gvk.Group,
						Version:  gvk.Version,
						Resource: r.Name,
					}

					if r.Namespaced {
						fetched, err = op.dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), o.GetName(), getOpts)
						break
					}

					fetched, err = op.dynamicClient.Resource(gvr).Get(context.TODO(), o.GetName(), getOpts)
				default:
					require.Failf(t, "couldn't find expected object", "%#v", obj)
				}

				require.NoError(t, err, "couldn't fetch %s %v", namespace, obj)
				require.EqualValues(t, obj, fetched)
			}
		})
	}
}

func TestSupportedDynamicResources(t *testing.T) {
	tests := []struct {
		testName       string
		resource       v1alpha1.StepResource
		expectedResult bool
	}{
		{
			testName: "UnsupportedObject",
			resource: v1alpha1.StepResource{
				Kind: "UnsupportedKind",
			},
			expectedResult: false,
		},
		{
			testName: "ServiceMonitorResource",
			resource: v1alpha1.StepResource{
				Kind: "ServiceMonitor",
			},
			expectedResult: true,
		},
		{
			testName: "UnsupportedObject",
			resource: v1alpha1.StepResource{
				Kind: "PrometheusRule",
			},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			require.Equal(t, tt.expectedResult, isSupported(tt.resource.Kind))
		})
	}
}

func TestExecutePlanDynamicResources(t *testing.T) {
	namespace := "ns"
	unsupportedYaml := yamlFromFilePath(t, "testdata/unsupportedkind.cr.yaml")

	tests := []struct {
		testName string
		in       *v1alpha1.InstallPlan
		err      error
	}{
		{
			testName: "UnsupportedObject",
			in: withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
				[]*v1alpha1.Step{
					{
						Resource: v1alpha1.StepResource{
							CatalogSource:          "catalog",
							CatalogSourceNamespace: namespace,
							Group:                  "some.unsupported.group",
							Version:                "v1",
							Kind:                   "UnsupportedKind",
							Name:                   "unsupportedkind",
							Manifest:               unsupportedYaml,
						},
						Status: v1alpha1.StepStatusUnknown,
					},
				},
			),
			err: v1alpha1.ErrInvalidInstallPlan,
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
					SourceType: "nope",
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				Message: "unknown sourcetype: nope",
				Reason:  v1alpha1.CatalogSourceSpecInvalidError,
			},
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
					LastUpdateTime:  now,
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
		{
			testName:  "CatalogSourceWithGrpcType/EnsuresImageOrAddressIsSet",
			namespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-spec-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
					Labels:    map[string]string{"olm.catalogSource": "invalid-spec-catalog"},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				Message: fmt.Sprintf("image and address unset: at least one must be set for sourcetype: %s", v1alpha1.SourceTypeGrpc),
				Reason:  v1alpha1.CatalogSourceSpecInvalidError,
			},
			expectedError: nil,
		},
		{
			testName:  "CatalogSourceWithInternalType/EnsuresConfigMapIsSet",
			namespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-spec-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
					Labels:    map[string]string{"olm.catalogSource": "invalid-spec-catalog"},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeInternal,
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				Message: fmt.Sprintf("configmap name unset: must be set for sourcetype: %s", v1alpha1.SourceTypeInternal),
				Reason:  v1alpha1.CatalogSourceSpecInvalidError,
			},
			expectedError: nil,
		},
		{
			testName:  "CatalogSourceWithConfigMapType/EnsuresConfigMapIsSet",
			namespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-spec-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
					Labels:    map[string]string{"olm.catalogSource": "invalid-spec-catalog"},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeConfigmap,
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				Message: fmt.Sprintf("configmap name unset: must be set for sourcetype: %s", v1alpha1.SourceTypeConfigmap),
				Reason:  v1alpha1.CatalogSourceSpecInvalidError,
			},
			expectedError: nil,
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
			updated, err := op.client.OperatorsV1alpha1().CatalogSources(tt.catalogSource.GetNamespace()).Get(context.TODO(), tt.catalogSource.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, updated)

			if tt.expectedStatus != nil {
				require.NotEmpty(t, updated.Status)
				require.Equal(t, *tt.expectedStatus, updated.Status)

				if tt.catalogSource.Spec.ConfigMap != "" {
					configMap, err := op.opClient.KubernetesInterface().CoreV1().ConfigMaps(tt.catalogSource.GetNamespace()).Get(context.TODO(), tt.catalogSource.Spec.ConfigMap, metav1.GetOptions{})
					require.NoError(t, err)
					require.True(t, ownerutil.EnsureOwner(configMap, updated))
				}
			}

			for _, o := range tt.expectedObjs {
				switch o.(type) {
				case *corev1.Pod:
					t.Log("verifying pod")
					pods, err := op.opClient.KubernetesInterface().CoreV1().Pods(tt.catalogSource.Namespace).List(context.TODO(), metav1.ListOptions{})
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
	yaml, err := yaml.Marshal([]apiextensionsv1beta1.CustomResourceDefinition{crd("fake-crd")})
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
	resolver      resolver.StepResolver
	reconciler    reconciler.RegistryReconcilerFactory
}

// fakeOperatorOption applies an option to the given fake operator configuration.
type fakeOperatorOption func(*fakeOperatorConfig)

func withResolver(res resolver.StepResolver) fakeOperatorOption {
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

func withExtObjs(extObjs ...runtime.Object) fakeOperatorOption {
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
func NewFakeOperator(ctx context.Context, namespace string, namespaces []string, fakeOptions ...fakeOperatorOption) (*Operator, error) {
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
	// TODO: Using the ReactionForwardingClientsetDecorator for k8s objects causes issues with adding Resources for discovery.
	// For now, directly use a SimpleClientset instead.
	k8sClientFake := k8sfake.NewSimpleClientset(config.k8sObjs...)
	k8sClientFake.Resources = apiResourcesForObjects(append(config.extObjs, config.regObjs...))
	opClientFake := operatorclient.NewClient(k8sClientFake, apiextensionsfake.NewSimpleClientset(config.extObjs...), apiregistrationfake.NewSimpleClientset(config.regObjs...))
	dynamicClientFake := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme())

	// Create operator namespace
	_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	wakeupInterval := 5 * time.Minute
	lister := operatorlister.NewLister()
	var sharedInformers []cache.SharedIndexInformer
	for _, ns := range namespaces {
		if ns != namespace {
			_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
			if err != nil {
				return nil, err
			}
		}
	}

	// Create informers and register listers
	operatorsFactory := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(metav1.NamespaceAll))
	catsrcInformer := operatorsFactory.Operators().V1alpha1().CatalogSources()
	subInformer := operatorsFactory.Operators().V1alpha1().Subscriptions()
	ipInformer := operatorsFactory.Operators().V1alpha1().InstallPlans()
	csvInformer := operatorsFactory.Operators().V1alpha1().ClusterServiceVersions()
	sharedInformers = append(sharedInformers, catsrcInformer.Informer(), subInformer.Informer(), ipInformer.Informer(), csvInformer.Informer())

	lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catsrcInformer.Lister())
	lister.OperatorsV1alpha1().RegisterSubscriptionLister(metav1.NamespaceAll, subInformer.Lister())
	lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, ipInformer.Lister())
	lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(metav1.NamespaceAll, csvInformer.Lister())

	factory := informers.NewSharedInformerFactoryWithOptions(opClientFake.KubernetesInterface(), wakeupInterval, informers.WithNamespace(metav1.NamespaceAll))
	roleInformer := factory.Rbac().V1().Roles()
	roleBindingInformer := factory.Rbac().V1().RoleBindings()
	serviceAccountInformer := factory.Core().V1().ServiceAccounts()
	serviceInformer := factory.Core().V1().Services()
	podInformer := factory.Core().V1().Pods()
	configMapInformer := factory.Core().V1().ConfigMaps()
	sharedInformers = append(sharedInformers, roleInformer.Informer(), roleBindingInformer.Informer(), serviceAccountInformer.Informer(), serviceInformer.Informer(), podInformer.Informer(), configMapInformer.Informer())

	lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, roleInformer.Lister())
	lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, roleBindingInformer.Lister())
	lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, serviceAccountInformer.Lister())
	lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, serviceInformer.Lister())
	lister.CoreV1().RegisterPodLister(metav1.NamespaceAll, podInformer.Lister())
	lister.CoreV1().RegisterConfigMapLister(metav1.NamespaceAll, configMapInformer.Lister())
	logger := logrus.New()

	// Create the new operator
	queueOperator, err := queueinformer.NewOperator(opClientFake.KubernetesInterface().Discovery())
	for _, informer := range sharedInformers {
		queueOperator.RegisterInformer(informer)
	}

	op := &Operator{
		Operator:      queueOperator,
		clock:         config.clock,
		logger:        config.logger,
		opClient:      opClientFake,
		dynamicClient: dynamicClientFake,
		client:        clientFake,
		lister:        lister,
		namespace:     namespace,
		nsResolveQueue: workqueue.NewNamedRateLimitingQueue(
			workqueue.NewMaxOfRateLimiter(
				workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 1000*time.Second),
				// 1 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(1), 100)},
			), "resolver"),
		resolver:              config.resolver,
		reconciler:            config.reconciler,
		clientAttenuator:      scoped.NewClientAttenuator(logger, &rest.Config{}, opClientFake, clientFake, dynamicClientFake),
		serviceAccountQuerier: scoped.NewUserDefinedServiceAccountQuerier(logger, clientFake),
		catsrcQueueSet:        queueinformer.NewEmptyResourceQueueSet(),
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
		TypeMeta: metav1.TypeMeta{
			Kind:       csvKind,
			APIVersion: "operators.coreos.com/v1alpha1",
		},
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

func crd(name string) apiextensionsv1beta1.CustomResourceDefinition {
	return apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   name + "group",
			Version: "v1",
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

func v1crd(name string) apiextensionsv1.CustomResourceDefinition {
	return apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: name + "group",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:   "v1",
					Served: true,
				},
			},
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

func service(name, namespace string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       serviceKind,
			APIVersion: "",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func secret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func serviceAccount(name, namespace, generateName string, secretRef *corev1.ObjectReference) *corev1.ServiceAccount {
	if secretRef == nil {
		return &corev1.ServiceAccount{
			TypeMeta:   metav1.TypeMeta{Kind: serviceAccountKind, APIVersion: ""},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, GenerateName: generateName},
		}
	}
	return &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{Kind: serviceAccountKind, APIVersion: ""},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, GenerateName: generateName},
		Secrets:    []corev1.ObjectReference{*secretRef},
	}
}

func configMap(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{Kind: configMapKind},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
}

func objectReference(name string) *corev1.ObjectReference {
	if name == "" {
		return &corev1.ObjectReference{}
	}
	return &corev1.ObjectReference{Name: name}
}

func yamlFromFilePath(t *testing.T, fileName string) string {
	yaml, err := ioutil.ReadFile(fileName)
	require.NoError(t, err)

	return string(yaml)
}

func toManifest(t *testing.T, obj runtime.Object) string {
	raw, err := json.Marshal(obj)
	require.NoError(t, err)

	return string(raw)
}

func pod(s v1alpha1.CatalogSource) *corev1.Pod {
	pod := reconciler.Pod(&s, "registry-server", s.Spec.Image, s.GetLabels(), 5, 10)
	ownerutil.AddOwner(pod, &s, false, false)
	return pod
}

func decodeFile(t *testing.T, file string, to runtime.Object) runtime.Object {
	manifest := yamlFromFilePath(t, file)
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 10)
	require.NoError(t, dec.Decode(to))

	return to
}

type modifierFunc func(t *testing.T, obj runtime.Object) runtime.Object

func modify(t *testing.T, obj runtime.Object, modifiers ...modifierFunc) runtime.Object {
	o := obj.DeepCopyObject()
	for _, modifier := range modifiers {
		o = modifier(t, o)
	}

	return o
}

type metaModifierFunc func(m metav1.Object)

func modifyMeta(mf metaModifierFunc) modifierFunc {
	return func(t *testing.T, obj runtime.Object) runtime.Object {
		accessor, err := meta.Accessor(obj)
		require.NoError(t, err)

		mf(accessor)

		return obj
	}
}

func withNamespace(namespace string) modifierFunc {
	return modifyMeta(func(m metav1.Object) {
		m.SetNamespace(namespace)
	})
}

func withOwner(owner ownerutil.Owner) modifierFunc {
	return modifyMeta(func(m metav1.Object) {
		ownerutil.AddNonBlockingOwner(m, owner)
	})
}

func withObjectMeta(t *testing.T, obj runtime.Object, m *metav1.ObjectMeta) runtime.Object {
	o := obj.DeepCopyObject()
	accessor, err := meta.Accessor(o)
	require.NoError(t, err)

	accessor.SetAnnotations(m.GetAnnotations())
	accessor.SetClusterName(m.GetClusterName())
	accessor.SetCreationTimestamp(m.GetCreationTimestamp())
	accessor.SetDeletionGracePeriodSeconds(m.GetDeletionGracePeriodSeconds())
	accessor.SetDeletionTimestamp(m.GetDeletionTimestamp())
	accessor.SetFinalizers(m.GetFinalizers())
	accessor.SetGenerateName(m.GetGenerateName())
	accessor.SetGeneration(m.GetGeneration())
	accessor.SetLabels(m.GetLabels())
	accessor.SetManagedFields(m.GetManagedFields())
	accessor.SetName(m.GetName())
	accessor.SetNamespace(m.GetNamespace())
	accessor.SetOwnerReferences(m.GetOwnerReferences())
	accessor.SetResourceVersion(m.GetResourceVersion())
	accessor.SetSelfLink(m.GetSelfLink())
	accessor.SetUID(m.GetUID())

	return o
}

func apiResourcesForObjects(objs []runtime.Object) []*metav1.APIResourceList {
	apis := []*metav1.APIResourceList{}
	for _, o := range objs {
		switch o.(type) {
		case *apiextensionsv1beta1.CustomResourceDefinition:
			crd := o.(*apiextensionsv1beta1.CustomResourceDefinition)
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: crd.Spec.Group, Version: crd.Spec.Versions[0].Name}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:         crd.GetName(),
						SingularName: crd.Spec.Names.Singular,
						Namespaced:   crd.Spec.Scope == apiextensionsv1beta1.NamespaceScoped,
						Group:        crd.Spec.Group,
						Version:      crd.Spec.Versions[0].Name,
						Kind:         crd.Spec.Names.Kind,
					},
				},
			})
		case *apiregistrationv1.APIService:
			a := o.(*apiregistrationv1.APIService)
			names := strings.Split(a.Name, ".")
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: names[1], Version: a.Spec.Version}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:    names[1],
						Group:   names[1],
						Version: a.Spec.Version,
						Kind:    names[1] + "Kind",
					},
				},
			})
		}
	}
	return apis
}
