package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/operator-framework/api/pkg/lib/version"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
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

const (
	subscriptionTestDataBaseDir = "subscription/"
)

var _ = Describe("Subscription", Label("Subscription"), func() {
	var (
		generatedNamespace corev1.Namespace
		operatorGroup      operatorsv1.OperatorGroup
		c                  operatorclient.ClientInterface
		crc                versioned.Interface
	)

	BeforeEach(func() {
		c = ctx.Ctx().KubeClient()
		crc = ctx.Ctx().OperatorClient()

		nsName := genName("subscription-e2e-")
		operatorGroup = operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", nsName),
				Namespace: nsName,
			},
		}
		generatedNamespace = SetupGeneratedTestNamespaceWithOperatorGroup(nsName, operatorGroup)
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
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

			crds := []apiextensionsv1.CustomResourceDefinition{newCRD(genName("crd-"))}
			csvs := []operatorsv1alpha1.ClusterServiceVersion{
				newCSV("csv-dependency-1", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), crds, nil, nil),
				newCSV("csv-dependency-2", generatedNamespace.GetName(), "csv-dependency-1", semver.MustParse("2.0.0"), nil, nil, nil),
				newCSV("csv-dependency-3", generatedNamespace.GetName(), "csv-dependency-2", semver.MustParse("3.0.0"), crds, nil, nil),
				newCSV("csv-root", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), nil, crds, nil),
			}

			_, teardown = createInternalCatalogSource(ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(), "test-catalog", generatedNamespace.GetName(), packages, crds, csvs)
			_, err := fetchCatalogSourceOnStatus(ctx.Ctx().OperatorClient(), "test-catalog", generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			Expect(err).NotTo(HaveOccurred())

			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), generatedNamespace.GetName(), "test-subscription", "test-catalog", "root", "channel-root", "", operatorsv1alpha1.ApprovalAutomatic)
		})

		AfterEach(func() {
			teardown()
		})

		It("should create a Subscription for the latest entry providing the required GVK", func() {
			Eventually(func() ([]operatorsv1alpha1.Subscription, error) {
				var list operatorsv1alpha1.SubscriptionList
				if err := ctx.Ctx().Client().List(context.Background(), &list); err != nil {
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
					CatalogSourceNamespace: generatedNamespace.GetName(),
					Package:                "dependency",
					Channel:                "channel-dependency",
					StartingCSV:            "csv-dependency-3",
				}),
			)))
		})
	})

	It("creation if not installed", func() {
		By(` I. Creating a new subscription`)
		By(`    A. If package is not installed, creating a subscription should install latest version`)

		defer func() {
			if env := os.Getenv("SKIP_CLEANUP"); env != "" {
				fmt.Printf("Skipping cleanup of subscriptions in namespace %s\n", generatedNamespace.GetName())
				return
			}
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		By("creating a catalog")
		require.NoError(GinkgoT(), initCatalog(GinkgoT(), generatedNamespace.GetName(), c, crc))

		By(fmt.Sprintf("creating a subscription: %s/%s", generatedNamespace.GetName(), testSubscriptionName))
		cleanup, _ := createSubscription(GinkgoT(), crc, generatedNamespace.GetName(), testSubscriptionName, testPackageName, betaChannel, operatorsv1alpha1.ApprovalAutomatic)

		defer cleanup()

		By("waiting for the subscription to have a current CSV and be at latest")
		var currentCSV string
		Eventually(func() bool {
			fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), testSubscriptionName, metav1.GetOptions{})
			if err != nil {
				return false
			}
			if fetched != nil {
				currentCSV = fetched.Status.CurrentCSV
				return fetched.Status.State == operatorsv1alpha1.SubscriptionStateAtLatest
			}
			return false
		}, 5*time.Minute, 10*time.Second).Should(BeTrue())

		csv, err := fetchCSV(crc, generatedNamespace.GetName(), currentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)

		By(`Check for the olm.package property as a proxy for`)
		By(`verifying that the annotation value is reasonable.`)
		Expect(
			projection.PropertyListFromPropertiesAnnotation(csv.GetAnnotations()["operatorframework.io/properties"]),
		).To(ContainElement(
			&registryapi.Property{Type: "olm.package", Value: `{"packageName":"myapp","version":"0.1.1"}`},
		))
	})

	It("creation using existing CSV", func() {
		By(`  I. Creating a new subscription`)
		By(`     B. If package is already installed, creating a subscription should upgrade it to the latest version`)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()
		require.NoError(GinkgoT(), initCatalog(GinkgoT(), generatedNamespace.GetName(), c, crc))

		By(`Will be cleaned up by the upgrade process`)
		_, err := createCSV(c, crc, stableCSV, generatedNamespace.GetName(), false, false)
		require.NoError(GinkgoT(), err)

		subscriptionCleanup, _ := createSubscription(GinkgoT(), crc, generatedNamespace.GetName(), testSubscriptionName, testPackageName, alphaChannel, operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), testSubscriptionName, subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)
	})
	It("skip range", func() {

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
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

		mainPackageName := genName("nginx-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		updatedPackageStable := fmt.Sprintf("%s-updated", mainPackageName)
		stableChannel := "stable"
		mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0-1556661347"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		updatedCSV := newCSV(updatedPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0-1556661832"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		updatedCSV.SetAnnotations(map[string]string{"olm.skipRange": ">=0.1.0-1556661347 <0.1.0-1556661832"})

		c := newKubeClient()
		crc := newCRClient()
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

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
		updatedManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: updatedPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Create catalog source`)
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		By(`Attempt to get the catalog source before creating subscription`)
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create a subscription`)
		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		By(`Wait for csv to install`)
		firstCSV, err := fetchCSV(crc, generatedNamespace.GetName(), mainCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Update catalog with a new csv in the channel with a skip range`)
		updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{updatedCSV}, updatedManifests)

		By(`Wait for csv to update`)
		finalCSV, err := fetchCSV(crc, generatedNamespace.GetName(), updatedCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure we set the replacement field based on the registry data`)
		require.Equal(GinkgoT(), firstCSV.GetName(), finalCSV.Spec.Replaces)
	})

	It("creation manual approval", func() {
		By(`If installPlanApproval is set to manual, the installplans created should be created with approval: manual`)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()
		require.NoError(GinkgoT(), initCatalog(GinkgoT(), generatedNamespace.GetName(), c, crc))

		subscriptionCleanup, _ := createSubscription(GinkgoT(), crc, generatedNamespace.GetName(), "manual-subscription", testPackageName, stableChannel, operatorsv1alpha1.ApprovalManual)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), "manual-subscription", subscriptionHasCondition(operatorsv1alpha1.SubscriptionInstallPlanPending, corev1.ConditionTrue, string(operatorsv1alpha1.InstallPlanPhaseRequiresApproval), ""))
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, subscription.Status.Install.Name, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval))
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), installPlan)

		require.Equal(GinkgoT(), operatorsv1alpha1.ApprovalManual, installPlan.Spec.Approval)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseRequiresApproval, installPlan.Status.Phase)

		By(`Delete the current installplan`)
		err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Delete(context.Background(), installPlan.Name, metav1.DeleteOptions{})
		require.NoError(GinkgoT(), err)

		var ipName string
		Eventually(func() bool {
			fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), "manual-subscription", metav1.GetOptions{})
			if err != nil {
				return false
			}
			if fetched.Status.Install != nil {
				ipName = fetched.Status.Install.Name
				return fetched.Status.Install.Name != installPlan.Name
			}
			return false
		}, 5*time.Minute, 10*time.Second).Should(BeTrue())

		By(`Fetch new installplan`)
		newInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, ipName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval))
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), newInstallPlan)

		require.NotEqual(GinkgoT(), installPlan.Name, newInstallPlan.Name, "expected new installplan recreated")
		require.Equal(GinkgoT(), operatorsv1alpha1.ApprovalManual, newInstallPlan.Spec.Approval)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseRequiresApproval, newInstallPlan.Status.Phase)

		By(`Set the InstallPlan's approved to True`)
		Eventually(Apply(newInstallPlan, func(p *operatorsv1alpha1.InstallPlan) error {

			p.Spec.Approved = true
			return nil
		})).Should(Succeed())

		subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), "manual-subscription", subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		_, err = fetchCSV(crc, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)
	})

	It("with starting CSV", func() {

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"

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

		By(`Create CSV`)
		packageName := genName("nginx-")
		stableChannel := "stable"

		csvA := newCSV("nginx-a", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		csvB := newCSV("nginx-b", generatedNamespace.GetName(), "nginx-a", semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)

		By(`Create PackageManifests`)
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Create the CatalogSource`)
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanupCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), operatorsv1alpha1.ApprovalManual)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.Install.Name

		By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
		requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval)
		fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), requiresApprovalChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure that only 1 installplan was created`)
		ips, err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).List(context.Background(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), ips.Items, 1)

		By(`Ensure that csvA and its crd are found in the plan`)
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

		By(`Ensure that csvB is not found in the plan`)
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

		By(`Approve the installplan and wait for csvA to be installed`)
		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Wait for the subscription to begin upgrading to csvB`)
		By(`The upgrade changes the installplanref on the subscription`)
		Eventually(func() (bool, error) {
			subscription, err = crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subscriptionName, metav1.GetOptions{})
			return subscription != nil && subscription.Status.InstallPlanRef.Name != fetchedInstallPlan.GetName() && subscription.Status.State == operatorsv1alpha1.SubscriptionStateUpgradePending, err
		}, 5*time.Minute, 1*time.Second).Should(BeTrue(), "expected new installplan for upgraded csv")

		upgradeInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, subscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), requiresApprovalChecker)
		require.NoError(GinkgoT(), err)

		By(`Approve the upgrade installplan and wait for`)
		upgradeInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Update(context.Background(), upgradeInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Ensure that 2 installplans were created`)
		ips, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).List(context.Background(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), ips.Items, 2)
	})

	It("[FLAKE] updates multiple intermediates", func() {
		By(`issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2635`)

		crd := newCRD("ins")

		By(`Create CSV`)
		packageName := genName("nginx-")
		stableChannel := "stable"

		csvA := newCSV("nginx-a", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		csvB := newCSV("nginx-b", generatedNamespace.GetName(), "nginx-a", semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		csvC := newCSV("nginx-c", generatedNamespace.GetName(), "nginx-b", semver.MustParse("0.3.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)

		By(`Create PackageManifests`)
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvA.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Create the CatalogSource with just one version`)
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA})
		defer cleanupCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), operatorsv1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		By(`Wait for csvA to be installed`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Set up async watches that will fail the test if csvB doesn't get created in between csvA and csvC`)
		var wg sync.WaitGroup
		go func(t GinkgoTInterface) {
			defer GinkgoRecover()
			wg.Add(1)
			defer wg.Done()
			_, err := fetchCSV(crc, generatedNamespace.GetName(), csvB.GetName(), csvReplacingChecker)
			require.NoError(GinkgoT(), err)
		}(GinkgoT())
		By(`Update the catalog to include multiple updates`)
		packages := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvC.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, catalogSourceName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB, csvC}, packages)

		By(`wait for checks on intermediate csvs to succeed`)
		wg.Wait()

		By(`Wait for csvC to be installed`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvC.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Should eventually GC the CSVs`)
		err = waitForCsvToDelete(generatedNamespace.GetName(), csvA.Name, crc)
		Expect(err).ShouldNot(HaveOccurred())

		err = waitForCsvToDelete(generatedNamespace.GetName(), csvB.Name, crc)
		Expect(err).ShouldNot(HaveOccurred())

		By(`TODO: check installplans, subscription status, etc`)
	})

	It("updates existing install plan", func() {
		By(`TestSubscriptionUpdatesExistingInstallPlan ensures that an existing InstallPlan has the appropriate approval requirement from Subscription.`)

		Skip("ToDo: This test was skipped before ginkgo conversion")

		By(`Create CSV`)
		packageName := genName("nginx-")
		stableChannel := "stable"

		csvA := newCSV("nginx-a", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, nil)
		csvB := newCSV("nginx-b", generatedNamespace.GetName(), "nginx-a", semver.MustParse("0.2.0"), nil, nil, nil)

		By(`Create PackageManifests`)
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvB.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By(`Create the CatalogSource with just one version`)
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogSourceName, generatedNamespace.GetName(), manifests, nil, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanupCatalogSource()

		By(`Attempt to get the catalog source before creating install plan`)
		_, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create a subscription to just get an InstallPlan for csvB`)
		subscriptionName := genName("sub-nginx-")
		createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, catalogSourceName, packageName, stableChannel, csvB.GetName(), operatorsv1alpha1.ApprovalAutomatic)

		By(`Wait for csvB to be installed`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, subscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))

		By(`Delete this subscription`)
		err = crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), *metav1.NewDeleteOptions(0), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		By(`Delete orphaned csvB`)
		require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.Background(), csvB.GetName(), metav1.DeleteOptions{}))

		By(`Create an InstallPlan for csvB`)
		ip := &operatorsv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "install-",
				Namespace:    generatedNamespace.GetName(),
			},
			Spec: operatorsv1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{csvB.GetName()},
				Approval:                   operatorsv1alpha1.ApprovalAutomatic,
				Approved:                   false,
			},
		}
		ip2, err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Create(context.Background(), ip, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		ip2.Status = operatorsv1alpha1.InstallPlanStatus{
			Plan:           fetchedInstallPlan.Status.Plan,
			CatalogSources: []string{catalogSourceName},
		}

		_, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).UpdateStatus(context.Background(), ip2, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		subscriptionName = genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), operatorsv1alpha1.ApprovalManual)
		defer cleanupSubscription()

		subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.Install.Name

		By(`Wait for InstallPlan to be status: Complete before checking resource presence`)
		requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseRequiresApproval)
		fetchedInstallPlan, err = fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), requiresApprovalChecker)
		require.NoError(GinkgoT(), err)

		By(`Approve the installplan and wait for csvA to be installed`)
		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Wait for csvA to be installed`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Wait for the subscription to begin upgrading to csvB`)
		subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionStateUpgradePendingChecker())
		require.NoError(GinkgoT(), err)

		By(`Fetch existing csvB installPlan`)
		fetchedInstallPlan, err = fetchInstallPlanWithNamespace(GinkgoT(), crc, subscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), requiresApprovalChecker)
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), ip2.GetName(), subscription.Status.InstallPlanRef.Name, "expected new installplan is the same with pre-exising one")

		By(`Approve the installplan and wait for csvB to be installed`)
		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		By(`Wait for csvB to be installed`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})

	Describe("puppeting CatalogSource health status", func() {
		var (
			getOpts    metav1.GetOptions
			deleteOpts *metav1.DeleteOptions
		)

		BeforeEach(func() {
			getOpts = metav1.GetOptions{}
			deleteOpts = &metav1.DeleteOptions{}
		})

		AfterEach(func() {
			err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		When("missing target catalog", func() {
			It("should surface the missing catalog", func() {
				By(`TestSubscriptionStatusMissingTargetCatalogSource ensures that a Subscription has the appropriate status condition when`)
				By(`its target catalog is missing.`)
				By(`			BySteps:`)
				By(`1. Generate an initial CatalogSource in the target namespace`)
				By(`2. Generate Subscription, "sub", targetting non-existent CatalogSource, "missing"`)
				By(`3. Wait for sub status to show SubscriptionCatalogSourcesUnhealthy with status True, reason CatalogSourcesUpdated, and appropriate missing message`)
				By(`4. Update sub's spec to target the "mysubscription"`)
				By(`5. Wait for sub's status to show SubscriptionCatalogSourcesUnhealthy with status False, reason AllCatalogSourcesHealthy, and reason "all available catalogsources are healthy"`)
				By(`6. Wait for sub to succeed`)
				err := initCatalog(GinkgoT(), generatedNamespace.GetName(), c, crc)
				Expect(err).NotTo(HaveOccurred())

				missingName := "missing"
				cleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), testSubscriptionName, missingName, testPackageName, betaChannel, "", operatorsv1alpha1.ApprovalAutomatic)
				defer cleanup()

				By("detecting its absence")
				sub, err := fetchSubscription(crc, generatedNamespace.GetName(), testSubscriptionName, subscriptionHasCondition(operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy, corev1.ConditionTrue, operatorsv1alpha1.UnhealthyCatalogSourceFound, fmt.Sprintf("targeted catalogsource %s/%s missing", generatedNamespace.GetName(), missingName)))
				Expect(err).NotTo(HaveOccurred())
				Expect(sub).ToNot(BeNil())

				By("updating the subscription to target an existing catsrc")
				Eventually(func() error {
					sub, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), testSubscriptionName, metav1.GetOptions{})
					if err != nil {
						return err
					}
					if sub == nil {
						return fmt.Errorf("subscription is nil")
					}
					sub.Spec.CatalogSource = catalogSourceName
					_, err = crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Update(context.Background(), sub, metav1.UpdateOptions{})
					return err
				}).Should(Succeed())

				By(`Wait for SubscriptionCatalogSourcesUnhealthy to be false`)
				By("detecting a new existing target")
				_, err = fetchSubscription(crc, generatedNamespace.GetName(), testSubscriptionName, subscriptionHasCondition(operatorsv1alpha1.SubscriptionCatalogSourcesUnhealthy, corev1.ConditionFalse, operatorsv1alpha1.AllCatalogSourcesHealthy, "all available catalogsources are healthy"))
				Expect(err).NotTo(HaveOccurred())

				By(`Wait for success`)
				_, err = fetchSubscription(crc, generatedNamespace.GetName(), testSubscriptionName, subscriptionStateAtLatestChecker())
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
							GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
								SecurityContextConfig: operatorsv1alpha1.Restricted,
							},
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(generatedNamespace.GetName()).Create(context.Background(), cs, metav1.CreateOptions{})
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

					By(`Get the latest CatalogSource`)
					cs, err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Get(context.Background(), cs.GetName(), getOpts)
					Expect(err).NotTo(HaveOccurred())
					Expect(cs).ToNot(BeNil())
				})
			})

			Context("is grpc and its spec is missing the address and image fields", func() {
				It("should surface catalog health", func() {
					By(`Create a CatalogSource pointing to the grpc pod`)
					cs := &operatorsv1alpha1.CatalogSource{
						TypeMeta: metav1.TypeMeta{
							Kind:       operatorsv1alpha1.CatalogSourceKind,
							APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      genName("cs-"),
							Namespace: generatedNamespace.GetName(),
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: operatorsv1alpha1.SourceTypeGrpc,
							GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
								SecurityContextConfig: operatorsv1alpha1.Restricted,
							},
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(generatedNamespace.GetName()).Create(context.Background(), cs, metav1.CreateOptions{})
					defer func() {
						err = crc.OperatorsV1alpha1().CatalogSources(cs.GetNamespace()).Delete(context.Background(), cs.GetName(), *deleteOpts)
						Expect(err).ToNot(HaveOccurred())
					}()

					By(`Wait for the CatalogSource status to be updated to reflect its invalid spec`)
					_, err = fetchCatalogSourceOnStatus(crc, cs.GetName(), cs.GetNamespace(), catalogSourceInvalidSpec)
					Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

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
							Namespace: generatedNamespace.GetName(),
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: operatorsv1alpha1.SourceTypeInternal,
							GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
								SecurityContextConfig: operatorsv1alpha1.Restricted,
							},
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(generatedNamespace.GetName()).Create(context.Background(), cs, metav1.CreateOptions{})
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
							Namespace: generatedNamespace.GetName(),
						},
						Spec: operatorsv1alpha1.CatalogSourceSpec{
							SourceType: operatorsv1alpha1.SourceTypeInternal,
							GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
								SecurityContextConfig: operatorsv1alpha1.Restricted,
							},
						},
					}

					var err error
					cs, err = crc.OperatorsV1alpha1().CatalogSources(generatedNamespace.GetName()).Create(context.Background(), cs, metav1.CreateOptions{})
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

	It("can reconcile InstallPlan status", func() {
		By(`TestSubscriptionInstallPlanStatus ensures that a Subscription has the appropriate status conditions for possible referenced InstallPlan states.`)
		c := newKubeClient()
		crc := newCRClient()

		By(`Create CatalogSource, cs, in ns`)
		pkgName := genName("pkg-")
		channelName := genName("channel-")
		crd := newCRD(pkgName)
		csv := newCSV(pkgName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
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
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalogName, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csv})
		defer cleanupCatalogSource()
		_, err := fetchCatalogSourceOnStatus(crc, catalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		Expect(err).ToNot(HaveOccurred())

		By(`Create Subscription to a package of cs in ns, sub`)
		subName := genName("sub-")
		defer createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subName, catalogName, pkgName, channelName, pkgName, operatorsv1alpha1.ApprovalAutomatic)()

		By(`Wait for the package from sub to install successfully with no remaining InstallPlan status conditions`)
		checker := subscriptionStateAtLatestChecker()
		sub, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			for _, cond := range s.Status.Conditions {
				switch cond.Type {
				case operatorsv1alpha1.SubscriptionInstallPlanMissing, operatorsv1alpha1.SubscriptionInstallPlanPending, operatorsv1alpha1.SubscriptionInstallPlanFailed:
					return false
				}
			}
			return checker(s)
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())

		By(`Store conditions for later comparision`)
		conds := sub.Status.Conditions

		ref := sub.Status.InstallPlanRef
		Expect(ref).ToNot(BeNil())

		By(`Get the InstallPlan`)
		plan := &operatorsv1alpha1.InstallPlan{}
		plan.SetNamespace(ref.Namespace)
		plan.SetName(ref.Name)

		By(`Set the InstallPlan's approval mode to Manual`)
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Spec.Approval = operatorsv1alpha1.ApprovalManual

			p.Spec.Approved = false
			return nil
		})).Should(Succeed())

		By(`Set the InstallPlan's phase to None`)
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseNone
			return nil
		})).Should(Succeed())

		By(`Wait for sub to have status condition SubscriptionInstallPlanPending true and reason InstallPlanNotYetReconciled`)
		sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanPending)
			return cond.Status == corev1.ConditionTrue && cond.Reason == operatorsv1alpha1.InstallPlanNotYetReconciled
		})
		Expect(err).ToNot(HaveOccurred())

		By(`Set the phase to InstallPlanPhaseRequiresApproval`)
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseRequiresApproval
			return nil
		})).Should(Succeed())

		By(`Wait for sub to have status condition SubscriptionInstallPlanPending true and reason RequiresApproval`)
		sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanPending)
			return cond.Status == corev1.ConditionTrue && cond.Reason == string(operatorsv1alpha1.InstallPlanPhaseRequiresApproval)
		})
		Expect(err).ToNot(HaveOccurred())

		By(`Set the phase to InstallPlanPhaseInstalling`)
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseInstalling
			return nil
		})).Should(Succeed())

		By(`Wait for sub to have status condition SubscriptionInstallPlanPending true and reason Installing`)
		sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanPending)
			isConditionPresent := cond.Status == corev1.ConditionTrue && cond.Reason == string(operatorsv1alpha1.InstallPlanPhaseInstalling)

			if isConditionPresent {
				return true
			}

			// Sometimes the transition from installing to complete can be so quick that the test does not capture
			// the condition in the subscription before it is removed. To mitigate this, we check if the installplan
			// has transitioned to complete and exit out the fetch subscription loop if so.
			// This is a mitigation. We should probably fix this test appropriately.
			// issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2667
			ip, err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.TODO(), plan.Name, metav1.GetOptions{})
			if err != nil {
				// retry on failure
				return false
			}
			isInstallPlanComplete := ip.Status.Phase == operatorsv1alpha1.InstallPlanPhaseComplete

			return isInstallPlanComplete
		})
		Expect(err).ToNot(HaveOccurred())

		By(`Set the phase to InstallPlanPhaseFailed and remove all status conditions`)
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseFailed
			p.Status.Conditions = nil
			return nil
		})).Should(Succeed())

		By(`Wait for sub to have status condition SubscriptionInstallPlanFailed true and reason InstallPlanFailed`)
		sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanFailed)
			return cond.Status == corev1.ConditionTrue && cond.Reason == operatorsv1alpha1.InstallPlanFailed
		})
		Expect(err).ToNot(HaveOccurred())

		By(`Set status condition of type Installed to false with reason InstallComponentFailed`)
		Eventually(Apply(plan, func(p *operatorsv1alpha1.InstallPlan) error {
			p.Status.Phase = operatorsv1alpha1.InstallPlanPhaseFailed
			failedCond := p.Status.GetCondition(operatorsv1alpha1.InstallPlanInstalled)
			failedCond.Status = corev1.ConditionFalse
			failedCond.Reason = operatorsv1alpha1.InstallPlanReasonComponentFailed
			p.Status.SetCondition(failedCond)
			return nil
		})).Should(Succeed())

		By(`Wait for sub to have status condition SubscriptionInstallPlanFailed true and reason InstallComponentFailed`)
		sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			cond := s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanFailed)
			return cond.Status == corev1.ConditionTrue && cond.Reason == string(operatorsv1alpha1.InstallPlanReasonComponentFailed)
		})
		Expect(err).ToNot(HaveOccurred())

		By(`Delete the referenced InstallPlan`)
		Eventually(func() error {
			return crc.OperatorsV1alpha1().InstallPlans(ref.Namespace).Delete(context.Background(), ref.Name, metav1.DeleteOptions{})
		}).Should(Succeed())

		By(`Wait for sub to have status condition SubscriptionInstallPlanMissing true`)
		sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, func(s *operatorsv1alpha1.Subscription) bool {
			return s.Status.GetCondition(operatorsv1alpha1.SubscriptionInstallPlanMissing).Status == corev1.ConditionTrue
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(sub).ToNot(BeNil())

		By(`Ensure InstallPlan-related status conditions match what we're expecting`)
		for _, cond := range conds {
			switch condType := cond.Type; condType {
			case operatorsv1alpha1.SubscriptionInstallPlanPending, operatorsv1alpha1.SubscriptionInstallPlanFailed:
				require.FailNowf(GinkgoT(), "failed", "subscription contains unexpected installplan condition: %v", cond)
			case operatorsv1alpha1.SubscriptionInstallPlanMissing:
				require.Equal(GinkgoT(), operatorsv1alpha1.ReferencedInstallPlanNotFound, cond.Reason)
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
			if apierrors.IsNotFound(getErr) {
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

		By(`Create a ConfigMap that is mounted to the operator via the subscription`)
		testConfigMapName := genName("test-configmap-")
		testConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: testConfigMapName,
			},
		}

		_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(generatedNamespace.GetName()).Create(context.Background(), testConfigMap, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(generatedNamespace.GetName()).Delete(context.Background(), testConfigMap.Name, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		By(`Configure the Subscription.`)

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
		catsrc, subSpec, catsrcCleanup := newCatalogSource(GinkgoT(), kubeClient, crClient, "podconfig", generatedNamespace.GetName(), permissions)
		defer catsrcCleanup()

		By(`Ensure that the catalog source is resolved before we create a subscription.`)
		_, err = fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("podconfig-sub-")
		subSpec.Config = podConfig
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		proxyEnv := proxyEnvVarFunc(GinkgoT(), config)
		expected := podEnv
		expected = append(expected, proxyEnv...)

		Eventually(func() error {
			csv, err := fetchCSV(crClient, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			if err != nil {
				return err
			}

			return checkDeploymentWithPodConfiguration(kubeClient, csv, podConfig.Env, podConfig.Volumes, podConfig.VolumeMounts, podConfig.Tolerations, podConfig.Resources)
		}).Should(Succeed())
	})

	It("creation with nodeSelector config", func() {
		kubeClient := newKubeClient()
		crClient := newCRClient()

		By(`Create a ConfigMap that is mounted to the operator via the subscription`)
		testConfigMapName := genName("test-configmap-")
		testConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: testConfigMapName,
			},
		}

		_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(generatedNamespace.GetName()).Create(context.Background(), testConfigMap, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		defer func() {
			err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(generatedNamespace.GetName()).Delete(context.Background(), testConfigMap.Name, metav1.DeleteOptions{})
			require.NoError(GinkgoT(), err)
		}()

		By(`Configure the Subscription.`)
		podNodeSelector := map[string]string{
			"foo": "bar",
		}

		podConfig := &operatorsv1alpha1.SubscriptionConfig{
			NodeSelector: podNodeSelector,
		}

		permissions := deploymentPermissions()
		catsrc, subSpec, catsrcCleanup := newCatalogSource(GinkgoT(), kubeClient, crClient, "podconfig", generatedNamespace.GetName(), permissions)
		defer catsrcCleanup()

		By(`Ensure that the catalog source is resolved before we create a subscription.`)
		_, err = fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("podconfig-sub-")
		subSpec.Config = podConfig
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		csv, err := fetchCSV(crClient, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseInstalling, operatorsv1alpha1.CSVPhaseSucceeded))
		require.NoError(GinkgoT(), err)

		Eventually(func() error {
			return checkDeploymentHasPodConfigNodeSelector(GinkgoT(), kubeClient, csv, podNodeSelector)
		}, timeout, interval).Should(Succeed())

	})

	It("[FLAKE] creation with dependencies", func() {

		kubeClient := newKubeClient()
		crClient := newCRClient()

		permissions := deploymentPermissions()

		catsrc, subSpec, catsrcCleanup := newCatalogSourceWithDependencies(GinkgoT(), kubeClient, crClient, "podconfig", generatedNamespace.GetName(), permissions)
		defer catsrcCleanup()

		By(`Ensure that the catalog source is resolved before we create a subscription.`)
		_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`Create duplicates of the CatalogSource`)
		for i := 0; i < 10; i++ {
			duplicateCatsrc, _, duplicateCatSrcCleanup := newCatalogSourceWithDependencies(GinkgoT(), kubeClient, crClient, "podconfig", generatedNamespace.GetName(), permissions)
			defer duplicateCatSrcCleanup()

			By(`Ensure that the catalog source is resolved before we create a subscription.`)
			_, err = fetchCatalogSourceOnStatus(crClient, duplicateCatsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)
		}

		By(`Create a subscription that has a dependency`)
		subscriptionName := genName("podconfig-sub-")
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		By(`Check that a single catalog source was used to resolve the InstallPlan`)
		installPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crClient, subscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), installPlan.Status.CatalogSources, 1)
	})

	It("creation with dependencies required and provided in different versions of an operator in the same package", func() {
		By(`	PARITY: this test covers the same scenario as the TestSolveOperators_PackageCannotSelfSatisfy unit test`)
		kubeClient := ctx.Ctx().KubeClient()
		crClient := ctx.Ctx().OperatorClient()

		crd := newCRD(genName("ins"))
		crd2 := newCRD(genName("ins"))

		By(`csvs for catalogsource 1`)
		csvs1 := make([]operatorsv1alpha1.ClusterServiceVersion, 0)

		By(`csvs for catalogsource 2`)
		csvs2 := make([]operatorsv1alpha1.ClusterServiceVersion, 0)

		testPackage := registry.PackageManifest{PackageName: "test-package"}
		By("Package A", func() {
			Step(1, "Default Channel: Stable", func() {
				testPackage.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator A (Requires CRD, CRD 2)", func() {
					csvA := newCSV("csv-a", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensionsv1.CustomResourceDefinition{crd, crd2}, nil)
					testPackage.
						Channels = append(testPackage.
						Channels, registry.PackageChannel{Name: stableChannel, CurrentCSVName: csvA.GetName()})
					csvs1 = append(csvs1, csvA)
				})
			})

			Step(1, "Channel Alpha", func() {
				Step(2, "Operator ABC (Provides: CRD, CRD 2)", func() {
					csvABC := newCSV("csv-abc", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd, crd2}, nil, nil)
					testPackage.
						Channels = append(testPackage.
						Channels, registry.PackageChannel{Name: alphaChannel, CurrentCSVName: csvABC.GetName()})
					csvs1 = append(csvs1, csvABC)
				})
			})
		})

		anotherPackage := registry.PackageManifest{PackageName: "another-package"}
		By("Package B", func() {
			Step(1, "Default Channel: Stable", func() {
				anotherPackage.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator B (Provides: CRD)", func() {
					csvB := newCSV("csv-b", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
					anotherPackage.Channels = append(anotherPackage.Channels, registry.PackageChannel{Name: stableChannel, CurrentCSVName: csvB.GetName()})
					csvs1 = append(csvs1, csvB)
				})
			})

			Step(1, "Channel Alpha", func() {
				Step(2, "Operator D (Provides: CRD)", func() {
					csvD := newCSV("csv-d", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
					anotherPackage.Channels = append(anotherPackage.Channels, registry.PackageChannel{Name: alphaChannel, CurrentCSVName: csvD.GetName()})
					csvs1 = append(csvs1, csvD)
				})
			})
		})

		packageBInCatsrc2 := registry.PackageManifest{PackageName: "another-package"}
		By("Package B", func() {
			Step(1, "Default Channel: Stable", func() {
				packageBInCatsrc2.DefaultChannelName = stableChannel
			})

			Step(1, "Channel Stable", func() {
				Step(2, "Operator C (Provides: CRD 2)", func() {
					csvC := newCSV("csv-c", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd2}, nil, nil)
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
					csvE := newCSV("csv-e", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd2}, nil, nil)
					packageC.Channels = append(packageC.Channels, registry.PackageChannel{Name: stable, CurrentCSVName: csvE.GetName()})
					csvs2 = append(csvs2, csvE)
				})
			})
		})

		By(`create catalogsources`)
		var catsrc, catsrc2 *operatorsv1alpha1.CatalogSource
		var cleanup cleanupFunc
		By("creating catalogsources", func() {
			var c1, c2 cleanupFunc
			catsrc, c1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(), []registry.PackageManifest{testPackage, anotherPackage}, []apiextensionsv1.CustomResourceDefinition{crd, crd2}, csvs1)
			catsrc2, c2 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc2"), generatedNamespace.GetName(), []registry.PackageManifest{packageBInCatsrc2, packageC}, []apiextensionsv1.CustomResourceDefinition{crd, crd2}, csvs2)
			cleanup = func() {
				c1()
				c2()
			}
		})
		defer cleanup()

		By("waiting for catalogsources to be ready", func() {
			_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)
			_, err = fetchCatalogSourceOnStatus(crClient, catsrc2.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)
		})

		By(`Create a subscription for test-package in catsrc`)
		subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catsrc.GetName(),
			CatalogSourceNamespace: catsrc.GetNamespace(),
			Package:                testPackage.PackageName,
			Channel:                stableChannel,
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
		}
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subscriptionSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		By(`ensure correct CSVs were picked`)
		var got []string
		Eventually(func() []string {
			ip, err := crClient.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.Background(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
			if err != nil {
				return nil
			}
			got = ip.Spec.ClusterServiceVersionNames
			return got
		}).ShouldNot(BeNil())
		require.ElementsMatch(GinkgoT(), []string{"csv-a", "csv-b", "csv-e"}, got)
	})

	Context("to an operator with dependencies from different CatalogSources with priorities", func() {
		var (
			kubeClient                                    operatorclient.ClientInterface
			crClient                                      versioned.Interface
			crd                                           apiextensionsv1.CustomResourceDefinition
			packageMain, packageDepRight, packageDepWrong registry.PackageManifest
			csvsMain, csvsRight, csvsWrong                []operatorsv1alpha1.ClusterServiceVersion
			catsrcMain, catsrcDepRight, catsrcDepWrong    *operatorsv1alpha1.CatalogSource
			cleanup, cleanupSubscription                  cleanupFunc
		)
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
			csv := newCSV(mainCSVName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil,
				[]apiextensionsv1.CustomResourceDefinition{crd}, nil)
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
				csv := newCSV(rightCSVName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"),
					[]apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsRight = append(csvsRight, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(),
					[]registry.PackageManifest{packageMain}, nil, csvsMain)

				catsrcDepRight, catsrcCleanup2 = createInternalCatalogSource(kubeClient, crClient, "catsrc1", generatedNamespace.GetName(),
					[]registry.PackageManifest{packageDepRight}, []apiextensionsv1.CustomResourceDefinition{crd}, csvsRight)

				catsrcDepWrong, catsrcCleanup3 = createInternalCatalogSource(kubeClient, crClient, "catsrc2", generatedNamespace.GetName(),
					[]registry.PackageManifest{packageDepWrong}, []apiextensionsv1.CustomResourceDefinition{crd}, csvsWrong)

				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepRight.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
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
					By(`Create a subscription for test-package in catsrc`)
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, generatedNamespace.GetName(), mainCSVName, csvSucceededChecker)
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

				It("[FLAKE] choose the dependency from the right CatalogSource based on lexicographical name ordering of catalogs", func() {
					By(`ensure correct CSVs were picked`)
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.Background(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
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
				csv := newCSV(rightCSVName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"),
					[]apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsMain = append(csvsMain, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(),
					[]registry.PackageManifest{packageDepRight, packageMain}, []apiextensionsv1.CustomResourceDefinition{crd}, csvsMain)

				catsrcDepWrong, catsrcCleanup2 = createInternalCatalogSourceWithPriority(kubeClient, crClient,
					genName("catsrc"), generatedNamespace.GetName(), []registry.PackageManifest{packageDepWrong}, []apiextensionsv1.CustomResourceDefinition{crd},
					csvsWrong, 100)

				By(`waiting for catalogsources to be ready`)
				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
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
					By(`Create a subscription for test-package in catsrc`)
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, generatedNamespace.GetName(), mainCSVName, csvSucceededChecker)
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
					By(`ensure correct CSVs were picked`)
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.Background(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
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
				csv := newCSV(rightCSVName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"),
					[]apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsRight = append(csvsRight, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(),
					[]registry.PackageManifest{packageMain}, nil, csvsMain)

				catsrcDepRight, catsrcCleanup2 = createInternalCatalogSourceWithPriority(kubeClient, crClient,
					genName("catsrc"), generatedNamespace.GetName(), []registry.PackageManifest{packageDepRight}, []apiextensionsv1.CustomResourceDefinition{crd},
					csvsRight, 100)

				catsrcDepWrong, catsrcCleanup3 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(),
					[]registry.PackageManifest{packageDepWrong}, []apiextensionsv1.CustomResourceDefinition{crd}, csvsWrong)

				By(`waiting for catalogsources to be ready`)
				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepRight.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
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
					By(`Create a subscription for test-package in catsrc`)
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, generatedNamespace.GetName(), mainCSVName, csvSucceededChecker)
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
					By(`ensure correct CSVs were picked`)
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.Background(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
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
				csv := newCSV(rightCSVName, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"),
					[]apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
				packageDepRight.DefaultChannelName = alphaChannel
				packageDepRight.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsRight = append(csvsRight, csv)

				csv.Name = wrongCSVName
				packageDepWrong = packageDepRight
				packageDepWrong.Channels = []registry.PackageChannel{{Name: alphaChannel, CurrentCSVName: csv.GetName()}}
				csvsWrong = append(csvsWrong, csv)

				catsrcMain, catsrcCleanup1 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(),
					[]registry.PackageManifest{packageMain}, nil, csvsMain)

				catsrcDepRight, catsrcCleanup2 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), generatedNamespace.GetName(),
					[]registry.PackageManifest{packageDepRight}, []apiextensionsv1.CustomResourceDefinition{crd}, csvsRight)

				catsrcDepWrong, catsrcCleanup3 = createInternalCatalogSource(kubeClient, crClient, genName("catsrc"), operatorNamespace,
					[]registry.PackageManifest{packageDepWrong}, []apiextensionsv1.CustomResourceDefinition{crd}, csvsWrong)

				By(`waiting for catalogsources to be ready`)
				_, err := fetchCatalogSourceOnStatus(crClient, catsrcMain.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepRight.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
				Expect(err).ToNot(HaveOccurred())
				_, err = fetchCatalogSourceOnStatus(crClient, catsrcDepWrong.GetName(), operatorNamespace, catalogSourceRegistryPodSynced())
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
					By(`Create a subscription for test-package in catsrc`)
					subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
						CatalogSource:          catsrcMain.GetName(),
						CatalogSourceNamespace: catsrcMain.GetNamespace(),
						Package:                packageMain.PackageName,
						Channel:                stableChannel,
						InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
					}
					subscriptionName := genName("sub-")
					cleanupSubscription = createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subscriptionSpec)

					var err error
					subscription, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
					Expect(err).ToNot(HaveOccurred())
					Expect(subscription).ToNot(BeNil())

					_, err = fetchCSV(crClient, generatedNamespace.GetName(), mainCSVName, csvSucceededChecker)
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
					By(`ensure correct CSVs were picked`)
					Eventually(func() ([]string, error) {
						ip, err := crClient.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.Background(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
						if err != nil {
							return nil, err
						}
						return ip.Spec.ClusterServiceVersionNames, nil
					}).Should(ConsistOf(mainCSVName, rightCSVName))
				})
			})
		})
	})

	It("creation in case of transferring providedAPIs", func() {
		By(`csvA owns CRD1 & csvB owns CRD2 and requires CRD1`)
		By(`Create subscription for csvB lead to installation of csvB and csvA`)
		By(`Update catsrc to upgrade csvA to csvNewA which now requires CRD1`)
		By(`csvNewA can't be installed due to no other operators provide CRD1 for it`)
		By(`(Note: OLM can't pick csvA as dependency for csvNewA as it is from the same`)
		By(`same package)`)
		By(`Update catsrc again to upgrade csvB to csvNewB which now owns both CRD1 and`)
		By(`CRD2.`)
		By(`Now csvNewA and csvNewB are installed successfully as csvNewB provides CRD1`)
		By(`that csvNewA requires`)
		By(`	PARITY: this test covers the same scenario as the TestSolveOperators_TransferApiOwnership unit test`)
		kubeClient := ctx.Ctx().KubeClient()
		crClient := ctx.Ctx().OperatorClient()

		crd := newCRD(genName("ins"))
		crd2 := newCRD(genName("ins"))

		By(`Create CSV`)
		packageName1 := genName("apackage")
		packageName2 := genName("bpackage")

		By(`csvA provides CRD`)
		csvA := newCSV("nginx-a", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		By(`csvB provides CRD2 and requires CRD`)
		csvB := newCSV("nginx-b", generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd2}, []apiextensionsv1.CustomResourceDefinition{crd}, nil)
		By(`New csvA requires CRD (transfer CRD ownership to the new csvB)`)
		csvNewA := newCSV("nginx-new-a", generatedNamespace.GetName(), "nginx-a", semver.MustParse("0.2.0"), nil, []apiextensionsv1.CustomResourceDefinition{crd}, nil)
		By(`New csvB provides CRD and CRD2`)
		csvNewB := newCSV("nginx-new-b", generatedNamespace.GetName(), "nginx-b", semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{crd, crd2}, nil, nil)

		By(`constraints not satisfiable:`)
		By(`apackagert6cq requires at least one of catsrcc6xgr/operators/stable/nginx-new-a,`)
		By(`apackagert6cq is mandatory,`)
		By(`pkgunique/apackagert6cq permits at most 1 of catsrcc6xgr/operators/stable/nginx-new-a, catsrcc6xgr/operators/stable/nginx-a,`)
		By(`catsrcc6xgr/operators/stable/nginx-new-a requires at least one of catsrcc6xgr/operators/stable/nginx-a`)

		By(`Create PackageManifests 1`)
		By(`Contain csvA, ABC and B`)
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
		catsrc, cleanup := createInternalCatalogSource(kubeClient, crClient, catalogSourceName, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd, crd2}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB})
		defer cleanup()

		By(`Ensure that the catalog source is resolved before we create a subscription.`)
		_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catsrc.GetName(),
			CatalogSourceNamespace: catsrc.GetNamespace(),
			Package:                packageName2,
			Channel:                stableChannel,
			StartingCSV:            csvB.GetName(),
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
		}

		By(`Create a subscription that has a dependency`)
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient, generatedNamespace.GetName(), subscriptionName, subscriptionSpec)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		By(`Check that a single catalog source was used to resolve the InstallPlan`)
		_, err = fetchInstallPlanWithNamespace(GinkgoT(), crClient, subscription.Status.InstallPlanRef.Name, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		By(`Fetch CSVs A and B`)
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvA.Name, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvB.Name, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Update PackageManifest`)
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
		updateInternalCatalog(GinkgoT(), kubeClient, crClient, catalogSourceName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd, crd2}, []operatorsv1alpha1.ClusterServiceVersion{csvNewA, csvA, csvB}, manifests)
		csvAsub := strings.Join([]string{packageName1, stableChannel, catalogSourceName, generatedNamespace.GetName()}, "-")
		_, err = fetchSubscription(crClient, generatedNamespace.GetName(), csvAsub, subscriptionStateAtLatestChecker())
		require.NoError(GinkgoT(), err)
		By(`Ensure csvNewA is not installed`)
		_, err = crClient.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Get(context.Background(), csvNewA.Name, metav1.GetOptions{})
		require.Error(GinkgoT(), err)
		By(`Ensure csvA still exists`)
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvA.Name, csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Update packagemanifest again`)
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
		updateInternalCatalog(GinkgoT(), kubeClient, crClient, catalogSourceName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd, crd2}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB, csvNewA, csvNewB}, manifests)

		_, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(subscription.Status.InstallPlanRef.Name))
		require.NoError(GinkgoT(), err)
		By(`Ensure csvNewA is installed`)
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvNewA.Name, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
		By(`Ensure csvNewB is installed`)
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvNewB.Name, csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})

	When("A subscription is created for an operator that requires an API that is not available", func() {
		var (
			c          operatorclient.ClientInterface
			crc        versioned.Interface
			teardown   func()
			cleanup    func()
			packages   []registry.PackageManifest
			crd        = newCRD(genName("foo-"))
			csvA       operatorsv1alpha1.ClusterServiceVersion
			csvB       operatorsv1alpha1.ClusterServiceVersion
			subName    = genName("test-subscription-")
			catSrcName = genName("test-catalog-")
		)

		BeforeEach(func() {
			c = newKubeClient()
			crc = newCRClient()

			packages = []registry.PackageManifest{
				{
					PackageName: "test-package",
					Channels: []registry.PackageChannel{
						{Name: "alpha", CurrentCSVName: "csvA"},
					},
					DefaultChannelName: "alpha",
				},
			}
			csvA = newCSV("csvA", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), nil, []apiextensionsv1.CustomResourceDefinition{crd}, nil)

			_, teardown = createInternalCatalogSource(c, ctx.Ctx().OperatorClient(), catSrcName, generatedNamespace.GetName(), packages, nil, []operatorsv1alpha1.ClusterServiceVersion{csvA})

			By(`Ensure that the catalog source is resolved before we create a subscription.`)
			_, err := fetchCatalogSourceOnStatus(crc, catSrcName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
			require.NoError(GinkgoT(), err)

			cleanup = createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subName, catSrcName, "test-package", "alpha", "", operatorsv1alpha1.ApprovalAutomatic)
		})

		AfterEach(func() {
			cleanup()
			teardown()
		})

		It("the subscription has a condition in it's status that indicates the resolution error", func() {
			Eventually(func() (corev1.ConditionStatus, error) {
				sub, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
				if err != nil {
					return corev1.ConditionUnknown, err
				}
				return sub.Status.GetCondition(operatorsv1alpha1.SubscriptionResolutionFailed).Status, nil
			}).Should(Equal(corev1.ConditionTrue))
		})

		When("the required API is made available", func() {

			BeforeEach(func() {
				newPkg := registry.PackageManifest{
					PackageName: "another-package",
					Channels: []registry.PackageChannel{
						{Name: "alpha", CurrentCSVName: "csvB"},
					},
					DefaultChannelName: "alpha",
				}
				packages = append(packages, newPkg)

				csvB = newCSV("csvB", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)

				updateInternalCatalog(GinkgoT(), c, crc, catSrcName, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB}, packages)
			})

			It("the ResolutionFailed condition previously set in its status that indicated the resolution error is cleared off", func() {
				Eventually(func() (corev1.ConditionStatus, error) {
					sub, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
					if err != nil {
						return corev1.ConditionFalse, err
					}
					return sub.Status.GetCondition(operatorsv1alpha1.SubscriptionResolutionFailed).Status, nil
				}).Should(Equal(corev1.ConditionUnknown))
			})
		})
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

			x := newCSV("csv-x", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), nil, nil, nil)
			y := newCSV("csv-y", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), nil, nil, nil)

			_, teardown = createInternalCatalogSource(ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(), "test-catalog", generatedNamespace.GetName(), packages, nil, []operatorsv1alpha1.ClusterServiceVersion{x, y})

			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), generatedNamespace.GetName(), "test-subscription-x", "test-catalog", "package", "channel-x", "", operatorsv1alpha1.ApprovalAutomatic)

			Eventually(func() error {
				var unannotated operatorsv1alpha1.ClusterServiceVersion
				if err := ctx.Ctx().Client().Get(context.Background(), client.ObjectKey{Namespace: generatedNamespace.GetName(), Name: "csv-x"}, &unannotated); err != nil {
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
			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), generatedNamespace.GetName(), "test-subscription-y", "test-catalog", "package", "channel-y", "", operatorsv1alpha1.ApprovalAutomatic)

			Consistently(func() error {
				var no operatorsv1alpha1.ClusterServiceVersion
				return ctx.Ctx().Client().Get(context.Background(), client.ObjectKey{Namespace: generatedNamespace.GetName(), Name: "csv-y"}, &no)
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

			crds := []apiextensionsv1.CustomResourceDefinition{newCRD(genName("crd-"))}
			csvs := []operatorsv1alpha1.ClusterServiceVersion{
				newCSV("csv-dependency", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), crds, nil, nil),
				newCSV("csv-root", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), nil, crds, nil),
			}

			_, teardown = createInternalCatalogSource(ctx.Ctx().KubeClient(), ctx.Ctx().OperatorClient(), "test-catalog", generatedNamespace.GetName(), packages, crds, csvs)

			createSubscriptionForCatalog(ctx.Ctx().OperatorClient(), generatedNamespace.GetName(), "test-subscription", "test-catalog", "root", "unimportant", "", operatorsv1alpha1.ApprovalAutomatic)
		})

		AfterEach(func() {
			teardown()
		})

		It("should create a Subscription using the candidate's default channel", func() {
			Eventually(func() ([]operatorsv1alpha1.Subscription, error) {
				var list operatorsv1alpha1.SubscriptionList
				if err := ctx.Ctx().Client().List(context.Background(), &list); err != nil {
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
					CatalogSourceNamespace: generatedNamespace.GetName(),
					Package:                "dependency",
					Channel:                "default",
				}),
			)))
		})
	})

	It("unpacks bundle image", func() {
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
		require.NoError(GinkgoT(), err)
		defer func() {
			Eventually(func() error {
				return client.IgnoreNotFound(ctx.Ctx().Client().Delete(context.Background(), catsrc))
			}).Should(Succeed())
		}()

		By("waiting for the CatalogSource to be ready")
		catsrc, err = fetchCatalogSourceOnStatus(crc, catsrc.GetName(), catsrc.GetNamespace(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By("generating a Subscription")
		subName := genName("kiali-")
		cleanUpSubscriptionFn := createSubscriptionForCatalog(crc, catsrc.GetNamespace(), subName, catsrc.GetName(), "kiali", stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer cleanUpSubscriptionFn()

		By("waiting for the InstallPlan to get created for the subscription")
		sub, err := fetchSubscription(crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker())
		require.NoError(GinkgoT(), err)

		By("waiting for the expected InstallPlan's execution to either fail or succeed")
		ipName := sub.Status.InstallPlanRef.Name
		ip, err := waitForInstallPlan(crc, ipName, sub.GetNamespace(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed, operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, ip.Status.Phase, "InstallPlan not complete")

		By("ensuring the InstallPlan contains the steps resolved from the bundle image")
		operatorName := "kiali-operator"
		expectedSteps := map[registry.ResourceKey]struct{}{
			{Name: operatorName, Kind: "ClusterServiceVersion"}:                                  {},
			{Name: "kialis.kiali.io", Kind: "CustomResourceDefinition"}:                          {},
			{Name: "monitoringdashboards.monitoring.kiali.io", Kind: "CustomResourceDefinition"}: {},
			{Name: operatorName, Kind: "ServiceAccount"}:                                         {},
			{Name: operatorName, Kind: "ClusterRole"}:                                            {},
			{Name: operatorName, Kind: "ClusterRoleBinding"}:                                     {},
		}
		require.Lenf(GinkgoT(), ip.Status.Plan, len(expectedSteps), "number of expected steps does not match installed: %v", ip.Status.Plan)

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
		require.Lenf(GinkgoT(), expectedSteps, 0, "Actual resource steps do not match expected: %#v", expectedSteps)
	})

	When("unpacking bundle", func() {
		var (
			magicCatalog      *MagicCatalog
			catalogSourceName string
			subName           string
		)

		BeforeEach(func() {
			By("deploying the testing catalog")
			provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.1.0.yaml"))
			Expect(err).To(BeNil())
			catalogSourceName = fmt.Sprintf("%s-catsrc", generatedNamespace.GetName())
			magicCatalog = NewMagicCatalog(ctx.Ctx().Client(), generatedNamespace.GetName(), catalogSourceName, provider)
			Expect(magicCatalog.DeployCatalog(context.Background())).To(BeNil())

			By("creating the testing subscription")
			subName = fmt.Sprintf("%s-test-package-sub", generatedNamespace.GetName())
			createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subName, catalogSourceName, "test-package", stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasInstallPlanChecker())
			Expect(err).Should(BeNil())

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crc, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())
		})

		It("should not report unpacking progress or errors after successfull unpacking", func() {
			By("verifying that the subscription is not reporting unpacking progress")
			Eventually(
				func() (corev1.ConditionStatus, error) {
					fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpacking)
					return cond.Status, nil
				},
				5*time.Minute,
				interval,
			).Should(Equal(corev1.ConditionUnknown))

			By("verifying that the subscription is not reporting unpacking errors")
			Eventually(
				func() (corev1.ConditionStatus, error) {
					fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
					if err != nil {
						return "", err
					}
					cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpackFailed)
					return cond.Status, nil
				},
				5*time.Minute,
				interval,
			).Should(Equal(corev1.ConditionUnknown))
		})

		Context("with bundle which OLM will fail to unpack", func() {
			BeforeEach(func() {
				By("patching the OperatorGroup to reduce the bundle unpacking timeout")
				ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
				addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "1s")

				By("updating the catalog with a broken v0.2.0 bundle image")
				brokenProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.2.0-non-existent-tag.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), brokenProvider)
				Expect(err).To(BeNil())
			})

			It("should expose a condition indicating failure to unpack", func() {
				By("verifying that the subscription is reporting bundle unpack failure condition")
				Eventually(
					func() (string, error) {
						fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
						if err != nil {
							return "", err
						}
						cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpackFailed)
						if cond.Status != corev1.ConditionTrue || cond.Reason != "BundleUnpackFailed" {
							return "", fmt.Errorf("%s condition not found", operatorsv1alpha1.SubscriptionBundleUnpackFailed)
						}

						return cond.Message, nil
					},
					5*time.Minute,
					interval,
				).Should(ContainSubstring("bundle unpacking failed. Reason: DeadlineExceeded"))

				By("waiting for the subscription to maintain the example-operator.v0.1.0 status.currentCSV")
				Consistently(subscriptionCurrentCSVGetter(crc, generatedNamespace.GetName(), subName)).Should(Equal("example-operator.v0.1.0"))
			})

			It("should be able to recover when catalog gets updated with a fixed version", func() {
				By("patching the OperatorGroup to reduce the bundle unpacking timeout")
				ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
				addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "5m")

				By("updating the catalog with a fixed v0.2.0 bundle image")
				brokenProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.2.0.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), brokenProvider)
				Expect(err).To(BeNil())

				By("waiting for the subscription to have v0.2.0 installed")
				_, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasCurrentCSV("example-operator.v0.2.0"))
				Expect(err).Should(BeNil())
			})

			It("should report deprecation conditions when package, channel, and bundle are referenced in an olm.deprecations object", func() {
				By("patching the OperatorGroup to reduce the bundle unpacking timeout")
				ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
				addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "5m")

				By("updating the catalog with a fixed v0.2.0 bundle image marked deprecated")
				provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.2.0-deprecations.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), provider)
				Expect(err).To(BeNil())

				By("waiting for the subscription to have v0.2.0 installed with a Bundle Deprecated condition")
				sub, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasCondition(
					operatorsv1alpha1.SubscriptionBundleDeprecated,
					corev1.ConditionTrue,
					"",
					"olm.bundle/example-operator.v0.2.0: bundle \"example-operator.v0.2.0\" has been deprecated. Please switch to a different one."))
				Expect(err).Should(BeNil())

				By("checking for the deprecated conditions")
				By(`Operator is deprecated at all three levels in the catalog`)
				packageCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionPackageDeprecated)
				Expect(packageCondition.Status).To(Equal(corev1.ConditionTrue))
				channelCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionChannelDeprecated)
				Expect(channelCondition.Status).To(Equal(corev1.ConditionTrue))
				bundleCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleDeprecated)
				Expect(bundleCondition.Status).To(Equal(corev1.ConditionTrue))

				By("verifying that a roll-up condition is present containing all deprecation conditions")
				By(`Roll-up condition should be present and contain deprecation messages from all three levels`)
				rollUpCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionDeprecated)
				Expect(rollUpCondition.Status).To(Equal(corev1.ConditionTrue))
				Expect(rollUpCondition.Message).To(ContainSubstring(packageCondition.Message))
				Expect(rollUpCondition.Message).To(ContainSubstring(channelCondition.Message))
				Expect(rollUpCondition.Message).To(ContainSubstring(bundleCondition.Message))

				By("updating the catalog with a fixed v0.3.0 bundle image no longer marked deprecated")
				provider, err = NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.3.0.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), provider)
				Expect(err).To(BeNil())

				By("waiting for the subscription to have v0.3.0 installed with no deprecation message present")
				_, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionDoesNotHaveCondition(operatorsv1alpha1.SubscriptionDeprecated))
				Expect(err).Should(BeNil())
			})

			It("[FLAKE] should report only package and channel deprecation conditions when bundle is no longer deprecated", func() {
				By("patching the OperatorGroup to reduce the bundle unpacking timeout")
				ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
				addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "5m")

				By("updating the catalog with a fixed v0.2.0 bundle image marked deprecated at all levels")
				provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.2.0-deprecations.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), provider)
				Expect(err).To(BeNil())

				By("waiting for the subscription to have v0.2.0 installed with a Bundle Deprecated condition")
				sub, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasCondition(
					operatorsv1alpha1.SubscriptionBundleDeprecated,
					corev1.ConditionTrue,
					"",
					"olm.bundle/example-operator.v0.2.0: bundle \"example-operator.v0.2.0\" has been deprecated. Please switch to a different one."))
				Expect(err).Should(BeNil())

				By("checking for the bundle deprecated condition")
				bundleCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleDeprecated)
				Expect(bundleCondition.Status).To(Equal(corev1.ConditionTrue))
				bundleDeprecatedMessage := bundleCondition.Message

				By("updating the catalog with a fixed v0.3.0 bundle marked partially deprecated")
				provider, err = NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.3.0-deprecations.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), provider)
				Expect(err).To(BeNil())

				By("waiting for the subscription to switch to v0.3.0")
				sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasCurrentCSV("example-operator.v0.3.0"))
				Expect(err).Should(BeNil())

				By("waiting for the subscription to have be at latest known")
				sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionStateAtLatestChecker())
				Expect(err).Should(BeNil())

				By("waiting for the install plan pending to go away")
				sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName,
					subscriptionHasCondition(
						operatorsv1alpha1.SubscriptionInstallPlanPending,
						corev1.ConditionUnknown,
						"",
						"",
					),
				)
				Expect(err).Should(BeNil())

				By("waiting for the subscription to have v0.3.0 installed without a bundle deprecated condition")
				sub, err = fetchSubscription(crc, generatedNamespace.GetName(), subName,
					subscriptionHasCondition(
						operatorsv1alpha1.SubscriptionBundleDeprecated,
						corev1.ConditionUnknown,
						"",
						"",
					),
				)
				Expect(err).Should(BeNil())

				By("checking for the deprecated conditions")
				By(`Operator is deprecated at only Package and Channel levels`)
				packageCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionPackageDeprecated)
				Expect(packageCondition.Status).To(Equal(corev1.ConditionTrue))
				channelCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionChannelDeprecated)
				Expect(channelCondition.Status).To(Equal(corev1.ConditionTrue))

				By("verifying that a roll-up condition is present not containing bundle deprecation condition")
				By(`Roll-up condition should be present and contain deprecation messages from Package and Channel levels`)
				rollUpCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionDeprecated)
				Expect(rollUpCondition.Status).To(Equal(corev1.ConditionTrue))
				Expect(rollUpCondition.Message).To(ContainSubstring(packageCondition.Message))
				Expect(rollUpCondition.Message).To(ContainSubstring(channelCondition.Message))
				Expect(rollUpCondition.Message).ToNot(ContainSubstring(bundleDeprecatedMessage))
			})

			It("should report deprecated status when catalog is updated to deprecate an installed bundle", func() {
				By("patching the OperatorGroup to reduce the bundle unpacking timeout")
				ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
				addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "5m")

				By("updating the catalog with a fixed v0.2.0 bundle not marked deprecated")
				provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.2.0.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), provider)
				Expect(err).To(BeNil())

				By("waiting for the subscription to have v0.2.0 installed")
				sub, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasCurrentCSV("example-operator.v0.2.0"))
				Expect(err).Should(BeNil())

				By("the subscription should not be marked deprecated")
				rollupCondition := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionDeprecated)
				Expect(rollupCondition.Status).To(Equal(corev1.ConditionUnknown))

				By("updating the catalog with a fixed v0.2.0 bundle image marked deprecated at all levels")
				provider, err = NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.2.0-deprecations.yaml"))
				Expect(err).To(BeNil())
				err = magicCatalog.UpdateCatalog(context.Background(), provider)
				Expect(err).To(BeNil())

				By("checking for the bundle deprecated condition")
				Eventually(func() (bool, error) {
					sub, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasCurrentCSV("example-operator.v0.2.0"))
					if err != nil {
						return false, err
					}
					cond := sub.Status.GetCondition(operatorsv1alpha1.SubscriptionDeprecated)
					if cond.Status != corev1.ConditionTrue {
						return false, fmt.Errorf("%s condition not found", operatorsv1alpha1.SubscriptionDeprecated)
					}

					return true, nil
				}, 2*time.Minute, time.Second*10).Should(BeTrue())
			})
		})
	})
	When("bundle unpack retries are enabled", func() {
		It("should retry failing unpack jobs", func() {
			if ok, err := inKind(c); ok && err == nil {
				Skip("This spec fails when run using KIND cluster. See https://github.com/operator-framework/operator-lifecycle-manager/issues/2420 for more details")
			} else if err != nil {
				Skip("Could not determine whether running in a kind cluster. Skipping.")
			}
			By("Ensuring a registry to host bundle images")
			local, err := Local(c)
			Expect(err).NotTo(HaveOccurred(), "cannot determine if test running locally or on CI: %s", err)

			var registryURL string
			var copyImage func(dst, dstTag, src, srcTag string) error
			if local {
				registryURL, err = createDockerRegistry(c, generatedNamespace.GetName())
				Expect(err).NotTo(HaveOccurred(), "error creating container registry: %s", err)
				defer deleteDockerRegistry(c, generatedNamespace.GetName())

				By(`ensure registry pod is ready before attempting port-forwarding`)
				_ = awaitPod(GinkgoT(), c, generatedNamespace.GetName(), registryName, podReady)

				err = registryPortForward(generatedNamespace.GetName())
				Expect(err).NotTo(HaveOccurred(), "port-forwarding local registry: %s", err)
				copyImage = func(dst, dstTag, src, srcTag string) error {
					if !strings.HasPrefix(src, "docker://") {
						src = fmt.Sprintf("docker://%s", src)
					}
					if !strings.HasPrefix(dst, "docker://") {
						dst = fmt.Sprintf("docker://%s", dst)
					}
					_, err := skopeoLocalCopy(dst, dstTag, src, srcTag)
					return err
				}
			} else {
				registryURL = fmt.Sprintf("%s/%s", openshiftregistryFQDN, generatedNamespace.GetName())
				registryAuthSecretName, err := getRegistryAuthSecretName(c, generatedNamespace.GetName())
				Expect(err).NotTo(HaveOccurred(), "error getting openshift registry authentication: %s", err)
				copyImage = func(dst, dstTag, src, srcTag string) error {
					if !strings.HasPrefix(src, "docker://") {
						src = fmt.Sprintf("docker://%s", src)
					}
					if !strings.HasPrefix(dst, "docker://") {
						dst = fmt.Sprintf("docker://%s", dst)
					}
					skopeoArgs := skopeoCopyCmd(dst, dstTag, src, srcTag, registryAuthSecretName)
					err = createSkopeoPod(c, skopeoArgs, generatedNamespace.GetName(), registryAuthSecretName)
					if err != nil {
						return fmt.Errorf("error creating skopeo pod: %v", err)
					}

					By(`wait for skopeo pod to exit successfully`)
					awaitPod(GinkgoT(), c, generatedNamespace.GetName(), skopeo, func(pod *corev1.Pod) bool {
						ctx.Ctx().Logf("skopeo pod status: %s (waiting for: %s)", pod.Status.Phase, corev1.PodSucceeded)
						return pod.Status.Phase == corev1.PodSucceeded
					})

					if err := deleteSkopeoPod(c, generatedNamespace.GetName()); err != nil {
						return fmt.Errorf("error deleting skopeo pod: %s", err)
					}
					return nil
				}
			}

			By(`The remote image to be copied onto the local registry`)
			srcImage := "quay.io/olmtest/example-operator-bundle:"
			srcTag := "0.1.0"

			By(`on-cluster image ref`)
			bundleImage := registryURL + "/unpack-retry-bundle:"
			bundleTag := genName("x")

			unpackRetryCatalog := fmt.Sprintf(`
schema: olm.package
name: unpack-retry-package
defaultChannel: stable
---
schema: olm.channel
package: unpack-retry-package
name: stable
entries:
  - name: example-operator.v0.1.0
---
schema: olm.bundle
name: example-operator.v0.1.0
package: unpack-retry-package
image: %s%s
properties:
  - type: olm.package
    value:
      packageName: unpack-retry-package
      version: 1.0.0
`, bundleImage, bundleTag)

			By("creating a catalog referencing a non-existent bundle image")
			unpackRetryProvider, err := NewRawFileBasedCatalogProvider(unpackRetryCatalog)
			Expect(err).ToNot(HaveOccurred())
			catalogSourceName := fmt.Sprintf("%s-catsrc", generatedNamespace.GetName())
			magicCatalog := NewMagicCatalog(ctx.Ctx().Client(), generatedNamespace.GetName(), catalogSourceName, unpackRetryProvider)
			Expect(magicCatalog.DeployCatalog(context.Background())).To(BeNil())

			By("patching the OperatorGroup to reduce the bundle unpacking timeout")
			ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
			addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "1s")

			By("creating a subscription for the missing bundle")
			unpackRetrySubName := fmt.Sprintf("%s-unpack-retry-package-sub", generatedNamespace.GetName())
			createSubscriptionForCatalog(crc, generatedNamespace.GetName(), unpackRetrySubName, catalogSourceName, "unpack-retry-package", stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			By("waiting for bundle unpack to fail")
			Eventually(
				func() error {
					fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), unpackRetrySubName, metav1.GetOptions{})
					if err != nil {
						return err
					}
					if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpackFailed); cond.Status != corev1.ConditionTrue || cond.Reason != "BundleUnpackFailed" {
						return fmt.Errorf("%s condition not found", operatorsv1alpha1.SubscriptionBundleUnpackFailed)
					}
					return nil
				},
				5*time.Minute,
				interval,
			).Should(Succeed())

			By("pushing missing bundle image")
			Expect(copyImage(bundleImage, bundleTag, srcImage, srcTag)).To(Succeed())

			By("patching the OperatorGroup to increase the bundle unpacking timeout")
			addBundleUnpackTimeoutOGAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "") // revert to default unpack timeout

			By("patching operator group to enable unpack retries")
			setBundleUnpackRetryMinimumIntervalAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "1s")

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), unpackRetrySubName, subscriptionHasInstallPlanChecker())
			Expect(err).Should(BeNil())

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crc, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())

			By("checking if old unpack conditions on subscription are removed")
			Eventually(func() error {
				fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), unpackRetrySubName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpacking); cond.Status != corev1.ConditionUnknown {
					return fmt.Errorf("subscription condition %s has unexpected value %s, expected %s", operatorsv1alpha1.SubscriptionBundleUnpacking, cond.Status, corev1.ConditionFalse)
				}
				if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpackFailed); cond.Status != corev1.ConditionUnknown {
					return fmt.Errorf("unexpected condition %s on subscription", operatorsv1alpha1.SubscriptionBundleUnpackFailed)
				}
				return nil
			}).Should(Succeed())
		})

		It("should not retry successful unpack jobs", func() {
			By("deploying the testing catalog")
			provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, subscriptionTestDataBaseDir, "example-operator.v0.1.0.yaml"))
			Expect(err).To(BeNil())
			catalogSourceName := fmt.Sprintf("%s-catsrc", generatedNamespace.GetName())
			magicCatalog := NewMagicCatalog(ctx.Ctx().Client(), generatedNamespace.GetName(), catalogSourceName, provider)
			Expect(magicCatalog.DeployCatalog(context.Background())).To(BeNil())

			By("creating the testing subscription")
			subName := fmt.Sprintf("%s-test-package-sub", generatedNamespace.GetName())
			createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subName, catalogSourceName, "test-package", stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			By("waiting until the subscription has an IP reference")
			subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subName, subscriptionHasInstallPlanChecker())
			Expect(err).Should(BeNil())

			By("waiting for the v0.1.0 CSV to report a succeeded phase")
			_, err = fetchCSV(crc, generatedNamespace.GetName(), subscription.Status.CurrentCSV, buildCSVConditionChecker(operatorsv1alpha1.CSVPhaseSucceeded))
			Expect(err).ShouldNot(HaveOccurred())

			By("waiting for the subscription bundle unpack conditions to be scrubbed")
			// This step removes flakes from this test where the conditions on the subscription haven't been
			// updated by the time the Consistently block executed a couple of steps below to ensure that the unpack
			// job has not been retried
			Eventually(func() error {
				fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpacking); cond.Status == corev1.ConditionTrue {
					return fmt.Errorf("unexpected condition status for %s on subscription %s", operatorsv1alpha1.SubscriptionBundleUnpacking, subName)
				}
				if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpackFailed); cond.Status == corev1.ConditionTrue {
					return fmt.Errorf("unexpected condition status for %s on subscription %s", operatorsv1alpha1.SubscriptionBundleUnpackFailed, subName)
				}
				return nil
			}).Should(Succeed())

			By("patching operator group to enable unpack retries")
			ogNN := types.NamespacedName{Name: operatorGroup.GetName(), Namespace: generatedNamespace.GetName()}
			setBundleUnpackRetryMinimumIntervalAnnotation(context.Background(), ctx.Ctx().Client(), ogNN, "1s")

			By("Ensuring successful bundle unpack jobs are not retried")
			Consistently(func() error {
				fetched, err := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
				if err != nil {
					return err
				}
				if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpacking); cond.Status == corev1.ConditionTrue {
					return fmt.Errorf("unexpected condition status for %s on subscription %s", operatorsv1alpha1.SubscriptionBundleUnpacking, subName)
				}
				if cond := fetched.Status.GetCondition(operatorsv1alpha1.SubscriptionBundleUnpackFailed); cond.Status == corev1.ConditionTrue {
					return fmt.Errorf("unexpected condition status for %s on subscription %s", operatorsv1alpha1.SubscriptionBundleUnpackFailed, subName)
				}
				return nil
			}).Should(Succeed())
		})
	})

	It("should support switching from one package to another", func() {
		kubeClient := ctx.Ctx().KubeClient()
		crClient := ctx.Ctx().OperatorClient()

		// Create CRDs for testing.
		// Both packages share the same CRD.
		crd := newCRD(genName("package1-crd"))

		// Create two packages
		packageName1 := "package1"
		packageName2 := "package2"

		// Create CSVs for each package
		csvPackage1 := newCSV("package1.v1.0.0", generatedNamespace.GetName(), "", semver.MustParse("1.0.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)
		csvPackage2 := newCSV("package2.v1.0.0", generatedNamespace.GetName(), "package1.v1.0.0", semver.MustParse("1.0.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, nil)

		// Create package manifests
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName1,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvPackage1.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
			{
				PackageName: packageName2,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: csvPackage2.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By("Creating a CatalogSource with both packages")
		catalogSourceName := genName("catalog-")
		catsrc, cleanup := createInternalCatalogSource(kubeClient, crClient, catalogSourceName,
			generatedNamespace.GetName(), manifests,
			[]apiextensionsv1.CustomResourceDefinition{crd},
			[]operatorsv1alpha1.ClusterServiceVersion{csvPackage1, csvPackage2})
		defer cleanup()

		By("Waiting for the catalog source to be ready")
		_, err := fetchCatalogSourceOnStatus(crClient, catsrc.GetName(), generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		Expect(err).NotTo(HaveOccurred())

		subscriptionName := genName("test-subscription-")
		By(fmt.Sprintf("Creating a subscription to package %q", packageName1))
		subscriptionSpec := &operatorsv1alpha1.SubscriptionSpec{
			CatalogSource:          catsrc.GetName(),
			CatalogSourceNamespace: catsrc.GetNamespace(),
			Package:                packageName1,
			Channel:                stableChannel,
			InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
		}

		cleanupSubscription := createSubscriptionForCatalogWithSpec(GinkgoT(), crClient,
			generatedNamespace.GetName(), subscriptionName, subscriptionSpec)
		defer cleanupSubscription()

		By(fmt.Sprintf("Waiting for package %q to be installed", packageName1))
		sub, err := fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionStateAtLatestChecker())
		Expect(err).NotTo(HaveOccurred())
		Expect(sub).NotTo(BeNil())

		By(fmt.Sprintf("Verifying that CSV %q is installed", csvPackage1.GetName()))
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvPackage1.GetName(), csvSucceededChecker)
		Expect(err).NotTo(HaveOccurred())

		// Record the current installplan for later comparison
		currentInstallPlanName := sub.Status.InstallPlanRef.Name

		By(fmt.Sprintf("Updating the subscription to point to package %q", packageName2))
		Eventually(func() error {
			subToUpdate, err := crClient.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.Background(), subscriptionName, metav1.GetOptions{})
			if err != nil {
				return err
			}

			// Switch the package in the subscription spec
			subToUpdate.Spec.Package = packageName2

			// Update the subscription
			_, err = crClient.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Update(context.Background(), subToUpdate, metav1.UpdateOptions{})
			return err
		}).Should(Succeed())

		By("Waiting for a new installplan to be created for the updated subscription")
		_, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanDifferentChecker(currentInstallPlanName))
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Waiting for subscription to reach 'AtLatestKnown' state for package %q", packageName2))
		_, err = fetchSubscription(crClient, generatedNamespace.GetName(), subscriptionName, subscriptionStateAtLatestChecker())
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("Verifying that CSV %q is installed", csvPackage2.GetName()))
		_, err = fetchCSV(crClient, generatedNamespace.GetName(), csvPackage2.GetName(), csvSucceededChecker)
		Expect(err).NotTo(HaveOccurred())
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
			Version:        version.OperatorVersion{Version: semver.MustParse("0.1.0")},
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
			Version:        version.OperatorVersion{Version: semver.MustParse("0.2.0")},
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
			Version:  version.OperatorVersion{Version: semver.MustParse("0.1.1")},
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
			Version:  version.OperatorVersion{Version: semver.MustParse("0.3.0")},
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
			GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
				SecurityContextConfig: operatorsv1alpha1.Restricted,
			},
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

func initCatalog(t GinkgoTInterface, namespace string, c operatorclient.ClientInterface, crc versioned.Interface) error {
	dummyCatalogConfigMap.SetNamespace(namespace)
	if _, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Create(context.Background(), dummyCatalogConfigMap, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("E2E bug detected: %v", err)
		}
		return err
	}
	t.Logf("created configmap %s/%s", dummyCatalogConfigMap.Namespace, dummyCatalogConfigMap.Name)

	dummyCatalogSource.SetNamespace(namespace)
	if _, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Create(context.Background(), &dummyCatalogSource, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("E2E bug detected: %v", err)
		}
		return err
	}
	t.Logf("created catalog source %s/%s", dummyCatalogSource.Namespace, dummyCatalogSource.Name)

	fetched, err := fetchCatalogSourceOnStatus(crc, dummyCatalogSource.GetName(), dummyCatalogSource.GetNamespace(), catalogSourceRegistryPodSynced())
	require.NoError(t, err)
	require.NotNil(t, fetched)

	return nil
}

type subscriptionStateChecker func(subscription *operatorsv1alpha1.Subscription) bool

func subscriptionStateUpgradeAvailableChecker() func(subscription *operatorsv1alpha1.Subscription) bool {
	var lastState operatorsv1alpha1.SubscriptionState
	lastTime := time.Now()
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		if subscription.Status.State != lastState {
			ctx.Ctx().Logf("waiting %s for subscription %s/%s to have state %s: has state %s", time.Since(lastTime), subscription.Namespace, subscription.Name, operatorsv1alpha1.SubscriptionStateUpgradeAvailable, subscription.Status.State)
			lastState = subscription.Status.State
			lastTime = time.Now()
		}
		return subscription.Status.State == operatorsv1alpha1.SubscriptionStateUpgradeAvailable
	}
}

func subscriptionStateUpgradePendingChecker() func(subscription *operatorsv1alpha1.Subscription) bool {
	var lastState operatorsv1alpha1.SubscriptionState
	lastTime := time.Now()
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		if subscription.Status.State != lastState {
			ctx.Ctx().Logf("waiting %s for subscription %s/%s to have state %s: has state %s", time.Since(lastTime), subscription.Namespace, subscription.Name, operatorsv1alpha1.SubscriptionStateUpgradePending, subscription.Status.State)
			lastState = subscription.Status.State
			lastTime = time.Now()
		}
		return subscription.Status.State == operatorsv1alpha1.SubscriptionStateUpgradePending
	}
}

func subscriptionStateAtLatestChecker() func(subscription *operatorsv1alpha1.Subscription) bool {
	var lastState operatorsv1alpha1.SubscriptionState
	lastTime := time.Now()
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		if subscription.Status.State != lastState {
			ctx.Ctx().Logf("waiting %s for subscription %s/%s to have state %s: has state %s", time.Since(lastTime), subscription.Namespace, subscription.Name, operatorsv1alpha1.SubscriptionStateAtLatest, subscription.Status.State)
			lastState = subscription.Status.State
			lastTime = time.Now()
		}
		return subscription.Status.State == operatorsv1alpha1.SubscriptionStateAtLatest
	}
}

func subscriptionHasInstallPlanChecker() func(subscription *operatorsv1alpha1.Subscription) bool {
	var lastState operatorsv1alpha1.SubscriptionState
	lastTime := time.Now()
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		if subscription.Status.State != lastState {
			ctx.Ctx().Logf("waiting %s for subscription %s/%s to have installplan ref: has ref %#v", time.Since(lastTime), subscription.Namespace, subscription.Name, subscription.Status.InstallPlanRef)
			lastState = subscription.Status.State
			lastTime = time.Now()
		}
		return subscription.Status.InstallPlanRef != nil
	}
}

func subscriptionHasInstallPlanDifferentChecker(currentInstallPlanName string) subscriptionStateChecker {
	checker := subscriptionHasInstallPlanChecker()
	var lastState operatorsv1alpha1.SubscriptionState
	lastTime := time.Now()
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		if subscription.Status.State != lastState {
			ctx.Ctx().Logf("waiting %s for subscription %s/%s to have installplan different from %s: has ref %#v", time.Since(lastTime), subscription.Namespace, subscription.Name, currentInstallPlanName, subscription.Status.InstallPlanRef)
			lastState = subscription.Status.State
			lastTime = time.Now()
		}
		return checker(subscription) && subscription.Status.InstallPlanRef.Name != currentInstallPlanName
	}
}

func subscriptionHasCurrentCSV(currentCSV string) subscriptionStateChecker {
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		return subscription.Status.CurrentCSV == currentCSV
	}
}

func subscriptionHasCondition(condType operatorsv1alpha1.SubscriptionConditionType, status corev1.ConditionStatus, reason, message string) subscriptionStateChecker {
	var lastCond operatorsv1alpha1.SubscriptionCondition
	lastTime := time.Now()
	// if status/reason/message meet expectations, then subscription state is considered met/true
	// IFF this is the result of a recent change of status/reason/message
	// else, cache the current status/reason/message for next loop/comparison
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		cond := subscription.Status.GetCondition(condType)
		if cond.Status == status && cond.Reason == reason && cond.Message == message {
			if lastCond.Status != cond.Status && lastCond.Reason != cond.Reason && lastCond.Message == cond.Message {
				GinkgoT().Logf("waited %s subscription condition met %v\n", time.Since(lastTime), cond)
				lastTime = time.Now()
				lastCond = cond
			}
			return true
		}

		if lastCond.Status != cond.Status && lastCond.Reason != cond.Reason && lastCond.Message == cond.Message {
			GinkgoT().Logf("waited %s subscription condition not met: %v\n", time.Since(lastTime), cond)
			lastTime = time.Now()
			lastCond = cond
		}
		return false
	}
}

func subscriptionDoesNotHaveCondition(condType operatorsv1alpha1.SubscriptionConditionType) subscriptionStateChecker {
	var lastStatus corev1.ConditionStatus
	lastTime := time.Now()
	// if status meets expectations, then subscription state is considered met/true
	// IFF this is the result of a recent change of status
	// else, cache the current status for next loop/comparison
	return func(subscription *operatorsv1alpha1.Subscription) bool {
		cond := subscription.Status.GetCondition(condType)
		if cond.Status == corev1.ConditionUnknown {
			if cond.Status != lastStatus {
				GinkgoT().Logf("waited %s subscription condition not found\n", time.Since(lastTime))
				lastStatus = cond.Status
				lastTime = time.Now()
			}
			return true
		}

		if cond.Status != lastStatus {
			GinkgoT().Logf("waited %s subscription condition found: %v\n", time.Since(lastTime), cond)
			lastStatus = cond.Status
			lastTime = time.Now()
		}
		return false
	}
}

func fetchSubscription(crc versioned.Interface, namespace, name string, checker subscriptionStateChecker) (*operatorsv1alpha1.Subscription, error) {
	var fetchedSubscription *operatorsv1alpha1.Subscription

	log := func(s string) {
		ctx.Ctx().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

	var lastState operatorsv1alpha1.SubscriptionState
	var lastCSV string
	var lastInstallPlanRef *corev1.ObjectReference

	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		var err error
		fetchedSubscription, err = crc.OperatorsV1alpha1().Subscriptions(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil || fetchedSubscription == nil {
			log(fmt.Sprintf("error getting subscription %s/%s: %v", namespace, name, err))
			return false, nil
		}
		thisState, thisCSV, thisInstallPlanRef := fetchedSubscription.Status.State, fetchedSubscription.Status.CurrentCSV, fetchedSubscription.Status.InstallPlanRef
		if thisState != lastState || thisCSV != lastCSV || !equality.Semantic.DeepEqual(thisInstallPlanRef, lastInstallPlanRef) {
			lastState, lastCSV, lastInstallPlanRef = thisState, thisCSV, thisInstallPlanRef
			log(fmt.Sprintf("subscription %s/%s state: %s (csv %s): installPlanRef: %#v", namespace, name, thisState, thisCSV, thisInstallPlanRef))
			log(fmt.Sprintf("subscription %s/%s state: %s (csv %s): status: %#v", namespace, name, thisState, thisCSV, fetchedSubscription.Status))
		}
		return checker(fetchedSubscription), nil
	})
	if err != nil {
		log(fmt.Sprintf("subscription %s/%s never got correct status: %#v", namespace, name, fetchedSubscription.Status))
		log(fmt.Sprintf("subscription %s/%s spec: %#v", namespace, name, fetchedSubscription.Spec))
		return nil, err
	}
	return fetchedSubscription, nil
}

func buildSubscriptionCleanupFunc(crc versioned.Interface, subscription *operatorsv1alpha1.Subscription) cleanupFunc {
	return func() {
		if env := os.Getenv("SKIP_CLEANUP"); env != "" {
			fmt.Printf("Skipping cleanup of install plan for subscription %s/%s...\n", subscription.GetNamespace(), subscription.GetName())
			return
		}

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
	t.Logf("created subscription %s/%s", subscription.Namespace, subscription.Name)
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

func waitForSubscriptionToDelete(namespace, name string, c versioned.Interface) error {
	var lastState operatorsv1alpha1.SubscriptionState
	var lastReason operatorsv1alpha1.ConditionReason
	lastTime := time.Now()

	ctx.Ctx().Logf("waiting for subscription %s/%s to delete", namespace, name)
	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		sub, err := c.OperatorsV1alpha1().Subscriptions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			ctx.Ctx().Logf("subscription %s/%s deleted", namespace, name)
			return true, nil
		}
		if err != nil {
			ctx.Ctx().Logf("error getting subscription %s/%s: %v", namespace, name, err)
		}
		if sub != nil {
			state, reason := sub.Status.State, sub.Status.Reason
			if state != lastState || reason != lastReason {
				ctx.Ctx().Logf("waited %s for subscription %s/%s status: %s (%s)", time.Since(lastTime), namespace, name, state, reason)
				lastState, lastReason = state, reason
				lastTime = time.Now()
			}
		}
		return false, nil
	})

	return err
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

func checkDeploymentWithPodConfiguration(client operatorclient.ClientInterface, csv *operatorsv1alpha1.ClusterServiceVersion, envVar []corev1.EnvVar, volumes []corev1.Volume, volumeMounts []corev1.VolumeMount, tolerations []corev1.Toleration, resources *corev1.ResourceRequirements) error {
	resolver := install.StrategyResolver{}

	strategy, err := resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	Expect(err).NotTo(HaveOccurred())

	strategyDetailsDeployment, ok := strategy.(*operatorsv1alpha1.StrategyDetailsDeployment)
	Expect(ok).To(BeTrue(), "could not cast install strategy as type %T", strategyDetailsDeployment)

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

	check := func(container *corev1.Container) error {
		for _, e := range envVar {
			existing, found := findEnvVar(container.Env, e.Name)
			if !found || existing == nil {
				return fmt.Errorf("env variable name=%s not injected", e.Name)
			}
			Expect(e.Value).Should(Equal(existing.Value), "env variable value does not match %s=%s", e.Name, e.Value)
		}

		for _, v := range volumeMounts {
			existing, found := findVolumeMount(container.VolumeMounts, v.Name)
			if !found || existing == nil {
				return fmt.Errorf("VolumeMount name=%s not injected", v.Name)
			}
			Expect(v.MountPath).Should(Equal(existing.MountPath), "VolumeMount MountPath does not match %s=%s", v.Name, v.MountPath)
		}

		existing, found := findResources(&container.Resources, resources)
		if !found || existing == nil {
			return fmt.Errorf("Resources not injected. Resource=%v", resources)
		}
		Expect(existing).Should(Equal(resources), "Resource=%v does not match expected Resource=%v", existing, resources)
		return nil
	}

	for _, deploymentSpec := range strategyDetailsDeployment.DeploymentSpecs {
		deployment, err := client.KubernetesInterface().AppsV1().Deployments(csv.GetNamespace()).Get(context.Background(), deploymentSpec.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		for _, v := range volumes {
			existing, found := findVolume(deployment.Spec.Template.Spec.Volumes, v.Name)
			if !found || existing == nil {
				return fmt.Errorf("Volume name=%s not injected", v.Name)
			}
			Expect(v.ConfigMap.LocalObjectReference.Name).Should(Equal(existing.ConfigMap.LocalObjectReference.Name), "volume ConfigMap Names does not match %s=%s", v.Name, v.ConfigMap.LocalObjectReference.Name)
		}

		for _, toleration := range tolerations {
			existing, found := findTolerations(deployment.Spec.Template.Spec.Tolerations, toleration)
			if !found || existing == nil {
				return fmt.Errorf("Toleration not injected. Toleration=%v", toleration)
			}
			Expect(*existing).Should(Equal(toleration), "Toleration=%v does not match expected Toleration=%v", existing, toleration)
		}

		for i := range deployment.Spec.Template.Spec.Containers {
			err = check(&deployment.Spec.Template.Spec.Containers[i])
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func updateInternalCatalog(t GinkgoTInterface, c operatorclient.ClientInterface, crc versioned.Interface, catalogSourceName, namespace string, crds []apiextensionsv1.CustomResourceDefinition, csvs []operatorsv1alpha1.ClusterServiceVersion, packages []registry.PackageManifest) {
	fetchedInitialCatalog, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, namespace, catalogSourceRegistryPodSynced())
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
	var lastState string
	lastTime := time.Now()
	_, err = fetchCatalogSourceOnStatus(crc, catalogSourceName, namespace, func(catalog *operatorsv1alpha1.CatalogSource) bool {
		before := fetchedInitialCatalog.Status.ConfigMapResource
		after := catalog.Status.ConfigMapResource
		if after != nil && after.LastUpdateTime.After(before.LastUpdateTime.Time) && after.ResourceVersion != before.ResourceVersion &&
			catalog.Status.GRPCConnectionState.LastConnectTime.After(after.LastUpdateTime.Time) && catalog.Status.GRPCConnectionState.LastObservedState == "READY" {
			fmt.Println("catalog updated")
			return true
		}
		if catalog.Status.GRPCConnectionState.LastObservedState != lastState {
			fmt.Printf("waited %s for catalog pod %v to be available (after catalog update) - %s\n", time.Since(lastTime), catalog.GetName(), catalog.Status.GRPCConnectionState.LastObservedState)
			lastState = catalog.Status.GRPCConnectionState.LastObservedState
			lastTime = time.Now()
		}
		return false
	})
	require.NoError(t, err)
}

func subscriptionCurrentCSVGetter(crclient versioned.Interface, namespace, subName string) func() string {
	return func() string {
		subscription, err := crclient.OperatorsV1alpha1().Subscriptions(namespace).Get(context.Background(), subName, metav1.GetOptions{})
		if err != nil || subscription == nil {
			return ""
		}
		return subscription.Status.CurrentCSV
	}
}
