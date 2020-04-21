package e2e

import (
	"context"
	"fmt"
	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	crdName       = "test"
	crdNamePlural = "tests"
	crdGroup      = "test.k8s.io"
)

var _ = Describe("CRD Versions ", func() {
	It("Handles CRD versioning changes as expected", func() {
		By("CRDs changed APIVersions from v1beta1 to v1 and OLM must support both versions. " +
			"Upgrading from a v1beta1 to a v1 version of the same CRD should be seamless because the client always returns the latest version.")

		c := ctx.Ctx().KubeClient()

		oldv1beta1CRD := &apiextensionsv1beta1.CustomResourceDefinition{
			TypeMeta: metav1.TypeMeta{APIVersion: "apiextensions.k8s.io/v1beta1", Kind: "CustomResourceDefinition"},
			ObjectMeta: metav1.ObjectMeta{
				Name: crdNamePlural + "." + crdGroup,
			},
			Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
				Group:   crdGroup,
				Version: "v1",
				Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
					Singular: crdName,
					Plural:   crdNamePlural,
					Kind:     "test",
				},
			},
		}

		// create v1beta1 CRD on server
		oldcrd, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), oldv1beta1CRD, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		By("created CRD")

		// poll for CRD to be ready (using the v1 client)
		Eventually(func() (bool, error) {
			fetchedCRD, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), oldcrd.GetName(), metav1.GetOptions{})
			if err != nil || fetchedCRD == nil {
				return false, err
			}
			return checkCRD(fetchedCRD), nil
		}, 5*time.Minute, 10*time.Second).Should(Equal(true))

		oldCRDConvertedToV1, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), oldcrd.GetName(), metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())

		// confirm the v1 crd as is as expected
		// run ensureCRDV1Versions on results
		newCRD := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdNamePlural + "." + crdGroup,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: crdGroup,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:   "v1",
						Served: true,
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Singular: crdName,
					Plural:   crdNamePlural,
				},
			},
		}

		err = catalog.EnsureV1CRDVersions(oldCRDConvertedToV1, newCRD)
		Expect(err).ToNot(HaveOccurred())
	})
	It("creates v1beta1 crds with a v1beta1 schema successfully", func() {
		By("This test proves that OLM is able to handle v1beta1 CRDs successfully. Creating v1 CRDs has more " +
			"restrictions around the schema. v1beta1 validation schemas are not necessarily valid in v1. " +
			"OLM should support both v1beta1 and v1 CRDs")
		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"
		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		crdPlural := genName("ins-v1beta1-")
		crdName := crdPlural + ".cluster.com"
		mainCRD := apiextensions.CustomResourceDefinition{
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
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
				// this validation is not a valid v1 structural schema because the "type: object" field is missing
				Validation: &apiextensions.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
						Description: "my crd schema",
					},
				},
			},
		}

		// Create the initial CSV
		cleanupCRD, err := createCRD(c, mainCRD)
		require.NoError(GinkgoT(), err)
		defer cleanupCRD()

		mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, mainNamedStrategy)
		mainCatalogName := genName("mock-ocs-main-update2-")
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the catalog sources
		_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err = fetchCatalogSource(GinkgoT(), crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)
		require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
		require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)
	})
	AfterEach(func() { cleaner.NotifyTestComplete(GinkgoT(), true) }, float64(10))
})

func checkCRD(v1crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, condition := range v1crd.Status.Conditions {
		if condition.Type == apiextensionsv1.Established {
			return true
		}
	}

	return false
}
