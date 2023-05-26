package e2e

import (
	"context"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

const (
	failForwardTestDataBaseDir = "fail-forward/base/"
)

var _ = Describe("Fail Forward Upgrades", func() {

	var (
		ns       corev1.Namespace
		crclient versioned.Interface
		c        client.Client
		ogName   string
	)

	BeforeEach(func() {
		crclient = newCRClient()
		c = ctx.Ctx().Client()

		By("creating the testing namespace with an OG that enabled fail forward behavior")
		namespaceName := genName("ff-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", namespaceName),
				Namespace: namespaceName,
			},
			Spec: operatorsv1.OperatorGroupSpec{
				UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
			},
		}
		ns = SetupGeneratedTestNamespaceWithOperatorGroup(namespaceName, og)
		ogName = og.GetName()
	})

	AfterEach(func() {
		By("deleting the testing namespace")
		TeardownNamespace(ns.GetName())
	})

	When("an InstallPlan is reporting a failed state", func() {

		var (
			magicCatalog           *MagicCatalog
			catalogSourceName      string
			subscription           *operatorsv1alpha1.Subscription
			originalInstallPlanRef *corev1.ObjectReference
			failedInstallPlanRef   *corev1.ObjectReference
		)

		BeforeEach(func() {
			By("creating a service account with no permission")
			saNameWithNoPerms := genName("scoped-sa-")
			newServiceAccount(ctx.Ctx().KubeClient(), ns.GetName(), saNameWithNoPerms)

			By("deploying the testing catalog")
			provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, failForwardTestDataBaseDir, "example-operator.v0.1.0.yaml"))
			Expect(err).To(BeNil())
			catalogSourceName = genName("mc-ip-failed-")
			magicCatalog = NewMagicCatalog(c, ns.GetName(), catalogSourceName, provider)
			Expect(magicCatalog.DeployCatalog(context.Background())).To(BeNil())

			By("creating the testing subscription")
			subscription = &operatorsv1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-sub", catalogSourceName),
					Namespace: ns.GetName(),
				},
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          catalogSourceName,
					CatalogSourceNamespace: ns.GetName(),
					Channel:                "stable",
					Package:                "packageA",
				},
			}
			Expect(c.Create(context.Background(), subscription)).To(BeNil())

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanChecker)
			Expect(err).Should(BeNil())

			originalInstallPlanRef = subscription.Status.InstallPlanRef

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crclient, subscription.Status.CurrentCSV, ns.GetName(), buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())

			By("updating the operator group to use the service account without required permissions to simulate InstallPlan failure")
			Eventually(operatorGroupServiceAccountNameSetter(crclient, ns.GetName(), ogName, saNameWithNoPerms)).Should(Succeed())

			By("updating the catalog with v0.2.0 bundle image")
			brokenProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, failForwardTestDataBaseDir, "example-operator.v0.2.0.yaml"))
			Expect(err).To(BeNil())
			err = magicCatalog.UpdateCatalog(context.Background(), brokenProvider)
			Expect(err).To(BeNil())

			By("verifying the subscription is referencing a new installplan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the bad InstallPlan to report a failed installation state")
			failedInstallPlanRef = subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, failedInstallPlanRef.Name, failedInstallPlanRef.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed))
			Expect(err).To(BeNil())

			By("updating the operator group remove service account without permissions")
			Eventually(operatorGroupServiceAccountNameSetter(crclient, ns.GetName(), ogName, "")).Should(Succeed())
		})
		AfterEach(func() {
			By("removing the testing catalog resources")
			Expect(magicCatalog.UndeployCatalog(context.Background())).To(BeNil())
		})
		It("eventually reports a successful state when multiple bad versions are rolled forward", func() {
			By("patching the OperatorGroup to reduce the bundle unpacking timeout")
			addBundleUnpackTimeoutOGAnnotation(context.Background(), c, types.NamespacedName{Name: ogName, Namespace: ns.GetName()}, "1s")

			By("patching the catalog with a bad bundle version")
			badProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/multiple-bad-versions", "example-operator.v0.2.1-non-existent-tag.yaml"))
			Expect(err).To(BeNil())
			err = magicCatalog.UpdateCatalog(context.Background(), badProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to maintain the example-operator.v0.2.0 status.currentCSV")
			Consistently(subscriptionCurrentCSVGetter(crclient, subscription.GetNamespace(), subscription.GetName())).Should(Equal("example-operator.v0.2.0"))

			By("patching the OperatorGroup to increase the bundle unpacking timeout")
			addBundleUnpackTimeoutOGAnnotation(context.Background(), c, types.NamespacedName{Name: ogName, Namespace: ns.GetName()}, "5m")

			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/multiple-bad-versions", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())
			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())

			By("verifying the subscription is referencing a new InstallPlan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the fixed v0.3.0 InstallPlan to report a successful state")
			ref := subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, ref.Name, ref.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			Expect(err).To(BeNil())
		})

		It("eventually reports a successful state when using skip ranges", func() {
			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/skip-range", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())
			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())

			By("verifying the subscription is referencing a new InstallPlan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the fixed v0.3.0 InstallPlan to report a successful state")
			ref := subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, ref.Name, ref.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			Expect(err).To(BeNil())
		})
		It("eventually reports a successful state when using skips", func() {
			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/skips", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())
			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())

			By("verifying the subscription is referencing a new InstallPlan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the fixed v0.3.0 InstallPlan to report a successful state")
			ref := subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, ref.Name, ref.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			Expect(err).To(BeNil())
		})
		It("eventually reports a failed state when using replaces", func() {
			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/replaces", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())
			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to maintain the example-operator.v0.2.0 status.currentCSV")
			Consistently(subscriptionCurrentCSVGetter(crclient, subscription.GetNamespace(), subscription.GetName())).Should(Equal("example-operator.v0.2.0"))

			By("verifying the subscription is referencing the same InstallPlan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanChecker)
			Expect(err).Should(BeNil())
			Expect(subscription.Status.InstallPlanRef.Name).To(Equal(failedInstallPlanRef.Name))
		})
	})
	When("a CSV resource is in a failed state", func() {

		var (
			magicCatalog      *MagicCatalog
			catalogSourceName string
			subscription      *operatorsv1alpha1.Subscription
		)

		BeforeEach(func() {
			By("deploying the testing catalog")
			provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, failForwardTestDataBaseDir, "example-operator.v0.1.0.yaml"))
			Expect(err).To(BeNil())
			catalogSourceName = genName("mc-csv-failed-")
			magicCatalog = NewMagicCatalog(c, ns.GetName(), catalogSourceName, provider)
			Expect(magicCatalog.DeployCatalog(context.Background())).To(BeNil())

			By("creating the testing subscription")
			subscription = &operatorsv1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-sub", catalogSourceName),
					Namespace: ns.GetName(),
				},
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          catalogSourceName,
					CatalogSourceNamespace: ns.GetName(),
					Channel:                "stable",
					Package:                "packageA",
				},
			}
			Expect(c.Create(context.Background(), subscription)).To(BeNil())

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanChecker)
			Expect(err).Should(BeNil())

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crclient, subscription.Status.CurrentCSV, ns.GetName(), buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())

			By("updating the catalog with a broken v0.2.0 csv")
			brokenProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, failForwardTestDataBaseDir, "example-operator.v0.2.0-invalid-csv.yaml"))
			Expect(err).To(BeNil())

			err = magicCatalog.UpdateCatalog(context.Background(), brokenProvider)
			Expect(err).To(BeNil())

			badCSV := "example-operator.v0.2.0"
			By("verifying the subscription has installed the current csv")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV(badCSV))
			Expect(err).Should(BeNil())

			By("waiting for the bad CSV to report a failed state")
			_, err = fetchCSV(crclient, subscription.Status.CurrentCSV, ns.GetName(), csvFailedChecker)
			Expect(err).To(BeNil())

		})

		AfterEach(func() {
			By("removing the testing catalog resources")
			Expect(magicCatalog.UndeployCatalog(context.Background())).To(BeNil())
		})

		It("eventually reports a successful state when using skip ranges", func() {
			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/skip-range", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())

			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())
		})

		It("eventually reports a successful state when using skips", func() {
			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/skips", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())

			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())
		})

		It("eventually reports a successful state when using replaces", func() {
			By("patching the catalog with a fixed version")
			fixedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "fail-forward/replaces", "example-operator.v0.3.0.yaml"))
			Expect(err).To(BeNil())

			err = magicCatalog.UpdateCatalog(context.Background(), fixedProvider)
			Expect(err).To(BeNil())

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())
		})
	})
})
