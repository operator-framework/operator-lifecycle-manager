package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/operator-framework/api/pkg/lib/version"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/comparison"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	registryapi "github.com/operator-framework/operator-registry/pkg/api"
)

func Step(level int, text string, callbacks ...func()) {
	By(strings.Repeat(" ", level*2)+text, callbacks...)
}

const (
	timeout  = time.Second * 20
	interval = time.Millisecond * 100
)

var _ = By

var _ = Describe("Subscription", func() {
	AfterEach(func() {
		TearDown(testNamespace)
	})

	When("an entry in the middle of a channel does not provide a required GVK", func() {
		var (
			teardown func()
		)

		BeforeEach(func() {
			teardown = func() {}

			packages := []registry.PackageManifest{
				{
					PackageName: "dependency",
					Channels: []registry.PackageChannel{
						{Name: "channel-dependency", CurrentCSVName: "csv-dependency-3"},
					},
					DefaultChannelName: "channel-dependency",
				},
				{
					PackageName: "root",
					Channels: []registry.PackageChannel{
						{Name: "channel-root", CurrentCSVName: "csv-root"},
					},
					DefaultChannelName: "channel-root",
				},
			}

			crds := []apiextensions.CustomResourceDefinition{newCRD(genName("crd-"))}
			csvs := []operatorsv1alpha1.ClusterServiceVersion{
				newCSV("csv-dependency-1", testNamespace, "", semver.MustParse("1.0.0"), crds, nil, nil),
				newCSV("csv-dependency-2", testNamespace, "csv-dependency-1", semver.MustParse("2.0.0"), nil, nil, nil),
				newCSV("csv-dependency-3", testNamespace, "csv-dependency-2", semver.MustParse("3.0.0"), crds, nil, nil),
				newCSV("csv-root", testNamespace, "", semver.MustParse("1.0.0"), nil, crds, nil),
			}

			_, teardown = createInternalCatalogSource(ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(), "test-catalog", testNamespace, packages, crds, csvs)

			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), testNamespace, "test-subscription", "test-catalog", "root", "channel-root", "", operatorsv1alpha1.ApprovalAutomatic)
		})

		AfterEach(func() {
			teardown()
		})

		It("should create a Subscription for the latest entry providing the required GVK", func() {
			Eventually(func() ([]operatorsv1alpha1.Subscription, error) {
				var list operatorsv1alpha1.SubscriptionList
				if err := ctx.Ctx().Client().List(context.TODO(), &list); err != nil {
					return nil, err
				}
				return list.Items, nil
			}).Should(ContainElement(WithTransform(
				func(in operatorsv1alpha1.Subscription) operatorsv1alpha1.SubscriptionSpec {
					return operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          in.Spec.CatalogSource,
						CatalogSourceNamespace: in.Spec.CatalogSourceNamespace,
						Package:                in.Spec.Package,
						Channel:                in.Spec.Channel,
						StartingCSV:            in.Spec.StartingCSV,
					}
				},
				Equal(operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          "test-catalog",
					CatalogSourceNamespace: testNamespace,
					Package:                "dependency",
					Channel:                "channel-dependency",
					StartingCSV:            "csv-dependency-3",
				}),
			)))
		})
	})

	//   I. Creating a new subscription
	//      A. If package is not installed, creating a subscription should install latest version
	It("creation if not installed", func() {

		c := newKubeClient()
		crc := newCRClient()
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()
		require.NoError(GinkgoT(), initCatalog(GinkgoT(), c, crc))

		cleanup, _ := createSubscription(GinkgoT(), crc, testNamespace, testSubscriptionName, testPackageName, betaChannel, operatorsv1alpha1.ApprovalAutomatic)
		defer cleanup()

		subscription, err := fetchSubscription(crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		csv, err := fetchCSV(crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)

		// Check for the olm.package property as a proxy for
		// verifying that the annotation value is reasonable.
		Expect(
			projection.PropertyListFromPropertiesAnnotation(csv.GetAnnotations()["operatorframework.io/properties"]),
		).To(ContainElement(
			&registryapi.Property{Type: "olm.package", Value: `{"packageName":"myapp","version":"0.1.1"}`},
		))
	})

	//   I. Creating a new subscription
	//      B. If package is already installed, creating a subscription should upgrade it to the latest
	//         version
	It("creation using existing CSV", func() {

		c := newKubeClient()
		crc := newCRClient()
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()
		require.NoError(GinkgoT(), initCatalog(GinkgoT(), c, crc))

		// Will be cleaned up by the upgrade process
		_, err := createCSV(c, crc, stableCSV, testNamespace, false, false)
		require.NoError(GinkgoT(), err)

		subscriptionCleanup, _ := createSubscription(GinkgoT(), crc, testNamespace, testSubscriptionName, testPackageName, alphaChannel, operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)
		_, err = fetchCSV(crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)
	})
	It("skip range", func() {

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		crd := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensions.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensions.NamespaceScoped,
			},
		}

		mainPackageName := genName("nginx-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		updatedPackageStable := fmt.Sprintf("%s-updated", mainPackageName)
		stableChannel := "stable"
		mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0-1556661347"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		updatedCSV := newCSV(updatedPackageStable, testNamespace, "", semver.MustParse("0.1.0-1556661832"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		updatedCSV.SetAnnotations(map[string]string{resolver.SkipPackageAnnotationKey: ">=0.1.0-1556661347 <0.1.0-1556661832"})

		c := newKubeClient()
		crc := newCRClient()
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		mainCatalogName := genName("mock-ocs-main-")

		// Create separate manifests for each CatalogSource
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}
		updatedManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: updatedPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create catalog source
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		// Attempt to get the catalog source before creating subscription
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Create a subscription
		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		// Wait for csv to install
		firstCSV, err := awaitCSV(crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Update catalog with a new csv in the channel with a skip range
		updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{updatedCSV}, updatedManifests)

		// Wait for csv to update
		finalCSV, err := awaitCSV(crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Ensure we set the replacement field based on the registry data
		require.Equal(GinkgoT(), firstCSV.GetName(), finalCSV.Spec.Replaces)
	})

	// If installPlanApproval is set to manual, the installplans created should be created with approval: manual
	It("creation manual approval", func() {

		c := newKubeClient()
		crc := newCRClient()
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()
		require.NoError(GinkgoT(), initCatalog(GinkgoT(), c, crc))

		subscriptionCleanup, _ := createSubscription(GinkgoT(), crc, testNamespace, "manual-subscription", testPackageName, stableChannel, operatorsv1alpha1.ApprovalManual)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, "manual-subscription", subscriptionStateUpgradePendingChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlan, err := fetchInstallPlan(GinkgoT(), crc, subscription.Status.Install.Name, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval))
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), installPlan)

		require.Equal(GinkgoT(), operatorsv1alpha1.ApprovalManual, installPlan.Spec.Approval)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseRequiresApproval, installPlan.Status.Phase)

		// Delete the current installplan
		err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Delete(context.Background(), installPlan.Name, metav1.DeleteOptions{})
		require.NoError(GinkgoT(), err)

		var ipName string
		Eventually(func() bool {
			fetched, err := crc.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), "manual-subscription", metav1.GetOptions{})
			if err != nil {
				return false
			}
			if fetched.Status.Install != nil {
				ipName = fetched.Status.Install.Name
				return fetched.Status.Install.Name != installPlan.Name
			}
			return false
		}, 5*time.Minute, 10*time.Second).Should(BeTrue())

		// Fetch new installplan
		newInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, ipName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval))
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), newInstallPlan)

		require.NotEqual(GinkgoT(), installPlan.Name, newInstallPlan.Name, "expected new installplan recreated")
		require.Equal(GinkgoT(), operatorsv1alpha1.ApprovalManual, newInstallPlan.Spec.Approval)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseRequiresApproval, newInstallPlan.Status.Phase)

		// Set the InstallPlan's approved to True
		Eventually(Apply(newInstallPlan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Spec.Approved = true
			return nil
		})).Should(Succeed())

		subscription, err = fetchSubscription(crc, testNamespace, "manual-subscription", subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		_, err = fetchCSV(crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)
	})

	It("with starting CSV", func() {

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"

		crd := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensions.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensions.NamespaceScoped,
			},
		}

		// Create CSV
		packageName := genName("nginx-")
		stableChannel := "stable"

		csvA := newCSV("nginx-a", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		csvB := newCSV("nginx-b", testNamespace, "nginx-a", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)

		// Create PackageManifests
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the CatalogSource
		c := newKubeClient()
		crc := newCRClient()
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanupCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), operatorsv1alpha1.ApprovalManual)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.Install.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval)
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, requiresApprovalChecker)
		require.NoError(GinkgoT(), err)

		// Ensure that only 1 installplan was created
		ips, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).List(context.Background(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), ips.Items, 1)

		// Ensure that csvA and its crd are found in the plan
		csvFound := false
		crdFound := false
		for _, s := range fetchedInstallPlan.Status.Plan {
			require.Equal(GinkgoT(), csvA.GetName(), s.Resolving, "unexpected resolution found")
			require.Equal(GinkgoT(), operatorsv1alpha1.StepStatusUnknown, s.Status, "status should be unknown")
			require.Equal(GinkgoT(), catalogSourceName, s.Resource.CatalogSource, "incorrect catalogsource on step resource")
			switch kind := s.Resource.Kind; kind {
			case operatorsv1alpha1.ClusterServiceVersionKind:
				require.Equal(GinkgoT(), csvA.GetName(), s.Resource.Name, "unexpected csv found")
				csvFound = true
			case "CustomResourceDefinition":
				require.Equal(GinkgoT(), crdName, s.Resource.Name, "unexpected crd found")
				crdFound = true
			default:
				GinkgoT().Fatalf("unexpected resource kind found in installplan: %s", kind)
			}
		}
		require.True(GinkgoT(), csvFound, "expected csv not found in installplan")
		require.True(GinkgoT(), crdFound, "expected crd not found in installplan")

		// Ensure that csvB is not found in the plan
		csvFound = false
		for _, s := range fetchedInstallPlan.Status.Plan {
			require.Equal(GinkgoT(), csvA.GetName(), s.Resolving, "unexpected resolution found")
			require.Equal(GinkgoT(), operatorsv1alpha1.StepStatusUnknown, s.Status, "status should be unknown")
			require.Equal(GinkgoT(), catalogSourceName, s.Resource.CatalogSource, "incorrect catalogsource on step resource")
			switch kind := s.Resource.Kind; kind {
			case operatorsv1alpha1.ClusterServiceVersionKind:
				if s.Resource.Name == csvB.GetName() {
					csvFound = true
				}
			}
		}
		require.False(GinkgoT(), csvFound, "expected csv not found in installplan")

		// Approve the installplan and wait for csvA to be installed
		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		_, err = awaitCSV(crc, testNamespace, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Wait for the subscription to begin upgrading to csvB
		subscription, err = fetchSubscription(crc, testNamespace, subscriptionName, subscriptionStateUpgradePendingChecker)
		require.NoError(GinkgoT(), err)
		require.NotEqual(GinkgoT(), fetchedInstallPlan.GetName(), subscription.Status.InstallPlanRef.Name, "expected new installplan for upgraded csv")

		upgradeInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, subscription.Status.InstallPlanRef.Name, requiresApprovalChecker)
		require.NoError(GinkgoT(), err)

		// Approve the upgrade installplan and wait for
		upgradeInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(context.Background(), upgradeInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		_, err = awaitCSV(crc, testNamespace, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Ensure that 2 installplans were created
		ips, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).List(context.Background(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), ips.Items, 2)
	})

	It("updates multiple intermediates", func() {

		crd := newCRD("ins")

		// Create CSV
		packageName := genName("nginx-")
		stableChannel := "stable"

		csvA := newCSV("nginx-a", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		csvB := newCSV("nginx-b", testNamespace, "nginx-a", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		csvC := newCSV("nginx-c", testNamespace, "nginx-b", semver.MustParse("0.3.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)

		// Create PackageManifests
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvA.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the CatalogSource with just one version
		c := newKubeClient()
		crc := newCRClient()
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA})
		defer cleanupCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), operatorsv1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		// Wait for csvA to be installed
		_, err = awaitCSV(crc, testNamespace, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Set up async watches that will fail the test if csvB doesn't get created in between csvA and csvC
		var wg sync.WaitGroup
		go func(t GinkgoTInterface) {
			defer GinkgoRecover()
			wg.Add(1)
			defer wg.Done()
			_, err := awaitCSV(crc, testNamespace, csvB.GetName(), csvReplacingChecker)
			require.NoError(GinkgoT(), err)
		}(GinkgoT())
		// Update the catalog to include multiple updates
		packages := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvC.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, catalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB, csvC}, packages)

		// wait for checks on intermediate csvs to succeed
		wg.Wait()

		// Wait for csvC to be installed
		_, err = awaitCSV(crc, testNamespace, csvC.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Should eventually GC the CSVs
		Eventually(func() bool {
			return csvExists(crc, csvA.Name)
		}).Should(BeFalse())

		Eventually(func() bool {
			return csvExists(crc, csvB.Name)
		}).Should(BeFalse())

		// TODO: check installplans, subscription status, etc
	})

	// TestSubscriptionUpdatesExistingInstallPlan ensures that an existing InstallPlan
	//  has the appropriate approval requirement from Subscription.
	It("updates existing install plan", func() {

		Skip("ToDo: This test was skipped before ginkgo conversion")

		// Create CSV
		packageName := genName("nginx-")
		stableChannel := "stable"

		csvA := newCSV("nginx-a", testNamespace, "", semver.MustParse("0.1.0"), nil, nil, nil)
		csvB := newCSV("nginx-b", testNamespace, "nginx-a", semver.MustParse("0.2.0"), nil, nil, nil)

		// Create PackageManifests
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the CatalogSource with just one version
		c := newKubeClient()
		crc := newCRClient()
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, testNamespace, manifests, nil, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanupCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Create a subscription to just get an InstallPlan for csvB
		subscriptionName := genName("sub-nginx-")
		createSubscriptionForCatalog(crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, csvB.GetName(), operatorsv1alpha1.ApprovalAutomatic)

		// Wait for csvB to be installed
		_, err = awaitCSV(crc, testNamespace, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, subscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))

		// Delete this subscription
		err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.Background(), *metav1.NewDeleteOptions(0), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		// Delete orphaned csvB
		require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.Background(), csvB.GetName(), metav1.DeleteOptions{}))

		// Create an InstallPlan for csvB
		ip := &operatorsv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "install-",
				Namespace:    testNamespace,
			},
			Spec: operatorsv1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{csvB.GetName()},
				Approval:                   operatorsv1alpha1.ApprovalAutomatic,
				Approved:                   false,
			},
		}
		ip2, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).Create(context.Background(), ip, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		ip2.Status = operatorsv1alpha1.InstallPlanStatus{
			Plan:           fetchedInstallPlan.Status.Plan,
			CatalogSources: []string{catalogSourceName},
		}

		_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).UpdateStatus(context.Background(), ip2, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		subscriptionName = genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), operatorsv1alpha1.ApprovalManual)
		defer cleanupSubscription()

		subscription, err = fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.Install.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval)
		fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, requiresApprovalChecker)
		require.NoError(GinkgoT(), err)

		// Approve the installplan and wait for csvA to be installed
		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		// Wait for csvA to be installed
		_, err = awaitCSV(crc, testNamespace, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Wait for the subscription to begin upgrading to csvB
		subscription, err = fetchSubscription(crc, testNamespace, subscriptionName, subscriptionStateUpgradePendingChecker)
		require.NoError(GinkgoT(), err)

		// Fetch existing csvB installPlan
		fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, subscription.Status.InstallPlanRef.Name, requiresApprovalChecker)
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), ip2.GetName(), subscription.Status.InstallPlanRef.Name, "expected new installplan is the same with pre-exising one")

		// Approve the installplan and wait for csvB to be installed
		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		// Wait for csvB to be installed
		_, err = awaitCSV(crc, testNamespace, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})

	Describe("puppeting CatalogSource health status", func() {
		var (
			c          operatorclient.ClientInterface
			crc        versioned.Interface
			getOpts    metav1.GetOptions
			deleteOpts *metav1.DeleteOptions
		)

		BeforeEach(func() {
			c = newKubeClient()
			crc = newCRClient()
			getOpts = metav1.GetOptions{}
			deleteOpts = &metav1.DeleteOptions{}
		})

		AfterEach(func() {

			err := crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		When("missing target catalog", func() {
			// TestSubscriptionStatusMissingTargetCatalogSource ensures that a Subscription has the appropriate status condition when
			// its target catalog is missing.
			//
			// Steps:
			// 1. Generate an initial CatalogSource in the target namespace
			// 2. Generate Subscription, "sub", targetting non-existent CatalogSource, "missing"
			// 3. Wait for sub status to show SubscriptionCatalogSourcesUnhealthy with status True, reason CatalogSourcesUpdated, and appropriate missing message
			// 4. Update sub's spec to target the "mysubscription"
			// 5. Wait for sub's status to show SubscriptionCatalogSourcesUnhealthy with status False, reason AllCatalogSourcesHealthy, and reason "all available catalogsources are healthy"
			// 6. Wait for sub to succeed
			It("should surface the missing catalog", func() {
				err := initCatalog(GinkgoT(), c, crc)
				Expect(err).NotTo(HaveOccurred())

				missingName := "missing"
				cleanup := createSubscriptionForCatalog(crc, testNamespace, testSubscriptionName, missingName, testPackageName, betaChannel, "", operatorsv1alpha1.ApprovalAutomatic)
				defer cleanup()

				By("detecting its absence")
				sub, err := fetchSubscription(crc, testNamespace, testSubscriptionName, subscriptionHasCondition(operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy, corev1.ConditionTrue, operatorsv1alpha1.UnhealthyCatalogSourceFound, fmt.Sprintf("targeted catalogsource %s/%s missing", testNamespace, missingName)))
				Expect(err).NotTo(HaveOccurred())
				Expect(sub).ToNot(BeNil())

				// Update sub to target an existing CatalogSource
				sub.Spec.CatalogSource = catalogSourceName
				_, err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).Update(context.Background(), sub, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred())

				// Wait for SubscriptionCatalogSourcesUnhealthy to be false
				By("detecting a new existing target")
				_, err = fetchSubscription(crc, testNamespace, testSubscriptionName, subscriptionHasCondition(operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy, corev1.ConditionFalse, operatorsv1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy"))
				Expect(err).NotTo(HaveOccurred())

				// Wait for success
				_, err = fetchSubscription(crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		When("the target catalog's sourceType", func() {
			Context("is unknown", func() {
				It("should surface catalog health", func() {
					cs := &operatorsv1alpha1.CatalogSource{
						TypeMeta: metav1.TypeMeta{
							Kind:       operatorsv1alpha1.CatalogSourceKind,
							APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "cs",
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: "goose",
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(context.Background(), cs, metav1.CreateOptions{})
					defer func() {
						err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Delete(context.Background(), cs.GetName(), *deleteOpts)
						Expect(err).ToNot(HaveOccurred())
					}()

					subName := genName("sub-")
					cleanup := createSubscriptionForCatalog(
						crc,
						cs.GetNamespace(),
						subName,
						cs.GetName(),
						testPackageName,
						betaChannel,
						"",
						operatorsv1alpha1.ApprovalManual,
					)
					defer cleanup()

					var sub *operatorsv1alpha1.Subscription
					sub, err = fetchSubscription(
						crc,
						cs.GetNamespace(),
						subName,
						subscriptionHasCondition(
							operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy,
							corev1.ConditionTrue,
							operatorsv1alpha1.UnhealthyCatalogSourceFound,
							fmt.Sprintf("targeted catalogsource %s/%s unhealthy", cs.GetNamespace(), cs.GetName()),
						),
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(sub).ToNot(BeNil())

					// Get the latest CatalogSource
					cs, err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Get(context.Background(), cs.GetName(), getOpts)
					Expect(err).NotTo(HaveOccurred())
					Expect(cs).ToNot(BeNil())
				})
			})

			Context("is grpc and its spec is missing the address and image fields", func() {
				It("should surface catalog health", func() {
					// Create a CatalogSource pointing to the grpc pod
					cs := &operatorsv1alpha1.CatalogSource{
						TypeMeta: metav1.TypeMeta{
							Kind:       operatorsv1alpha1.CatalogSourceKind,
							APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      genName("cs-"),
							Namespace: testNamespace,
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: operatorsv1alpha1.SourceTypeGrpc,
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(context.Background(), cs, metav1.CreateOptions{})
					defer func() {
						err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Delete(context.Background(), cs.GetName(), *deleteOpts)
						Expect(err).ToNot(HaveOccurred())
					}()

					subName := genName("sub-")
					cleanup := createSubscriptionForCatalog(
						crc,
						cs.GetNamespace(),
						subName,
						cs.GetName(),
						testPackageName,
						betaChannel,
						"",
						operatorsv1alpha1.ApprovalManual,
					)
					defer cleanup()

					var sub *operatorsv1alpha1.Subscription
					sub, err = fetchSubscription(
						crc,
						cs.GetNamespace(),
						subName,
						subscriptionHasCondition(
							operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy,
							corev1.ConditionTrue,
							operatorsv1alpha1.UnhealthyCatalogSourceFound,
							fmt.Sprintf("targeted catalogsource %s/%s unhealthy", cs.GetNamespace(), cs.GetName()),
						),
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(sub).ToNot(BeNil())
				})
			})

			Context("is internal and its spec is missing the configmap reference", func() {
				It("should surface catalog health", func() {
					cs := &operatorsv1alpha1.CatalogSource{
						TypeMeta: metav1.TypeMeta{
							Kind:       operatorsv1alpha1.CatalogSourceKind,
							APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      genName("cs-"),
							Namespace: testNamespace,
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: operatorsv1alpha1.SourceTypeInternal,
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(context.Background(), cs, metav1.CreateOptions{})
					defer func() {
						err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Delete(context.Background(), cs.GetName(), *deleteOpts)
						Expect(err).ToNot(HaveOccurred())
					}()

					subName := genName("sub-")
					cleanup := createSubscriptionForCatalog(
						crc,
						cs.GetNamespace(),
						subName,
						cs.GetName(),
						testPackageName,
						betaChannel,
						"",
						operatorsv1alpha1.ApprovalManual,
					)
					defer cleanup()

					var sub *operatorsv1alpha1.Subscription
					sub, err = fetchSubscription(
						crc,
						cs.GetNamespace(),
						subName,
						subscriptionHasCondition(
							operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy,
							corev1.ConditionTrue,
							operatorsv1alpha1.UnhealthyCatalogSourceFound,
							fmt.Sprintf("targeted catalogsource %s/%s unhealthy", cs.GetNamespace(), cs.GetName()),
						),
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(sub).ToNot(BeNil())
				})
			})

			Context("is configmap and its spec is missing the configmap reference", func() {
				It("should surface catalog health", func() {
					cs := &operatorsv1alpha1.CatalogSource{
						TypeMeta: metav1.TypeMeta{
							Kind:       operatorsv1alpha1.CatalogSourceKind,
							APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      genName("cs-"),
							Namespace: testNamespace,
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: operatorsv1alpha1.SourceTypeInternal,
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(context.Background(), cs, metav1.CreateOptions{})
					defer func() {
						err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Delete(context.Background(), cs.GetName(), *deleteOpts)
						Expect(err).ToNot(HaveOccurred())
					}()

					subName := genName("sub-")
					cleanup := createSubscriptionForCatalog(
						crc,
						cs.GetNamespace(),
						subName,
						cs.GetName(),
						testPackageName,
						betaChannel,
						"",
						operatorsv1alpha1.ApprovalAutomatic,
					)
					defer cleanup()

					var sub *operatorsv1alpha1.Subscription
					sub, err = fetchSubscription(
						crc,
						cs.GetNamespace(),
						subName,
						subscriptionHasCondition(
							operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy,
							corev1.ConditionTrue,
							operatorsv1alpha1.UnhealthyCatalogSourceFound,
							fmt.Sprintf("targeted catalogsource %s/%s unhealthy", cs.GetNamespace(), cs.GetName()),
						),
					)
					Expect(err).NotTo(HaveOccurred())
					Expect(sub).ToNot(BeNil())
				})
			})
		})

	})

	// TestSubscriptionInstallPlanStatus ensures that a Subscription has the appropriate status conditions for possible referenced
	// InstallPlan states.
	//
	// Steps:
	// 1. Create namespace, ns
	// 2. Create CatalogSource, cs, in ns
	// 3. Create OperatorGroup, og, in ns selecting its own namespace
	// 4. Create Subscription to a package of cs in ns, sub
	// 5. Wait for the package from sub to install successfully with no remaining InstallPlan status conditions
	// 6. Store conditions for later comparision
	// 7. Get the InstallPlan
	// 8. Set the InstallPlan's approval mode to Manual
	// 9. Set the InstallPlan's phase to None
	// 10. Wait for sub to have status condition SubscriptionInstallPlanPending true and reason InstallPlanNotYetReconciled
	// 11. Get the latest IntallPlan and set the phase to InstallPlanPhaseRequiresApproval
	// 12. Wait for sub to have status condition SubscriptionInstallPlanPending true and reason RequiresApproval
	// 13. Get the latest InstallPlan and set the phase to InstallPlanPhaseInstalling
	// 14. Wait for sub to have status condition SubscriptionInstallPlanPending true and reason Installing
	// 15. Get the latest InstallPlan and set the phase to InstallPlanPhaseFailed and remove all status conditions
	// 16. Wait for sub to have status condition SubscriptionInstallPlanFailed true and reason InstallPlanFailed
	// 17. Get the latest InstallPlan and set status condition of type Installed to false with reason InstallComponentFailed
	// 18. Wait for sub to have status condition SubscriptionInstallPlanFailed true and reason InstallComponentFailed
	// 19. Delete the referenced InstallPlan
	// 20. Wait for sub to have status condition SubscriptionInstallPlanMissing true
	// 21. Ensure original non-InstallPlan status conditions remain after InstallPlan transitions
	It("can reconcile InstallPlan status", func() {
		c := newKubeClient()
		crc := newCRClient()

		// Create namespace ns
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("ns-"),
			},
		}
		Eventually(func() error {
			_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
			return err
		}).Should(Succeed())

		defer func() {
			Eventually(func() error {
				return c.KubernetesInterface().CoreV1().Namespaces().Delete(context.Background(), ns.GetName(), metav1.DeleteOptions{})
			}).Should(Succeed())
		}()

		// Create CatalogSource, cs, in ns
		pkgName := genName("pkg-")
		channelName := genName("channel-")
		crd := newCRD(pkgName)
		csv := newCSV(pkgName, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		manifests := []registry.PackageManifest{
			{
				PackageName: pkgName,
				Channels: []registry.PackageChannel{
					{Name: channelName, CurrentCSVName: csv.GetName()},
				},
				DefaultChannelName: channelName,
			},
		}
		catalogName := genName("catalog-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogName, ns.GetName(), manifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csv})
		defer cleanupCatalogSource()
		_, err := fetchCatalogSourceOnStatus(crc, catalogName, ns.GetName(), catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		// Create OperatorGroup, og, in ns selecting its own namespace
		og := newOperatorGroup(ns.GetName(), genName("og-"), nil, nil, []string{ns.GetName()}, false)
		Eventually(func() error {
			_, err = crc.OperatorsV1().OperatorGroups(og.GetNamespace()).Create(context.Background(), og, metav1.CreateOptions{})
			return err
		}).Should(Succeed())

		// Create Subscription to a package of cs in ns, sub
		subName := genName("sub-")
		defer createSubscriptionForCatalog(crc, ns.GetName(), subName, catalogName, pkgName, channelName, pkgName, operatorsv1alpha1.ApprovalAutomatic)()

		// Wait for the package from sub to install successfully with no remaining InstallPlan status conditions
		sub, err := fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			for _, cond := range s.Status.Conditions {
				switch cond.Type {
				case operatorsv1alpha1.SubscriptionInstallPlanMissing, operatorsv1alpha1.SubscriptionInstallPlanPending, operatorsv1alpha1.SubscriptionInstallPlanFailed:
					return false
				}
			}
			return subscriptionStateAtLatestChecker(s)
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())

		// Store conditions for later comparision
		conds := sub.Status.Conditions

		ref := sub.Status.InstallPlanRef
		Expect(ref).ToNot(BeNil())

		plan := &operatorsv1alpha1.InstallPlan{}
		plan.SetNamespace(ref.Namespace)
		plan.SetName(ref.Name)

		// Set the InstallPlan's approval mode to Manual
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Spec.Approval = operatorsv1alpha1.ApprovalManual
			p.Spec.Approved = false
			return nil
		})).Should(Succeed())

		// Set the InstallPlan's phase to None
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseNone
			return nil
		})).Should(Succeed())

		// Wait for sub to have status condition SubscriptionInstallPlanPending true and reason InstallPlanNotYetReconciled
		sub, err = fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanPending)
			return cond.Status == corev1.ConditionTrue && cond.Reason == operatorsv1alpha1.InstallPlanNotYetReconciled
		})
		Expect(err).ToNot(HaveOccurred())

		// Set the phase to InstallPlanPhaseRequiresApproval
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseRequiresApproval
			return nil
		})).Should(Succeed())

		// Wait for sub to have status condition SubscriptionInstallPlanPending true and reason RequiresApproval
		sub, err = fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanPending)
			return cond.Status == corev1.ConditionTrue && cond.Reason == string(operatorsv1alpha1.InstallPlanPhaseRequiresApproval)
		})
		Expect(err).ToNot(HaveOccurred())

		// Set the phase to InstallPlanPhaseInstalling
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseInstalling
			return nil
		})).Should(Succeed())

		// Wait for sub to have status condition SubscriptionInstallPlanPending true and reason Installing
		sub, err = fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanPending)
			return cond.Status == corev1.ConditionTrue && cond.Reason == string(operatorsv1alpha1.InstallPlanPhaseInstalling)
		})
		Expect(err).ToNot(HaveOccurred())

		// Set the phase to InstallPlanPhaseFailed and remove all status conditions
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseFailed
			p.Status.Conditions = nil
			return nil
		})).Should(Succeed())

		// Wait for sub to have status condition SubscriptionInstallPlanFailed true and reason InstallPlanFailed
		sub, err = fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanFailed)
			return cond.Status == corev1.ConditionTrue && cond.Reason == operatorsv1alpha1.InstallPlanFailed
		})
		Expect(err).ToNot(HaveOccurred())

		// Set status condition of type Installed to false with reason InstallComponentFailed
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseFailed
			failedCond := p.Status.GetCondition(operatorsv1alpha1.InstallPlanInstalled)
			failedCond.Status = corev1.ConditionFalse
			failedCond.Reason = operatorsv1alpha1.InstallPlanReasonComponentFailed
			p.Status.SetCondition(failedCond)
			return nil
		})).Should(Succeed())

		// Wait for sub to have status condition SubscriptionInstallPlanFailed true and reason InstallComponentFailed
		sub, err = fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanFailed)
			return cond.Status == corev1.ConditionTrue && cond.Reason == string(operatorsv1alpha1.InstallPlanReasonComponentFailed)
		})
		Expect(err).ToNot(HaveOccurred())

		// Delete the referenced InstallPlan
		Eventually(func() error {
			return crc.OperatorsV1alpha1().InstallPlans(ref.Namespace).Delete(context.Background(), ref.Name, metav1.DeleteOptions{})
		}).Should(Succeed())

		// Wait for sub to have status condition SubscriptionInstallPlanMissing true
		sub, err = fetchSubscription(crc, ns.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			return s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanMissing).Status == corev1.ConditionTrue
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())

		// Ensure original non-InstallPlan status conditions remain after InstallPlan transitions
		hashEqual := comparison.NewHashEqualitor()
		for _, cond := range conds {
			switch condType := cond.Type; condType {
			case operatorsv1alpha1.SubscriptionInstallPlanPending, operatorsv1alpha1.SubscriptionInstallPlanFailed:
				require.FailNowf(GinkgoT(), "failed", "subscription contains unexpected installplan condition: %v", cond)
			case operatorsv1alpha1.SubscriptionInstallPlanMissing:
				require.Equal(GinkgoT(), operatorsv1alpha1.ReferencedInstallPlanNotFound, cond.Reason)
			default:
				require.True(GinkgoT(), hashEqual(cond, sub.Status.GetCondition(condType)), "non-installplan status condition changed")
			}
		}
	})

	It("creation with pod config", func() {

		newConfigClient := func(t GinkgoTInterface) configv1client.ConfigV1Interface {
			client, err := configv1client.NewForConfig(ctx.Ctx().RESTConfig())
			require.NoError(GinkgoT(), err)

			return client
		}

		proxyEnvVarFunc := func(t GinkgoTInterface, client configv1client.ConfigV1Interface) []corev1.EnvVar {
			if discovery.ServerSupportsVersion(ctx.Ctx().KubeClient().KubernetesInterface().Discovery(), configv1.GroupVersion) != nil {
				return nil
			}

			proxy, getErr := client.Proxies().Get(context.Background(), "cluster", metav1.GetOptions{})
			if k8serrors.IsNotFound(getErr) {
				return nil
			}
			require.NoError(GinkgoT(), getErr)
			require.NotNil(GinkgoT(), proxy)

			proxyEnv := []corev1.EnvVar{}

			if proxy.Status.HTTPProxy != "" {
				proxyEnv = append(proxyEnv, corev1.EnvVar{
					Name:  "HTTP_PROXY",
					Value: proxy.Status.HTTPProxy,
				})
			}

			if proxy.Status.HTTPSProxy != "" {
				proxyEnv = append(proxyEnv, corev1.EnvVar{
					Name:  "HTTPS_PROXY",
					Value: proxy.Status.HTTPSProxy,
				})
			}

			if proxy.Status.NoProxy != "" {
				proxyEnv = append(proxyEnv, corev1.EnvVar{
					Name:  "NO_PROXY",
					Value: proxy.Status.NoProxy,
				})
			}

			return proxyEnv
		}

		kubeClient := newKubeClient()
		crClient := newCRClient()
		config := newConfigClient(GinkgoT())

		// Create a ConfigMap that is mounted to the operator via the subscription
		testConfigMapName := genName("test-configmap-")
		testConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: testConfigMapName,
			},
		}

		_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(context.Background(), testConfigMap, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Delete(context.Background(), testConfigMap.Name, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		// Configure the Subscription.

		podEnv := []corev1.EnvVar{
			{
				Name:  "MY_ENV_VARIABLE1",
				Value: "value1",
			},
			{
				Name:  "MY_ENV_VARIABLE2",
				Value: "value2",
			},
		}
		testVolumeName := genName("test-volume-")
		podVolumes := []corev1.Volume{
			{
				Name: testVolumeName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: testConfigMapName,
						},
					},
				},
			},
		}

		podVolumeMounts := []corev1.VolumeMount{
			{Name: testVolumeName, MountPath: "/test"},
		}

		podTolerations := []corev1.Toleration{
			{
				Key:      "my-toleration-key",
				Value:    "my-toleration-value",
				Effect:   corev1.TaintEffectNoSchedule,
				Operator: corev1.TolerationOpEqual,
			},
		}
		podResources := &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("100m"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		}

		podConfig := &operatorsv1alpha1.SubscriptionConfig{
			Env:          podEnv,
			Volumes:      podVolumes,
			VolumeMounts: podVolumeMounts,
			Tolerations:  podTolerations,
			Resources:    podResources,
		}

		permissions := deploymentPermissions()
		catsrc, subSpec, catsrcCleanup := newCatalogSource(GinkgoT(), kubeClient, crClient, "podconfig", testNamespace, permissions)
		defer catsrcCleanup()

		// Ensure that the catalog source is resolved before we create a subscription.
		_, err = fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("podconfig-sub-")
		subSpec.Config = podConfig
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		csv, err := fetchCSV(crClient, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)

		proxyEnv := proxyEnvVarFunc(GinkgoT(), config)
		expected := podEnv
		expected = append(expected, proxyEnv...)

		checkDeploymentWithPodConfiguration(GinkgoT(), kubeClient, csv, podConfig.Env, podConfig.Volumes, podConfig.VolumeMounts, podConfig.Tolerations, podConfig.Resources)
	})

	It("creation with nodeSelector config", func() {
		kubeClient := newKubeClient()
		crClient := newCRClient()

		// Create a ConfigMap that is mounted to the operator via the subscription
		testConfigMapName := genName("test-configmap-")
		testConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: testConfigMapName,
			},
		}

		_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(context.Background(), testConfigMap, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Delete(context.Background(), testConfigMap.Name, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		// Configure the Subscription.
		podNodeSelector := map[string]string{
			"foo": "bar",
		}

		podConfig := &operatorsv1alpha1.SubscriptionConfig{
			NodeSelector: podNodeSelector,
		}

		permissions := deploymentPermissions()
		catsrc, subSpec, catsrcCleanup := newCatalogSource(GinkgoT(), kubeClient, crClient, "podconfig", testNamespace, permissions)
		defer catsrcCleanup()

		// Ensure that the catalog source is resolved before we create a subscription.
		_, err = fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("podconfig-sub-")
		subSpec.Config = podConfig
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		csv, err := fetchCSV(crClient, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseInstalling))
		require.NoError(GinkgoT(), err)

		Eventually(func() error {
			return checkDeploymentHasPodConfigNodeSelector(GinkgoT(), kubeClient, csv, podNodeSelector)
		}, timeout, interval).Should(Succeed())

	})

	It("creation with dependencies", func() {

		kubeClient := newKubeClient()
		crClient := newCRClient()

		permissions := deploymentPermissions()

		catsrc, subSpec, catsrcCleanup := newCatalogSourceWithDependencies(GinkgoT(), kubeClient, crClient, "podconfig", testNamespace, permissions)
		defer catsrcCleanup()

		// Ensure that the catalog source is resolved before we create a subscription.
		_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Create duplicates of the CatalogSource
		for i := 0; i < 10; i++ {
			duplicateCatsrc, _, duplicateCatSrcCleanup := newCatalogSourceWithDependencies(GinkgoT(), kubeClient, crClient, "podconfig", testNamespace, permissions)
			defer duplicateCatSrcCleanup()

			// Ensure that the catalog source is resolved before we create a subscription.
			_, err = fetchCatalogSourceOnStatus(crClient, duplicateCatsrc.GetName(), testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)
		}

		// Create a subscription that has a dependency
		subscriptionName := genName("podconfig-sub-")
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		// Check that a single catalog source was used to resolve the InstallPlan
		installPlan, err := fetchInstallPlan(GinkgoT(), crClient, subscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), installPlan.Status.CatalogSources, 1)
	})

	It("creation with dependencies required and provided in different versions of an operator in the same package", func() {
		// 	PARITY: this test covers the same scenario as the TestSolveOperators_PackageCannotSelfSatisfy unit test
		kubeClient := ctx.Ctx().KubeClient()
		crClient := ctx.Ctx().OperatorClient()

		crd := newCRD(genName("ins"))
		crd2 := newCRD(genName("ins"))

		// csvs for catalogsource 1
		csvs1 := make([]operatorsv1alpha1.ClusterServiceVersion, 0)

		// csvs for catalogsource 2
		csvs2 := make([]operatorsv1alpha1.ClusterServiceVersion, 0)

		packageA := registry.PackageManifest{PackageName: "PackageA"}
		By("Package A", func() {
			Step(1, "Default Channel: Stable", func() {
				packageA.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator A (Requires CRD, CRD 2)", func() {
					csvA := newCSV("csv-a", testNamespace, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{crd, crd2}, nil)
					packageA.Channels = append(packageA.Channels, registry.PackageChannel{Name: stableChannel, CurrentCSVName: csvA.GetName()})
					csvs1 = append(csvs1, csvA)
				})
			})

			Step(1, "Channel Alpha", func() {
				Step(2, "Operator ABC (Provides: CRD, CRD 2)", func() {
					csvABC := newCSV("csv-abc", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd, crd2}, nil, nil)
					packageA.Channels = append(packageA.Channels, registry.PackageChannel{Name: alphaChannel, CurrentCSVName: csvABC.GetName()})
					csvs1 = append(csvs1, csvABC)
				})
			})
		})

		packageB := registry.PackageManifest{PackageName: "PackageB"}
		By("Package B", func() {
			Step(1, "Default Channel: Stable", func() {
				packageB.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator B (Provides: CRD)", func() {
					csvB := newCSV("csv-b", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
					packageB.Channels = append(packageB.Channels, registry.PackageChannel{Name: stableChannel, CurrentCSVName: csvB.GetName()})
					csvs1 = append(csvs1, csvB)
				})
			})

			Step(1, "Channel Alpha", func() {
				Step(2, "Operator D (Provides: CRD)", func() {
					csvD := newCSV("csv-d", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
					packageB.Channels = append(packageB.Channels, registry.PackageChannel{Name: alphaChannel, CurrentCSVName: csvD.GetName()})
					csvs1 = append(csvs1, csvD)
				})
			})
		})

		packageBInCatsrc2 := registry.PackageManifest{PackageName: "PackageB"}
		By("Package B", func() {
			Step(1, "Default Channel: Stable", func() {
				packageBInCatsrc2.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator C (Provides: CRD 2)", func() {
					csvC := newCSV("csv-c", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd2}, nil, nil)
					packageBInCatsrc2.Channels = append(packageBInCatsrc2.Channels, registry.PackageChannel{Name: stableChannel, CurrentCSVName: csvC.GetName()})
					csvs2 = append(csvs2, csvC)
				})
			})
		})

		packageC := registry.PackageManifest{PackageName: "PackageC"}
		By("Package C", func() {
			Step(1, "Default Channel: Stable", func() {
				packageC.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator E (Provides: CRD 2)", func() {
					csvE := newCSV("csv-e", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd2}, nil, nil)
					packageC.Channels = append(packageC.Channels, registry.PackageChannel{Name: stable, CurrentCSVName: csvE.GetName()})
					csvs2 = append(csvs2, csvE)
				})
			})
		})

		// create catalogsources
		var catsrc, catsrc2 *operatorsv1alpha1.CatalogSource
		var cleanup cleanupFunc
		By("creating catalogsources", func() {
			var c1, c2 cleanupFunc
			catsrc, c1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace, []registry.PackageManifest{packageA, packageB}, []apiextensions.CustomResourceDefinition{crd, crd2}, csvs1)
			catsrc2, c2 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc2"), testNamespace, []registry.PackageManifest{packageBInCatsrc2, packageC}, []apiextensions.CustomResourceDefinition{crd, crd2}, csvs2)
			cleanup = func() {
				c1()
				c2()
			}
		})
		defer cleanup()

		By("waiting for catalogsources to be ready", func() {
			_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)
			_, err = fetchCatalogSourceOnStatus(crClient, catsrc2.GetName(), testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)
		})

		// Create a subscription for packageA in catsrc
		subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catsrc.GetName(),
			CatalogSourceNamespace: catsrc.GetNamespace(),
			Package:                packageA.PackageName,
			Channel:                stableChannel,
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
		}
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subscriptionSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		// ensure correct CSVs were picked
		var got []string
		Eventually(func() []string {
			ip, err := crClient.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
			if err != nil {
				return nil
			}
			got = ip.Spec.ClusterServiceVersionNames
			return got
		}).ShouldNot(BeNil())
		require.ElementsMatch(GinkgoT(), []string{"csv-a", "csv-b", "csv-e"}, got)
	})

	Context("to an operator with dependencies from different CatalogSources with priorities", func() {
		var kubeClient operatorclient.ClientInterface
		var crClient versioned.Interface
		var crd apiextensions.CustomResourceDefinition
		var packageMain, packageDepRight, packageDepWrong registry.PackageManifest
		var csvsMain, csvsRight, csvsWrong []operatorsv1alpha1.ClusterServiceVersion
		var catsrcMain, catsrcDepRight, catsrcDepWrong *operatorsv1alpha1.CatalogSource
		var cleanup, cleanupSubscription cleanupFunc
		const (
			mainCSVName  = "csv-main"
			rightCSVName = "csv-right"
			wrongCSVName = "csv-wrong"
		)

		BeforeEach(func() {
			kubeClient = ctx.Ctx().KubeClient()
			crClient = ctx.Ctx().OperatorClient()
			crd = newCRD(genName("ins"))

			packageMain = registry.PackageManifest{PackageName: genName("PkgMain-")}
			csv := newCSV(mainCSVName, testNamespace, "", semver.MustParse("0.1.0"), nil,
				[]apiextensions.CustomResourceDefinition{crd}, nil)
			packageMain.DefaultChannelName = stableChannel
			packageMain.Channels = append(packageMain.Channels, registry.PackageChannel{Name: stableChannel, CurrentCSVName: csv.GetName()})

			csvsMain = []operatorsv1alpha1.ClusterServiceVersion{csv}
			csvsRight = []operatorsv1alpha1.ClusterServiceVersion{}
			csvsWrong = []operatorsv1alpha1.ClusterServiceVersion{}
		})

		Context("creating CatalogSources providing the same dependency with different names", func() {
			var catsrcCleanup1, catsrcCleanup2, catsrcCleanup3 cleanupFunc

			BeforeEach(func() {

				packageDepRight = registry.PackageManifest{PackageName: "PackageDependent"}
				csv := newCSV(rightCSVName, testNamespace, "", semver.MustParse("0.1.0"),
					[]apiextensions.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsRight = append(csvsRight, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace,
					[]registry.PackageManifest{packageMain}, nil, csvsMain)

				catsrcDepRight, catsrcCleanup2 = createInternalCatalogSource(kubeClient, crClient, "catsrc1", testNamespace,
					[]registry.PackageManifest{packageDepRight}, []apiextensions.CustomResourceDefinition{crd}, csvsRight)

				catsrcDepWrong, catsrcCleanup3 = createInternalCatalogSource(kubeClient, crClient, "catsrc2", testNamespace,
					[]registry.PackageManifest{packageDepWrong}, []apiextensions.CustomResourceDefinition{crd}, csvsWrong)

				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepRight.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				if catsrcCleanup1 != nil {
					catsrcCleanup1()
				}
				if catsrcCleanup2 != nil {
					catsrcCleanup2()
				}
				if catsrcCleanup3 != nil {
					catsrcCleanup3()
				}
			})

			When("creating subscription for the main package", func() {

				var subscription *operatorsv1alpha1.Subscription
				BeforeEach(func() {
					// Create a subscription for packageA in catsrc
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, mainCSVName, testNamespace, csvSucceededChecker)
					Expect(err).ToNot(HaveOccurred())

				})
				AfterEach(func() {
					if cleanupSubscription != nil {
						cleanupSubscription()
					}
					if cleanup != nil {
						cleanup()
					}
				})

				It("choose the dependency from the right CatalogSource based on lexicographical name ordering of catalogs", func() {
					// ensure correct CSVs were picked
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
						if err != nil {
							return nil, err
						}
						return ip.Spec.ClusterServiceVersionNames, nil
					}).Should(ConsistOf(mainCSVName, rightCSVName))
				})
			})
		})

		Context("creating the main and an arbitrary CatalogSources providing the same dependency", func() {
			var catsrcCleanup1, catsrcCleanup2 cleanupFunc

			BeforeEach(func() {

				packageDepRight = registry.PackageManifest{PackageName: "PackageDependent"}
				csv := newCSV(rightCSVName, testNamespace, "", semver.MustParse("0.1.0"),
					[]apiextensions.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsMain = append(csvsMain, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace,
					[]registry.PackageManifest{packageDepRight, packageMain}, []apiextensions.CustomResourceDefinition{crd}, csvsMain)

				catsrcDepWrong, catsrcCleanup2 = createInternalCatalogSourceWithPriority(kubeClient, crClient,
					genName("catsrc"), testNamespace, []registry.PackageManifest{packageDepWrong}, []apiextensions.CustomResourceDefinition{crd},
					csvsWrong, 100)

				// waiting for catalogsources to be ready
				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				if catsrcCleanup1 != nil {
					catsrcCleanup1()
				}
				if catsrcCleanup2 != nil {
					catsrcCleanup2()
				}

			})

			When("creating subscription for the main package", func() {
				var subscription *operatorsv1alpha1.Subscription
				BeforeEach(func() {
					// Create a subscription for packageA in catsrc
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, mainCSVName, testNamespace, csvSucceededChecker)
					Expect(err).ToNot(HaveOccurred())

				})
				AfterEach(func() {
					if cleanupSubscription != nil {
						cleanupSubscription()
					}
					if cleanup != nil {
						cleanup()
					}
				})
				It("choose the dependent package from the same catsrc as the installing operator", func() {
					// ensure correct CSVs were picked
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
						if err != nil {
							return nil, err
						}
						return ip.Spec.ClusterServiceVersionNames, nil
					}).Should(ConsistOf(mainCSVName, rightCSVName))
				})

			})

		})

		Context("creating CatalogSources providing the same dependency with different priority value", func() {
			var catsrcCleanup1, catsrcCleanup2, catsrcCleanup3 cleanupFunc

			BeforeEach(func() {

				packageDepRight = registry.PackageManifest{PackageName: "PackageDependent"}
				csv := newCSV(rightCSVName, testNamespace, "", semver.MustParse("0.1.0"),
					[]apiextensions.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsRight = append(csvsRight, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace,
					[]registry.PackageManifest{packageMain}, nil, csvsMain)

				catsrcDepRight, catsrcCleanup2 = createInternalCatalogSourceWithPriority(kubeClient, crClient,
					genName("catsrc"), testNamespace, []registry.PackageManifest{packageDepRight}, []apiextensions.CustomResourceDefinition{crd},
					csvsRight, 100)

				catsrcDepWrong, catsrcCleanup3 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace,
					[]registry.PackageManifest{packageDepWrong}, []apiextensions.CustomResourceDefinition{crd}, csvsWrong)

				// waiting for catalogsources to be ready
				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepRight.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				if catsrcCleanup1 != nil {
					catsrcCleanup1()
				}
				if catsrcCleanup2 != nil {
					catsrcCleanup2()
				}
				if catsrcCleanup3 != nil {
					catsrcCleanup3()
				}

			})

			When("creating subscription for the main package", func() {
				var subscription *operatorsv1alpha1.Subscription
				BeforeEach(func() {
					// Create a subscription for packageA in catsrc
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, mainCSVName, testNamespace, csvSucceededChecker)
					Expect(err).ToNot(HaveOccurred())

				})
				AfterEach(func() {
					if cleanupSubscription != nil {
						cleanupSubscription()
					}
					if cleanup != nil {
						cleanup()
					}
				})
				It("choose the dependent package from the catsrc with higher priority", func() {
					// ensure correct CSVs were picked
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
						if err != nil {
							return nil, err
						}
						return ip.Spec.ClusterServiceVersionNames, nil
					}).Should(ConsistOf(mainCSVName, rightCSVName))
				})

			})

		})

		Context("creating CatalogSources providing the same dependency under test and global namespaces", func() {
			var catsrcCleanup1, catsrcCleanup2, catsrcCleanup3 cleanupFunc

			BeforeEach(func() {

				packageDepRight = registry.PackageManifest{PackageName: "PackageDependent"}
				csv := newCSV(rightCSVName, testNamespace, "", semver.MustParse("0.1.0"),
					[]apiextensions.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsRight = append(csvsRight, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace,
					[]registry.PackageManifest{packageMain}, nil, csvsMain)

				catsrcDepRight, catsrcCleanup2 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), testNamespace,
					[]registry.PackageManifest{packageDepRight}, []apiextensions.CustomResourceDefinition{crd}, csvsRight)

				catsrcDepWrong, catsrcCleanup3 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), operatorNamespace,
					[]registry.PackageManifest{packageDepWrong}, []apiextensions.CustomResourceDefinition{crd}, csvsWrong)

				// waiting for catalogsources to be ready
				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepRight.GetName(), testNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), operatorNamespace, catalogSourceRegistryPodSynced)
				Expect(err).ToNot(HaveOccurred())
			})
			AfterEach(func() {
				if catsrcCleanup1 != nil {
					catsrcCleanup1()
				}
				if catsrcCleanup2 != nil {
					catsrcCleanup2()
				}
				if catsrcCleanup3 != nil {
					catsrcCleanup3()
				}

			})

			When("creating subscription for the main package", func() {
				var subscription *operatorsv1alpha1.Subscription
				BeforeEach(func() {
					// Create a subscription for packageA in catsrc
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, mainCSVName, testNamespace, csvSucceededChecker)
					Expect(err).ToNot(HaveOccurred())

				})
				AfterEach(func() {
					if cleanupSubscription != nil {
						cleanupSubscription()
					}
					if cleanup != nil {
						cleanup()
					}
				})
				It("choose the dependent package from the catsrc in the same namespace as the installing operator", func() {
					// ensure correct CSVs were picked
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
						if err != nil {
							return nil, err
						}
						return ip.Spec.ClusterServiceVersionNames, nil
					}).Should(ConsistOf(mainCSVName, rightCSVName))
				})

			})

		})

	})

	// csvA owns CRD1 & csvB owns CRD2 and requires CRD1
	// Create subscription for csvB lead to installation of csvB and csvA
	// Update catsrc to upgrade csvA to csvNewA which now requires CRD1
	// csvNewA can't be installed due to no other operators provide CRD1 for it
	// (Note: OLM can't pick csvA as dependency for csvNewA as it is from the same
	// same package)
	// Update catsrc again to upgrade csvB to csvNewB which now owns both CRD1 and
	// CRD2.
	// Now csvNewA and csvNewB are installed successfully as csvNewB provides CRD1
	// that csvNewA requires
	It("creation in case of transferring providedAPIs", func() {
		// 	PARITY: this test covers the same scenario as the TestSolveOperators_TransferApiOwnership unit test
		kubeClient := ctx.Ctx().KubeClient()
		crClient := ctx.Ctx().OperatorClient()

		crd := newCRD(genName("ins"))
		crd2 := newCRD(genName("ins"))

		// Create CSV
		packageName1 := genName("apackage")
		packageName2 := genName("bpackage")

		// csvA provides CRD
		csvA := newCSV("nginx-a", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		// csvB provides CRD2 and requires CRD
		csvB := newCSV("nginx-b", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd2}, []apiextensions.CustomResourceDefinition{crd}, nil)
		// New csvA requires CRD (transfer CRD ownership to the new csvB)
		csvNewA := newCSV("nginx-new-a", testNamespace, "nginx-a", semver.MustParse("0.2.0"), nil, []apiextensions.CustomResourceDefinition{crd}, nil)
		// New csvB provides CRD and CRD2
		csvNewB := newCSV("nginx-new-b", testNamespace, "nginx-b", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd, crd2}, nil, nil)

		// constraints not satisfiable:
		// apackagert6cq requires at least one of catsrcc6xgr/operators/stable/nginx-new-a,
		// apackagert6cq is mandatory,
		// pkgunique/apackagert6cq permits at most 1 of catsrcc6xgr/operators/stable/nginx-new-a, catsrcc6xgr/operators/stable/nginx-a,
		// catsrcc6xgr/operators/stable/nginx-new-a requires at least one of catsrcc6xgr/operators/stable/nginx-a

		// Create PackageManifests 1
		// Contain csvA, ABC and B
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName1,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvA.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
			{
				PackageName: packageName2,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		catalogSourceName := genName("catsrc")
		catsrc, cleanup := createInternalCatalogSource(kubeClient, crClient, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd, crd2}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanup()

		// Ensure that the catalog source is resolved before we create a subscription.
		_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catsrc.GetName(),
			CatalogSourceNamespace: catsrc.GetNamespace(),
			Package:                packageName2,
			Channel:                stableChannel,
			StartingCSV:            csvB.GetName(),
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
		}

		// Create a subscription that has a dependency
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, testNamespace, subscriptionName, subscriptionSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		// Check that a single catalog source was used to resolve the InstallPlan
		_, err = fetchInstallPlan(GinkgoT(), crClient, subscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		// Fetch CSVs A and B
		_, err = fetchCSV(crClient, csvA.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		_, err = fetchCSV(crClient, csvB.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Update PackageManifest
		manifests = []registry.PackageManifest{
			{
				PackageName: packageName1,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvNewA.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
			{
				PackageName: packageName2,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}
		updateInternalCatalog(GinkgoT(), kubeClient, crClient, catalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{crd, crd2}, []operatorsv1alpha1.ClusterServiceVersion{csvNewA, csvA, csvB}, manifests)
		csvAsub := strings.Join([]string{packageName1, stableChannel, catalogSourceName, testNamespace}, "-")
		_, err = fetchSubscription(crClient, testNamespace, csvAsub, subscriptionStateUpgradeAvailableChecker)
		require.NoError(GinkgoT(), err)
		// Ensure csvNewA is not installed
		_, err = crClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.Background(), csvNewA.Name, metav1.GetOptions{})
		require.Error(GinkgoT(), err)
		// Ensure csvA still exists
		_, err = fetchCSV(crClient, csvA.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Update packagemanifest again
		manifests = []registry.PackageManifest{
			{
				PackageName: packageName1,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvNewA.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
			{
				PackageName: packageName2,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvNewB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}
		updateInternalCatalog(GinkgoT(), kubeClient, crClient, catalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{crd, crd2}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB, csvNewA, csvNewB}, manifests)

		_, err = fetchSubscription(crClient, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(subscription.Status.InstallPlanRef.Name))
		require.NoError(GinkgoT(), err)
		// Ensure csvNewA is installed
		_, err = fetchCSV(crClient, csvNewA.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		// Ensure csvNewB is installed
		_, err = fetchCSV(crClient, csvNewB.Name, testNamespace, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})

	When("an unannotated ClusterServiceVersion exists with an associated Subscription", func() {
		var (
			teardown func()
		)

		BeforeEach(func() {
			teardown = func() {}

			packages := []registry.PackageManifest{
				{
					PackageName: "package",
					Channels: []registry.PackageChannel{
						{Name: "channel-x", CurrentCSVName: "csv-x"},
						{Name: "channel-y", CurrentCSVName: "csv-y"},
					},
					DefaultChannelName: "channel-x",
				},
			}

			x := newCSV("csv-x", testNamespace, "", semver.MustParse("1.0.0"), nil, nil, nil)
			y := newCSV("csv-y", testNamespace, "", semver.MustParse("1.0.0"), nil, nil, nil)

			_, teardown = createInternalCatalogSource(ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(), "test-catalog", testNamespace, packages, nil, []operatorsv1alpha1.ClusterServiceVersion{x, y})

			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), testNamespace, "test-subscription-x", "test-catalog", "package", "channel-x", "", operatorsv1alpha1.ApprovalAutomatic)

			Eventually(func() error {
				var unannotated operatorsv1alpha1.ClusterServiceVersion
				if err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "csv-x"}, &unannotated); err != nil {
					return err
				}
				if _, ok := unannotated.Annotations["operatorframework.io/properties"]; !ok {
					return nil
				}
				delete(unannotated.Annotations, "operatorframework.io/properties")
				return ctx.Ctx().Client().Update(context.Background(), &unannotated)
			}).Should(Succeed())
		})

		AfterEach(func() {
			teardown()
		})

		It("uses inferred properties to prevent a duplicate installation from the same package ", func() {
			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), testNamespace, "test-subscription-y", "test-catalog", "package", "channel-y", "", operatorsv1alpha1.ApprovalAutomatic)

			Consistently(func() error {
				var no operatorsv1alpha1.ClusterServiceVersion
				return ctx.Ctx().Client().Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: "csv-y"}, &no)
			}).ShouldNot(Succeed())
		})
	})

	When("there exists a Subscription to an operator having dependency candidates in both default and nondefault channels", func() {
		var (
			teardown func()
		)

		BeforeEach(func() {
			teardown = func() {}

			packages := []registry.PackageManifest{
				{
					PackageName: "dependency",
					Channels: []registry.PackageChannel{
						{Name: "default", CurrentCSVName: "csv-dependency"},
						{Name: "nondefault", CurrentCSVName: "csv-dependency"},
					},
					DefaultChannelName: "default",
				},
				{
					PackageName: "root",
					Channels: []registry.PackageChannel{
						{Name: "unimportant", CurrentCSVName: "csv-root"},
					},
					DefaultChannelName: "unimportant",
				},
			}

			crds := []apiextensions.CustomResourceDefinition{newCRD(genName("crd-"))}
			csvs := []operatorsv1alpha1.ClusterServiceVersion{
				newCSV("csv-dependency", testNamespace, "", semver.MustParse("1.0.0"), crds, nil, nil),
				newCSV("csv-root", testNamespace, "", semver.MustParse("1.0.0"), nil, crds, nil),
			}

			_, teardown = createInternalCatalogSource(ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(), "test-catalog", testNamespace, packages, crds, csvs)

			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), testNamespace, "test-subscription", "test-catalog", "root", "unimportant", "", operatorsv1alpha1.ApprovalAutomatic)
		})

		AfterEach(func() {
			teardown()
		})

		It("should create a Subscription using the candidate's default channel", func() {
			Eventually(func() ([]operatorsv1alpha1.Subscription, error) {
				var list operatorsv1alpha1.SubscriptionList
				if err := ctx.Ctx().Client().List(context.TODO(), &list); err != nil {
					return nil, err
				}
				return list.Items, nil
			}).Should(ContainElement(WithTransform(
				func(in operatorsv1alpha1.Subscription) operatorsv1alpha1.SubscriptionSpec {
					return operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          in.Spec.CatalogSource,
						CatalogSourceNamespace: in.Spec.CatalogSourceNamespace,
						Package:                in.Spec.Package,
						Channel:                in.Spec.Channel,
					}
				},
				Equal(operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          "test-catalog",
					CatalogSourceNamespace: testNamespace,
					Package:                "dependency",
					Channel:                "default",
				}),
			)))
		})
	})
})

const (
	catalogSourceName    = "mock-ocs"
	catalogConfigMapName = "mock-ocs"
	testSubscriptionName = "mysubscription"
	testPackageName      = "myapp"

	stableChannel = "stable"
	betaChannel   = "beta"
	alphaChannel  = "alpha"

	outdated = "myapp-outdated"
	stable   = "myapp-stable"
	alpha    = "myapp-alpha"
	beta     = "myapp-beta"
)

var (
	dummyManifest = []registry.PackageManifest{{
		PackageName: testPackageName,
		Channels: []registry.PackageChannel{
			{Name: stableChannel, CurrentCSVName: stable},
			{Name: betaChannel, CurrentCSVName: beta},
			{Name: alphaChannel, CurrentCSVName: alpha},
		},
		DefaultChannelName: stableChannel,
	}}
	csvType = metav1.TypeMeta{
		Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
		APIVersion: operatorsv1alpha1.GroupVersion,
	}

	strategy = operatorsv1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}
	strategyRaw, _  = json.Marshal(strategy)
	installStrategy = operatorsv1alpha1.NamedInstallStrategy{
		StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
		StrategySpec: strategy,
	}
	outdatedCSV = operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: outdated,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			Replaces:       "",
			Version:        version.OperatorVersion{semver.MustParse("0.1.0")},
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
			InstallStrategy: installStrategy,
		},
	}
	stableCSV = operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: stable,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			Replaces:       outdated,
			Version:        version.OperatorVersion{semver.MustParse("0.2.0")},
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
			InstallStrategy: installStrategy,
		},
	}
	betaCSV = operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: beta,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			Replaces: stable,
			Version:  version.OperatorVersion{semver.MustParse("0.1.1")},
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
			InstallStrategy: installStrategy,
		},
	}
	alphaCSV = operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: alpha,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			Replaces: beta,
			Version:  version.OperatorVersion{semver.MustParse("0.3.0")},
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
			InstallStrategy: installStrategy,
		},
	}
	csvList = []operatorsv1alpha1.ClusterServiceVersion{outdatedCSV, stableCSV, betaCSV, alphaCSV}

	strategyNew = operatorsv1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
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
	}

	dummyCatalogConfigMap = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: catalogConfigMapName,
		},
		Data: map[string]string{},
	}

	dummyCatalogSource = operatorsv1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.CatalogSourceKind,
			APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: catalogSourceName,
		},
		Spec: operatorsv1alpha1.CatalogSourceSpec{
			SourceType: "internal",
			ConfigMap:  catalogConfigMapName,
		},
	}
)

func init() {
	for i := 0; i < len(csvList); i++ {
		csvList[i].Spec.InstallStrategy.StrategySpec = strategyNew
	}

	manifestsRaw, err := yaml.Marshal(dummyManifest)
	if err != nil {
		panic(err)
	}
	dummyCatalogConfigMap.Data[registry.ConfigMapPackageName] = string(manifestsRaw)
	csvsRaw, err := yaml.Marshal(csvList)
	if err != nil {
		panic(err)
	}
	dummyCatalogConfigMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)
	dummyCatalogConfigMap.Data[registry.ConfigMapCRDName] = ""
}

func initCatalog(t GinkgoTInterface, c operatorclient.ClientInterface, crc versioned.Interface) error {

	dummyCatalogConfigMap.SetNamespace(testNamespace)
	if _, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(context.Background(), dummyCatalogConfigMap, metav1.CreateOptions{}); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("E2E bug detected: %v", err)
		}
		return err
	}

	dummyCatalogSource.SetNamespace(testNamespace)
	if _, err := crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(context.Background(), &dummyCatalogSource, metav1.CreateOptions{}); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("E2E bug detected: %v", err)
		}
		return err
	}

	fetched, err := fetchCatalogSourceOnStatus(crc, dummyCatalogSource.GetName(), dummyCatalogSource.GetNamespace(), catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	return nil
}

type subscriptionStateChecker func(subscription *operatorsv1alpha1.Subscription) bool

func subscriptionStateUpgradeAvailableChecker(subscription *operatorsv1alpha1.Subscription) bool {
	return subscription.Status.State == operatorsv1alpha1.SubscriptionStateUpgradeAvailable
}

func subscriptionStateUpgradePendingChecker(subscription *operatorsv1alpha1.Subscription) bool {
	return subscription.Status.State == operatorsv1alpha1.SubscriptionStateUpgradePending
}

func subscriptionStateAtLatestChecker(subscription *operatorsv1alpha1.Subscription) bool {
	return subscription.Status.State == operatorsv1alpha1.SubscriptionStateAtLatest
}

func subscriptionHasInstallPlanChecker(subscription *operatorsv1alpha1.Subscription) bool {
	ctx.Ctx().Logf("waiting for %s to have installplan ref", subscription.GetName())
	return subscription.Status.InstallPlanRef != nil
}

func subscriptionHasInstallPlanDifferentChecker(currentInstallPlanName string) subscriptionStateChecker {
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		return subscriptionHasInstallPlanChecker(subscription) && subscription.Status.InstallPlanRef.Name != currentInstallPlanName
	}
}

func subscriptionStateNoneChecker(subscription *operatorsv1alpha1.Subscription) bool {
	return subscription.Status.State == operatorsv1alpha1.SubscriptionStateNone
}

func subscriptionStateAny(subscription *operatorsv1alpha1.Subscription) bool {
	return subscriptionStateNoneChecker(subscription) ||
		subscriptionStateAtLatestChecker(subscription) ||
		subscriptionStateUpgradePendingChecker(subscription) ||
		subscriptionStateUpgradeAvailableChecker(subscription)
}

func subscriptionHasCurrentCSV(currentCSV string) subscriptionStateChecker {
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		return subscription.Status.CurrentCSV == currentCSV
	}
}

func subscriptionHasCondition(condType operatorsv1alpha1.SubscriptionConditionType, status corev1.ConditionStatus, reason, message string) subscriptionStateChecker {
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		cond := subscription.Status.GetCondition(condType)
		if cond.Status == status && cond.Reason == reason && cond.Message == message {
			fmt.Printf("subscription condition met %v\n", cond)
			return true
		}

		fmt.Printf("subscription condition not met: %v\n", cond)
		return false
	}
}

func fetchSubscription(crc versioned.Interface, namespace, name string, checker subscriptionStateChecker) (*operatorsv1alpha1.Subscription, error) {
	var fetchedSubscription *operatorsv1alpha1.Subscription
	var err error

	log := func(s string) {
		ctx.Ctx().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedSubscription, err = crc.OperatorsV1alpha1().Subscriptions(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil || fetchedSubscription == nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetchedSubscription.Status.State, fetchedSubscription.Status.CurrentCSV, fetchedSubscription.Status.InstallPlanRef))
		return checker(fetchedSubscription), nil
	})
	if err != nil {
		log(fmt.Sprintf("never got correct status: %#v", fetchedSubscription.Status))
		log(fmt.Sprintf("subscription spec: %#v", fetchedSubscription.Spec))
	}
	return fetchedSubscription, err
}

func buildSubscriptionCleanupFunc(crc versioned.Interface, subscription *operatorsv1alpha1.Subscription) cleanupFunc {
	return func() {

		if installPlanRef := subscription.Status.InstallPlanRef; installPlanRef != nil {

			installPlan, err := crc.OperatorsV1alpha1().InstallPlans(subscription.GetNamespace()).Get(context.Background(), installPlanRef.Name, metav1.GetOptions{})
			if err == nil {
				buildInstallPlanCleanupFunc(crc, subscription.GetNamespace(), installPlan)()
			} else {
				ctx.Ctx().Logf("Could not get installplan %s while building subscription %s's cleanup function", installPlan.GetName(), subscription.GetName())
			}
		}

		err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Delete(context.Background(), subscription.GetName(), metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
	}
}

func createSubscription(t GinkgoTInterface, crc versioned.Interface, namespace, name, packageName, channel string, approval operatorsv1alpha1.Approval) (cleanupFunc, *operatorsv1alpha1.Subscription) {
	subscription := &operatorsv1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.SubscriptionKind,
			APIVersion: operatorsv1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catalogSourceName,
			CatalogSourceNamespace: namespace,
			Package:                packageName,
			Channel:                channel,
			InstallPlanApproval:    approval,
		},
	}

	subscription, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Create(context.Background(), subscription, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())
	return buildSubscriptionCleanupFunc(crc, subscription), subscription
}

func createSubscriptionForCatalog(crc versioned.Interface, namespace, name, catalog, packageName, channel, startingCSV string, approval operatorsv1alpha1.Approval) cleanupFunc {
	subscription := &operatorsv1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.SubscriptionKind,
			APIVersion: operatorsv1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catalog,
			CatalogSourceNamespace: namespace,
			Package:                packageName,
			Channel:                channel,
			StartingCSV:            startingCSV,
			InstallPlanApproval:    approval,
		},
	}

	subscription, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Create(context.Background(), subscription, metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())
	return buildSubscriptionCleanupFunc(crc, subscription)
}

func createSubscriptionForCatalogWithSpec(t GinkgoTInterface, crc versioned.Interface, namespace, name string, spec *operatorsv1alpha1.SubscriptionSpec) cleanupFunc {
	subscription := &operatorsv1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.SubscriptionKind,
			APIVersion: operatorsv1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: spec,
	}

	subscription, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Create(context.Background(), subscription, metav1.CreateOptions{})
	require.NoError(t, err)
	return buildSubscriptionCleanupFunc(crc, subscription)
}

func checkDeploymentHasPodConfigNodeSelector(t GinkgoTInterface, client operatorclient.ClientInterface, csv *operatorsv1alpha1.ClusterServiceVersion, nodeSelector map[string]string) error {
	resolver := install.StrategyResolver{}

	strategy, err := resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return err
	}

	strategyDetailsDeployment, ok := strategy.(*operatorsv1alpha1.StrategyDetailsDeployment)
	require.Truef(t, ok, "could not cast install strategy as type %T", strategyDetailsDeployment)

	for _, deploymentSpec := range strategyDetailsDeployment.DeploymentSpecs {
		deployment, err := client.KubernetesInterface().AppsV1().Deployments(csv.GetNamespace()).Get(context.Background(), deploymentSpec.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		isEqual := reflect.DeepEqual(nodeSelector, deployment.Spec.Template.Spec.NodeSelector)
		if !isEqual {
			err = fmt.Errorf("actual nodeSelector=%v does not match expected nodeSelector=%v", deploymentSpec.Spec.Template.Spec.NodeSelector, nodeSelector)
		}
	}
	return nil
}

func checkDeploymentWithPodConfiguration(t GinkgoTInterface, client operatorclient.ClientInterface, csv *operatorsv1alpha1.ClusterServiceVersion, envVar []corev1.EnvVar, volumes []corev1.Volume, volumeMounts []corev1.VolumeMount, tolerations []corev1.Toleration, resources *corev1.ResourceRequirements) {
	resolver := install.StrategyResolver{}

	strategy, err := resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	require.NoError(t, err)

	strategyDetailsDeployment, ok := strategy.(*operatorsv1alpha1.StrategyDetailsDeployment)
	require.Truef(t, ok, "could not cast install strategy as type %T", strategyDetailsDeployment)

	findEnvVar := func(envVar []corev1.EnvVar, name string) (foundEnvVar *corev1.EnvVar, found bool) {
		for i := range envVar {
			if name == envVar[i].Name {
				found = true
				foundEnvVar = &envVar[i]

				break
			}
		}

		return
	}

	findVolumeMount := func(volumeMounts []corev1.VolumeMount, name string) (foundVolumeMount *corev1.VolumeMount, found bool) {
		for i := range volumeMounts {
			if name == volumeMounts[i].Name {
				found = true
				foundVolumeMount = &volumeMounts[i]

				break
			}
		}

		return
	}

	findVolume := func(volumes []corev1.Volume, name string) (foundVolume *corev1.Volume, found bool) {
		for i := range volumes {
			if name == volumes[i].Name {
				found = true
				foundVolume = &volumes[i]

				break
			}
		}

		return
	}

	findTolerations := func(tolerations []corev1.Toleration, toleration corev1.Toleration) (foundToleration *corev1.Toleration, found bool) {
		for i := range tolerations {
			if reflect.DeepEqual(toleration, tolerations[i]) {
				found = true
				foundToleration = &toleration

				break
			}
		}

		return
	}

	findResources := func(existingResource *corev1.ResourceRequirements, podResource *corev1.ResourceRequirements) (foundResource *corev1.ResourceRequirements, found bool) {
		if reflect.DeepEqual(existingResource, podResource) {
			found = true
			foundResource = podResource
		}

		return
	}

	check := func(container *corev1.Container) {
		for _, e := range envVar {
			existing, found := findEnvVar(container.Env, e.Name)
			require.Truef(t, found, "env variable name=%s not injected", e.Name)
			require.NotNil(t, existing)
			require.Equalf(t, e.Value, existing.Value, "env variable value does not match %s=%s", e.Name, e.Value)
		}

		for _, v := range volumeMounts {
			existing, found := findVolumeMount(container.VolumeMounts, v.Name)
			require.Truef(t, found, "VolumeMount name=%s not injected", v.Name)
			require.NotNil(t, existing)
			require.Equalf(t, v.MountPath, existing.MountPath, "VolumeMount MountPath does not match %s=%s", v.Name, v.MountPath)
		}

		existing, found := findResources(&container.Resources, resources)
		require.Truef(t, found, "Resources not injected. Resource=%v", resources)
		require.NotNil(t, existing)
		require.Equalf(t, existing, resources, "Resource=%v does not match expected Resource=%v", existing, resources)
	}

	for _, deploymentSpec := range strategyDetailsDeployment.DeploymentSpecs {
		deployment, err := client.KubernetesInterface().AppsV1().Deployments(csv.GetNamespace()).Get(context.Background(), deploymentSpec.Name, metav1.GetOptions{})
		require.NoError(t, err)
		for _, v := range volumes {
			existing, found := findVolume(deployment.Spec.Template.Spec.Volumes, v.Name)
			require.Truef(t, found, "Volume name=%s not injected", v.Name)
			require.NotNil(t, existing)
			require.Equalf(t, v.ConfigMap.LocalObjectReference.Name, existing.ConfigMap.LocalObjectReference.Name, "volume ConfigMap Names does not match %s=%s", v.Name, v.ConfigMap.LocalObjectReference.Name)
		}

		for _, toleration := range tolerations {
			existing, found := findTolerations(deployment.Spec.Template.Spec.Tolerations, toleration)
			require.Truef(t, found, "Toleration not injected. Toleration=%v", toleration)
			require.NotNil(t, existing)
			require.Equalf(t, *existing, toleration, "Toleration=%v does not match expected Toleration=%v", existing, toleration)
		}

		for i := range deployment.Spec.Template.Spec.Containers {
			check(&deployment.Spec.Template.Spec.Containers[i])
		}
	}
}

func updateInternalCatalog(t GinkgoTInterface, c operatorclient.ClientInterface, crc versioned.Interface, catalogSourceName, namespace string, crds []apiextensions.CustomResourceDefinition, csvs []operatorsv1alpha1.ClusterServiceVersion, packages []registry.PackageManifest) {
	fetchedInitialCatalog, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, namespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Get initial configmap
	configMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Get(context.Background(), fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
	require.NoError(t, err)

	// Update package to point to new csv
	manifestsRaw, err := yaml.Marshal(packages)
	require.NoError(t, err)
	configMap.Data[registry.ConfigMapPackageName] = string(manifestsRaw)

	// Update raw CRDs
	var crdsRaw []byte
	crdStrings := []string{}
	for _, crd := range crds {
		crdStrings = append(crdStrings, serializeCRD(crd))
	}
	crdsRaw, err = yaml.Marshal(crdStrings)
	require.NoError(t, err)
	configMap.Data[registry.ConfigMapCRDName] = strings.Replace(string(crdsRaw), "- |\n  ", "- ", -1)

	// Update raw CSVs
	csvsRaw, err := yaml.Marshal(csvs)
	require.NoError(t, err)
	configMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)

	// Update configmap
	_, err = c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Update(context.Background(), configMap, metav1.UpdateOptions{})
	require.NoError(t, err)

	// wait for catalog to update
	_, err = fetchCatalogSourceOnStatus(crc, catalogSourceName, namespace, func(catalog *operatorsv1alpha1.CatalogSource) bool {
		before := fetchedInitialCatalog.Status.ConfigMapResource
		after := catalog.Status.ConfigMapResource
		if after != nil && after.LastUpdateTime.After(before.LastUpdateTime.Time) && after.ResourceVersion != before.ResourceVersion &&
			catalog.Status.GRPCConnectionState.LastConnectTime.After(after.LastUpdateTime.Time) && catalog.Status.GRPCConnectionState.LastObservedState == "READY" {
			fmt.Println("catalog updated")
			return true
		}
		fmt.Printf("waiting for catalog pod %v to be available (after catalog update) - %s\n", catalog.GetName(), catalog.Status.GRPCConnectionState.LastObservedState)
		return false
	})
	require.NoError(t, err)
}

func updateCatSrcPriority(crClient versioned.Interface, namespace string, catsrc *operatorsv1alpha1.CatalogSource, priority int) {
	catsrc.Spec.Priority = priority
	_, err := crClient.OperatorsV1alpha1().CatalogSources(namespace).Update(context.TODO(), catsrc, metav1.UpdateOptions{})
	Expect(err).Should(BeNil())
}
