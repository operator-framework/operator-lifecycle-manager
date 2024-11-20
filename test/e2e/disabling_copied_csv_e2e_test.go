package e2e

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	apitypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	olmDeploymentName                       = "olm-operator"
	protectedCopiedCSVNamespacesRuntimeFlag = "--protectedCopiedCSVNamespaces"
)

var _ = Describe("Disabling copied CSVs", Label("DisablingCopiedCSVs"), func() {
	var (
		generatedNamespace              corev1.Namespace
		csv                             operatorsv1alpha1.ClusterServiceVersion
		nonTerminatingNamespaceSelector = fields.ParseSelectorOrDie("status.phase!=Terminating")
		protectedCopiedCSVNamespaces    = map[string]struct{}{}
	)

	BeforeEach(func() {
		nsname := genName("disabling-copied-csv-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", nsname),
				Namespace: nsname,
			},
		}
		generatedNamespace = SetupGeneratedTestNamespaceWithOperatorGroup(nsname, og)

		csv = operatorsv1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("csv-toggle-test-"),
				Namespace: nsname,
			},
			Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
				InstallStrategy: newNginxInstallStrategy(genName("csv-toggle-test-"), nil, nil),
				InstallModes: []operatorsv1alpha1.InstallMode{
					{
						Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
						Supported: true,
					},
				},
			},
		}
		err := ctx.Ctx().Client().Create(context.Background(), &csv)
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		Eventually(func() error {
			return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), &csv))
		}).Should(Succeed())
		TeardownNamespace(generatedNamespace.GetName())
	})

	When("an operator is installed in AllNamespace mode", func() {
		It("should have Copied CSVs in all other namespaces", func() {
			Eventually(func() error {
				requirement, err := k8slabels.NewRequirement(operatorsv1alpha1.CopiedLabelKey, selection.Equals, []string{csv.GetNamespace()})
				if err != nil {
					return err
				}

				var copiedCSVs operatorsv1alpha1.ClusterServiceVersionList
				err = ctx.Ctx().Client().List(context.TODO(), &copiedCSVs, &client.ListOptions{
					LabelSelector: k8slabels.NewSelector().Add(*requirement),
				})
				if err != nil {
					return err
				}

				var namespaces corev1.NamespaceList
				if err := ctx.Ctx().Client().List(context.TODO(), &namespaces, &client.ListOptions{
					FieldSelector: nonTerminatingNamespaceSelector,
				}); err != nil {
					return err
				}

				if len(namespaces.Items)-1 != len(copiedCSVs.Items) {
					return fmt.Errorf("%d copied CSVs found, expected %d", len(copiedCSVs.Items), len(namespaces.Items)-1)
				}

				return nil
			}).Should(Succeed())
		})
	})

	When("Copied CSVs are disabled", func() {
		BeforeEach(func() {
			Eventually(func() error {
				var olmConfig operatorsv1.OLMConfig
				if err := ctx.Ctx().Client().Get(context.TODO(), apitypes.NamespacedName{Name: "cluster"}, &olmConfig); err != nil {
					ctx.Ctx().Logf("Error getting olmConfig %v", err)
					return err
				}

				By(`Exit early if copied CSVs are disabled.`)
				if !olmConfig.CopiedCSVsAreEnabled() {
					return nil
				}

				olmConfig.Spec = operatorsv1.OLMConfigSpec{
					Features: &operatorsv1.Features{
						DisableCopiedCSVs: getPointer(true),
					},
				}

				if err := ctx.Ctx().Client().Update(context.TODO(), &olmConfig); err != nil {
					ctx.Ctx().Logf("Error setting olmConfig %v", err)
					return err
				}

				return nil
			}).Should(Succeed())

			Eventually(func() error {
				return setProtectedCopiedCSVNamespaces(protectedCopiedCSVNamespaces)
			}).Should(Succeed())
		})

		It("should not have any copied CSVs", func() {
			Eventually(func() error {
				requirement, err := k8slabels.NewRequirement(operatorsv1alpha1.CopiedLabelKey, selection.Equals, []string{csv.GetNamespace()})
				if err != nil {
					return err
				}

				var copiedCSVs operatorsv1alpha1.ClusterServiceVersionList
				err = ctx.Ctx().Client().List(context.TODO(), &copiedCSVs, &client.ListOptions{
					LabelSelector: k8slabels.NewSelector().Add(*requirement),
				})
				if err != nil {
					return err
				}

				if numCSVs := len(copiedCSVs.Items); numCSVs != len(protectedCopiedCSVNamespaces) {
					return fmt.Errorf("Found %d copied CSVs, should be %d", numCSVs, len(protectedCopiedCSVNamespaces))
				}

				for _, csv := range copiedCSVs.Items {
					if _, ok := protectedCopiedCSVNamespaces[csv.GetNamespace()]; !ok {
						return fmt.Errorf("copied CSV %s/%s should not exist in the given namespace", csv.GetNamespace(), csv.GetName())
					}
				}
				return nil
			}).Should(Succeed())
		})

		It("should be reflected in the olmConfig.Status.Condition array that the expected number of copied CSVs exist", func() {
			Eventually(func() error {
				var olmConfig operatorsv1.OLMConfig
				if err := ctx.Ctx().Client().Get(context.TODO(), apitypes.NamespacedName{Name: "cluster"}, &olmConfig); err != nil {
					return err
				}

				foundCondition := meta.FindStatusCondition(olmConfig.Status.Conditions, operatorsv1.DisabledCopiedCSVsConditionType)
				if foundCondition == nil {
					return fmt.Errorf("%s condition not found", operatorsv1.DisabledCopiedCSVsConditionType)
				}

				expectedCondition := metav1.Condition{
					Reason:  "CopiedCSVsDisabled",
					Message: "Copied CSVs are disabled and no unexpected copied CSVs were found for operators installed in AllNamespace mode",
					Status:  metav1.ConditionTrue,
				}

				if foundCondition.Reason != expectedCondition.Reason ||
					foundCondition.Message != expectedCondition.Message ||
					foundCondition.Status != expectedCondition.Status {
					return fmt.Errorf("condition does not have expected reason, message, and status. Expected %v, got %v", expectedCondition, foundCondition)
				}

				return nil
			}).Should(Succeed())
		})
	})

	When("Copied CSVs are toggled back on", func() {

		BeforeEach(func() {
			Eventually(func() error {
				var olmConfig operatorsv1.OLMConfig
				if err := ctx.Ctx().Client().Get(context.TODO(), apitypes.NamespacedName{Name: "cluster"}, &olmConfig); err != nil {
					return err
				}

				By(`Exit early if copied CSVs are enabled.`)
				if olmConfig.CopiedCSVsAreEnabled() {
					return nil
				}

				olmConfig.Spec = operatorsv1.OLMConfigSpec{
					Features: &operatorsv1.Features{
						DisableCopiedCSVs: getPointer(false),
					},
				}

				if err := ctx.Ctx().Client().Update(context.TODO(), &olmConfig); err != nil {
					return err
				}

				return nil
			}).Should(Succeed())
		})

		It("should have copied CSVs in all other Namespaces", func() {
			Eventually(func() error {
				By(`find copied csvs...`)
				requirement, err := k8slabels.NewRequirement(operatorsv1alpha1.CopiedLabelKey, selection.Equals, []string{csv.GetNamespace()})
				if err != nil {
					return err
				}

				var copiedCSVs operatorsv1alpha1.ClusterServiceVersionList
				err = ctx.Ctx().Client().List(context.TODO(), &copiedCSVs, &client.ListOptions{
					LabelSelector: k8slabels.NewSelector().Add(*requirement),
				})
				if err != nil {
					return err
				}

				var namespaces corev1.NamespaceList
				if err := ctx.Ctx().Client().List(context.TODO(), &namespaces, &client.ListOptions{FieldSelector: nonTerminatingNamespaceSelector}); err != nil {
					return err
				}

				if len(namespaces.Items)-1 != len(copiedCSVs.Items) {
					return fmt.Errorf("%d copied CSVs found, expected %d", len(copiedCSVs.Items), len(namespaces.Items)-1)
				}

				return nil
			}).Should(Succeed())
		})

		It("should be reflected in the olmConfig.Status.Condition array that the expected number of copied CSVs exist", func() {
			Eventually(func() error {
				var olmConfig operatorsv1.OLMConfig
				if err := ctx.Ctx().Client().Get(context.TODO(), apitypes.NamespacedName{Name: "cluster"}, &olmConfig); err != nil {
					return err
				}
				foundCondition := meta.FindStatusCondition(olmConfig.Status.Conditions, operatorsv1.DisabledCopiedCSVsConditionType)
				if foundCondition == nil {
					return fmt.Errorf("%s condition not found", operatorsv1.DisabledCopiedCSVsConditionType)
				}

				expectedCondition := metav1.Condition{
					Reason:  "CopiedCSVsEnabled",
					Message: "Copied CSVs are enabled and present across the cluster",
					Status:  metav1.ConditionFalse,
				}

				if foundCondition.Reason != expectedCondition.Reason ||
					foundCondition.Message != expectedCondition.Message ||
					foundCondition.Status != expectedCondition.Status {
					return fmt.Errorf("condition does not have expected reason, message, and status. Expected %v, got %v", expectedCondition, foundCondition)
				}

				return nil
			}).Should(Succeed())
		})
	})
})

func setProtectedCopiedCSVNamespaces(protectedCopiedCSVNamespaces map[string]struct{}) error {
	var olmDeployment appsv1.Deployment
	if err := ctx.Ctx().Client().Get(context.TODO(), apitypes.NamespacedName{Name: olmDeploymentName, Namespace: operatorNamespace}, &olmDeployment); err != nil {
		return err
	}

	if protectedNamespaceArgument := getRuntimeFlagValue(&olmDeployment, olmDeploymentName, protectedCopiedCSVNamespacesRuntimeFlag); protectedNamespaceArgument != "" {
		for _, namespace := range strings.Split(protectedNamespaceArgument, ",") {
			protectedCopiedCSVNamespaces[namespace] = struct{}{}
		}
	}

	return nil
}

func getRuntimeFlagValue(deployment *appsv1.Deployment, containerName string, runtimeFlag string) string {
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == containerName {
			for i := range container.Args {
				if container.Args[i] == runtimeFlag && len(container.Args) > i+1 {
					return container.Args[i+1]
				}
			}
		}
	}
	return ""
}
