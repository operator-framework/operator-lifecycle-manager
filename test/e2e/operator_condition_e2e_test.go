package e2e

import (
	"context"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Operator Condition", func() {
	var (
		generatedNamespace corev1.Namespace
	)

	BeforeEach(func() {
		generatedNamespace = SetupGeneratedTestNamespace(genName("operator-conditions-e2e-"))
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
	})

	It("[FLAKE] OperatorCondition Upgradeable type and overrides", func() {
		By("This test proves that an operator can upgrade successfully when" +
			" Upgrade condition type is set in OperatorCondition spec. Plus, an operator" +
			" chooses not to use OperatorCondition, the upgrade process will proceed as" +
			" expected. The overrides spec in OperatorCondition can be used to override" +
			" the conditions spec. The overrides spec will remain in place until" +
			" they are unset.")
		c := ctx.Ctx().KubeClient()
		crc := ctx.Ctx().OperatorClient()

		By(`Create a catalog for csvA, csvB, and csvD`)
		pkgA := genName("a-")
		pkgB := genName("b-")
		pkgD := genName("d-")
		pkgAStable := pkgA + "-stable"
		pkgBStable := pkgB + "-stable"
		pkgDStable := pkgD + "-stable"
		stableChannel := "stable"
		strategyA := newNginxInstallStrategy(pkgAStable, nil, nil)
		strategyB := newNginxInstallStrategy(pkgBStable, nil, nil)
		strategyD := newNginxInstallStrategy(pkgDStable, nil, nil)
		crd := newCRD(genName(pkgA))
		csvA := newCSV(pkgAStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, &strategyA)
		csvB := newCSV(pkgBStable, generatedNamespace.GetName(), pkgAStable, semver.MustParse("0.2.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, &strategyB)
		csvD := newCSV(pkgDStable, generatedNamespace.GetName(), pkgBStable, semver.MustParse("0.3.0"), []apiextensionsv1.CustomResourceDefinition{crd}, nil, &strategyD)

		By(`Create the initial catalogsources`)
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
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalog, generatedNamespace.GetName(), manifests, []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA})
		defer cleanupCatalogSource()
		_, err := fetchCatalogSourceOnStatus(crc, catalog, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		subName := genName("sub-")
		cleanupSub := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subName, catalog, pkgA, stableChannel, pkgAStable, operatorsv1alpha1.ApprovalAutomatic)
		defer cleanupSub()

		By(`Await csvA's success`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Get the OperatorCondition for csvA and report that it is not upgradeable`)
		var cond *operatorsv2.OperatorCondition
		upgradeableFalseCondition := metav1.Condition{
			Type:               operatorsv2.Upgradeable,
			Status:             metav1.ConditionFalse,
			Reason:             "test",
			Message:            "test",
			LastTransitionTime: metav1.Now(),
		}

		var currentGen int64
		Eventually(func() error {
			cond, err := crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Get(context.TODO(), csvA.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			currentGen = cond.ObjectMeta.GetGeneration()
			upgradeableFalseCondition.ObservedGeneration = currentGen
			meta.SetStatusCondition(&cond.Spec.Conditions, upgradeableFalseCondition)
			_, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Update(context.TODO(), cond, metav1.UpdateOptions{})
			return err
		}, pollInterval, pollDuration).Should(Succeed())

		By(`Update the catalogsources`)
		manifests = []registry.PackageManifest{
			{
				PackageName: pkgA,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: pkgBStable},
				},
				DefaultChannelName: stableChannel,
			},
		}
		updateInternalCatalog(GinkgoT(), c, crc, catalog, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB}, manifests)

		By(`Attempt to get the catalog source before creating install plan(s)`)
		_, err = fetchCatalogSourceOnStatus(crc, catalog, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`csvB will be in Pending phase due to csvA reports Upgradeable=False condition`)
		fetchedCSV, err := fetchCSV(crc, generatedNamespace.GetName(), csvB.GetName(), buildCSVReasonChecker(operatorsv1alpha1.CSVReasonOperatorConditionNotUpgradeable))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), fetchedCSV.Status.Phase, operatorsv1alpha1.CSVPhasePending)

		By(`Get the OperatorCondition for csvA and report that it is upgradeable, unblocking csvB`)
		upgradeableTrueCondition := metav1.Condition{
			Type:               operatorsv2.Upgradeable,
			Status:             metav1.ConditionTrue,
			Reason:             "test",
			Message:            "test",
			LastTransitionTime: metav1.Now(),
		}
		Eventually(func() error {
			cond, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Get(context.TODO(), csvA.GetName(), metav1.GetOptions{})
			if err != nil || currentGen == cond.ObjectMeta.GetGeneration() {
				return err
			}
			currentGen = cond.ObjectMeta.GetGeneration()
			upgradeableTrueCondition.ObservedGeneration = cond.ObjectMeta.GetGeneration()
			meta.SetStatusCondition(&cond.Spec.Conditions, upgradeableTrueCondition)
			_, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Update(context.TODO(), cond, metav1.UpdateOptions{})
			return err
		}, pollInterval, pollDuration).Should(Succeed())

		By(`Await csvB's success`)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		By(`Get the OperatorCondition for csvB and purposedly change ObservedGeneration`)
		By(`to cause mismatch generation situation`)
		Eventually(func() error {
			cond, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Get(context.TODO(), csvB.GetName(), metav1.GetOptions{})
			if err != nil || currentGen == cond.ObjectMeta.GetGeneration() {
				return err
			}
			currentGen = cond.ObjectMeta.GetGeneration()
			upgradeableTrueCondition.ObservedGeneration = currentGen + 1
			meta.SetStatusCondition(&cond.Status.Conditions, upgradeableTrueCondition)
			_, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).UpdateStatus(context.TODO(), cond, metav1.UpdateOptions{})
			return err
		}, pollInterval, pollDuration).Should(Succeed())

		By(`Update the catalogsources`)
		manifests = []registry.PackageManifest{
			{
				PackageName: pkgA,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: pkgDStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, catalog, generatedNamespace.GetName(), []apiextensionsv1.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB, csvD}, manifests)
		By(`Attempt to get the catalog source before creating install plan(s)`)
		_, err = fetchCatalogSourceOnStatus(crc, catalog, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		require.NoError(GinkgoT(), err)

		By(`CSVD will be in Pending status due to overrides in csvB's condition`)
		fetchedCSV, err = fetchCSV(crc, generatedNamespace.GetName(), csvD.GetName(), buildCSVReasonChecker(operatorsv1alpha1.CSVReasonOperatorConditionNotUpgradeable))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), fetchedCSV.Status.Phase, operatorsv1alpha1.CSVPhasePending)

		By(`Get the OperatorCondition for csvB and override the upgradeable false condition`)
		Eventually(func() error {
			cond, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Get(context.TODO(), csvB.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			meta.SetStatusCondition(&cond.Spec.Overrides, upgradeableTrueCondition)
			By(`Update the condition`)
			_, err = crc.OperatorsV2().OperatorConditions(generatedNamespace.GetName()).Update(context.TODO(), cond, metav1.UpdateOptions{})
			return err
		}, pollInterval, pollDuration).Should(Succeed())
		require.NoError(GinkgoT(), err)

		require.NoError(GinkgoT(), err)
		_, err = fetchCSV(crc, generatedNamespace.GetName(), csvD.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})
})
