package e2e

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	operatorv1 "github.com/operator-framework/api/pkg/operators/v1"
	v1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

var _ = Describe("Operator Condition", func() {
	AfterEach(func() {
		TearDown(testNamespace)
	})

	It("OperatorUpgradeable condition", func() {
		By("OLM will only transition CSV to Succeeded state when OperatorUpgradeable condition is true or non-present")
		c := newKubeClient()
		crc := newCRClient()

		// Create a catalog for csvA, csvB, and csvD
		pkgA := genName("a-")
		pkgB := genName("b-")
		pkgD := genName("d-")
		pkgAStable := pkgA + "-stable"
		pkgBStable := pkgB + "-stable"
		pkgDStable := pkgD + "-stable"
		stableChannel := "stable"
		strategy := newNginxInstallStrategy(pkgAStable, nil, nil)
		crd := newCRD(genName(pkgA))
		csvA := newCSV(pkgAStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, &strategy)
		csvB := newCSV(pkgBStable, testNamespace, pkgAStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crc}, nil, &strategy)
		csvD := newCSV(pkgDStable, testNamespace, pkgBStable, semver.MustParse("0.3.0"), []apiextensions.CustomResourceDefinition{crd}, nil, &strategy)

		// Create the initial catalogsources
		manifests := []registry.PackageManifest{
			{
				PackageName: pkgA,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: pkgAStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		catalog := genName("catalog-")
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalog, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csvA})
		defer cleanupCatalogSource()
		_, err := fetchCatalogSourceOnStatus(crc, catalog, testNamespace, catalogSourceRegistryPodSynced)

		subName := genName("sub-")
		cleanupSub := createSubscriptionForCatalog(crc, testNamespace, subName, catalog, pkgA, stableChannel, pkgAStable, v1alpha1.ApprovalAutomatic)
		defer cleanupSub()

		// Create OperatorCondition CR
		opCondition := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "operators.coreos.com/v1",
				"kind":       "OperatorCondition",
				"metadata": map[string]interface{}{
					"namespace": testNamespace,
					"name":      pkgAStable,
				},
			},
		}
		cleanupCR, err := createCR(c, opCondition, "operators.coreos.com", "v1", testNamespace, "OperatorCondition", pkgAStable)
		require.NoError(GinkgoT(), err)
		defer cleanupCR()

		// Await csvA's success
		_, err = awaitCSV(crc, testPackageName, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Add upgradeable condition to OperatorCondition CR
		opCondition = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "operators.coreos.com/v1",
				"kind":       "OperatorCondition",
				"metadata": map[string]interface{}{
					"namespace": testNamespace,
					"name":      pkgAStable,
				},
				"status": map[string]interface{}{
					"conditions": []map[string]interface{}{
						"type":   operatorv1.OperatorUpgradeable,
						"status": corev1.ConditionTrue,
					},
				},
			},
		}
		err = c.UpdateCustomResourceStatus(opCondition)
		require.NoError(GinkgoT(), err)

		// Update the catalogsources
		manifests = []registry.PackageManifest{
			{
				PackageName: pkgA,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: pkgBStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, catalog, testNamespace, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB}, manifests)
		// Attempt to get the catalog source before creating install plan(s)
		_, err = fetchCatalogSourceOnStatus(crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)
		subscription, err = fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(installPlanName))
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		// Await csvB's success
		_, err = awaitCSV(crc, testPackageName, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Inject overrides into OperatorCondition CR
		opCondition = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "operators.coreos.com/v1",
				"kind":       "OperatorCondition",
				"metadata": map[string]interface{}{
					"namespace": testNamespace,
					"name":      pkgAStable,
				},
				"spec": map[string]interface{}{
					"overrides": []map[string]interface{}{
						"type":   operatorv1.OperatorUpgradeable,
						"status": corev1.ConditionFalse,
					},
				},
			},
		}
		err = c.UpdateCustomResource(opCondition)
		require.NoError(GinkgoT(), err)
		// CSV will be in Pending status due to overrides
		_, err = awaitCSV(crc, testPackageName, csvD.GetName(), csvPendingChecker)
		require.NoError(GinkgoT(), err)

		// Remove the overrides setting
		opCondition = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "operators.coreos.com/v1",
				"kind":       "OperatorCondition",
				"metadata": map[string]interface{}{
					"namespace": testNamespace,
					"name":      pkgAStable,
				},
			},
		}
		err = c.UpdateCustomResource(opCondition)
		require.NoError(GinkgoT(), err)
		_, err = awaitCSV(crc, testPackageName, csvD.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})
})
