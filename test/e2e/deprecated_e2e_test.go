package e2e

import (
	"bytes"
	"context"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/crd"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Deprecated APIs", func() {
	BeforeEach(func() {
		csv := newCSV("test-csv", testNamespace, "", semver.Version{}, nil, nil, nil)
		Expect(ctx.Ctx().Client().Create(context.TODO(), &csv)).To(Succeed())
	})
	AfterEach(func() {
		TearDown(testNamespace)
	})

	When("deprecated objects are created in the installplan", func() {
		v1beta1CRD := apiextensionsv1beta1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "plums.cluster.com",
			},
			TypeMeta: metav1.TypeMeta{
				Kind:       crd.Kind,
				APIVersion: crd.Group + "v1beta1",
			},
			Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Versions: []apiextensionsv1beta1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1beta1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1beta1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
					Plural:   "plums",
					Singular: "plum",
					Kind:     "plum",
					ListKind: "list" + "plum",
				},
				Scope: "Namespaced",
			},
		}

		rbacv1beta1ClusterRole := rbacv1beta1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-rbac",
			},
			TypeMeta: metav1.TypeMeta{
				Kind:       "ClusterRole",
				APIVersion: "rbac.authorization.k8s.io/v1beta1",
			},
			Rules: []rbacv1beta1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"get"},
				},
			},
		}

		scheme := runtime.NewScheme()
		Expect(apiextensionsv1beta1.AddToScheme(scheme)).To(Succeed())
		Expect(rbacv1beta1.AddToScheme(scheme)).To(Succeed())

		var crdb bytes.Buffer
		Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&v1beta1CRD, &crdb)).To(Succeed())
		v1beta1Manifest := crdb.String()

		var rbacb bytes.Buffer
		Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&rbacv1beta1ClusterRole, &rbacb)).To(Succeed())
		rbacv1beta1Manifest := rbacb.String()

		// each entry is an installplan with a deprecated resource
		type payload struct {
			name string
			ip   *operatorsv1alpha1.InstallPlan
		}

		var tableEntries []table.TableEntry
		tableEntries = []table.TableEntry{
			table.Entry("contains a v1beta1 CRD", payload{
				name: "installplan contains v1beta1 CRD",
				ip: &operatorsv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: *namespace, // this is necessary due to ginkgo table semantics, see https://github.com/onsi/ginkgo/issues/378
						Name:      "test-plan",
					},
					Spec: operatorsv1alpha1.InstallPlanSpec{
						Approval:                   operatorsv1alpha1.ApprovalAutomatic,
						Approved:                   true,
						ClusterServiceVersionNames: []string{},
					},
					Status: operatorsv1alpha1.InstallPlanStatus{
						Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
						CatalogSources: []string{},
						Plan: []*operatorsv1alpha1.Step{
							{
								Resolving: "test-csv",
								Status:    operatorsv1alpha1.StepStatusUnknown,
								Resource: operatorsv1alpha1.StepResource{
									Name:     v1beta1CRD.GetName(),
									Version:  v1beta1CRD.APIVersion,
									Kind:     v1beta1CRD.Kind,
									Manifest: v1beta1Manifest,
								},
							},
						},
					},
				},
			}),
			table.Entry("contains a rbacv1beta1 clusterrole", payload{
				name: "installplan contains a rbacv1beta1 clusterrole",
				ip: &operatorsv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: *namespace, // this is necessary due to ginkgo table semantics, see https://github.com/onsi/ginkgo/issues/378
						Name:      "test-plan",
					},
					Spec: operatorsv1alpha1.InstallPlanSpec{
						Approval:                   operatorsv1alpha1.ApprovalAutomatic,
						Approved:                   true,
						ClusterServiceVersionNames: []string{},
					},
					Status: operatorsv1alpha1.InstallPlanStatus{
						Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
						CatalogSources: []string{},
						Plan: []*operatorsv1alpha1.Step{
							{
								Resolving: "test-csv",
								Status:    operatorsv1alpha1.StepStatusUnknown,
								Resource: operatorsv1alpha1.StepResource{
									Name:     rbacv1beta1ClusterRole.GetName(),
									Version:  rbacv1beta1ClusterRole.APIVersion,
									Kind:     rbacv1beta1ClusterRole.Kind,
									Manifest: rbacv1beta1Manifest,
								},
							},
						},
					},
				},
			}),
		}

		table.DescribeTable("the ip enters a failed state", func(tt payload) {
			Expect(ctx.Ctx().Client().Create(context.Background(), tt.ip)).To(Succeed())
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), tt.ip)).To(Succeed())

			// The installplan is initially stuck in the installing phase, with retries.
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return tt.ip, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(tt.ip), tt.ip)
			}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseInstalling))

			// ensure error is related to deprecation
			Eventually(func() string {
				ip := operatorsv1alpha1.InstallPlan{}
				err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(tt.ip), &ip)
				Expect(err).To(BeNil())
				return ip.Status.Message
			}).Should(ContainSubstring("resource has been deprecated"))

			// Eventually the IP fails, after installplan retries, which is by default 1 minute.
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return tt.ip, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(tt.ip), tt.ip)
			}, 2*time.Minute).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseFailed))
		}, tableEntries...)
	})
})
