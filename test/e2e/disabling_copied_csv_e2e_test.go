package e2e

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	apitypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Disabling copied CSVs", func() {
	var (
		ns                              corev1.Namespace
		csv                             operatorsv1alpha1.ClusterServiceVersion
		nonTerminatingNamespaceSelector = fields.ParseSelectorOrDie("status.phase!=Terminating")
	)

	BeforeEach(func() {
		nsname := genName("csv-toggle-test-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", nsname),
				Namespace: nsname,
			},
		}
		ns = SetupGeneratedTestNamespaceWithOperatorGroup(nsname, og)

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
		TeardownNamespace(ns.GetName())
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

				// Exit early if copied CSVs are disabled.
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

				if numCSVs := len(copiedCSVs.Items); numCSVs != 0 {
					return fmt.Errorf("Found %d copied CSVs, should be 0", numCSVs)
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
					Reason:  "NoCopiedCSVsFound",
					Message: "Copied CSVs are disabled and none were found for operators installed in AllNamespace mode",
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

				// Exit early if copied CSVs are enabled.
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
				// find copied csvs...
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
