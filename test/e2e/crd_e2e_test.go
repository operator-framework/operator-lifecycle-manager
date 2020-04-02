package e2e

import (
	"fmt"
	"github.com/blang/semver"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	crdName       = "test"
	crdNamePlural = "tests"
	crdGroup      = "test.k8s.io"
)

var _ = Describe("CRD Versions", func() {
	It("creates v1beta1 crds with a v1beta1 schema successfully", func() {
		By("This test proves that OLM is able to handle v1beta1 CRDs successfully. Creating v1 CRDs has more " +
			"restrictions around the schema. v1beta1 validation schemas are not necessarily valid in v1. " +
			"OLM should support both v1beta1 and v1 CRDs")
		c := newKubeClient()
		crc := newCRClient()

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
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(Equal(nil))
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(mainCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))
	})
	It("creates v1 CRDs with a v1 schema successfully", func() {
		By("v1 crds with a valid openapiv3 schema should be created successfully by OLM")
		c := newKubeClient()
		crc := newCRClient()

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"
		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		crdPlural := genName("ins-v1beta1-")
		crdName := crdPlural + ".cluster.com"
		v1crd := apiextensionsv1.CustomResourceDefinition{
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
			},
		}

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
		_, cleanupMainCatalogSource := createV1CRDInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensionsv1.CustomResourceDefinition{v1crd}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(Equal(nil))
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(mainCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))
	})
	AfterEach(func() { cleaner.NotifyTestComplete(true) }, float64(10))
})

func checkCRD(v1crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, condition := range v1crd.Status.Conditions {
		if condition.Type == apiextensionsv1.Established {
			return true
		}
	}

	return false
}
