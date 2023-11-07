package e2e

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	operatorsscheme "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/scheme"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

//go:embed testdata/fail-forward
var testData embed.FS

func loadData(versions ...string) ([]registry.PackageManifest, []operatorsv1alpha1.ClusterServiceVersion, error) {
	var packageManifests []registry.PackageManifest
	var clusterServiceVersions []operatorsv1alpha1.ClusterServiceVersion
	for _, version := range versions {
		packageManifest, err := loadPackageManifest(version)
		if err != nil {
			return nil, nil, err
		}
		packageManifests = append(packageManifests, *packageManifest)

		clusterServiceVersion, err := loadClusterServiceVersion(version)
		if err != nil {
			return nil, nil, err
		}
		clusterServiceVersions = append(clusterServiceVersions, *clusterServiceVersion)
	}

	return packageManifests, clusterServiceVersions, nil
}

func loadPackageManifest(version string) (*registry.PackageManifest, error) {
	raw, err := testData.ReadFile(filepath.Join("testdata", "fail-forward", version, "packagemanifest.yaml"))
	if err != nil {
		return nil, err
	}

	var packageManifest registry.PackageManifest
	if err := yaml.Unmarshal(raw, &packageManifest); err != nil {
		return nil, err
	}

	return &packageManifest, nil
}

func loadClusterServiceVersion(version string) (*operatorsv1alpha1.ClusterServiceVersion, error) {
	raw, err := testData.ReadFile(filepath.Join("testdata", "fail-forward", version, "clusterserviceversion.yaml"))
	if err != nil {
		return nil, err
	}

	var clusterServiceVersion operatorsv1alpha1.ClusterServiceVersion
	gvk := operatorsv1alpha1.SchemeGroupVersion.WithKind("ClusterServiceVersion")
	obj, gotGvk, err := operatorsscheme.Codecs.UniversalDeserializer().Decode(raw, &gvk, &clusterServiceVersion)
	if err != nil {
		return nil, err
	}
	if gotGvk.String() != gvk.String() {
		return nil, fmt.Errorf("expcted to unmarshal to %v, got %v", gvk.String(), gotGvk.String())
	}

	return obj.(*operatorsv1alpha1.ClusterServiceVersion), nil
}

func deployCatalogSource(namespace, name string, packages ...string) (func(), error) {
	pms, csvs, err := loadData(packages...)
	if err != nil {
		return nil, fmt.Errorf("failed to load data: %w", err)
	}
	_, cleanup := createInternalCatalogSource(
		ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(),
		name, namespace,
		pms, []apiextensionsv1.CustomResourceDefinition{}, csvs,
	)

	_, err = fetchCatalogSourceOnStatus(ctx.Ctx().OperatorClient(), name, namespace, catalogSourceRegistryPodSynced())
	if err != nil {
		err = fmt.Errorf("failed to ensure registry pod synced: %w", err)
	}
	return cleanup, err
}

func updateCatalogSource(namespace, name string, packages ...string) (func(), error) {
	By("removing the existing catalog source")
	if err := ctx.Ctx().OperatorClient().OperatorsV1alpha1().CatalogSources(namespace).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		return nil, err
	}

	By("removing the previous catalog source pod(s)")
	Eventually(func() (bool, error) {
		listOpts := metav1.ListOptions{
			LabelSelector: "olm.catalogSource=" + name,
			FieldSelector: "status.phase=Running",
		}
		fetched, err := ctx.Ctx().KubeClient().KubernetesInterface().CoreV1().Pods(namespace).List(context.Background(), listOpts)
		if err != nil {
			return false, err
		}
		if len(fetched.Items) == 0 {
			return true, nil
		}
		ctx.Ctx().Logf("waiting for the catalog source %s pod to be deleted...", fetched.Items[0].GetName())
		return false, nil
	}).Should(BeTrue())

	By("updating the catalog with a new bundle images")
	return deployCatalogSource(namespace, name, packages...)
}

var _ = Describe("Fail Forward Upgrades", func() {

	var (
		generatedNamespace corev1.Namespace
		crclient           versioned.Interface
		c                  client.Client
	)

	BeforeEach(func() {
		crclient = newCRClient()
		c = ctx.Ctx().Client()

		By("creating the testing namespace with an OG that enabled fail forward behavior")
		namespaceName := genName("fail-forward-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", namespaceName),
				Namespace: namespaceName,
			},
			Spec: operatorsv1.OperatorGroupSpec{
				UpgradeStrategy: operatorsv1.UpgradeStrategyUnsafeFailForward,
			},
		}
		generatedNamespace = SetupGeneratedTestNamespaceWithOperatorGroup(namespaceName, og)
	})

	AfterEach(func() {
		By("deleting the testing namespace")
		TeardownNamespace(generatedNamespace.GetName())
	})

	When("an InstallPlan is reporting a failed state", func() {

		var (
			catalogSourceName      string
			subscription           *operatorsv1alpha1.Subscription
			originalInstallPlanRef *corev1.ObjectReference
			failedInstallPlanRef   *corev1.ObjectReference
			cleanups               []func()
		)

		BeforeEach(func() {
			By("deploying the testing catalog")
			catalogSourceName = genName("mc-ip-failed-")
			cleanup, deployError := deployCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("creating the testing subscription")
			subscription = &operatorsv1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-sub", catalogSourceName),
					Namespace: generatedNamespace.GetName(),
				},
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          catalogSourceName,
					CatalogSourceNamespace: generatedNamespace.GetName(),
					Channel:                "stable",
					Package:                "packageA",
				},
			}
			Expect(c.Create(context.Background(), subscription)).To(BeNil())

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanChecker())
			Expect(err).Should(BeNil())

			originalInstallPlanRef = subscription.Status.InstallPlanRef

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crclient, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())

			By("updating the catalog with a v0.2.0 bundle that has an invalid CSV")
			cleanup, deployError = updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-csv")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("verifying the subscription is referencing a new installplan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the bad InstallPlan to report a failed installation state")
			failedInstallPlanRef = subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, failedInstallPlanRef.Name, failedInstallPlanRef.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed))
			Expect(err).To(BeNil())
		})
		AfterEach(func() {
			By("removing the testing catalog resources")
			for _, cleanup := range cleanups {
				cleanup()
			}
		})
		It("eventually reports a successful state when multiple bad versions are rolled forward", func() {
			By("updating the catalog with a v0.2.1 bundle that has an invalid CSV")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-csv", "v0.2.1-invalid-csv")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.2.1&invalid status.currentCSV")
			_, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.2.1&invalid"))
			Expect(err).Should(BeNil())

			By("verifying the subscription is referencing a new InstallPlan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the v0.2.1 InstallPlan to report a failed state")
			ref := subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, ref.Name, ref.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed))
			Expect(err).To(BeNil())

			By("updating the catalog with a fixed v0.3.0 bundle")
			cleanup, deployError = updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-csv", "v0.2.1-invalid-csv", "v0.3.0-skips")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())

			By("verifying the subscription is referencing a new InstallPlan")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanDifferentChecker(originalInstallPlanRef.Name))
			Expect(err).Should(BeNil())

			By("waiting for the fixed v0.3.0 InstallPlan to report a successful state")
			ref = subscription.Status.InstallPlanRef
			_, err = fetchInstallPlan(GinkgoT(), crclient, ref.Name, ref.Namespace, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			Expect(err).To(BeNil())
		})

		It("eventually reports a successful state when using skip ranges", func() {
			By("updating the catalog with a fixed v0.3.0 bundle")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-csv", "v0.3.0-skip-range")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
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
			By("updating the catalog with a fixed v0.3.0 bundle")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-csv", "v0.3.0-skips")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
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
			By("updating the catalog with a fixed v0.3.0 bundle")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-csv", "v0.3.0-replaces-invalid-csv")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to maintain the example-operator.v0.2.0&invalid status.currentCSV")
			Consistently(subscriptionCurrentCSVGetter(crclient, subscription.GetNamespace(), subscription.GetName())).Should(Equal("example-operator.v0.2.0&invalid"))

			By("verifying the subscription is referencing the same InstallPlan")
			subscription, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanChecker())
			Expect(err).Should(BeNil())
			Expect(subscription.Status.InstallPlanRef.Name).To(Equal(failedInstallPlanRef.Name))
		})
	})
	When("a CSV resource is in a failed state", func() {

		var (
			catalogSourceName string
			subscription      *operatorsv1alpha1.Subscription
			cleanups          []func()
		)

		BeforeEach(func() {
			By("deploying the testing catalog")
			catalogSourceName = genName("mc-csv-failed-")
			cleanup, deployError := deployCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("creating the testing subscription")
			subscription = &operatorsv1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-sub", catalogSourceName),
					Namespace: generatedNamespace.GetName(),
				},
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          catalogSourceName,
					CatalogSourceNamespace: generatedNamespace.GetName(),
					Channel:                "stable",
					Package:                "packageA",
				},
			}
			Expect(c.Create(context.Background(), subscription)).To(BeNil())

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasInstallPlanChecker())
			Expect(err).Should(BeNil())

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crclient, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())

			By("updating the catalog with a broken v0.2.0 csv")
			cleanup, deployError = updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-deployment")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			badCSV := "example-operator.v0.2.0"
			By("verifying the subscription has installed the current csv")
			subscription, err = fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV(badCSV))
			Expect(err).Should(BeNil())

			By("waiting for the bad CSV to report a failed state")
			_, err = fetchCSV(crclient, generatedNamespace.GetName(), subscription.Status.CurrentCSV, csvFailedChecker)
			Expect(err).To(BeNil())

		})

		AfterEach(func() {
			By("removing the testing catalog resources")
			for _, cleanup := range cleanups {
				cleanup()
			}
		})

		It("[FLAKE] eventually reports a successful state when using skip ranges", func() {
			By("patching the catalog with a fixed version")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-deployment", "v0.3.0-skip-range")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())
		})

		It("eventually reports a successful state when using skips", func() {
			By("patching the catalog with a fixed version")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-deployment", "v0.3.0-skips")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())
		})

		It("eventually reports a successful state when using replaces", func() {
			By("patching the catalog with a fixed version")
			cleanup, deployError := updateCatalogSource(generatedNamespace.GetName(), catalogSourceName, "v0.1.0", "v0.2.0-invalid-deployment", "v0.3.0-replaces-invalid-deployment")
			Expect(deployError).To(BeNil())
			if cleanup != nil {
				cleanups = append(cleanups, cleanup)
			}

			By("waiting for the subscription to have the example-operator.v0.3.0 status.currentCSV")
			_, err := fetchSubscription(crclient, subscription.GetNamespace(), subscription.GetName(), subscriptionHasCurrentCSV("example-operator.v0.3.0"))
			Expect(err).Should(BeNil())
		})
	})
})
