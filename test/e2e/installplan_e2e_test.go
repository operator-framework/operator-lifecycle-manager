package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opver "github.com/operator-framework/api/pkg/lib/version"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/apis/rbac"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/util"
)

const (
	deprecatedCRDDir = "deprecated-crd"
)

var _ = Describe("Install Plan", func() {
	var (
		c                  operatorclient.ClientInterface
		crc                versioned.Interface
		generatedNamespace corev1.Namespace
	)

	BeforeEach(func() {
		namespaceName := genName("install-plan-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", namespaceName),
				Namespace: namespaceName,
			},
		}
		generatedNamespace = SetupGeneratedTestNamespaceWithOperatorGroup(namespaceName, og)
		c = ctx.Ctx().KubeClient()
		crc = ctx.Ctx().OperatorClient()
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
	})

	When("an InstallPlan step contains a deprecated resource version", func() {
		var (
			csv        operatorsv1alpha1.ClusterServiceVersion
			plan       operatorsv1alpha1.InstallPlan
			deprecated client.Object
			manifest   string
			counter    float64
		)

		BeforeEach(func() {
			dc, err := discovery.NewDiscoveryClientForConfig(ctx.Ctx().RESTConfig())
			Expect(err).ToNot(HaveOccurred())

			v, err := dc.ServerVersion()
			Expect(err).ToNot(HaveOccurred())

			if minor, err := strconv.Atoi(v.Minor); err == nil && minor < 16 {
				Skip("test is dependent on CRD v1 introduced at 1.16")
			}
		})

		BeforeEach(func() {
			counter = 0
			for _, metric := range getMetricsFromPod(ctx.Ctx().KubeClient(), getPodWithLabel(ctx.Ctx().KubeClient(), "app=catalog-operator")) {
				if metric.Family == "installplan_warnings_total" {
					counter = metric.Value
				}
			}
			deprecatedCRD, err := util.DecodeFile(filepath.Join(testdataDir, deprecatedCRDDir, "deprecated.crd.yaml"), &apiextensionsv1.CustomResourceDefinition{})
			Expect(err).NotTo(HaveOccurred())

			Expect(ctx.Ctx().Client().Create(context.Background(), deprecatedCRD)).To(Succeed())

			csv = newCSV(genName("test-csv-"), generatedNamespace.GetName(), "", semver.Version{}, nil, nil, nil)
			Expect(ctx.Ctx().Client().Create(context.Background(), &csv)).To(Succeed())

			deprecated, err = util.DecodeFile(filepath.Join(testdataDir, deprecatedCRDDir, "deprecated.cr.yaml"), &unstructured.Unstructured{}, util.WithNamespace(generatedNamespace.GetName()))
			Expect(err).NotTo(HaveOccurred())

			scheme := runtime.NewScheme()
			{
				var b bytes.Buffer
				Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(deprecated, &b)).To(Succeed())
				manifest = b.String()
			}

			plan = operatorsv1alpha1.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      genName("test-plan-"),
				},
				Spec: operatorsv1alpha1.InstallPlanSpec{
					Approval:                   operatorsv1alpha1.ApprovalAutomatic,
					Approved:                   true,
					ClusterServiceVersionNames: []string{},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &plan)).To(Succeed())
			plan.Status = operatorsv1alpha1.InstallPlanStatus{
				Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
				CatalogSources: []string{},
				Plan: []*operatorsv1alpha1.Step{
					{
						Resolving: csv.GetName(),
						Status:    operatorsv1alpha1.StepStatusUnknown,
						Resource: operatorsv1alpha1.StepResource{
							Name:     deprecated.GetName(),
							Version:  "v1",
							Kind:     "Deprecated",
							Manifest: manifest,
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), &plan)).To(Succeed())
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return &plan, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&plan), &plan)
			}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))
		})

		AfterEach(func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &csv))
			}).Should(Succeed())
			Eventually(func() error {
				deprecatedCRD := &apiextensionsv1.CustomResourceDefinition{
					ObjectMeta: metav1.ObjectMeta{
						Name: "deprecateds.operators.io.operator-framework",
					},
				}
				return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), deprecatedCRD))
			}).Should(Succeed())
		})

		It("creates an Event surfacing the deprecation warning", func() {
			Eventually(func() ([]corev1.Event, error) {
				var events corev1.EventList
				if err := ctx.Ctx().Client().List(context.Background(), &events, client.InNamespace(generatedNamespace.GetName())); err != nil {
					return nil, err
				}
				var result []corev1.Event
				for _, item := range events.Items {
					result = append(result, corev1.Event{
						InvolvedObject: corev1.ObjectReference{
							APIVersion: item.InvolvedObject.APIVersion,
							Kind:       item.InvolvedObject.Kind,
							Namespace:  item.InvolvedObject.Namespace,
							Name:       item.InvolvedObject.Name,
							FieldPath:  item.InvolvedObject.FieldPath,
						},
						Reason:  item.Reason,
						Message: item.Message,
					})
				}
				return result, nil
			}).Should(ContainElement(corev1.Event{
				InvolvedObject: corev1.ObjectReference{
					APIVersion: operatorsv1alpha1.InstallPlanAPIVersion,
					Kind:       operatorsv1alpha1.InstallPlanKind,
					Namespace:  generatedNamespace.GetName(),
					Name:       plan.GetName(),
					FieldPath:  "status.plan[0]",
				},
				Reason:  "AppliedWithWarnings",
				Message: fmt.Sprintf("1 warning(s) generated during installation of operator \"%s\" (Deprecated \"%s\"): operators.io.operator-framework/v1 Deprecated is deprecated", csv.GetName(), deprecated.GetName()),
			}))
		})

		It("increments a metric counting the warning", func() {
			Eventually(func() []Metric {
				return getMetricsFromPod(ctx.Ctx().KubeClient(), getPodWithLabel(ctx.Ctx().KubeClient(), "app=catalog-operator"))
			}).Should(ContainElement(LikeMetric(
				WithFamily("installplan_warnings_total"),
				WithValueGreaterThan(counter),
			)))
		})
	})

	When("a CustomResourceDefinition step resolved from a bundle is applied", func() {
		var (
			crd      apiextensionsv1.CustomResourceDefinition
			manifest string
		)

		BeforeEach(func() {
			csv := newCSV("test-csv", generatedNamespace.GetName(), "", semver.Version{}, nil, nil, nil)
			Expect(ctx.Ctx().Client().Create(context.Background(), &csv)).To(Succeed())

			crd = apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tests.example.com",
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "CustomResourceDefinition",
					APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "example.com",
					Scope: apiextensionsv1.ClusterScoped,
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   "tests",
						Singular: "test",
						Kind:     "Test",
						ListKind: "TestList",
					},
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
						Name:    "v1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object",
							},
						},
					}},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			{
				var b bytes.Buffer
				Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&crd, &b)).To(Succeed())
				manifest = b.String()
			}

			plan := operatorsv1alpha1.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-plan",
				},
				Spec: operatorsv1alpha1.InstallPlanSpec{
					Approval:                   operatorsv1alpha1.ApprovalAutomatic,
					Approved:                   true,
					ClusterServiceVersionNames: []string{},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &plan)).To(Succeed())
			plan.Status = operatorsv1alpha1.InstallPlanStatus{
				Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
				CatalogSources: []string{},
				Plan: []*operatorsv1alpha1.Step{
					{
						Resolving: "test-csv",
						Status:    operatorsv1alpha1.StepStatusUnknown,
						Resource: operatorsv1alpha1.StepResource{
							Name:     crd.GetName(),
							Version:  apiextensionsv1.SchemeGroupVersion.String(),
							Kind:     "CustomResourceDefinition",
							Manifest: manifest,
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), &plan)).To(Succeed())
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return &plan, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&plan), &plan)
			}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))
		})

		AfterEach(func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
		})

		It("is annotated with a reference to its associated ClusterServiceVersion", func() {
			Eventually(func() (map[string]string, error) {
				if err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&crd), &crd); err != nil {
					return nil, err
				}
				return crd.GetAnnotations(), nil
			}).Should(HaveKeyWithValue(
				HavePrefix("operatorframework.io/installed-alongside-"),
				fmt.Sprintf("%s/test-csv", generatedNamespace.GetName()),
			))
		})

		When("a second plan includes the same CustomResourceDefinition", func() {
			var (
				csv  operatorsv1alpha1.ClusterServiceVersion
				plan operatorsv1alpha1.InstallPlan
			)

			BeforeEach(func() {
				csv = newCSV("test-csv-two", generatedNamespace.GetName(), "", semver.Version{}, nil, nil, nil)
				Expect(ctx.Ctx().Client().Create(context.Background(), &csv)).To(Succeed())

				plan = operatorsv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: generatedNamespace.GetName(),
						Name:      "test-plan-two",
					},
					Spec: operatorsv1alpha1.InstallPlanSpec{
						Approval:                   operatorsv1alpha1.ApprovalAutomatic,
						Approved:                   true,
						ClusterServiceVersionNames: []string{},
					},
				}
				Expect(ctx.Ctx().Client().Create(context.Background(), &plan)).To(Succeed())
				plan.Status = operatorsv1alpha1.InstallPlanStatus{
					Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
					CatalogSources: []string{},
					Plan: []*operatorsv1alpha1.Step{
						{
							Resolving: "test-csv-two",
							Status:    operatorsv1alpha1.StepStatusUnknown,
							Resource: operatorsv1alpha1.StepResource{
								Name:     crd.GetName(),
								Version:  apiextensionsv1.SchemeGroupVersion.String(),
								Kind:     "CustomResourceDefinition",
								Manifest: manifest,
							},
						},
					},
				}
				Expect(ctx.Ctx().Client().Status().Update(context.Background(), &plan)).To(Succeed())
				Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
					return &plan, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&plan), &plan)
				}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))
			})

			AfterEach(func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &csv))
				}).Should(Succeed())
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &plan))
				}).Should(Succeed())
			})

			It("has one annotation for each ClusterServiceVersion", func() {
				Eventually(func() ([]struct{ Key, Value string }, error) {
					if err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&crd), &crd); err != nil {
						return nil, err
					}
					var pairs []struct{ Key, Value string }
					for k, v := range crd.GetAnnotations() {
						pairs = append(pairs, struct{ Key, Value string }{Key: k, Value: v})
					}
					return pairs, nil
				}).Should(ConsistOf(
					MatchFields(IgnoreExtras, Fields{
						"Key":   HavePrefix("operatorframework.io/installed-alongside-"),
						"Value": Equal(fmt.Sprintf("%s/test-csv", generatedNamespace.GetName())),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Key":   HavePrefix("operatorframework.io/installed-alongside-"),
						"Value": Equal(fmt.Sprintf("%s/test-csv-two", generatedNamespace.GetName())),
					}),
				))
			})
		})
	})

	When("an error is encountered during InstallPlan step execution", func() {
		var (
			plan  *operatorsv1alpha1.InstallPlan
			owned *corev1.ConfigMap
		)

		BeforeEach(func() {
			By(`It's hard to reliably generate transient`)
			By(`errors in an uninstrumented end-to-end`)
			By(`test, so simulate it by constructing an`)
			By(`error state that can be easily corrected`)
			By(`during a test.`)
			owned = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-owned",
					OwnerReferences: []metav1.OwnerReference{{
						APIVersion: "operators.coreos.com/v1alpha1",
						Kind:       "ClusterServiceVersion",
						Name:       "test-owner", // Does not exist!
						UID:        "",           // catalog-operator populates this if the named CSV exists.
					}},
				},
			}

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			var manifest bytes.Buffer
			Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(owned, &manifest)).To(Succeed())

			plan = &operatorsv1alpha1.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-plan",
				},
				Spec: operatorsv1alpha1.InstallPlanSpec{
					Approval:                   operatorsv1alpha1.ApprovalAutomatic,
					Approved:                   true,
					ClusterServiceVersionNames: []string{},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), plan)).To(Succeed())
			plan.Status = operatorsv1alpha1.InstallPlanStatus{
				Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
				CatalogSources: []string{},
				Plan: []*operatorsv1alpha1.Step{
					{
						Status: operatorsv1alpha1.StepStatusUnknown,
						Resource: operatorsv1alpha1.StepResource{
							Name:     owned.GetName(),
							Version:  "v1",
							Kind:     "ConfigMap",
							Manifest: manifest.String(),
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), plan)).To(Succeed())
		})

		AfterEach(func() {
			Expect(ctx.Ctx().Client().Delete(context.Background(), owned)).To(Or(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			))
			Expect(ctx.Ctx().Client().Delete(context.Background(), plan)).To(Or(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			))
		})

		It("times out if the error persists", func() {
			Eventually(
				func() (*operatorsv1alpha1.InstallPlan, error) {
					return plan, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(plan), plan)
				},
				90*time.Second,
			).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseFailed))
		})

		It("succeeds if there is no error on a later attempt", func() {
			owner := newCSV("test-owner", generatedNamespace.GetName(), "", semver.Version{}, nil, nil, nil)
			Expect(ctx.Ctx().Client().Create(context.Background(), &owner)).To(Succeed())
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return plan, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(plan), plan)
			}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))
		})
	})

	When("an InstallPlan transfers ownership of a ServiceAccount to a new ClusterServiceVersion", func() {
		var (
			csv1, csv2 operatorsv1alpha1.ClusterServiceVersion
			sa         corev1.ServiceAccount
			plan       operatorsv1alpha1.InstallPlan
		)

		BeforeEach(func() {
			csv1 = newCSV("test-csv-old", generatedNamespace.GetName(), "", semver.Version{}, nil, nil, nil)
			Expect(ctx.Ctx().Client().Create(context.Background(), &csv1)).To(Succeed())
			csv2 = newCSV("test-csv-new", generatedNamespace.GetName(), "", semver.Version{}, nil, nil, nil)
			Expect(ctx.Ctx().Client().Create(context.Background(), &csv2)).To(Succeed())

			sa = corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-serviceaccount",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
							Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
							Name:       csv1.GetName(),
							UID:        csv1.GetUID(),
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &sa)).To(Succeed())

			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			var manifest bytes.Buffer
			Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-serviceaccount",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
							Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
							Name:       csv2.GetName(),
						},
					},
				},
			}, &manifest)).To(Succeed())

			plan = operatorsv1alpha1.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-plan",
				},
				Spec: operatorsv1alpha1.InstallPlanSpec{
					Approval:                   operatorsv1alpha1.ApprovalAutomatic,
					Approved:                   true,
					ClusterServiceVersionNames: []string{},
				},
			}
			Expect(ctx.Ctx().Client().Create(context.Background(), &plan)).To(Succeed())
			plan.Status = operatorsv1alpha1.InstallPlanStatus{
				Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
				CatalogSources: []string{},
				Plan: []*operatorsv1alpha1.Step{
					{
						Status: operatorsv1alpha1.StepStatusUnknown,
						Resource: operatorsv1alpha1.StepResource{
							Name:     sa.GetName(),
							Version:  "v1",
							Kind:     "ServiceAccount",
							Manifest: manifest.String(),
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), &plan)).To(Succeed())
		})

		AfterEach(func() {
			Expect(ctx.Ctx().Client().Delete(context.Background(), &sa)).To(Or(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			))
			Expect(ctx.Ctx().Client().Delete(context.Background(), &csv1)).To(Or(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			))
			Expect(ctx.Ctx().Client().Delete(context.Background(), &csv2)).To(Or(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			))
			Expect(ctx.Ctx().Client().Delete(context.Background(), &plan)).To(Or(
				Succeed(),
				WithTransform(apierrors.IsNotFound, BeTrue()),
			))
		})

		It("preserves owner references to both the old and the new ClusterServiceVersion", func() {
			Eventually(func() ([]metav1.OwnerReference, error) {
				if err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(&sa), &sa); err != nil {
					return nil, err
				}
				return sa.GetOwnerReferences(), nil
			}).Should(ContainElements([]metav1.OwnerReference{
				{
					APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
					Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
					Name:       csv1.GetName(),
					UID:        csv1.GetUID(),
				},
				{
					APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
					Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
					Name:       csv2.GetName(),
					UID:        csv2.GetUID(),
				},
			}))
		})
	})

	When("a ClusterIP service exists", func() {
		var (
			service *corev1.Service
		)

		BeforeEach(func() {
			service = &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-service",
				},
				Spec: corev1.ServiceSpec{
					Type: corev1.ServiceTypeClusterIP,
					Ports: []corev1.ServicePort{
						{
							Port: 12345,
						},
					},
				},
			}

			Expect(ctx.Ctx().Client().Create(context.Background(), service.DeepCopy())).To(Succeed())
		})

		AfterEach(func() {
			Expect(ctx.Ctx().Client().Delete(context.Background(), service)).To(Succeed())
		})

		It("it can be updated by an InstallPlan step", func() {
			scheme := runtime.NewScheme()
			Expect(corev1.AddToScheme(scheme)).To(Succeed())
			var manifest bytes.Buffer
			Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(service, &manifest)).To(Succeed())

			plan := &operatorsv1alpha1.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: generatedNamespace.GetName(),
					Name:      "test-plan",
				},
				Spec: operatorsv1alpha1.InstallPlanSpec{
					Approval:                   operatorsv1alpha1.ApprovalAutomatic,
					Approved:                   true,
					ClusterServiceVersionNames: []string{},
				},
			}

			Expect(ctx.Ctx().Client().Create(context.Background(), plan)).To(Succeed())
			plan.Status = operatorsv1alpha1.InstallPlanStatus{
				Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
				CatalogSources: []string{},
				Plan: []*operatorsv1alpha1.Step{
					{
						Status: operatorsv1alpha1.StepStatusUnknown,
						Resource: operatorsv1alpha1.StepResource{
							Name:     service.Name,
							Version:  "v1",
							Kind:     "Service",
							Manifest: manifest.String(),
						},
					},
				},
			}
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), plan)).To(Succeed())

			key := client.ObjectKeyFromObject(plan)

			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return plan, ctx.Ctx().Client().Get(context.Background(), key, plan)
			}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))
			Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), plan))).To(Succeed())
		})
	})

	It("with CSVs across multiple catalog sources", func() {

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		dependentCRD := newCRD(genName("ins-"))
		mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil)
		dependentCSV := newCSV(dependentPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil, nil)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		dependentCatalogName := genName("mock-ocs-dependent-")
		mainCatalogName := genName("mock-ocs-main-")

		By(`Create separate manifests for each CatalogSource`)
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		dependentManifests := []registry.PackageManifest{
			{
				PackageName: dependentPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: dependentPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Defer CRD clean up`)
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), dependentCRD.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
		}()

		By(`Create the catalog sources`)
		require.NotEqual(GinkgoT(), "", generatedNamespace.GetName())
		_, cleanupDependentCatalogSource := createInternalCatalogSource(c, crc, dependentCatalogName, generatedNamespace.GetName(), dependentManifests, []apiextensionsv1.CustomResourceDefinition{dependentCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentCSV})
		defer cleanupDependentCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err := fetchCatalogSourceOnStatus(crc, dependentCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, nil, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err = fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create expected install plan step sources`)
		expectedStepSources := map[registry.ResourceKey]registry.ResourceKey{
			{Name: dependentCRD.Name, Kind: "CustomResourceDefinition"}:                                                                                               {Name: dependentCatalogName, Namespace: generatedNamespace.GetName()},
			{Name: dependentPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}:                                                                         {Name: dependentCatalogName, Namespace: generatedNamespace.GetName()},
			{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}:                                                                              {Name: mainCatalogName, Namespace: generatedNamespace.GetName()},
			{Name: strings.Join([]string{dependentPackageStable, dependentCatalogName, generatedNamespace.GetName()}, "-"), Kind: operatorsv1alpha1.SubscriptionKind}: {Name: dependentCatalogName, Namespace: generatedNamespace.GetName()},
		}

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase))

		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		By(`Fetch installplan again to check for unnecessary control loops`)
		fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), generatedNamespace.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
			By(`Don't compare object meta as labels can be applied by the operator controller.`)
			Expect(equality.Semantic.DeepEqual(fetchedInstallPlan.Spec, fip.Spec)).Should(BeTrue(), diff.ObjectDiff(fetchedInstallPlan, fip))
			Expect(equality.Semantic.DeepEqual(fetchedInstallPlan.Status, fip.Status)).Should(BeTrue(), diff.ObjectDiff(fetchedInstallPlan, fip))
			return true
		})
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), len(expectedStepSources), len(fetchedInstallPlan.Status.Plan), "Number of resolved steps matches the number of expected steps")

		By(`Ensure resolved step resources originate from the correct catalog sources`)
		log(fmt.Sprintf("%#v", expectedStepSources))
		for _, step := range fetchedInstallPlan.Status.Plan {
			log(fmt.Sprintf("checking %s", step.Resource))
			key := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
			expectedSource, ok := expectedStepSources[key]
			require.True(GinkgoT(), ok, "didn't find %v", key)
			require.Equal(GinkgoT(), expectedSource.Name, step.Resource.CatalogSource)
			require.Equal(GinkgoT(), expectedSource.Namespace, step.Resource.CatalogSourceNamespace)

			By(`delete`)
		}
	EXPECTED:
		for key := range expectedStepSources {
			for _, step := range fetchedInstallPlan.Status.Plan {
				if step.Resource.Name == key.Name && step.Resource.Kind == key.Kind {
					continue EXPECTED
				}
			}
			GinkgoT().Fatalf("expected step %s not found in %#v", key, fetchedInstallPlan.Status.Plan)
		}

		log("All expected resources resolved")

		By(`Verify that the dependent subscription is in a good state`)
		dependentSubscription, err := fetchSubscription(crc, generatedNamespace.GetName(), strings.Join([]string{dependentPackageStable, dependentCatalogName, generatedNamespace.GetName()}, "-"), subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dependentSubscription)
		require.NotNil(GinkgoT(), dependentSubscription.Status.InstallPlanRef)
		require.Equal(GinkgoT(), dependentCSV.GetName(), dependentSubscription.Status.CurrentCSV)

		By(`Verify CSV is created`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), dependentCSV.GetName(), csvAnyChecker)
		require.NoError(GinkgoT(), err)

		By(`Update dependent subscription in catalog and wait for csv to update`)
		updatedDependentCSV := newCSV(dependentPackageStable+"-v2", generatedNamespace.GetName(), dependentPackageStable, semver.MustParse("0.1.1"), []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil, nil)
		dependentManifests = []registry.PackageManifest{
			{
				PackageName: dependentPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: updatedDependentCSV.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, dependentCatalogName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{dependentCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentCSV, updatedDependentCSV}, dependentManifests)

		By(`Wait for subscription to update`)
		updatedDepSubscription, err := fetchSubscription(crc, generatedNamespace.GetName(), strings.Join([]string{dependentPackageStable, dependentCatalogName, generatedNamespace.GetName()}, "-"), subscriptionHasCurrentCSV(updatedDependentCSV.GetName()))
		require.NoError(GinkgoT(), err)

		By(`Verify installplan created and installed`)
		fetchedUpdatedDepInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedDepSubscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedUpdatedDepInstallPlan.GetName(), fetchedUpdatedDepInstallPlan.Status.Phase))
		require.NotEqual(GinkgoT(), fetchedInstallPlan.GetName(), fetchedUpdatedDepInstallPlan.GetName())

		By(`Wait for csv to update`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), updatedDependentCSV.GetName(), csvAnyChecker)
		require.NoError(GinkgoT(), err)
	})

	Context("creation with pre existing CRD owners", func() {

		It("OnePreExistingCRDOwner", func() {

			mainPackageName := genName("nginx-")
			dependentPackageName := genName("nginx-dep-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)
			dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)
			dependentPackageBeta := fmt.Sprintf("%s-beta", dependentPackageName)

			stableChannel := "stable"
			betaChannel := "beta"

			By(`Create manifests`)
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
					},
					DefaultChannelName: stableChannel,
				},
				{
					PackageName: dependentPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: dependentPackageStable},
						{Name: betaChannel, CurrentCSVName: dependentPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Create new CRDs`)
			mainCRD := newCRD(genName("ins-"))
			dependentCRD := newCRD(genName("ins-"))

			By(`Create new CSVs`)
			mainStableCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil)
			mainBetaCSV := newCSV(mainPackageBeta, generatedNamespace.GetName(), mainPackageStable, semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil)
			dependentStableCSV := newCSV(dependentPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil, nil)
			dependentBetaCSV := newCSV(dependentPackageBeta, generatedNamespace.GetName(), dependentPackageStable, semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil, nil)

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), mainCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), dependentCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
			}()

			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			By(`Create the catalog source`)
			mainCatalogSourceName := genName("mock-ocs-main-" + strings.ToLower(K8sSafeCurrentTestDescription()) + "-")
			_, cleanupCatalogSource := createInternalCatalogSource(c, crc, mainCatalogSourceName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{dependentCRD, mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
			defer cleanupCatalogSource()

			By(`Attempt to get the catalog source before creating install plan(s)`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			expectedSteps := map[registry.ResourceKey]struct{}{
				{Name: mainCRD.Name, Kind: "CustomResourceDefinition"}:                       {},
				{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}: {},
			}

			By(`Create the preexisting CRD and CSV`)
			cleanupCRD, err := createCRD(c, dependentCRD)
			require.NoError(GinkgoT(), err)
			defer cleanupCRD()
			cleanupCSV, err := createCSV(c, crc, dependentBetaCSV, generatedNamespace.GetName(), true, false)
			require.NoError(GinkgoT(), err)
			defer cleanupCSV()
			GinkgoT().Log("Dependent CRD and preexisting CSV created")

			subscriptionName := genName("sub-nginx-")
			subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete or Failed before checking resource presence`)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Fetch installplan again to check for unnecessary control loops`)
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), generatedNamespace.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				Expect(equality.Semantic.DeepEqual(fetchedInstallPlan, fip)).Should(BeTrue(), diff.ObjectDiff(fetchedInstallPlan, fip))
				return true
			})
			require.NoError(GinkgoT(), err)

			for _, step := range fetchedInstallPlan.Status.Plan {
				GinkgoT().Logf("%#v", step)
			}
			require.Equal(GinkgoT(), len(fetchedInstallPlan.Status.Plan), len(expectedSteps), "number of expected steps does not match installed")
			GinkgoT().Logf("Number of resolved steps matches the number of expected steps")

			for _, step := range fetchedInstallPlan.Status.Plan {
				key := registry.ResourceKey{
					Name: step.Resource.Name,
					Kind: step.Resource.Kind,
				}
				_, ok := expectedSteps[key]
				require.True(GinkgoT(), ok)

				By(`Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)`)
				delete(expectedSteps, key)
			}

			By(`Should have removed every matching step`)
			require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected")

			By(`Delete CRDs`)
			Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &mainCRD))).To(Succeed())
			Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &dependentCRD))).To(Succeed())
		})
	})

	Describe("with CRD schema change", func() {
		type schemaPayload struct {
			name            string
			expectedPhase   operatorsv1alpha1.InstallPlanPhase
			oldCRD          *apiextensionsv1.CustomResourceDefinition
			intermediateCRD *apiextensionsv1.CustomResourceDefinition
			newCRD          *apiextensionsv1.CustomResourceDefinition
		}

		var min float64 = 2
		var max float64 = 256
		var newMax float64 = 50
		// generated outside of the test table so that the same naming can be used for both old and new CSVs
		mainCRDPlural := genName("testcrd-")

		// excluded: new CRD, same version, same schema - won't trigger a CRD update
		tableEntries := []TableEntry{
			Entry("all existing versions are present, different (backwards compatible) schema", schemaPayload{
				name:          "all existing versions are present, different (backwards compatible) schema",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensionsv1.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "a")
					oldCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &max,
												},
											},
										},
									},
								},
							},
						},
					}
					return &oldCRD
				}(),
				newCRD: func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "a")
					newCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &max,
												},
											},
										},
									},
								},
							},
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &max,
												},
											},
										},
									},
								},
							},
						},
					}
					return &newCRD
				}(),
			}),
			Entry("all existing versions are present, different (backwards incompatible) schema", schemaPayload{name: "all existing versions are present, different (backwards incompatible) schema",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseFailed,
				oldCRD: func() *apiextensionsv1.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "b")
					oldCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
												},
											},
										},
									},
								},
							},
						},
					}
					return &oldCRD
				}(),
				newCRD: func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "b")
					newCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &newMax,
												},
											},
										},
									},
								},
							},
						},
					}
					return &newCRD
				}(),
			}),
			Entry("missing existing versions in new CRD", schemaPayload{name: "missing existing versions in new CRD",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensionsv1.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "c")
					oldCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					}
					return &oldCRD
				}(),
				newCRD: func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "c")
					newCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &max,
												},
											},
										},
									},
								},
							},
						},
						{
							Name:    "v1",
							Served:  true,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &max,
												},
											},
										},
									},
								},
							},
						},
					}
					return &newCRD
				}()}),
			Entry("existing version is present in new CRD (deprecated field)", schemaPayload{name: "existing version is present in new CRD (deprecated field)",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensionsv1.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "d")
					return &oldCRD
				}(),
				newCRD: func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "d")
					newCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"spec": {
											Type:        "object",
											Description: "Spec of a test object.",
											Properties: map[string]apiextensionsv1.JSONSchemaProps{
												"scalar": {
													Type:        "number",
													Description: "Scalar value that should have a min and max.",
													Minimum:     &min,
													Maximum:     &max,
												},
											},
										},
									},
								},
							},
						},
						{
							Name:    "v1alpha3",
							Served:  false,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
							},
						},
					}
					return &newCRD
				}()}),
		}

		DescribeTable("Test", func(tt schemaPayload) {

			mainPackageName := genName("nginx-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)

			stableChannel := "stable"
			betaChannel := "beta"

			By(`Create manifests`)
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
						{Name: betaChannel, CurrentCSVName: mainPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Create new CSVs`)
			mainStableCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{*tt.oldCRD}, nil, nil)
			mainBetaCSV := newCSV(mainPackageBeta, generatedNamespace.GetName(), mainPackageStable, semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{*tt.oldCRD}, nil, nil)

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), tt.oldCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), tt.newCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
				if tt.intermediateCRD != nil {
					Eventually(func() error {
						return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), tt.intermediateCRD.GetName(), metav1.DeleteOptions{}))
					}).Should(Succeed())
				}
			}()

			By(`Existing custom resource`)
			existingCR := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cluster.com/v1alpha1",
					"kind":       tt.oldCRD.Spec.Names.Kind,
					"metadata": map[string]interface{}{
						"namespace": generatedNamespace.GetName(),
						"name":      "my-cr-1",
					},
					"spec": map[string]interface{}{
						"scalar": 100,
					},
				},
			}

			By(`Create the catalog source`)
			mainCatalogSourceName := genName("mock-ocs-main-")
			_, cleanupCatalogSource := createInternalCatalogSource(c, crc, mainCatalogSourceName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{*tt.oldCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV})
			defer cleanupCatalogSource()

			By(`Attempt to get the catalog source before creating install plan(s)`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-alpha-")
			cleanupSubscription := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer cleanupSubscription()

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete or failed before checking resource presence`)
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), completeOrFailedFunc)
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Ensure that the desired resources have been created`)
			expectedSteps := map[registry.ResourceKey]struct{}{
				{Name: tt.oldCRD.Name, Kind: "CustomResourceDefinition"}:                     {},
				{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}: {},
			}

			require.Equal(GinkgoT(), len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

			for _, step := range fetchedInstallPlan.Status.Plan {
				key := registry.ResourceKey{
					Name: step.Resource.Name,
					Kind: step.Resource.Kind,
				}
				_, ok := expectedSteps[key]
				require.True(GinkgoT(), ok, "couldn't find %v in expected steps: %#v", key, expectedSteps)

				By(`Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)`)
				delete(expectedSteps, key)
			}

			By(`Should have removed every matching step`)
			require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected")

			By(`Create initial CR`)
			cleanupCR, err := createCR(c, existingCR, "cluster.com", "v1alpha1", generatedNamespace.GetName(), tt.oldCRD.Spec.Names.Plural, "my-cr-1")
			require.NoError(GinkgoT(), err)
			defer cleanupCR()

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogSourceName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{*tt.newCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV}, mainManifests)

			By(`Attempt to get the catalog source before creating install plan(s)`)
			_, err = fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			By(`Update the subscription resource to point to the beta CSV`)
			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
				require.NoError(GinkgoT(), err)
				require.NotNil(GinkgoT(), subscription)

				subscription.Spec.Channel = betaChannel
				subscription, err = crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Update(context.Background(), subscription, metav1.UpdateOptions{})

				return err
			})

			By(`Wait for subscription to have a new installplan`)
			subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete or Failed before checking resource presence`)
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(tt.expectedPhase))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			By(`Ensure correct in-cluster resource(s)`)
			fetchedCSV, err := fetchCSV(crc, generatedNamespace.GetName(), mainBetaCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			GinkgoT().Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		}, tableEntries)

	})

	Describe("with deprecated version CRD", func() {

		// generated outside of the test table so that the same naming can be used for both old and new CSVs
		mainCRDPlural := genName("ins")

		type schemaPayload struct {
			name            string
			expectedPhase   operatorsv1alpha1.InstallPlanPhase
			oldCRD          *apiextensionsv1.CustomResourceDefinition
			intermediateCRD *apiextensionsv1.CustomResourceDefinition
			newCRD          *apiextensionsv1.CustomResourceDefinition
		}

		// excluded: new CRD, same version, same schema - won't trigger a CRD update

		tableEntries := []TableEntry{
			Entry("[FLAKE] upgrade CRD with deprecated version", schemaPayload{
				name:          "upgrade CRD with deprecated version",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensionsv1.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural)
					oldCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					}
					return &oldCRD
				}(),
				intermediateCRD: func() *apiextensionsv1.CustomResourceDefinition {
					intermediateCRD := newCRD(mainCRDPlural)
					intermediateCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
						{
							Name:    "v1alpha1",
							Served:  false,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					}
					return &intermediateCRD
				}(),
				newCRD: func() *apiextensionsv1.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural)
					newCRD.Spec.Versions = []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
						{
							Name:    "v1beta1",
							Served:  true,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
						{
							Name:    "v1alpha1",
							Served:  false,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					}
					return &newCRD
				}(),
			}),
		}

		DescribeTable("Test", func(tt schemaPayload) {

			mainPackageName := genName("nginx-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)
			mainPackageDelta := fmt.Sprintf("%s-delta", mainPackageName)

			stableChannel := "stable"

			By(`Create manifests`)
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Create new CSVs`)
			mainStableCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{*tt.oldCRD}, nil, nil)
			mainBetaCSV := newCSV(mainPackageBeta, generatedNamespace.GetName(), mainPackageStable, semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{*tt.intermediateCRD}, nil, nil)
			mainDeltaCSV := newCSV(mainPackageDelta, generatedNamespace.GetName(), mainPackageBeta, semver.MustParse("0.3.0"), []apiextensionsv1.CustomResourceDefinition{*tt.newCRD}, nil, nil)

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), tt.oldCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), tt.newCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
				if tt.intermediateCRD != nil {
					Eventually(func() error {
						return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), tt.intermediateCRD.GetName(), metav1.DeleteOptions{}))
					}).Should(Succeed())
				}
			}()

			By(`Defer crd clean up`)
			defer func() {
				Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), tt.newCRD))).To(Succeed())
				Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), tt.oldCRD))).To(Succeed())
				Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), tt.intermediateCRD))).To(Succeed())
			}()

			By(`Create the catalog source`)
			mainCatalogSourceName := genName("mock-ocs-main-")
			_, cleanupCatalogSource := createInternalCatalogSource(c, crc, mainCatalogSourceName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{*tt.oldCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV})
			defer cleanupCatalogSource()

			By(`Attempt to get the catalog source before creating install plan(s)`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-")

			By(`this subscription will be cleaned up below without the clean up function`)
			createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete or failed before checking resource presence`)
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), completeOrFailedFunc)
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Ensure CRD versions are accurate`)
			expectedVersions := map[string]struct{}{
				"v1alpha1": {},
			}

			validateCRDVersions(GinkgoT(), c, tt.oldCRD.GetName(), expectedVersions)

			By(`Update the manifest`)
			mainManifests = []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogSourceName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{*tt.intermediateCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV}, mainManifests)
			By(`Attempt to get the catalog source before creating install plan(s)`)
			_, err = fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)
			subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(installPlanName))
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete or Failed before checking resource presence`)
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			By(`Ensure correct in-cluster resource(s)`)
			fetchedCSV, err := fetchCSV(crc, generatedNamespace.GetName(), mainBetaCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`Ensure CRD versions are accurate`)
			expectedVersions = map[string]struct{}{
				"v1alpha1": {},
				"v1alpha2": {},
			}

			validateCRDVersions(GinkgoT(), c, tt.oldCRD.GetName(), expectedVersions)

			By(`Update the manifest`)
			mainManifests = []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageDelta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogSourceName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{*tt.newCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV, mainDeltaCSV}, mainManifests)
			By(`Attempt to get the catalog source before creating install plan(s)`)
			_, err = fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)
			subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(installPlanName))
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete or Failed before checking resource presence`)
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			By(`Ensure correct in-cluster resource(s)`)
			fetchedCSV, err = fetchCSV(crc, generatedNamespace.GetName(), mainDeltaCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`Ensure CRD versions are accurate`)
			expectedVersions = map[string]struct{}{
				"v1alpha2": {},
				"v1beta1":  {},
				"v1alpha1": {},
			}

			validateCRDVersions(GinkgoT(), c, tt.oldCRD.GetName(), expectedVersions)
			GinkgoT().Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		}, tableEntries)

	})

	Describe("update catalog for subscription", func() {

		// crdVersionKey uniquely identifies a version within a CRD
		type crdVersionKey struct {
			name    string
			served  bool
			storage bool
		}
		It("AmplifyPermissions", func() {

			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			By(`Build initial catalog`)
			mainPackageName := genName("nginx-amplify-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			stableChannel := "stable"
			crdPlural := genName("ins-amplify-")
			crdName := crdPlural + ".cluster.com"
			mainCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			By(`Generate permissions`)
			serviceAccountName := genName("nginx-sa")
			permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}
			By(`Generate permissions`)
			clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}

			By(`Create the catalog sources`)
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), permissions, clusterPermissions)
			mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, &mainNamedStrategy)
			mainCatalogName := genName("mock-ocs-amplify-")
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), mainCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
			}()

			_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			By(`Attempt to get the catalog source before creating install plan`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update-perms1")
			subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)
			require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
			require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Verify CSV is created`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`Update CatalogSource with a new CSV with more permissions`)
			updatedPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"local.cluster.com"},
							Resources: []string{"locals"},
						},
					},
				},
			}
			updatedClusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"two.cluster.com"},
							Resources: []string{"twos"},
						},
					},
				},
			}

			By(`Create the catalog sources`)
			updatedNamedStrategy := newNginxInstallStrategy(genName("dep-"), updatedPermissions, updatedClusterPermissions)
			updatedCSV := newCSV(mainPackageStable+"-next", generatedNamespace.GetName(), mainCSV.GetName(), semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &updatedNamedStrategy)
			updatedManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: updatedCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Update catalog with updated CSV with more permissions`)
			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

			_, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedInstallPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

			By(`Wait for csv to update`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), updatedCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`If the CSV is succeeded, we successfully rolled out the RBAC changes`)
		})

		It("AttenuatePermissions", func() {

			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			By(`Build initial catalog`)
			mainPackageName := genName("nginx-attenuate-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			stableChannel := "stable"
			crdPlural := genName("ins-attenuate-")
			crdName := crdPlural + ".cluster.com"
			mainCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			By(`Generate permissions`)
			serviceAccountName := genName("nginx-sa")
			permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"local.cluster.com"},
							Resources: []string{"locals"},
						},
					},
				},
			}

			By(`Generate permissions`)
			clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"two.cluster.com"},
							Resources: []string{"twos"},
						},
					},
				},
			}

			By(`Create the catalog sources`)
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), permissions, clusterPermissions)
			mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, &mainNamedStrategy)
			mainCatalogName := genName("mock-ocs-main-update-perms1-")
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), mainCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
			}()

			_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			By(`Attempt to get the catalog source before creating install plan`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update-perms1")
			subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)
			require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
			require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Verify CSV is created`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By("Wait for ServiceAccount to have access")
			err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
				res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.Background(), &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: "system:serviceaccount:" + generatedNamespace.GetName() + ":" + serviceAccountName,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Group:    "cluster.com",
							Version:  "v1alpha1",
							Resource: crdPlural,
							Verb:     rbac.VerbAll,
						},
					},
				}, metav1.CreateOptions{})
				if err != nil {
					return false, err
				}
				if res == nil {
					return false, nil
				}
				GinkgoT().Log("checking serviceaccount for permission")

				By("should be allowed")
				return res.Status.Allowed, nil
			})
			Expect(err).NotTo(HaveOccurred())

			By(`Update CatalogSource with a new CSV with fewer permissions`)
			updatedPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"local.cluster.com"},
							Resources: []string{"locals"},
						},
					},
				},
			}
			updatedClusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"two.cluster.com"},
							Resources: []string{"twos"},
						},
					},
				},
			}

			By(`Create the catalog sources`)
			updatedNamedStrategy := newNginxInstallStrategy(genName("dep-"), updatedPermissions, updatedClusterPermissions)
			updatedCSV := newCSV(mainPackageStable+"-next", generatedNamespace.GetName(), mainCSV.GetName(), semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &updatedNamedStrategy)
			updatedManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: updatedCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Update catalog with updated CSV with more permissions`)
			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

			By(`Wait for subscription to update its status`)
			_, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedInstallPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

			By(`Wait for csv to update`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), updatedCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`Wait for ServiceAccount to not have access anymore`)
			err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
				res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.Background(), &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: "system:serviceaccount:" + generatedNamespace.GetName() + ":" + serviceAccountName,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Group:    "cluster.com",
							Version:  "v1alpha1",
							Resource: crdPlural,
							Verb:     rbac.VerbAll,
						},
					},
				}, metav1.CreateOptions{})
				if err != nil {
					return false, err
				}
				if res == nil {
					return false, nil
				}
				GinkgoT().Log("checking serviceaccount for permission")

				By(`should not be allowed`)
				return !res.Status.Allowed, nil
			})
		})

		It("StopOnCSVModifications", func() {

			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			By(`Build initial catalog`)
			mainPackageName := genName("nginx-amplify-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			stableChannel := "stable"
			crdPlural := genName("ins-amplify-")
			crdName := crdPlural + ".cluster.com"
			mainCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			By(`Generate permissions`)
			serviceAccountName := genName("nginx-sa")
			permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}

			By(`Generate permissions`)
			clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}

			By(`Create the catalog sources`)
			deploymentName := genName("dep-")
			mainNamedStrategy := newNginxInstallStrategy(deploymentName, permissions, clusterPermissions)
			mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, &mainNamedStrategy)
			mainCatalogName := genName("mock-ocs-stomper-")
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), mainCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
			}()

			_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			By(`Attempt to get the catalog source before creating install plan`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-stompy-")
			subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)
			require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
			require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Verify CSV is created`)
			csv, err := fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			addedEnvVar := corev1.EnvVar{Name: "EXAMPLE", Value: "value"}
			modifiedDetails := operatorsv1alpha1.StrategyDetailsDeployment{
				DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
					{
						Name: deploymentName,
						Spec: appsv1.DeploymentSpec{
							Selector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "nginx"},
							},
							Replicas: &singleInstance,
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Labels: map[string]string{"app": "nginx"},
								},
								Spec: corev1.PodSpec{Containers: []corev1.Container{
									{
										Name:            genName("nginx"),
										Image:           *dummyImage,
										Ports:           []corev1.ContainerPort{{ContainerPort: 80}},
										ImagePullPolicy: corev1.PullIfNotPresent,
										Env:             []corev1.EnvVar{addedEnvVar},
									},
								}},
							},
						},
					},
				},
				Permissions:        permissions,
				ClusterPermissions: clusterPermissions,
			}

			// wrapping the csv update in an eventually helps eliminate a flake in this test
			// it can happen that the csv changes in the meantime (e.g. reconciler adds a condition)
			// and the update fails with a conflict
			Eventually(func() error {
				csv, err := crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Get(context.Background(), csv.GetName(), metav1.GetOptions{})
				if err != nil {
					return nil
				}

				// update spec
				csv.Spec.InstallStrategy = operatorsv1alpha1.NamedInstallStrategy{
					StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
					StrategySpec: modifiedDetails,
				}

				// update csv
				_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Update(context.Background(), csv, metav1.UpdateOptions{})
				return err
			}).Should(Succeed())

			By(`Wait for csv to update`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), csv.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`Should have the updated env var`)
			err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
				dep, err := c.GetDeployment(generatedNamespace.GetName(), deploymentName)
				if err != nil {
					return false, nil
				}
				if len(dep.Spec.Template.Spec.Containers[0].Env) == 0 {
					return false, nil
				}
				for _, envVar := range dep.Spec.Template.Spec.Containers[0].Env {
					if envVar == addedEnvVar {
						return true, nil
					}
				}
				return false, nil
			})
			require.NoError(GinkgoT(), err)

			By(`Create the catalog sources`)
			By(`Updated csv has the same deployment strategy as main`)
			updatedCSV := newCSV(mainPackageStable+"-next", generatedNamespace.GetName(), mainCSV.GetName(), semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, &mainNamedStrategy)
			updatedManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: updatedCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Update catalog with updated CSV with more permissions`)
			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

			_, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedInstallPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

			By(`Wait for csv to update`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), updatedCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			By(`Should have created deployment and stomped on the env changes`)
			updatedDep, err := c.GetDeployment(generatedNamespace.GetName(), deploymentName)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), updatedDep)

			By(`Should have the updated env var`)
			for _, envVar := range updatedDep.Spec.Template.Spec.Containers[0].Env {
				require.False(GinkgoT(), envVar == addedEnvVar)
			}
		})

		It("UpdateSingleExistingCRDOwner", func() {

			mainPackageName := genName("nginx-update-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)

			stableChannel := "stable"

			crdPlural := genName("ins-update-")
			crdName := crdPlural + ".cluster.com"
			mainCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			updatedCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{mainCRD}, nil, nil)
			betaCSV := newCSV(mainPackageBeta, generatedNamespace.GetName(), mainPackageStable, semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{updatedCRD}, nil, nil)

			By(`Defer CRD clean up`)
			defer func() {
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), mainCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
				Eventually(func() error {
					return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), updatedCRD.GetName(), metav1.DeleteOptions{}))
				}).Should(Succeed())
			}()

			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			mainCatalogName := genName("mock-ocs-main-update-")

			By(`Create separate manifests for each CatalogSource`)
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Create the catalog sources`)
			_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			By(`Attempt to get the catalog source before creating install plan`)
			_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update-")
			createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)
			require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
			require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Fetch installplan again to check for unnecessary control loops`)
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), generatedNamespace.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				Expect(equality.Semantic.DeepEqual(fetchedInstallPlan, fip)).Should(BeTrue(), diff.ObjectDiff(fetchedInstallPlan, fip))
				return true
			})
			require.NoError(GinkgoT(), err)

			By(`Verify CSV is created`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			mainManifests = []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{updatedCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, betaCSV}, mainManifests)
			By(`Wait for subscription to update`)
			updatedSubscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			By(`Verify installplan created and installed`)
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedSubscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.NotEqual(GinkgoT(), fetchedInstallPlan.GetName(), fetchedUpdatedInstallPlan.GetName())

			By(`Wait for csv to update`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), betaCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			By(`Get the CRD to see if it is updated`)
			fetchedCRD, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), crdName, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), len(fetchedCRD.Spec.Versions), len(updatedCRD.Spec.Versions), "The CRD versions counts don't match")

			fetchedCRDVersions := map[crdVersionKey]struct{}{}
			for _, version := range fetchedCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				fetchedCRDVersions[key] = struct{}{}
			}

			for _, version := range updatedCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				_, ok := fetchedCRDVersions[key]
				require.True(GinkgoT(), ok, "couldn't find %v in fetched CRD versions: %#v", key, fetchedCRDVersions)
			}
		})

		It("UpdatePreexistingCRDFailed", func() {

			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			mainPackageName := genName("nginx-update2-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)

			stableChannel := "stable"

			crdPlural := genName("ins-update2-")
			crdName := crdPlural + ".cluster.com"
			mainCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			updatedCRD := apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crdName,
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: "cluster.com",
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
									Type:        "object",
									Description: "my crd schema",
								},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: apiextensionsv1.NamespaceScoped,
				},
			}

			expectedCRDVersions := map[crdVersionKey]struct{}{}
			for _, version := range mainCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				expectedCRDVersions[key] = struct{}{}
			}

			By(`Create the initial CSV`)
			cleanupCRD, err := createCRD(c, mainCRD)
			require.NoError(GinkgoT(), err)
			defer cleanupCRD()

			mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, nil)

			mainCatalogName := genName("mock-ocs-main-update2-")

			By(`Create separate manifests for each CatalogSource`)
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
					},
					DefaultChannelName: stableChannel,
				},
			}

			By(`Create the catalog sources`)
			_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{updatedCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			By(`Attempt to get the catalog source before creating install plan`)
			_, err = fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update2-")
			subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)
			require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
			require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

			installPlanName := subscription.Status.InstallPlanRef.Name

			By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			By(`Fetch installplan again to check for unnecessary control loops`)
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), generatedNamespace.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				Expect(equality.Semantic.DeepEqual(fetchedInstallPlan, fip)).Should(BeTrue(), diff.ObjectDiff(fetchedInstallPlan, fip))
				return true
			})
			require.NoError(GinkgoT(), err)

			By(`Verify CSV is created`)
			_, err = fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			By(`Get the CRD to see if it is updated`)
			fetchedCRD, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), crdName, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), len(fetchedCRD.Spec.Versions), len(mainCRD.Spec.Versions), "The CRD versions counts don't match")

			fetchedCRDVersions := map[crdVersionKey]struct{}{}
			for _, version := range fetchedCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				fetchedCRDVersions[key] = struct{}{}
			}

			for _, version := range mainCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				_, ok := fetchedCRDVersions[key]
				require.True(GinkgoT(), ok, "couldn't find %v in fetched CRD versions: %#v", key, fetchedCRDVersions)
			}
		})
	})

	It("creation with permissions", func() {
		By(`This It spec creates an InstallPlan with a CSV containing a set of permissions to be resolved.`)

		packageName := genName("nginx")
		stableChannel := "stable"
		stableCSVName := packageName + "-stable"

		By("Create manifests")
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{
						Name:           stableChannel,
						CurrentCSVName: stableCSVName,
					},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By("Create new CRDs")
		crdPlural := genName("ins")
		crd := newCRD(crdPlural)

		By("Defer CRD clean up")
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
		}()

		By("Generate permissions")
		By(`Permissions must be different than ClusterPermissions defined below if OLM is going to lift role/rolebindings to cluster level.`)
		serviceAccountName := genName("nginx-sa")
		permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccountName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{rbac.VerbAll},
						APIGroups: []string{"cluster.com"},
						Resources: []string{crdPlural},
					},
					{
						Verbs:     []string{rbac.VerbAll},
						APIGroups: []string{corev1.GroupName},
						Resources: []string{corev1.ResourceConfigMaps.String()},
					},
				},
			},
		}

		By("Generate permissions")
		clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccountName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{rbac.VerbAll},
						APIGroups: []string{"cluster.com"},
						Resources: []string{crdPlural},
					},
				},
			},
		}

		By("Create a new NamedInstallStrategy")
		namedStrategy := newNginxInstallStrategy(genName("dep-"), permissions, clusterPermissions)

		By("Create new CSVs")
		stableCSV := newCSV(stableCSVName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, &namedStrategy)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		By("Create CatalogSource")
		mainCatalogSourceName := genName("nginx-catalog")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, mainCatalogSourceName, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{stableCSV})
		defer cleanupCatalogSource()

		By("Attempt to get CatalogSource")
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By("Creating a Subscription")
		subscriptionName := genName("sub-nginx-")
		// Subscription is explitly deleted as part of the test to avoid CSV being recreated,
		// so ignore cleanup function
		_ = createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogSourceName, packageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

		By("Attempt to get Subscription")
		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		By("Attempt to get InstallPlan")
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed, operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		require.NotEqual(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase, "InstallPlan failed")

		By("Expect correct RBAC resources to be resolved and created")
		expectedSteps := map[registry.ResourceKey]struct{}{
			{Name: crd.Name, Kind: "CustomResourceDefinition"}:   {},
			{Name: stableCSVName, Kind: "ClusterServiceVersion"}: {},
			{Name: serviceAccountName, Kind: "ServiceAccount"}:   {},
			{Name: stableCSVName, Kind: "Role"}:                  {},
			{Name: stableCSVName, Kind: "RoleBinding"}:           {},
			{Name: stableCSVName, Kind: "ClusterRole"}:           {},
			{Name: stableCSVName, Kind: "ClusterRoleBinding"}:    {},
		}

		require.Equal(GinkgoT(), len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

		for _, step := range fetchedInstallPlan.Status.Plan {
			key := registry.ResourceKey{
				Name: step.Resource.Name,
				Kind: step.Resource.Kind,
			}
			for expected := range expectedSteps {
				if expected == key {
					delete(expectedSteps, expected)
				} else if strings.HasPrefix(key.Name, expected.Name) && key.Kind == expected.Kind {
					delete(expectedSteps, expected)
				} else {
					GinkgoT().Logf("Found unexpected step %#v, expected %#v: name has prefix: %v kinds match %v", key, expected, strings.HasPrefix(key.Name, expected.Name), key.Kind == expected.Kind)
				}
			}

			By("This operator was installed into a global operator group, so the roles should have been lifted to clusterroles")
			if step.Resource.Kind == "Role" {
				err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
					_, err = c.GetClusterRole(step.Resource.Name)
					if err != nil {
						if apierrors.IsNotFound(err) {
							return false, nil
						}
						return false, err
					}
					return true, nil
				})
				require.NoError(GinkgoT(), err)
			}
			if step.Resource.Kind == "RoleBinding" {
				err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
					_, err = c.GetClusterRoleBinding(step.Resource.Name)
					if err != nil {
						if apierrors.IsNotFound(err) {
							return false, nil
						}
						return false, err
					}
					return true, nil
				})
				require.NoError(GinkgoT(), err)
			}
		}

		By("Should have removed every matching step")
		require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected: %#v", expectedSteps)

		By(fmt.Sprintf("Explicitly deleting subscription %s/%s", generatedNamespace.GetName(), subscriptionName))
		err = crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Delete(context.Background(), subscriptionName, metav1.DeleteOptions{})
		By("Looking for no error OR IsNotFound error")
		require.NoError(GinkgoT(), client.IgnoreNotFound(err))

		By("Waiting for the Subscription to delete")
		err = waitForSubscriptionToDelete(generatedNamespace.GetName(), subscriptionName, crc)
		require.NoError(GinkgoT(), client.IgnoreNotFound(err))

		By(fmt.Sprintf("Explicitly deleting csv %s/%s", generatedNamespace.GetName(), stableCSVName))
		err = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.Background(), stableCSVName, metav1.DeleteOptions{})
		By("Looking for no error OR IsNotFound error")
		require.NoError(GinkgoT(), client.IgnoreNotFound(err))
		By("Waiting for the CSV to delete")
		err = waitForCsvToDelete(generatedNamespace.GetName(), stableCSVName, crc)
		require.NoError(GinkgoT(), client.IgnoreNotFound(err))

		nCrs := 0
		nCrbs := 0
		By("Waiting for CRBs and CRs and SAs to delete")
		Eventually(func() bool {

			crbs, err := c.KubernetesInterface().RbacV1().ClusterRoleBindings().List(context.Background(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
			if err != nil {
				GinkgoT().Logf("error getting crbs: %v", err)
				return false
			}
			if n := len(crbs.Items); n != 0 {
				if n != nCrbs {
					GinkgoT().Logf("CRBs remaining:  %v", n)
					nCrbs = n
				}
				return false
			}

			crs, err := c.KubernetesInterface().RbacV1().ClusterRoles().List(context.Background(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
			if err != nil {
				GinkgoT().Logf("error getting crs: %v", err)
				return false
			}
			if n := len(crs.Items); n != 0 {
				if n != nCrs {
					GinkgoT().Logf("CRs remaining: %v", n)
					nCrs = n
				}
				return false
			}

			_, err = c.KubernetesInterface().CoreV1().ServiceAccounts(generatedNamespace.GetName()).Get(context.Background(), serviceAccountName, metav1.GetOptions{})
			if client.IgnoreNotFound(err) != nil {
				GinkgoT().Logf("error getting sa %s/%s: %v", generatedNamespace.GetName(), serviceAccountName, err)
				return false
			}

			return true
		}, pollDuration, pollInterval).Should(BeTrue())
		By("Cleaning up the test")
	})

	It("CRD validation", func() {
		By(`Tests if CRD validation works with the "minimum" property after being`)
		By(`pulled from a CatalogSource's operator-registry.`)

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		var min float64 = 2
		var max float64 = 256

		By(`Create CRD with offending property`)
		crd := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"spec": {
										Type:        "object",
										Description: "Spec of a test object.",
										Properties: map[string]apiextensionsv1.JSONSchemaProps{
											"scalar": {
												Type:        "number",
												Description: "Scalar value that should have a min and max.",
												Minimum:     &min,
												Maximum:     &max,
											},
										},
									},
								},
							},
						},
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensionsv1.NamespaceScoped,
			},
		}

		By(`Defer CRD clean up`)
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
		}()

		By(`Create CSV`)
		packageName := genName("nginx-")
		stableChannel := "stable"
		packageNameStable := packageName + "-" + stableChannel
		csv := newCSV(packageNameStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)

		By(`Create PackageManifests`)
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: packageNameStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Create the CatalogSource`)
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csv})
		defer cleanupCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, catalogSourceName, packageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
		require.NoError(GinkgoT(), err)
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)
	})

	It("[FLAKE] consistent generation", func() {
		By(`This It spec verifies that, in cases where there are multiple options to fulfil a dependency`)
		By(`across multiple catalogs, we only generate one installplan with one set of resolved resources.`)
		//issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2633

		By(`Configure catalogs:`)
		By(`) - one catalog with a package that has a dependency`)
		By(`) - several duplicate catalog with a package that satisfies the dependency`)
		By(`Install the package from the main catalog`)
		By(`Should see only 1 installplan created`)
		By(`Should see the main CSV installed`)

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		dependentCRD := newCRD(genName("ins-"))
		mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil)
		dependentCSV := newCSV(dependentPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{dependentCRD}, nil, nil)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		dependentCatalogName := genName("mock-ocs-dependent-")
		mainCatalogName := genName("mock-ocs-main-")

		By(`Create separate manifests for each CatalogSource`)
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		dependentManifests := []registry.PackageManifest{
			{
				PackageName: dependentPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: dependentPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Defer CRD clean up`)
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), dependentCRD.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
		}()

		By(`Create the dependent catalog source`)
		_, cleanupDependentCatalogSource := createInternalCatalogSource(c, crc, dependentCatalogName, generatedNamespace.GetName(), dependentManifests, []apiextensionsv1.CustomResourceDefinition{dependentCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentCSV})
		defer cleanupDependentCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		dependentCatalogSource, err := fetchCatalogSourceOnStatus(crc, dependentCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create the alt dependent catalog sources`)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ { // Creating more increases the odds that the race condition will be triggered
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				By(`Create a CatalogSource pointing to the grpc pod`)
				addressSource := &operatorsv1alpha1.CatalogSource{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1alpha1.CatalogSourceKind,
						APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
					},
					Spec: operatorsv1alpha1.CatalogSourceSpec{
						SourceType: operatorsv1alpha1.SourceTypeGrpc,
						Address:    dependentCatalogSource.Status.RegistryServiceStatus.Address(),
						GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
							SecurityContextConfig: operatorsv1alpha1.Restricted,
						},
					},
				}
				addressSource.SetName(genName("alt-dep-"))

				_, err := crc.OperatorsV1alpha1().CatalogSources(generatedNamespace.GetName()).Create(context.Background(), addressSource, metav1.CreateOptions{})
				require.NoError(GinkgoT(), err)

				By(`Attempt to get the catalog source before creating install plan`)
				_, err = fetchCatalogSourceOnStatus(crc, addressSource.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				require.NoError(GinkgoT(), err)
				wg.Done()
			}(i)
		}
		wg.Wait()

		By(`Create the main catalog source`)
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, nil, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err = fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
		fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase))

		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		By(`Verify CSV is created`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Make sure to clean up the installed CRD`)
		deleteOpts := &metav1.DeleteOptions{}
		defer func() {
			require.NoError(GinkgoT(), c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), dependentCRD.GetName(), *deleteOpts))
		}()

		By(`ensure there is only one installplan`)
		ips, err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).List(context.Background(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), 1, len(ips.Items), "If this test fails it should be taken seriously and not treated as a flake. \n%v", ips.Items)
	})

	When("an InstallPlan is created with no valid OperatorGroup present", func() {
		var (
			installPlanName string
		)

		BeforeEach(func() {
			By(`Make sure there are no OGs in the namespace already`)
			require.NoError(GinkgoT(), crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))

			By(`Create InstallPlan`)
			installPlanName = "ip"
			ip := newInstallPlanWithDummySteps(installPlanName, generatedNamespace.GetName(), operatorsv1alpha1.InstallPlanPhaseInstalling)
			outIP, err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Create(context.Background(), ip, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(outIP).NotTo(BeNil())

			By(`The status gets ignored on create so we need to update it else the InstallPlan sync ignores`)
			By(`InstallPlans without any steps or bundle lookups`)
			outIP.Status = ip.Status
			_, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).UpdateStatus(context.Background(), outIP, metav1.UpdateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Delete(context.Background(), installPlanName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("[FLAKE] should clear up the condition in the InstallPlan status that contains an error message when a valid OperatorGroup is created", func() {
			By(`issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2636`)

			By(`first wait for a condition with a message exists`)
			cond := operatorsv1alpha1.InstallPlanCondition{Type: operatorsv1alpha1.InstallPlanInstalled, Status: corev1.ConditionFalse, Reason: operatorsv1alpha1.InstallPlanReasonInstallCheckFailed,
				Message: "no operator group found that is managing this namespace"}

			Eventually(func() bool {
				fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseInstalling))
				if err != nil || fetchedInstallPlan == nil {
					return false
				}
				if fetchedInstallPlan.Status.Phase != operatorsv1alpha1.InstallPlanPhaseInstalling {
					return false
				}
				return hasCondition(fetchedInstallPlan, cond)
			}, 5*time.Minute, interval).Should(BeTrue())

			By(`Create an operatorgroup for the same namespace`)
			og := &operatorsv1.OperatorGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "og",
					Namespace: generatedNamespace.GetName(),
				},
				Spec: operatorsv1.OperatorGroupSpec{
					TargetNamespaces: []string{generatedNamespace.GetName()},
				},
			}
			Eventually(func() error {
				return ctx.Ctx().Client().Create(context.Background(), og)
			}, timeout, interval).Should(Succeed(), "could not create OperatorGroup")

			By(`Wait for the OperatorGroup to be synced`)
			Eventually(
				func() ([]string, error) {
					err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(og), og)
					ctx.Ctx().Logf("Waiting for OperatorGroup(%v) to be synced with status.namespaces: %v", og.Name, og.Status.Namespaces)
					return og.Status.Namespaces, err
				},
				1*time.Minute,
				interval,
			).Should(ContainElement(generatedNamespace.GetName()))

			By(`check that the condition has been cleared up`)
			Eventually(func() (bool, error) {
				fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseInstalling))
				if err != nil {
					return false, err
				}
				if fetchedInstallPlan == nil {
					return false, err
				}
				if hasCondition(fetchedInstallPlan, cond) {
					return false, nil
				}
				return true, nil
			}).Should(BeTrue())
		})
	})

	It("compresses installplan step resource manifests to configmap references", func() {
		By(`Test ensures that all steps for index-based catalogs are references to configmaps. This avoids the problem`)
		By(`of installplans growing beyond the etcd size limit when manifests are written to the ip status.`)
		catsrc := &operatorsv1alpha1.CatalogSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("kiali-"),
				Namespace: generatedNamespace.GetName(),
				Labels:    map[string]string{"olm.catalogSource": "kaili-catalog"},
			},
			Spec: operatorsv1alpha1.CatalogSourceSpec{
				Image:      "quay.io/operator-framework/ci-index:latest",
				SourceType: operatorsv1alpha1.SourceTypeGrpc,
				GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
					SecurityContextConfig: operatorsv1alpha1.Restricted,
				},
			},
		}
		catsrc, err := crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Create(context.Background(), catsrc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By(`Wait for the CatalogSource to be ready`)
		catsrc, err = fetchCatalogSourceOnStatus(crc, catsrc.GetName(), catsrc.GetNamespace(), catalogSourceRegistryPodSynced())
		Expect(err).ToNot(HaveOccurred())

		By(`Generate a Subscription`)
		subName := genName("kiali-")
		cleanUpSubscriptionFn := createSubscriptionForCatalog(crc, catsrc.GetNamespace(), subName, catsrc.GetName(), "kiali", stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer cleanUpSubscriptionFn()

		sub, err := fetchSubscription(crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker())
		Expect(err).ToNot(HaveOccurred())

		By(`Wait for the expected InstallPlan's execution to either fail or succeed`)
		ipName := sub.Status.InstallPlanRef.Name
		ip, err := waitForInstallPlan(crc, ipName, sub.GetNamespace(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed, operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		Expect(operatorsv1alpha1.InstallPlanPhaseComplete).To(Equal(ip.Status.Phase), "InstallPlan not complete")

		By(`Ensure the InstallPlan contains the steps resolved from the bundle image`)
		operatorName := "kiali-operator"
		expectedSteps := map[registry.ResourceKey]struct{}{
			{Name: operatorName, Kind: "ClusterServiceVersion"}:                                  {},
			{Name: "kialis.kiali.io", Kind: "CustomResourceDefinition"}:                          {},
			{Name: "monitoringdashboards.monitoring.kiali.io", Kind: "CustomResourceDefinition"}: {},
			{Name: operatorName, Kind: "ServiceAccount"}:                                         {},
			{Name: operatorName, Kind: "ClusterRole"}:                                            {},
			{Name: operatorName, Kind: "ClusterRoleBinding"}:                                     {},
		}
		Expect(ip.Status.Plan).To(HaveLen(len(expectedSteps)), "number of expected steps does not match installed: %v", ip.Status.Plan)

		for _, step := range ip.Status.Plan {
			key := registry.ResourceKey{
				Name: step.Resource.Name,
				Kind: step.Resource.Kind,
			}
			for expected := range expectedSteps {
				if strings.HasPrefix(key.Name, expected.Name) && key.Kind == expected.Kind {
					delete(expectedSteps, expected)
				}
			}
		}
		Expect(expectedSteps).To(HaveLen(0), "Actual resource steps do not match expected: %#v", expectedSteps)

		By(`Ensure that all the steps have a configmap based reference`)
		for _, step := range ip.Status.Plan {
			manifest := step.Resource.Manifest
			var ref catalog.UnpackedBundleReference
			err := json.Unmarshal([]byte(manifest), &ref)
			Expect(err).ToNot(HaveOccurred())
			Expect(ref.Kind).To(Equal("ConfigMap"))
		}
	})

	It("limits installed resources if the scoped serviceaccount has no permissions", func() {
		By("creating a scoped serviceaccount specified in the operatorgroup")
		By(`create SA`)
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("sa-"),
				Namespace: generatedNamespace.GetName(),
			},
		}
		_, err := c.KubernetesInterface().CoreV1().ServiceAccounts(generatedNamespace.GetName()).Create(context.Background(), sa, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		By(`Create token secret for the serviceaccount`)
		_, cleanupSE := newTokenSecret(c, generatedNamespace.GetName(), sa.GetName())
		defer cleanupSE()

		By(`role has no explicit permissions`)
		role := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("role-"),
			},
			Rules: []rbacv1.PolicyRule{},
		}

		By(`bind role to SA`)
		rb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("rb-"),
			},
			RoleRef: rbacv1.RoleRef{
				Name:     role.GetName(),
				Kind:     "ClusterRole",
				APIGroup: "rbac.authorization.k8s.io",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.GetName(),
					APIGroup:  "",
					Namespace: sa.GetNamespace(),
				},
			},
		}

		_, err = c.KubernetesInterface().RbacV1().ClusterRoleBindings().Create(context.Background(), rb, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		defer c.KubernetesInterface().RbacV1().ClusterRoles().Delete(context.Background(), role.GetName(), metav1.DeleteOptions{})

		By(`Update the existing OG to use the ServiceAccount`)
		Eventually(func() error {
			existingOG, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Get(context.Background(), fmt.Sprintf("%s-operatorgroup", generatedNamespace.GetName()), metav1.GetOptions{})
			if err != nil {
				return err
			}
			existingOG.Spec.ServiceAccountName = sa.GetName()
			_, err = crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Update(context.Background(), existingOG, metav1.UpdateOptions{})
			return err
		}).Should(Succeed())

		By(`Wait for the OperatorGroup to be synced and have a status.ServiceAccountRef`)
		By(`before moving on. Otherwise the catalog operator treats it as an invalid OperatorGroup`)
		By(`and the InstallPlan is resynced`)
		Eventually(func() (*corev1.ObjectReference, error) {
			outOG, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Get(context.Background(), fmt.Sprintf("%s-operatorgroup", generatedNamespace.GetName()), metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			ctx.Ctx().Logf("[DEBUG] Operator Group Status: %+v\n", outOG.Status)
			return outOG.Status.ServiceAccountRef, nil
		}).ShouldNot(BeNil())

		crd := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ins" + ".cluster.com",
			},
			TypeMeta: metav1.TypeMeta{
				Kind:       "CustomResourceDefinition",
				APIVersion: "v1",
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "ins",
					Singular: "ins",
					Kind:     "ins",
					ListKind: "ins" + "list",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
			},
		}

		By(`Defer CRD clean up`)
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
		}()

		scheme := runtime.NewScheme()
		Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())
		var crdManifest bytes.Buffer
		Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&crd, &crdManifest)).To(Succeed())
		By("using the OLM client to create the CRD")
		plan := &operatorsv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: generatedNamespace.GetName(),
				Name:      genName("ip-"),
			},
			Spec: operatorsv1alpha1.InstallPlanSpec{
				Approval:                   operatorsv1alpha1.ApprovalAutomatic,
				Approved:                   true,
				ClusterServiceVersionNames: []string{},
			},
		}

		Expect(ctx.Ctx().Client().Create(context.Background(), plan)).To(Succeed())
		plan.Status = operatorsv1alpha1.InstallPlanStatus{
			AttenuatedServiceAccountRef: &corev1.ObjectReference{
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
				Kind:      "ServiceAccount",
			},
			Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
			CatalogSources: []string{},
			Plan: []*operatorsv1alpha1.Step{
				{
					Status: operatorsv1alpha1.StepStatusUnknown,
					Resource: operatorsv1alpha1.StepResource{
						Name:     crd.GetName(),
						Version:  "v1",
						Kind:     "CustomResourceDefinition",
						Manifest: crdManifest.String(),
					},
				},
			},
		}
		Expect(ctx.Ctx().Client().Status().Update(context.Background(), plan)).To(Succeed())

		key := client.ObjectKeyFromObject(plan)

		Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
			return plan, ctx.Ctx().Client().Get(context.Background(), key, plan)
		}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))

		By(`delete installplan, then create one with an additional resource that the SA does not have permissions to create`)
		By(`expect installplan to fail`)
		By("failing to install resources that are not explicitly allowed in the SA")
		err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Delete(context.Background(), plan.GetName(), metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())

		service := &corev1.Service{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Service",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: generatedNamespace.GetName(),
				Name:      "test-service",
			},
			Spec: corev1.ServiceSpec{
				Type: corev1.ServiceTypeClusterIP,
				Ports: []corev1.ServicePort{
					{
						Port: 12345,
					},
				},
			},
		}

		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		var manifest bytes.Buffer
		Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(service, &manifest)).To(Succeed())

		newPlan := &operatorsv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: generatedNamespace.GetName(),
				Name:      genName("ip-"),
			},
			Spec: operatorsv1alpha1.InstallPlanSpec{
				Approval:                   operatorsv1alpha1.ApprovalAutomatic,
				Approved:                   true,
				ClusterServiceVersionNames: []string{},
			},
		}

		Expect(ctx.Ctx().Client().Create(context.Background(), newPlan)).To(Succeed())
		newPlan.Status = operatorsv1alpha1.InstallPlanStatus{
			StartTime: &metav1.Time{Time: time.Unix(0, 0)}, // disable retries
			AttenuatedServiceAccountRef: &corev1.ObjectReference{
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
				Kind:      "ServiceAccount",
			},
			Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
			CatalogSources: []string{},
			Plan: []*operatorsv1alpha1.Step{
				{
					Status: operatorsv1alpha1.StepStatusUnknown,
					Resource: operatorsv1alpha1.StepResource{
						Name:     service.Name,
						Version:  "v1",
						Kind:     "Service",
						Manifest: manifest.String(),
					},
				},
			},
		}
		Expect(ctx.Ctx().Client().Status().Update(context.Background(), newPlan)).To(Succeed())

		ipPhaseCheckerFunc := buildInstallPlanMessageCheckFunc(`cannot create resource "services" in API group`)
		_, err = fetchInstallPlanWithNamespace(GinkgoT(), crc, newPlan.Name, newPlan.Namespace, ipPhaseCheckerFunc)
		require.NoError(GinkgoT(), err)

		Expect(client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &crd))).To(Succeed())
	})

	It("uses the correct client when installing resources from an installplan", func() {
		By("creating a scoped serviceaccount specifified in the operatorgroup")
		By(`create SA`)
		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("sa-"),
				Namespace: generatedNamespace.GetName(),
			},
		}
		_, err := c.KubernetesInterface().CoreV1().ServiceAccounts(generatedNamespace.GetName()).Create(context.Background(), sa, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		By(`Create token secret for the serviceaccount`)
		_, cleanupSE := newTokenSecret(c, generatedNamespace.GetName(), sa.GetName())
		defer cleanupSE()

		By(`see https://github.com/operator-framework/operator-lifecycle-manager/blob/master/doc/design/scoped-operator-install.md`)
		By(`create permissions with the ability to get and list CRDs, but not create CRDs`)
		role := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("role-"),
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"operators.coreos.com"},
					Resources: []string{"subscriptions", "clusterserviceversions"},
					Verbs:     []string{"get", "create", "update", "patch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"services", "serviceaccounts", "configmaps", "endpoints", "events", "persistentvolumeclaims", "pods"},
					Verbs:     []string{"create", "delete", "get", "list", "update", "patch", "watch"},
				},
				{
					APIGroups: []string{"apps"},
					Resources: []string{"deployments", "replicasets", "statefulsets"},
					Verbs:     []string{"list", "watch", "get", "create", "update", "patch", "delete"},
				},
				{
					APIGroups: []string{"apiextensions.k8s.io"},
					Resources: []string{"customresourcedefinitions"},
					Verbs:     []string{"get", "list", "watch"},
				},
			},
		}

		_, err = c.KubernetesInterface().RbacV1().ClusterRoles().Create(context.Background(), role, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By(`bind role to SA`)
		rb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("rb-"),
			},
			RoleRef: rbacv1.RoleRef{
				Name:     role.GetName(),
				Kind:     "ClusterRole",
				APIGroup: "rbac.authorization.k8s.io",
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.GetName(),
					APIGroup:  "",
					Namespace: sa.GetNamespace(),
				},
			},
		}

		_, err = c.KubernetesInterface().RbacV1().ClusterRoleBindings().Create(context.Background(), rb, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		defer c.KubernetesInterface().RbacV1().ClusterRoles().Delete(context.Background(), role.GetName(), metav1.DeleteOptions{})

		By(`Update the existing OG to use the ServiceAccount`)
		Eventually(func() error {
			existingOG, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Get(context.Background(), fmt.Sprintf("%s-operatorgroup", generatedNamespace.GetName()), metav1.GetOptions{})
			if err != nil {
				return err
			}
			existingOG.Spec.ServiceAccountName = sa.GetName()
			_, err = crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Update(context.Background(), existingOG, metav1.UpdateOptions{})
			return err
		}).Should(Succeed())

		By(`Wait for the OperatorGroup to be synced and have a status.ServiceAccountRef`)
		By(`before moving on. Otherwise the catalog operator treats it as an invalid OperatorGroup`)
		By(`and the InstallPlan is resynced`)
		Eventually(func() (*corev1.ObjectReference, error) {
			outOG, err := crc.OperatorsV1().OperatorGroups(generatedNamespace.GetName()).Get(context.Background(), fmt.Sprintf("%s-operatorgroup", generatedNamespace.GetName()), metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			ctx.Ctx().Logf("[DEBUG] Operator Group Status: %+v\n", outOG.Status)
			return outOG.Status.ServiceAccountRef, nil
		}).ShouldNot(BeNil())

		By("using the OLM client to install CRDs from the installplan and the scoped client for other resources")

		crd := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ins" + ".cluster.com",
			},
			TypeMeta: metav1.TypeMeta{
				Kind:       "CustomResourceDefinition",
				APIVersion: "v1",
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   "ins",
					Singular: "ins",
					Kind:     "ins",
					ListKind: "ins" + "list",
				},
				Scope: apiextensionsv1.NamespaceScoped,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
			},
		}
		csv := newCSV("stable", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, nil)

		By(`Defer CRD clean up`)
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{}))
			}).Should(Succeed())
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &csv))
			}).Should(Succeed())
		}()

		scheme := runtime.NewScheme()
		Expect(apiextensionsv1.AddToScheme(scheme)).To(Succeed())
		Expect(operatorsv1alpha1.AddToScheme(scheme)).To(Succeed())
		var crdManifest, csvManifest bytes.Buffer
		Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&crd, &crdManifest)).To(Succeed())
		Expect(k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, false).Encode(&csv, &csvManifest)).To(Succeed())

		plan := &operatorsv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: generatedNamespace.GetName(),
				Name:      genName("ip-"),
			},
			Spec: operatorsv1alpha1.InstallPlanSpec{
				Approval:                   operatorsv1alpha1.ApprovalAutomatic,
				Approved:                   true,
				ClusterServiceVersionNames: []string{csv.GetName()},
			},
		}

		Expect(ctx.Ctx().Client().Create(context.Background(), plan)).To(Succeed())
		plan.Status = operatorsv1alpha1.InstallPlanStatus{
			AttenuatedServiceAccountRef: &corev1.ObjectReference{
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
				Kind:      "ServiceAccount",
			},
			Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
			CatalogSources: []string{},
			Plan: []*operatorsv1alpha1.Step{
				{
					Status: operatorsv1alpha1.StepStatusUnknown,
					Resource: operatorsv1alpha1.StepResource{
						Name:     csv.GetName(),
						Version:  "v1alpha1",
						Kind:     "ClusterServiceVersion",
						Manifest: csvManifest.String(),
					},
				},
				{
					Status: operatorsv1alpha1.StepStatusUnknown,
					Resource: operatorsv1alpha1.StepResource{
						Name:     crd.GetName(),
						Version:  "v1",
						Kind:     "CustomResourceDefinition",
						Manifest: crdManifest.String(),
					},
				},
			},
		}
		Expect(ctx.Ctx().Client().Status().Update(context.Background(), plan)).To(Succeed())

		key := client.ObjectKeyFromObject(plan)

		Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
			return plan, ctx.Ctx().Client().Get(context.Background(), key, plan)
		}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))

		By(`delete installplan, and create one with just a CSV resource which should succeed`)
		By("installing additional resources that are allowed in the SA")
		err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Delete(context.Background(), plan.GetName(), metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())

		newPlan := &operatorsv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: generatedNamespace.GetName(),
				Name:      genName("ip-"),
			},
			Spec: operatorsv1alpha1.InstallPlanSpec{
				Approval:                   operatorsv1alpha1.ApprovalAutomatic,
				Approved:                   true,
				ClusterServiceVersionNames: []string{csv.GetName()},
			},
		}

		Expect(ctx.Ctx().Client().Create(context.Background(), newPlan)).To(Succeed())
		newPlan.Status = operatorsv1alpha1.InstallPlanStatus{
			AttenuatedServiceAccountRef: &corev1.ObjectReference{
				Name:      sa.GetName(),
				Namespace: sa.GetNamespace(),
				Kind:      "ServiceAccount",
			},
			Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
			CatalogSources: []string{},
			Plan: []*operatorsv1alpha1.Step{
				{
					Status: operatorsv1alpha1.StepStatusUnknown,
					Resource: operatorsv1alpha1.StepResource{
						Name:     csv.GetName(),
						Version:  "v1alpha1",
						Kind:     "ClusterServiceVersion",
						Manifest: csvManifest.String(),
					},
				},
			},
		}
		Expect(ctx.Ctx().Client().Status().Update(context.Background(), newPlan)).To(Succeed())

		newKey := client.ObjectKeyFromObject(newPlan)

		Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
			return newPlan, ctx.Ctx().Client().Get(context.Background(), newKey, newPlan)
		}).Should(HavePhase(operatorsv1alpha1.InstallPlanPhaseComplete))
	})
})

type checkInstallPlanFunc func(fip *operatorsv1alpha1.InstallPlan) bool

func validateCRDVersions(t GinkgoTInterface, c operatorclient.ClientInterface, name string, expectedVersions map[string]struct{}) {
	By(`Retrieve CRD information`)
	crd, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, len(expectedVersions), len(crd.Spec.Versions), "number of CRD versions don't not match installed")

	for _, version := range crd.Spec.Versions {
		_, ok := expectedVersions[version.Name]
		require.True(t, ok, "couldn't find %v in expected versions: %#v", version.Name, expectedVersions)

		By(`Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)`)
		delete(expectedVersions, version.Name)
	}

	By(`Should have removed every matching version`)
	require.Equal(t, 0, len(expectedVersions), "Actual CRD versions do not match expected")
}

func buildInstallPlanMessageCheckFunc(substring string) checkInstallPlanFunc {
	var lastMessage string
	lastTime := time.Now()
	return func(fip *operatorsv1alpha1.InstallPlan) bool {
		if fip.Status.Message != lastMessage {
			ctx.Ctx().Logf("waiting %s for installplan %s/%s to have message substring %q, have message %q", time.Since(lastTime), fip.Namespace, fip.Name, substring, fip.Status.Message)
			lastMessage = fip.Status.Message
			lastTime = time.Now()
		}
		return strings.Contains(fip.Status.Message, substring)
	}
}

func buildInstallPlanPhaseCheckFunc(phases ...operatorsv1alpha1.InstallPlanPhase) checkInstallPlanFunc {
	var lastPhase operatorsv1alpha1.InstallPlanPhase
	lastTime := time.Now()
	return func(fip *operatorsv1alpha1.InstallPlan) bool {
		if fip.Status.Phase != lastPhase {
			ctx.Ctx().Logf("waiting %s for installplan %s/%s to be phases %v, in phase %s", time.Since(lastTime), fip.Namespace, fip.Name, phases, fip.Status.Phase)
			lastPhase = fip.Status.Phase
			lastTime = time.Now()
		}
		satisfiesAny := false
		for _, phase := range phases {
			satisfiesAny = satisfiesAny || fip.Status.Phase == phase
		}
		return satisfiesAny
	}
}

func buildInstallPlanCleanupFunc(crc versioned.Interface, namespace string, installPlan *operatorsv1alpha1.InstallPlan) cleanupFunc {
	return func() {
		deleteOptions := &metav1.DeleteOptions{}
		for _, step := range installPlan.Status.Plan {
			if step.Resource.Kind == operatorsv1alpha1.ClusterServiceVersionKind {
				if err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(context.Background(), step.Resource.Name, *deleteOptions); err != nil {
					fmt.Println(err)
				}
			}
		}

		if err := crc.OperatorsV1alpha1().InstallPlans(namespace).Delete(context.Background(), installPlan.GetName(), *deleteOptions); err != nil {
			fmt.Println(err)
		}

		err := waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().InstallPlans(namespace).Get(context.Background(), installPlan.GetName(), metav1.GetOptions{})
			return err
		})

		if err != nil {
			fmt.Println(err)
		}
	}
}

func fetchInstallPlan(t GinkgoTInterface, c versioned.Interface, name string, namespace string, checkPhase checkInstallPlanFunc) (*operatorsv1alpha1.InstallPlan, error) {
	return fetchInstallPlanWithNamespace(t, c, name, namespace, checkPhase)
}

func fetchInstallPlanWithNamespace(t GinkgoTInterface, c versioned.Interface, name string, namespace string, checkPhase checkInstallPlanFunc) (*operatorsv1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *operatorsv1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil || fetchedInstallPlan == nil {
			return false, err
		}

		return checkPhase(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

// do not return an error if the installplan has not been created yet
func waitForInstallPlan(c versioned.Interface, name string, namespace string, checkPhase checkInstallPlanFunc) (*operatorsv1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *operatorsv1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}

		return checkPhase(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

func newNginxInstallStrategy(name string, permissions []operatorsv1alpha1.StrategyDeploymentPermissions, clusterPermissions []operatorsv1alpha1.StrategyDeploymentPermissions) operatorsv1alpha1.NamedInstallStrategy {
	By(`Create an nginx details deployment`)
	details := operatorsv1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
			{
				Name: name,
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "nginx"},
					},
					Replicas: &singleInstance,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "nginx"},
						},
						Spec: corev1.PodSpec{Containers: []corev1.Container{
							{
								Name:            genName("nginx"),
								Image:           *dummyImage,
								Ports:           []corev1.ContainerPort{{ContainerPort: 80}},
								ImagePullPolicy: corev1.PullIfNotPresent,
							},
						}},
					},
				},
			},
		},
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
	}
	namedStrategy := operatorsv1alpha1.NamedInstallStrategy{
		StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
		StrategySpec: details,
	}

	return namedStrategy
}

func newCRD(plural string) apiextensionsv1.CustomResourceDefinition {
	crd := apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: plural + ".cluster.com",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cluster.com",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:        "object",
							Description: "my crd schema",
						},
					},
				},
			},
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: plural,
				Kind:     plural,
				ListKind: plural + "list",
			},
			Scope: apiextensionsv1.NamespaceScoped,
		},
	}

	return crd
}

func newCSV(name, namespace, replaces string, version semver.Version, owned []apiextensionsv1.CustomResourceDefinition, required []apiextensionsv1.CustomResourceDefinition, namedStrategy *operatorsv1alpha1.NamedInstallStrategy) operatorsv1alpha1.ClusterServiceVersion {
	csvType = metav1.TypeMeta{
		Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
		APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
	}

	By(`set a simple default strategy if none given`)
	var strategy operatorsv1alpha1.NamedInstallStrategy
	if namedStrategy == nil {
		strategy = newNginxInstallStrategy(genName("dep"), nil, nil)
	} else {
		strategy = *namedStrategy
	}

	csv := operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			Replaces:       replaces,
			Version:        opver.OperatorVersion{Version: version},
			MinKubeVersion: "0.0.0",
			InstallModes: []operatorsv1alpha1.InstallMode{
				{
					Type:      operatorsv1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: strategy,
			CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
				Owned:    nil,
				Required: nil,
			},
		},
	}

	By(`Populate owned and required`)
	for _, crd := range owned {
		crdVersion := "v1alpha1"
		for _, v := range crd.Spec.Versions {
			if v.Served && v.Storage {
				crdVersion = v.Name
				break
			}
		}
		desc := operatorsv1alpha1.CRDDescription{
			Name:        crd.GetName(),
			Version:     crdVersion,
			Kind:        crd.Spec.Names.Plural,
			DisplayName: crd.GetName(),
			Description: crd.GetName(),
		}
		csv.Spec.CustomResourceDefinitions.Owned = append(csv.Spec.CustomResourceDefinitions.Owned, desc)
	}

	for _, crd := range required {
		crdVersion := "v1alpha1"
		for _, v := range crd.Spec.Versions {
			if v.Served && v.Storage {
				crdVersion = v.Name
				break
			}
		}
		desc := operatorsv1alpha1.CRDDescription{
			Name:        crd.GetName(),
			Version:     crdVersion,
			Kind:        crd.Spec.Names.Plural,
			DisplayName: crd.GetName(),
			Description: crd.GetName(),
		}
		csv.Spec.CustomResourceDefinitions.Required = append(csv.Spec.CustomResourceDefinitions.Required, desc)
	}

	return csv
}

func newInstallPlanWithDummySteps(name, namespace string, phase operatorsv1alpha1.InstallPlanPhase) *operatorsv1alpha1.InstallPlan {
	return &operatorsv1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: operatorsv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{"foobar"},
			Approval:                   operatorsv1alpha1.ApprovalAutomatic,
			Approved:                   true,
		},
		Status: operatorsv1alpha1.InstallPlanStatus{
			CatalogSources: []string{"catalog"},
			Phase:          phase,
			Plan: []*operatorsv1alpha1.Step{
				{
					Resource: operatorsv1alpha1.StepResource{
						CatalogSource:          "catalog",
						CatalogSourceNamespace: namespace,
						Group:                  "",
						Version:                "v1",
						Kind:                   "Foo",
						Name:                   "bar",
					},
					Status: operatorsv1alpha1.StepStatusUnknown,
				},
			},
		},
	}
}

func hasCondition(ip *operatorsv1alpha1.InstallPlan, expectedCondition operatorsv1alpha1.InstallPlanCondition) bool {
	for _, cond := range ip.Status.Conditions {
		if cond.Type == expectedCondition.Type && cond.Message == expectedCondition.Message && cond.Status == expectedCondition.Status {
			return true
		}
	}
	return false
}
