package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"k8s.io/utils/ptr"

	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"

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
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apiserver/pkg/storage/names"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"
	utilclock "k8s.io/utils/clock"
	utilclocktesting "k8s.io/utils/clock/testing"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle/bundlefakes"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/grpc"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
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

func (m *mockTransitioner) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func TestCreateInstallPlanHasExpectedClusterServiceVersionNames(t *testing.T) {
	namespace := "foo"
	tests := []struct {
		testName                           string
		steps                              []*v1alpha1.Step
		bundleLookups                      []v1alpha1.BundleLookup
		expectedClusterServiceVersionNames []string
	}{
		/******************************************************************************
		Historically, when creating an installPlan it's spec.ClusterServiceVersionNames
		was derived from two sources:
		   1. The names of CSVs found in "steps" of the installPlan's status.plan
		   2. The metadata associated with the bundle image

		These sources couldn't result in duplicate entries as the unpacking job would
		finish after the installPlan was created and the steps weren't populated until
		the unpacking job finished.

		OLM was later updated to complete the unpacking jobs prior to creating
		the installPlan, which caused CSVs to be listed twice as the createInstallPlan
		function was called with steps and a bundle.
		*****************************************************************************/
		{
			testName: "Check that CSVs are not listed twice if steps and bundles are provided",
			steps: []*v1alpha1.Step{{
				Resolving: "csv",
				Resource: v1alpha1.StepResource{
					CatalogSource:          "catalog",
					CatalogSourceNamespace: namespace,
					Group:                  "operators.coreos.com",
					Version:                "v1alpha1",
					Kind:                   "ClusterServiceVersion",
					Name:                   "csvA",
					Manifest:               toManifest(t, csv("csvA", namespace, nil, nil)),
				},
				Status: v1alpha1.StepStatusUnknown,
			}},
			bundleLookups: []v1alpha1.BundleLookup{
				{
					Identifier: "csvA",
				},
			},
			expectedClusterServiceVersionNames: []string{"csvA"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			op, err := NewFakeOperator(ctx, namespace, []string{namespace})
			require.NoError(t, err)

			_, err = op.createInstallPlan(namespace, 0, nil, v1alpha1.ApprovalAutomatic, tt.steps, tt.bundleLookups)
			require.NoError(t, err)

			ipList, err := op.client.OperatorsV1alpha1().InstallPlans(namespace).List(ctx, metav1.ListOptions{})
			require.NoError(t, err)
			require.Len(t, ipList.Items, 1)
			require.Equal(t, tt.expectedClusterServiceVersionNames, ipList.Items[0].Spec.ClusterServiceVersionNames)
		})
	}
}

func TestTransitionInstallPlan(t *testing.T) {
	errMsg := "transition test error"
	err := errors.New(errMsg)
	clockFake := utilclocktesting.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
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
		timeout    time.Duration
	}{
		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseComplete, installed, 0},
		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseComplete, installed, 0},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseFailed, failed, 0},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseFailed, failed, 0},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseInstalling, nil, 1},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseInstalling, nil, 1},

		{v1alpha1.InstallPlanPhaseRequiresApproval, nil, v1alpha1.ApprovalManual, false, v1alpha1.InstallPlanPhaseRequiresApproval, nil, 0},
		{v1alpha1.InstallPlanPhaseRequiresApproval, nil, v1alpha1.ApprovalManual, true, v1alpha1.InstallPlanPhaseInstalling, nil, 0},
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
		out, _ := transitionInstallPlanState(logrus.New(), transitioner, *plan, now, tt.timeout)

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
	ipWithSteps := withSteps(installPlan("p", namespace, v1alpha1.InstallPlanPhaseInstalling, "csv"),
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
	)

	tests := []struct {
		testName          string
		err               error
		in                *v1alpha1.InstallPlan
		expectedPhase     v1alpha1.InstallPlanPhase
		expectedCondition *v1alpha1.InstallPlanCondition
		clientObjs        []runtime.Object
	}{
		{
			testName:      "NoStatus",
			err:           nil,
			expectedPhase: v1alpha1.InstallPlanPhaseNone,
			in:            installPlan("p", namespace, v1alpha1.InstallPlanPhaseNone),
		},
		{
			// This checks that an installplan's status.Condition contains a condition with error message when no operatorgroup is present
			testName:      "HasSteps/NoOperatorGroup",
			err:           fmt.Errorf("attenuated service account query failed - no operator group found that is managing this namespace"),
			expectedPhase: v1alpha1.InstallPlanPhaseInstalling,
			expectedCondition: &v1alpha1.InstallPlanCondition{
				Type: v1alpha1.InstallPlanInstalled, Status: corev1.ConditionFalse, Reason: v1alpha1.InstallPlanReasonInstallCheckFailed,
				Message: "no operator group found that is managing this namespace",
			},
			in: ipWithSteps,
		},
		{
			// This checks that an installplan's status.Condition contains a condition with error message when multiple operator groups are present for the same namespace
			testName:      "HasSteps/TooManyOperatorGroups",
			err:           fmt.Errorf("attenuated service account query failed - more than one operator group(s) are managing this namespace count=2"),
			expectedPhase: v1alpha1.InstallPlanPhaseInstalling,
			in:            ipWithSteps,
			expectedCondition: &v1alpha1.InstallPlanCondition{
				Type: v1alpha1.InstallPlanInstalled, Status: corev1.ConditionFalse, Reason: v1alpha1.InstallPlanReasonInstallCheckFailed,
				Message: "more than one operator group(s) are managing this namespace count=2",
			},
			clientObjs: []runtime.Object{
				operatorGroup("og1", "sa", namespace,
					&corev1.ObjectReference{
						Kind:      "ServiceAccount",
						Namespace: namespace,
						Name:      "sa",
					}),
				operatorGroup("og2", "sa", namespace,
					&corev1.ObjectReference{
						Kind:      "ServiceAccount",
						Namespace: namespace,
						Name:      "sa",
					}),
			},
		},
		{
			// This checks that an installplan's status.Condition contains a condition with error message when no service account is synced for the operator group, i.e the service account ref doesn't exist
			testName:      "HasSteps/NonExistentServiceAccount",
			err:           fmt.Errorf("attenuated service account query failed - please make sure the service account exists. sa=sa1 operatorgroup=ns/og"),
			expectedPhase: v1alpha1.InstallPlanPhaseInstalling,
			expectedCondition: &v1alpha1.InstallPlanCondition{
				Type: v1alpha1.InstallPlanInstalled, Status: corev1.ConditionFalse, Reason: v1alpha1.InstallPlanReasonInstallCheckFailed,
				Message: "please make sure the service account exists. sa=sa1 operatorgroup=ns/og",
			},
			in: ipWithSteps,
			clientObjs: []runtime.Object{
				operatorGroup("og", "sa1", namespace, nil),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			tt.clientObjs = append(tt.clientObjs, tt.in)

			op, err := NewFakeOperator(ctx, namespace, []string{namespace}, withClientObjs(tt.clientObjs...))
			require.NoError(t, err)

			err = op.syncInstallPlans(tt.in)
			require.Equal(t, tt.err, err)

			ip, err := op.client.OperatorsV1alpha1().InstallPlans(namespace).Get(ctx, tt.in.Name, metav1.GetOptions{})
			require.NoError(t, err)

			require.Equal(t, tt.expectedPhase, ip.Status.Phase)

			if tt.expectedCondition != nil {
				require.True(t, hasExpectedCondition(ip, *tt.expectedCondition))
			}
		})
	}
}

type ipSet []v1alpha1.InstallPlan

func (ipSet) Generate(rand *rand.Rand, size int) reflect.Value {
	ips := []v1alpha1.InstallPlan{}

	// each i is the generation value
	for i := 0; i < rand.Intn(size)+1; i++ {
		// generate a few at each generation to account for bugs that don't increment the generation
		for j := 0; j < rand.Intn(3); j++ {
			ips = append(ips, v1alpha1.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      names.SimpleNameGenerator.GenerateName(fmt.Sprintf("%d", i)),
				},
				Spec: v1alpha1.InstallPlanSpec{
					Generation: i,
				},
			})
		}
	}
	return reflect.ValueOf(ipSet(ips))
}

func TestGCInstallPlans(t *testing.T) {
	f := func(ips ipSet) bool {
		if len(ips) == 0 {
			return true
		}
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()

		var maxGen int64
		for _, i := range ips {
			if g := i.Generation; g > maxGen {
				maxGen = g
			}
		}
		objs := make([]runtime.Object, 0)
		for _, i := range ips {
			objs = append(objs, i.DeepCopy())
		}
		op, err := NewFakeOperator(ctx, "ns", []string{"ns"}, withClientObjs(objs...))
		require.NoError(t, err)

		var out []v1alpha1.InstallPlan
		for {
			op.gcInstallPlans(logrus.New(), "ns")
			require.NoError(t, err)

			outList, err := op.client.OperatorsV1alpha1().InstallPlans("ns").List(ctx, metav1.ListOptions{})
			require.NoError(t, err)
			out = outList.Items

			if len(out) <= maxInstallPlanCount {
				break
			}
		}

		keptMax := false
		for _, o := range out {
			if o.Generation == maxGen {
				keptMax = true
				break
			}
		}
		require.True(t, keptMax)

		if len(ips) < maxInstallPlanCount {
			return len(out) == len(ips)
		}
		return len(out) == maxInstallPlanCount
	}
	require.NoError(t, quick.Check(f, nil))
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
					modifyMeta(func(m metav1.Object) {
						labels := m.GetLabels()
						if labels == nil {
							labels = map[string]string{}
						}
						labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
						m.SetLabels(labels)
					}),
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
					ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}},
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

func withStatus(catalogSource v1alpha1.CatalogSource, status v1alpha1.CatalogSourceStatus) *v1alpha1.CatalogSource {
	copy := catalogSource.DeepCopy()
	copy.Status = status
	return copy
}

func TestSyncCatalogSourcesSecurityPolicy(t *testing.T) {
	assertLegacySecurityPolicy := func(t *testing.T, pod *corev1.Pod) {
		require.Nil(t, pod.Spec.SecurityContext)
		require.Equal(t, &corev1.SecurityContext{
			ReadOnlyRootFilesystem: ptr.To(false),
		}, pod.Spec.Containers[0].SecurityContext)
	}

	assertRestrictedPolicy := func(t *testing.T, pod *corev1.Pod) {
		require.Equal(t, &corev1.PodSecurityContext{
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			RunAsNonRoot:   ptr.To(true),
			RunAsUser:      ptr.To(int64(1001)),
		}, pod.Spec.SecurityContext)
		require.Equal(t, &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   ptr.To(false),
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		}, pod.Spec.Containers[0].SecurityContext)
	}

	clockFake := utilclocktesting.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	tests := []struct {
		testName      string
		namespace     *corev1.Namespace
		catalogSource *v1alpha1.CatalogSource
		check         func(*testing.T, *corev1.Pod)
	}{
		{
			testName: "UnlabeledNamespace/NoUserPreference/LegacySecurityPolicy",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cool-namespace",
				},
			},
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					Image:      "catalog-image",
					SourceType: v1alpha1.SourceTypeGrpc,
				},
			},
			check: assertLegacySecurityPolicy,
		}, {
			testName: "UnlabeledNamespace/UserPreferenceForRestricted/RestrictedSecurityPolicy",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cool-namespace",
				},
			},
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					Image:      "catalog-image",
					SourceType: v1alpha1.SourceTypeGrpc,
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			},
			check: assertRestrictedPolicy,
		}, {
			testName: "LabeledNamespace/NoUserPreference/RestrictedSecurityPolicy",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cool-namespace",
					Labels: map[string]string{
						// restricted is the default psa policy
						"pod-security.kubernetes.io/enforce": "restricted",
					},
				},
			},
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					Image:      "catalog-image",
					SourceType: v1alpha1.SourceTypeGrpc,
				},
			},
			check: assertRestrictedPolicy,
		}, {
			testName: "LabeledNamespace/UserPreferenceForLegacy/LegacySecurityPolicy",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cool-namespace",
				},
			},
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					Image:      "catalog-image",
					SourceType: v1alpha1.SourceTypeGrpc,
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Legacy,
					},
				},
			},
			check: assertLegacySecurityPolicy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			// Create existing objects
			clientObjs := []runtime.Object{tt.catalogSource}

			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			op, err := NewFakeOperator(
				ctx,
				tt.namespace.GetName(),
				[]string{tt.namespace.GetName()},
				withClock(clockFake),
				withClientObjs(clientObjs...),
			)
			require.NoError(t, err)

			// Because NewFakeOperator creates the namespace, we need to update the namespace to match the test case
			// before running the sync function
			_, err = op.opClient.KubernetesInterface().CoreV1().Namespaces().Update(context.TODO(), tt.namespace, metav1.UpdateOptions{})
			require.NoError(t, err)

			// Run sync
			err = op.syncCatalogSources(tt.catalogSource)
			require.NoError(t, err)

			pods, err := op.opClient.KubernetesInterface().CoreV1().Pods(tt.catalogSource.Namespace).List(context.TODO(), metav1.ListOptions{})
			require.NoError(t, err)
			require.Len(t, pods.Items, 1)

			tt.check(t, &pods.Items[0])
		})
	}
}

func TestSyncCatalogSources(t *testing.T) {
	clockFake := utilclocktesting.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
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
		testName        string
		namespace       string
		catalogSource   *v1alpha1.CatalogSource
		k8sObjs         []runtime.Object
		configMap       *corev1.ConfigMap
		expectedStatus  *v1alpha1.CatalogSourceStatus
		expectedObjs    []runtime.Object
		expectedError   error
		existingSources []sourceAddress
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
			expectedError:  errors.New("failed to get catalog config map cool-configmap: configmaps \"cool-configmap\" not found"),
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
				pod(t, *grpcCatalog),
			},
		},
		{
			testName:      "CatalogSourceWithGrpcImage/EnsuresCorrectImage",
			namespace:     "cool-namespace",
			catalogSource: grpcCatalog,
			k8sObjs: []runtime.Object{
				pod(t, v1alpha1.CatalogSource{
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
				pod(t, *grpcCatalog),
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
		{
			testName:  "GRPCConnectionStateAddressIsUpdated",
			namespace: "cool-namespace",
			catalogSource: withStatus(*grpcCatalog, v1alpha1.CatalogSourceStatus{
				RegistryServiceStatus: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
					CreatedAt:        now,
				},
				GRPCConnectionState: &v1alpha1.GRPCConnectionState{
					Address: "..svc:", // Needs to be updated to cool-catalog.cool-namespace.svc:50051
				},
			}),
			k8sObjs: []runtime.Object{
				pod(t, *grpcCatalog),
				service(grpcCatalog.GetName(), grpcCatalog.GetNamespace()),
				serviceAccount(grpcCatalog.GetName(), grpcCatalog.GetNamespace(), "", objectReference("init secret")),
			},
			existingSources: []sourceAddress{
				{
					sourceKey: registry.CatalogKey{Name: "cool-catalog", Namespace: "cool-namespace"},
					address:   "cool-catalog.cool-namespace.svc:50051",
				},
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				RegistryServiceStatus: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
					CreatedAt:        now,
				},
				GRPCConnectionState: &v1alpha1.GRPCConnectionState{
					Address:           "cool-catalog.cool-namespace.svc:50051",
					LastObservedState: "",
					LastConnectTime:   now,
				},
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

			op, err := NewFakeOperator(ctx, tt.namespace, []string{tt.namespace}, withClock(clockFake), withClientObjs(clientObjs...), withK8sObjs(tt.k8sObjs...), withSources(tt.existingSources...))
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
				if tt.expectedStatus.GRPCConnectionState != nil {
					updated.Status.GRPCConnectionState.LastConnectTime = now
					// Ignore LastObservedState difference if an expected LastObservedState is not provided
					if tt.expectedStatus.GRPCConnectionState.LastObservedState == "" {
						updated.Status.GRPCConnectionState.LastObservedState = ""
					}
				}
				require.NotEmpty(t, updated.Status)
				require.Equal(t, *tt.expectedStatus, updated.Status)

				if tt.catalogSource.Spec.ConfigMap != "" {
					configMap, err := op.opClient.KubernetesInterface().CoreV1().ConfigMaps(tt.catalogSource.GetNamespace()).Get(context.TODO(), tt.catalogSource.Spec.ConfigMap, metav1.GetOptions{})
					require.NoError(t, err)
					require.True(t, ownerutil.EnsureOwner(configMap, updated))
				}
			}

			for _, o := range tt.expectedObjs {
				switch o := o.(type) {
				case *corev1.Pod:
					t.Log("verifying pod")
					pods, err := op.opClient.KubernetesInterface().CoreV1().Pods(tt.catalogSource.Namespace).List(context.TODO(), metav1.ListOptions{})
					require.NoError(t, err)
					require.Len(t, pods.Items, 1)

					// set the name to the generated name
					o.SetName(pods.Items[0].GetName())
					require.EqualValues(t, o, &pods.Items[0])
				}
			}
		})
	}
}

func TestSyncResolvingNamespace(t *testing.T) {
	clockFake := utilclocktesting.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	testNamespace := "testNamespace"
	og := &operatorsv1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "og",
			Namespace: testNamespace,
		},
	}

	type fields struct {
		clientOptions   []clientfake.Option
		resolveErr      error
		existingOLMObjs []runtime.Object
	}
	tests := []struct {
		name              string
		fields            fields
		wantSubscriptions []*v1alpha1.Subscription
		wantErr           error
	}{
		{
			name: "NoError",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
					},
				},
			},
		},
		{
			name: "NotSatisfiableError",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
				resolveErr: solver.NotSatisfiable{
					{
						Variable:   resolver.NewSubscriptionVariable("a", nil),
						Constraint: resolver.PrettyConstraint(solver.Mandatory(), "something"),
					},
				},
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
						Conditions: []v1alpha1.SubscriptionCondition{
							{
								Type:    v1alpha1.SubscriptionResolutionFailed,
								Reason:  "ConstraintsNotSatisfiable",
								Message: "constraints not satisfiable: something",
								Status:  corev1.ConditionTrue,
							},
						},
						LastUpdated: now,
					},
				},
			},
		},
		{
			name: "OtherError",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.ClusterServiceVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv.v.1",
							Namespace: testNamespace,
						},
						Status: v1alpha1.ClusterServiceVersionStatus{
							Phase: v1alpha1.CSVPhaseSucceeded,
						},
					},
					&v1alpha1.Subscription{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
				resolveErr: fmt.Errorf("some error"),
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
						Conditions: []v1alpha1.SubscriptionCondition{
							{
								Type:    v1alpha1.SubscriptionResolutionFailed,
								Reason:  "ErrorPreventedResolution",
								Message: "some error",
								Status:  corev1.ConditionTrue,
							},
						},
						LastUpdated: now,
					},
				},
			},
			wantErr: fmt.Errorf("some error"),
		},
		{
			name: "HadErrorShouldClearError",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							InstalledCSV: "sub-csv",
							State:        "AtLatestKnown",
							Conditions: []v1alpha1.SubscriptionCondition{
								{
									Type:    v1alpha1.SubscriptionResolutionFailed,
									Reason:  "ConstraintsNotSatisfiable",
									Message: "constraints not satisfiable: no operators found from catalog src in namespace testNamespace referenced by subscrition sub, subscription sub exists",
									Status:  corev1.ConditionTrue,
								},
							},
						},
					},
				},
				resolveErr: nil,
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						InstalledCSV: "sub-csv",
						State:        "AtLatestKnown",
						LastUpdated:  now,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			o, err := NewFakeOperator(ctx, testNamespace, []string{testNamespace}, withClock(clockFake), withClientObjs(append(tt.fields.existingOLMObjs, og)...), withFakeClientOptions(tt.fields.clientOptions...))
			require.NoError(t, err)

			o.reconciler = &fakes.FakeRegistryReconcilerFactory{
				ReconcilerForSourceStub: func(source *v1alpha1.CatalogSource) reconciler.RegistryReconciler {
					return &fakes.FakeRegistryReconciler{
						EnsureRegistryServerStub: func(logger *logrus.Entry, source *v1alpha1.CatalogSource) error {
							return nil
						},
					}
				},
			}

			o.resolver = &fakes.FakeStepResolver{
				ResolveStepsStub: func(string) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
					return nil, nil, nil, tt.fields.resolveErr
				},
			}

			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}

			err = o.syncResolvingNamespace(namespace)
			if tt.wantErr != nil {
				require.Equal(t, tt.wantErr, err)
			} else {
				require.NoError(t, err)
			}

			for _, s := range tt.wantSubscriptions {
				fetched, err := o.client.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), s.GetName(), metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, s, fetched)
			}
		})
	}
}

func TestCompetingCRDOwnersExist(t *testing.T) {
	t.Parallel()

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
	for _, xt := range tests {
		tt := xt
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

func TestValidateV1Beta1CRDCompatibility(t *testing.T) {
	unstructuredForFile := func(file string) *unstructured.Unstructured {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 30)
		k8sFile := &unstructured.Unstructured{}
		require.NoError(t, dec.Decode(k8sFile))
		return k8sFile
	}

	unversionedCRDForV1beta1File := func(file string) *apiextensionsv1beta1.CustomResourceDefinition {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 30)
		k8sFile := &apiextensionsv1beta1.CustomResourceDefinition{}
		require.NoError(t, dec.Decode(k8sFile))
		return k8sFile
	}

	tests := []struct {
		name            string
		existingObjects []runtime.Object
		gvr             schema.GroupVersionResource
		oldCRD          *apiextensionsv1beta1.CustomResourceDefinition
		newCRD          *apiextensionsv1beta1.CustomResourceDefinition
		want            error
	}{
		{
			name: "label validation",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/hivebug/cr.yaml"),
			},
			gvr: schema.GroupVersionResource{
				Group:    "hive.openshift.io",
				Version:  "v1",
				Resource: "machinepools",
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/hivebug/crd.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/hivebug/crd.yaml"),
		},
		{
			name: "fail validation",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/hivebug/fail.yaml"),
			},
			gvr: schema.GroupVersionResource{
				Group:    "hive.openshift.io",
				Version:  "v1",
				Resource: "machinepools",
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/hivebug/crd.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/hivebug/crd.yaml"),
			want:   validationError{fmt.Errorf("error validating hive.openshift.io/v1, Kind=MachinePool \"test\": updated validation is too restrictive: [[].spec.clusterDeploymentRef: Invalid value: \"null\": spec.clusterDeploymentRef in body must be of type object: \"null\", [].spec.name: Required value, [].spec.platform: Required value]")},
		},
		{
			name: "backwards incompatible change",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.yaml"),
			},
			gvr: schema.GroupVersionResource{
				Group:    "cluster.com",
				Version:  "v1alpha1",
				Resource: "testcrd",
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.old.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.yaml"),
			want:   validationError{fmt.Errorf("error validating cluster.com/v1alpha1, Kind=testcrd \"my-cr-1\": updated validation is too restrictive: [].spec.scalar: Invalid value: 2: spec.scalar in body should be greater than or equal to 3")},
		},
		{
			name: "unserved version",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.yaml"),
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.v2.yaml"),
			},
			gvr: schema.GroupVersionResource{
				Group:    "cluster.com",
				Version:  "v1alpha1",
				Resource: "testcrd",
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.old.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.unserved.yaml"),
		},
		{
			name: "cr not validated against currently unserved version",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.yaml"),
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.v2.yaml"),
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.unserved.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.yaml"),
		},
		{
			name: "crd with no versions list",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.yaml"),
				unstructuredForFile("testdata/apiextensionsv1beta1/cr.v2.yaml"),
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.no-versions-list.old.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/apiextensionsv1beta1/crd.no-versions-list.yaml"),
			want:   validationError{fmt.Errorf("error validating cluster.com/v1alpha1, Kind=testcrd \"my-cr-1\": updated validation is too restrictive: [].spec.scalar: Invalid value: 2: spec.scalar in body should be greater than or equal to 3")},
		},
		{
			name: "crd with incorrect comparison",
			existingObjects: []runtime.Object{
				unstructuredForFile("testdata/postgrestolerations/pgadmin.cr.yaml"),
			},
			oldCRD: unversionedCRDForV1beta1File("testdata/postgrestolerations/crd.yaml"),
			newCRD: unversionedCRDForV1beta1File("testdata/postgrestolerations/crd.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme(), tt.existingObjects...)
			require.Equal(t, tt.want, validateV1Beta1CRDCompatibility(client, tt.oldCRD, tt.newCRD))
		})
	}
}

func TestValidateV1CRDCompatibility(t *testing.T) {
	unstructuredForFile := func(file string) *unstructured.Unstructured {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 30)
		k8sFile := &unstructured.Unstructured{}
		require.NoError(t, dec.Decode(k8sFile))
		return k8sFile
	}

	unversionedCRDForV1File := func(file string) *apiextensionsv1.CustomResourceDefinition {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 30)
		k8sFile := &apiextensionsv1.CustomResourceDefinition{}
		require.NoError(t, dec.Decode(k8sFile))
		return k8sFile
	}

	tests := []struct {
		name        string
		existingCRs []runtime.Object
		gvr         schema.GroupVersionResource
		oldCRD      *apiextensionsv1.CustomResourceDefinition
		newCRD      *apiextensionsv1.CustomResourceDefinition
		want        error
	}{
		{
			name: "valid",
			existingCRs: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v1.yaml"),
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v2.yaml"),
			},
			oldCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.old.yaml"),
			newCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.yaml"),
		},
		{
			name: "validation failure",
			existingCRs: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v1.yaml"),
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.fail.v2.yaml"),
			},
			oldCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.old.yaml"),
			newCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.yaml"),
			want:   validationError{fmt.Errorf("error validating stable.example.com/v2, Kind=CronTab \"my-crontab\": updated validation is too restrictive: [].spec.replicas: Invalid value: 10: spec.replicas in body should be less than or equal to 9")},
		},
		{
			name: "cr not invalidated by unserved version",
			existingCRs: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v1.yaml"),
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v2.yaml"),
			},
			oldCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.old.yaml"),
			newCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.unserved.yaml"),
		},
		{
			name: "cr not validated against currently unserved version",
			existingCRs: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v1.yaml"),
				unstructuredForFile("testdata/apiextensionsv1/crontabs.cr.valid.v2.yaml"),
			},
			oldCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.old.unserved.yaml"),
			newCRD: unversionedCRDForV1File("testdata/apiextensionsv1/crontabs.crd.yaml"),
		},
		{
			name: "validation failure with single CRD version",
			existingCRs: []runtime.Object{
				unstructuredForFile("testdata/apiextensionsv1/single-version-cr.yaml"),
			},
			oldCRD: unversionedCRDForV1File("testdata/apiextensionsv1/single-version-crd.old.yaml"),
			newCRD: unversionedCRDForV1File("testdata/apiextensionsv1/single-version-crd.yaml"),
			want:   validationError{fmt.Errorf("error validating cluster.com/v1alpha1, Kind=testcrd \"my-cr-1\": updated validation is too restrictive: [].spec.scalar: Invalid value: 100: spec.scalar in body should be less than or equal to 50")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fakedynamic.NewSimpleDynamicClient(runtime.NewScheme(), tt.existingCRs...)
			require.Equal(t, tt.want, validateV1CRDCompatibility(client, tt.oldCRD, tt.newCRD))
		})
	}
}

func TestSyncRegistryServer(t *testing.T) {
	namespace := "ns"

	tests := []struct {
		testName   string
		err        error
		catSrc     *v1alpha1.CatalogSource
		clientObjs []runtime.Object
	}{
		{
			testName: "EmptyRegistryPoll",
			err:      fmt.Errorf("empty polling interval; cannot requeue registry server sync without a provided polling interval"),
			catSrc: &v1alpha1.CatalogSource{
				Spec: v1alpha1.CatalogSourceSpec{
					UpdateStrategy: &v1alpha1.UpdateStrategy{
						RegistryPoll: &v1alpha1.RegistryPoll{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			tt.clientObjs = append(tt.clientObjs, tt.catSrc)
			op, err := NewFakeOperator(ctx, namespace, []string{namespace}, withClientObjs(tt.clientObjs...))
			require.NoError(t, err)

			op.reconciler = &fakes.FakeRegistryReconcilerFactory{
				ReconcilerForSourceStub: func(source *v1alpha1.CatalogSource) reconciler.RegistryReconciler {
					return &fakes.FakeRegistryReconciler{
						EnsureRegistryServerStub: func(logger *logrus.Entry, source *v1alpha1.CatalogSource) error {
							return nil
						},
					}
				},
			}
			require.NotPanics(t, func() {
				_, _, err = op.syncRegistryServer(logrus.NewEntry(op.logger), tt.catSrc)
			})
			require.Equal(t, tt.err, err)
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
	clock          utilclock.Clock
	clientObjs     []runtime.Object
	k8sObjs        []runtime.Object
	extObjs        []runtime.Object
	regObjs        []runtime.Object
	clientOptions  []clientfake.Option
	logger         *logrus.Logger
	resolver       resolver.StepResolver
	recorder       record.EventRecorder
	reconciler     reconciler.RegistryReconcilerFactory
	bundleUnpacker bundle.Unpacker
	sources        []sourceAddress
}

// fakeOperatorOption applies an option to the given fake operator configuration.
type fakeOperatorOption func(*fakeOperatorConfig)

func withResolver(res resolver.StepResolver) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.resolver = res
	}
}

func withBundleUnpacker(bundleUnpacker bundle.Unpacker) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.bundleUnpacker = bundleUnpacker
	}
}

func withSources(sources ...sourceAddress) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.sources = sources
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

type sourceAddress struct {
	address   string
	sourceKey registry.CatalogKey
}

// NewFakeOperator creates a new operator using fake clients.
func NewFakeOperator(ctx context.Context, namespace string, namespaces []string, fakeOptions ...fakeOperatorOption) (*Operator, error) {
	// Apply options to default config
	config := &fakeOperatorConfig{
		logger:         logrus.StandardLogger(),
		clock:          utilclock.RealClock{},
		resolver:       &fakes.FakeStepResolver{},
		recorder:       &record.FakeRecorder{},
		bundleUnpacker: &bundlefakes.FakeUnpacker{},
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
	ogInformer := operatorsFactory.Operators().V1().OperatorGroups()
	sharedInformers = append(sharedInformers, catsrcInformer.Informer(), subInformer.Informer(), ipInformer.Informer(), csvInformer.Informer(), ogInformer.Informer())

	lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catsrcInformer.Lister())
	lister.OperatorsV1alpha1().RegisterSubscriptionLister(metav1.NamespaceAll, subInformer.Lister())
	lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, ipInformer.Lister())
	lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(metav1.NamespaceAll, csvInformer.Lister())
	lister.OperatorsV1().RegisterOperatorGroupLister(metav1.NamespaceAll, ogInformer.Lister())

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
	if err != nil {
		return nil, fmt.Errorf("failed to create queueinformer operator: %w", err)
	}
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
		nsResolveQueue: workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](
			workqueue.NewTypedMaxOfRateLimiter[types.NamespacedName](
				workqueue.NewTypedItemExponentialFailureRateLimiter[types.NamespacedName](1*time.Second, 1000*time.Second),
				// 1 qps, 100 bucket size.  This is only for retry speed and its only the overall factor (not per item)
				&workqueue.TypedBucketRateLimiter[types.NamespacedName]{Limiter: rate.NewLimiter(rate.Limit(1), 100)},
			),
			workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
				Name: "resolver",
			}),
		resolver:              config.resolver,
		reconciler:            config.reconciler,
		recorder:              config.recorder,
		clientAttenuator:      scoped.NewClientAttenuator(logger, &rest.Config{}, opClientFake),
		serviceAccountQuerier: scoped.NewUserDefinedServiceAccountQuerier(logger, clientFake),
		bundleUnpacker:        config.bundleUnpacker,
		catsrcQueueSet:        queueinformer.NewEmptyResourceQueueSet(),
		clientFactory: &stubClientFactory{
			operatorClient:   opClientFake,
			kubernetesClient: clientFake,
			dynamicClient:    dynamicClientFake,
		},
	}
	op.sources = grpc.NewSourceStore(config.logger, 1*time.Second, 5*time.Second, op.syncSourceState)
	if op.reconciler == nil {
		s := runtime.NewScheme()
		err := k8sfake.AddToScheme(s)
		if err != nil {
			return nil, err
		}
		applier := controllerclient.NewFakeApplier(s, "testowner")
		op.reconciler = reconciler.NewRegistryReconcilerFactory(lister, op.opClient, "test:pod", op.now, applier, 1001, "", "")
	}

	op.RunInformers(ctx)
	op.sources.Start(ctx)
	for _, source := range config.sources {
		op.sources.Add(source.sourceKey, source.address)
	}

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
			Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
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
			Name:   name,
			Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
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

func service(name, namespace string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       serviceKind,
			APIVersion: "",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
		},
	}
}

func secret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
		},
	}
}

func serviceAccount(name, namespace, generateName string, secretRef *corev1.ObjectReference) *corev1.ServiceAccount {
	if secretRef == nil {
		return &corev1.ServiceAccount{
			TypeMeta:   metav1.TypeMeta{Kind: serviceAccountKind, APIVersion: ""},
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, GenerateName: generateName, Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}},
		}
	}
	return &corev1.ServiceAccount{
		TypeMeta:   metav1.TypeMeta{Kind: serviceAccountKind, APIVersion: ""},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, GenerateName: generateName, Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}},
		Secrets:    []corev1.ObjectReference{*secretRef},
	}
}

func configMap(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{Kind: configMapKind},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}},
	}
}

func objectReference(name string) *corev1.ObjectReference {
	if name == "" {
		return &corev1.ObjectReference{}
	}
	return &corev1.ObjectReference{Name: name}
}

func yamlFromFilePath(t *testing.T, fileName string) string {
	yaml, err := os.ReadFile(fileName)
	require.NoError(t, err)

	return string(yaml)
}

func toManifest(t *testing.T, obj runtime.Object) string {
	raw, err := json.Marshal(obj)
	require.NoError(t, err)

	return string(raw)
}

func pod(t *testing.T, s v1alpha1.CatalogSource) *corev1.Pod {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: s.GetNamespace(),
			Name:      s.GetName(),
		},
	}
	pod, err := reconciler.Pod(&s, "registry-server", "central-opm", "central-util", s.Spec.Image, serviceAccount, s.GetLabels(), s.GetAnnotations(), 5, 10, 1001, v1alpha1.Legacy)
	if err != nil {
		t.Fatal(err)
	}
	ownerutil.AddOwner(pod, &s, false, true)
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

func apiResourcesForObjects(objs []runtime.Object) []*metav1.APIResourceList {
	apis := []*metav1.APIResourceList{}
	for _, o := range objs {
		switch o := o.(type) {
		case *apiextensionsv1beta1.CustomResourceDefinition:
			crd := o
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
			a := o
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

func operatorGroup(ogName, saName, namespace string, saRef *corev1.ObjectReference) *operatorsv1.OperatorGroup {
	return &operatorsv1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ogName,
			Namespace: namespace,
		},
		Spec: operatorsv1.OperatorGroupSpec{
			TargetNamespaces:   []string{namespace},
			ServiceAccountName: saName,
		},
		Status: operatorsv1.OperatorGroupStatus{
			Namespaces:        []string{namespace},
			ServiceAccountRef: saRef,
		},
	}
}

func hasExpectedCondition(ip *v1alpha1.InstallPlan, expectedCondition v1alpha1.InstallPlanCondition) bool {
	for _, cond := range ip.Status.Conditions {
		if cond.Type == expectedCondition.Type && cond.Message == expectedCondition.Message && cond.Status == expectedCondition.Status {
			return true
		}
	}
	return false
}
