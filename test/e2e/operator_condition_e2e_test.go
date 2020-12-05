package e2e

import (
	"context"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

var _ = Describe("Operator Condition", func() {
	AfterEach(func() {
		TearDown(testNamespace)
	})

	It("OperatorUpgradeable condition and overrides", func() {
		By("This test proves that an operator can upgrade successfully when" +
			"OperatorUpgrade condition is set in OperatorCondition CR. Plus, an operator" +
			"chooses not to use OperatorCondition, the upgrade process will proceed as" +
			" asexpected. The overrides specification in OperatorCondition can be used to" +
			" override the status condition. The overrides spec will remain in place until" +
			"they are unset.")
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
		strategyA := newNginxInstallStrategy(pkgAStable, nil, nil)
		strategyB := newNginxInstallStrategy(pkgBStable, nil, nil)
		strategyD := newNginxInstallStrategy(pkgDStable, nil, nil)
		crd := newCRD(genName(pkgA))
		csvA := newCSV(pkgAStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, &strategyA)
		csvB := newCSV(pkgBStable, testNamespace, pkgAStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd}, nil, &strategyB)
		csvD := newCSV(pkgDStable, testNamespace, pkgBStable, semver.MustParse("0.3.0"), []apiextensions.CustomResourceDefinition{crd}, nil, &strategyD)

		// Create OperatorCondition for csvA
		opCondition := operatorsv1.OperatorCondition{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pkgAStable,
				Namespace: testNamespace,
			},
		}
		_, err := crc.OperatorsV1().OperatorConditions(testNamespace).Create(context.TODO(), &opCondition, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		// Create OperatorCondition for csvB
		opCondition = operatorsv1.OperatorCondition{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pkgBStable,
				Namespace: testNamespace,
			},
		}
		_, err = crc.OperatorsV1().OperatorConditions(testNamespace).Create(context.TODO(), &opCondition, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

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
		_, cleanupCatalogSource := createInternalCatalogSource(c, crc, catalog, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA})
		defer cleanupCatalogSource()
		_, err = fetchCatalogSourceOnStatus(crc, catalog, testNamespace, catalogSourceRegistryPodSynced)
		subName := genName("sub-")
		cleanupSub := createSubscriptionForCatalog(crc, testNamespace, subName, catalog, pkgA, stableChannel, pkgAStable, operatorsv1alpha1.ApprovalAutomatic)
		defer cleanupSub()

		// Await csvA's success
		_, err = awaitCSV(crc, testNamespace, csvA.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Get OperatorCondition for csvA
		var cond *operatorsv1.OperatorCondition
		Eventually(func() (bool, error) {
			cond, err = crc.OperatorsV1().OperatorConditions(testNamespace).Get(context.TODO(), csvA.GetName(), metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}, pollInterval, pollDuration).Should(BeTrue())
		require.NoError(GinkgoT(), err)

		// Set upgradeable condition to false
		newCond := metav1.Condition{
			Type:               operatorsv1.OperatorUpgradeable,
			Status:             metav1.ConditionFalse,
			Reason:             "test",
			Message:            "test",
			LastTransitionTime: metav1.Now(),
		}

		meta.SetStatusCondition(&cond.Status.Conditions, newCond)
		_, err = crc.OperatorsV1().OperatorConditions(testNamespace).UpdateStatus(context.TODO(), cond, metav1.UpdateOptions{})
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
		_, err = fetchCatalogSourceOnStatus(crc, catalog, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)
		// csvB will be in Pending phase due to csvA reports Upgradeable=False condition
		fetchedCSV, err := fetchCSV(crc, csvB.GetName(), testNamespace, buildCSVReasonChecker(operatorsv1alpha1.CSVReasonOperatorConditionNotUpgradeable))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), fetchedCSV.Status.Phase, operatorsv1alpha1.CSVPhasePending)

		// Get OperatorCondition for csvA
		Eventually(func() (bool, error) {
			cond, err = crc.OperatorsV1().OperatorConditions(testNamespace).Get(context.TODO(), csvA.GetName(), metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}, pollInterval, pollDuration).Should(BeTrue())
		require.NoError(GinkgoT(), err)

		// Switch upgradeable condition to true
		newCond = metav1.Condition{
			Type:               operatorsv1.OperatorUpgradeable,
			Status:             metav1.ConditionTrue,
			Reason:             "test",
			Message:            "test",
			LastTransitionTime: metav1.Now(),
		}

		meta.SetStatusCondition(&cond.Status.Conditions, newCond)
		_, err = crc.OperatorsV1().OperatorConditions(testNamespace).UpdateStatus(context.TODO(), cond, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		// Await csvB's success
		_, err = awaitCSV(crc, testNamespace, csvB.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Get OperatorCondition for csvB
		Eventually(func() (bool, error) {
			cond, err = crc.OperatorsV1().OperatorConditions(testNamespace).Get(context.TODO(), csvB.GetName(), metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}, pollInterval, pollDuration).Should(BeTrue())
		require.NoError(GinkgoT(), err)
		// Add Condition overrides
		cond.Spec = operatorsv1.OperatorConditionSpec{
			Overrides: []metav1.Condition{{
				Type:               operatorsv1.OperatorUpgradeable,
				Status:             metav1.ConditionFalse,
				Reason:             "test",
				Message:            "test",
				LastTransitionTime: metav1.Now(),
			}},
		}

		_, err = crc.OperatorsV1().OperatorConditions(testNamespace).Update(context.TODO(), cond, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)

		// Update the catalogsources
		manifests = []registry.PackageManifest{
			{
				PackageName: pkgA,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: pkgDStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, catalog, testNamespace, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csvA, csvB, csvD}, manifests)
		// Attempt to get the catalog source before creating install plan(s)
		_, err = fetchCatalogSourceOnStatus(crc, catalog, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// CSVD will be in Pending status due to overrides in csvB's condition
		fetchedCSV, err = fetchCSV(crc, csvD.GetName(), testNamespace, buildCSVReasonChecker(operatorsv1alpha1.CSVReasonOperatorConditionNotUpgradeable))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), fetchedCSV.Status.Phase, operatorsv1alpha1.CSVPhasePending)

		// Get OperatorCondition for csvB
		Eventually(func() (bool, error) {
			cond, err = crc.OperatorsV1().OperatorConditions(testNamespace).Get(context.TODO(), csvB.GetName(), metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		}, pollInterval, pollDuration).Should(BeTrue())
		require.NoError(GinkgoT(), err)
		// Set Condition overrides to True
		cond.Spec = operatorsv1.OperatorConditionSpec{
			Overrides: []metav1.Condition{{
				Type:               operatorsv1.OperatorUpgradeable,
				Status:             metav1.ConditionTrue,
				Reason:             "test",
				Message:            "test",
				LastTransitionTime: metav1.Now(),
			}},
		}

		_, err = crc.OperatorsV1().OperatorConditions(testNamespace).Update(context.TODO(), cond, metav1.UpdateOptions{})
		require.NoError(GinkgoT(), err)
		_, err = awaitCSV(crc, testNamespace, csvD.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)
	})
})
