package e2e

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/onsi/ginkgo/extensions/table"
	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/apis/rbac"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	opver "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/version"
	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/retry"

	"errors"

	. "github.com/onsi/ginkgo"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = Describe("Install Plan", func() {
	It("with CSVs across multiple catalog sources", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		crdPlural := genName("ins-")

		dependentCRD := newCRD(crdPlural)
		mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentCSV := newCSV(dependentPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		dependentCatalogName := genName("mock-ocs-dependent-")
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

		dependentManifests := []registry.PackageManifest{
			{
				PackageName: dependentPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: dependentPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the catalog sources
		require.NotEqual(GinkgoT(), "", testNamespace)
		_, cleanupDependentCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, dependentCatalogName, testNamespace, dependentManifests, []apiextensions.CustomResourceDefinition{dependentCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentCSV})
		defer cleanupDependentCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSource(GinkgoT(), crc, dependentCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, nil, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err = fetchCatalogSource(GinkgoT(), crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Create expected install plan step sources
		expectedStepSources := map[registry.ResourceKey]registry.ResourceKey{
			{Name: dependentCRD.Name, Kind: "CustomResourceDefinition"}:                                                                                {Name: dependentCatalogName, Namespace: testNamespace},
			{Name: dependentPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}:                                                          {Name: dependentCatalogName, Namespace: testNamespace},
			{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}:                                                               {Name: mainCatalogName, Namespace: testNamespace},
			{Name: strings.Join([]string{dependentPackageStable, dependentCatalogName, testNamespace}, "-"), Kind: operatorsv1alpha1.SubscriptionKind}: {Name: dependentCatalogName, Namespace: testNamespace},
		}

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase))

		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Fetch installplan again to check for unnecessary control loops
		fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
			compareResources(GinkgoT(), fetchedInstallPlan, fip)
			return true
		})
		require.NoError(GinkgoT(), err)

		require.Equal(GinkgoT(), len(expectedStepSources), len(fetchedInstallPlan.Status.Plan), "Number of resolved steps matches the number of expected steps")

		// Ensure resolved step resources originate from the correct catalog sources
		log(fmt.Sprintf("%#v", expectedStepSources))
		for _, step := range fetchedInstallPlan.Status.Plan {
			log(fmt.Sprintf("checking %s", step.Resource))
			key := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
			expectedSource, ok := expectedStepSources[key]
			require.True(GinkgoT(), ok, "didn't find %v", key)
			require.Equal(GinkgoT(), expectedSource.Name, step.Resource.CatalogSource)
			require.Equal(GinkgoT(), expectedSource.Namespace, step.Resource.CatalogSourceNamespace)

			// delete
		}
	EXPECTED:
		for key := range expectedStepSources {
			for _, step := range fetchedInstallPlan.Status.Plan {
				if step.Resource.Name == key.Name && step.Resource.Kind == key.Kind {
					continue EXPECTED
				}
			}
			GinkgoT().Fatalf("expected step %s not found in %#v", key, fetchedInstallPlan.Status.Plan)
		}

		log("All expected resources resolved")

		// Verify that the dependent subscription is in a good state
		dependentSubscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, strings.Join([]string{dependentPackageStable, dependentCatalogName, testNamespace}, "-"), subscriptionStateAtLatestChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), dependentSubscription)
		require.NotNil(GinkgoT(), dependentSubscription.Status.InstallPlanRef)
		require.Equal(GinkgoT(), dependentCSV.GetName(), dependentSubscription.Status.CurrentCSV)

		// Verify CSV is created
		_, err = awaitCSV(GinkgoT(), crc, testNamespace, dependentCSV.GetName(), csvAnyChecker)
		require.NoError(GinkgoT(), err)

		// Update dependent subscription in catalog and wait for csv to update
		updatedDependentCSV := newCSV(dependentPackageStable+"-v2", testNamespace, dependentPackageStable, semver.MustParse("0.1.1"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentManifests = []registry.PackageManifest{
			{
				PackageName: dependentPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: updatedDependentCSV.GetName()},
				},
				DefaultChannelName: stableChannel,
			},
		}

		updateInternalCatalog(GinkgoT(), c, crc, dependentCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{dependentCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentCSV, updatedDependentCSV}, dependentManifests)

		// Wait for subscription to update
		updatedDepSubscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, strings.Join([]string{dependentPackageStable, dependentCatalogName, testNamespace}, "-"), subscriptionHasCurrentCSV(updatedDependentCSV.GetName()))
		require.NoError(GinkgoT(), err)

		// Verify installplan created and installed
		fetchedUpdatedDepInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedDepSubscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedUpdatedDepInstallPlan.GetName(), fetchedUpdatedDepInstallPlan.Status.Phase))
		require.NotEqual(GinkgoT(), fetchedInstallPlan.GetName(), fetchedUpdatedDepInstallPlan.GetName())

		// Wait for csv to update
		_, err = awaitCSV(GinkgoT(), crc, testNamespace, updatedDependentCSV.GetName(), csvAnyChecker)
		require.NoError(GinkgoT(), err)
	})

	Context("creation with pre existing CRD owners", func() {

		It("OnePreExistingCRDOwner", func() {
			defer cleaner.NotifyTestComplete(GinkgoT(), true)

			mainPackageName := genName("nginx-")
			dependentPackageName := genName("nginx-dep-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)
			dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)
			dependentPackageBeta := fmt.Sprintf("%s-beta", dependentPackageName)

			stableChannel := "stable"
			betaChannel := "beta"

			// Create manifests
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
					},
					DefaultChannelName: stableChannel,
				},
				{
					PackageName: dependentPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: dependentPackageStable},
						{Name: betaChannel, CurrentCSVName: dependentPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Create new CRDs
			mainCRDPlural := genName("ins-")
			mainCRD := newCRD(mainCRDPlural)

			// Create a new named install strategy
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
			dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

			dependentCRDPlural := genName("ins-")
			dependentCRD := newCRD(dependentCRDPlural)

			// Create new CSVs
			mainStableCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
			mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
			dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
			dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			// Create the catalog source
			mainCatalogSourceName := genName("mock-ocs-main-" + strings.ToLower(CurrentGinkgoTestDescription().TestText) + "-")
			_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{dependentCRD, mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
			defer cleanupCatalogSource()

			// Attempt to get the catalog source before creating install plan(s)
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			expectedSteps := map[registry.ResourceKey]struct{}{
				{Name: mainCRD.Name, Kind: "CustomResourceDefinition"}:                       {},
				{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}: {},
			}

			// Create the preexisting CRD and CSV
			cleanupCRD, err := createCRD(c, dependentCRD)
			require.NoError(GinkgoT(), err)
			defer cleanupCRD()
			cleanupCSV, err := createCSV(GinkgoT(), c, crc, dependentBetaCSV, testNamespace, true, false)
			require.NoError(GinkgoT(), err)
			defer cleanupCSV()
			GinkgoT().Log("Dependent CRD and preexisting CSV created")

			subscriptionName := genName("sub-nginx-")
			subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Fetch installplan again to check for unnecessary control loops
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				compareResources(GinkgoT(), fetchedInstallPlan, fip)
				return true
			})
			require.NoError(GinkgoT(), err)

			for _, step := range fetchedInstallPlan.Status.Plan {
				GinkgoT().Logf("%#v", step)
			}
			require.Equal(GinkgoT(), len(fetchedInstallPlan.Status.Plan), len(expectedSteps), "number of expected steps does not match installed")
			GinkgoT().Logf("Number of resolved steps matches the number of expected steps")

			for _, step := range fetchedInstallPlan.Status.Plan {
				key := registry.ResourceKey{
					Name: step.Resource.Name,
					Kind: step.Resource.Kind,
				}
				_, ok := expectedSteps[key]
				require.True(GinkgoT(), ok)

				// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
				delete(expectedSteps, key)
			}

			// Should have removed every matching step
			require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected")
		})
		It("PreExistingCRDOwnerIsReplaced", func() {
			defer cleaner.NotifyTestComplete(GinkgoT(), true)

			mainPackageName := genName("nginx-")
			dependentPackageName := genName("nginx-dep-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)
			dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)
			dependentPackageBeta := fmt.Sprintf("%s-beta", dependentPackageName)

			stableChannel := "stable"
			betaChannel := "beta"

			// Create manifests
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
						{Name: betaChannel, CurrentCSVName: mainPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
				{
					PackageName: dependentPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: dependentPackageStable},
						{Name: betaChannel, CurrentCSVName: dependentPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Create new CRDs
			mainCRDPlural := genName("ins-")
			mainCRD := newCRD(mainCRDPlural)

			// Create a new named install strategy
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
			dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

			dependentCRDPlural := genName("ins-")
			dependentCRD := newCRD(dependentCRDPlural)

			// Create new CSVs
			mainStableCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
			mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
			dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
			dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			// Create the catalog source
			mainCatalogSourceName := genName("mock-ocs-main-" + strings.ToLower(CurrentGinkgoTestDescription().TestText) + "-")
			_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{dependentCRD, mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
			defer cleanupCatalogSource()

			// Attempt to get the catalog source before creating install plan(s)
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-")
			subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or failed before checking resource presence
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, completeOrFailedFunc)
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Ensure that the desired resources have been created
			expectedSteps := map[registry.ResourceKey]struct{}{
				{Name: mainCRD.Name, Kind: "CustomResourceDefinition"}:                                                                                      {},
				{Name: dependentCRD.Name, Kind: "CustomResourceDefinition"}:                                                                                 {},
				{Name: dependentPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}:                                                           {},
				{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}:                                                                {},
				{Name: strings.Join([]string{dependentPackageStable, mainCatalogSourceName, testNamespace}, "-"), Kind: operatorsv1alpha1.SubscriptionKind}: {},
			}

			require.Equal(GinkgoT(), len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

			for _, step := range fetchedInstallPlan.Status.Plan {
				key := registry.ResourceKey{
					Name: step.Resource.Name,
					Kind: step.Resource.Kind,
				}
				_, ok := expectedSteps[key]
				require.True(GinkgoT(), ok, "couldn't find %v in expected steps: %#v", key, expectedSteps)

				// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
				delete(expectedSteps, key)
			}

			// Should have removed every matching step
			require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected")

			// Update the subscription resource to point to the beta CSV
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), *metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(GinkgoT(), err)

			// Delete orphaned csv
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), mainStableCSV.GetName(), metav1.DeleteOptions{}))

			// existing cleanup should remove this
			createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, betaChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Fetch installplan again to check for unnecessary control loops
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				compareResources(GinkgoT(), fetchedInstallPlan, fip)
				return true
			})
			require.NoError(GinkgoT(), err)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err := fetchCSV(GinkgoT(), crc, mainBetaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		})

	})

	Describe("with CRD schema change", func() {
		type schemaPayload struct {
			name            string
			expectedPhase   operatorsv1alpha1.InstallPlanPhase
			oldCRD          *apiextensions.CustomResourceDefinition
			intermediateCRD *apiextensions.CustomResourceDefinition
			newCRD          *apiextensions.CustomResourceDefinition
		}

		var min float64 = 2
		var max float64 = 256
		var newMax float64 = 50
		// generated outside of the test table so that the same naming can be used for both old and new CSVs
		mainCRDPlural := "testcrd"

		// excluded: new CRD, same version, same schema - won't trigger a CRD update
		tableEntries := []table.TableEntry{
			table.Entry("all existing versions are present, different (backwards compatible) schema", schemaPayload{
				name:          "all existing versions are present, different (backwards compatible) schema",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensions.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "a")
					oldCRD.Spec.Version = ""
					oldCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
					}
					return &oldCRD
				}(),
				newCRD: func() *apiextensions.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "a")
					newCRD.Spec.Version = ""
					newCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
						},
					}
					newCRD.Spec.Validation = &apiextensions.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensions.JSONSchemaProps{
								"spec": {
									Type:        "object",
									Description: "Spec of a test object.",
									Properties: map[string]apiextensions.JSONSchemaProps{
										"scalar": {
											Type:        "number",
											Description: "Scalar value that should have a min and max.",
											Minimum:     &min,
											Maximum:     &max,
										},
									},
								},
							},
						},
					}
					return &newCRD
				}(),
			}),
			table.Entry("all existing versions are present, different (backwards incompatible) schema", schemaPayload{name: "all existing versions are present, different (backwards incompatible) schema",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseFailed,
				oldCRD: func() *apiextensions.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "b")
					oldCRD.Spec.Version = ""
					oldCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
					}
					return &oldCRD
				}(),
				newCRD: func() *apiextensions.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "b")
					newCRD.Spec.Version = ""
					newCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
						},
					}
					newCRD.Spec.Validation = &apiextensions.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensions.JSONSchemaProps{
								"spec": {
									Type:        "object",
									Description: "Spec of a test object.",
									Properties: map[string]apiextensions.JSONSchemaProps{
										"scalar": {
											Type:        "number",
											Description: "Scalar value that should have a min and max.",
											Minimum:     &min,
											Maximum:     &newMax,
										},
									},
								},
							},
						},
					}
					return &newCRD
				}(),
			}),
			table.Entry("missing existing versions in new CRD", schemaPayload{name: "missing existing versions in new CRD",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseFailed,
				oldCRD: func() *apiextensions.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "c")
					oldCRD.Spec.Version = ""
					oldCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
						},
					}
					return &oldCRD
				}(),
				newCRD: func() *apiextensions.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "c")
					newCRD.Spec.Version = ""
					newCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
						{
							Name:    "v1",
							Served:  true,
							Storage: false,
						},
					}
					newCRD.Spec.Validation = &apiextensions.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensions.JSONSchemaProps{
								"spec": {
									Type:        "object",
									Description: "Spec of a test object.",
									Properties: map[string]apiextensions.JSONSchemaProps{
										"scalar": {
											Type:        "number",
											Description: "Scalar value that should have a min and max.",
											Minimum:     &min,
											Maximum:     &max,
										},
									},
								},
							},
						},
					}
					return &newCRD
				}()}),
			table.Entry("existing version is present in new CRD (deprecated field)", schemaPayload{name: "existing version is present in new CRD (deprecated field)",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensions.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural + "d")
					oldCRD.Spec.Version = "v1alpha1"
					return &oldCRD
				}(),
				newCRD: func() *apiextensions.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural + "d")
					newCRD.Spec.Version = "v1alpha1"
					newCRD.Spec.Validation = &apiextensions.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensions.JSONSchemaProps{
								"spec": {
									Type:        "object",
									Description: "Spec of a test object.",
									Properties: map[string]apiextensions.JSONSchemaProps{
										"scalar": {
											Type:        "number",
											Description: "Scalar value that should have a min and max.",
											Minimum:     &min,
											Maximum:     &max,
										},
									},
								},
							},
						},
					}
					return &newCRD
				}()}),
		}

		table.DescribeTable("Test", func(tt schemaPayload) {
			defer cleaner.NotifyTestComplete(GinkgoT(), true)

			mainPackageName := genName("nginx-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)

			stableChannel := "stable"
			betaChannel := "beta"

			// Create manifests
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
						{Name: betaChannel, CurrentCSVName: mainPackageBeta},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Create a new named install strategy
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

			// Create new CSVs
			mainStableCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{*tt.oldCRD}, nil, mainNamedStrategy)
			mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{*tt.oldCRD}, nil, mainNamedStrategy)

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())

			// Existing custom resource
			existingCR := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "cluster.com/v1alpha1",
					"kind":       tt.oldCRD.Spec.Names.Kind,
					"metadata": map[string]interface{}{
						"namespace": testNamespace,
						"name":      "my-cr-1",
					},
					"spec": map[string]interface{}{
						"scalar": 100,
					},
				},
			}

			// Create the catalog source
			mainCatalogSourceName := genName("mock-ocs-main-")
			_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{*tt.oldCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV})
			defer cleanupCatalogSource()

			// Attempt to get the catalog source before creating install plan(s)
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-alpha-")
			cleanupSubscription := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer cleanupSubscription()

			subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or failed before checking resource presence
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, completeOrFailedFunc)
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Ensure that the desired resources have been created
			expectedSteps := map[registry.ResourceKey]struct{}{
				{Name: tt.oldCRD.Name, Kind: "CustomResourceDefinition"}:                     {},
				{Name: mainPackageStable, Kind: operatorsv1alpha1.ClusterServiceVersionKind}: {},
			}

			require.Equal(GinkgoT(), len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

			for _, step := range fetchedInstallPlan.Status.Plan {
				key := registry.ResourceKey{
					Name: step.Resource.Name,
					Kind: step.Resource.Kind,
				}
				_, ok := expectedSteps[key]
				require.True(GinkgoT(), ok, "couldn't find %v in expected steps: %#v", key, expectedSteps)

				// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
				delete(expectedSteps, key)
			}

			// Should have removed every matching step
			require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected")

			// Create initial CR
			cleanupCR, err := createCR(c, existingCR, "cluster.com", "v1alpha1", testNamespace, tt.oldCRD.Spec.Names.Plural, "my-cr-1")
			require.NoError(GinkgoT(), err)
			defer cleanupCR()

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{*tt.newCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV}, mainManifests)

			// Attempt to get the catalog source before creating install plan(s)
			_, err = fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			// Update the subscription resource to point to the beta CSV
			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				subscription, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
				require.NoError(GinkgoT(), err)
				require.NotNil(GinkgoT(), subscription)

				subscription.Spec.Channel = betaChannel
				subscription, err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).Update(context.TODO(), subscription, metav1.UpdateOptions{})

				return err
			})

			// Wait for subscription to have a new installplan
			subscription, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err := fetchCSV(GinkgoT(), crc, mainBetaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			GinkgoT().Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		}, tableEntries...)

	})

	Describe("with deprecated version CRD", func() {

		// generated outside of the test table so that the same naming can be used for both old and new CSVs
		mainCRDPlural := genName("ins")

		type schemaPayload struct {
			name            string
			expectedPhase   operatorsv1alpha1.InstallPlanPhase
			oldCRD          *apiextensions.CustomResourceDefinition
			intermediateCRD *apiextensions.CustomResourceDefinition
			newCRD          *apiextensions.CustomResourceDefinition
		}

		// excluded: new CRD, same version, same schema - won't trigger a CRD update

		tableEntries := []table.TableEntry{
			table.Entry("upgrade CRD with deprecated version", schemaPayload{
				name:          "upgrade CRD with deprecated version",
				expectedPhase: operatorsv1alpha1.InstallPlanPhaseComplete,
				oldCRD: func() *apiextensions.CustomResourceDefinition {
					oldCRD := newCRD(mainCRDPlural)
					oldCRD.Spec.Version = "v1alpha1"
					oldCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Served:  true,
							Storage: true,
						},
					}
					return &oldCRD
				}(),
				intermediateCRD: func() *apiextensions.CustomResourceDefinition {
					intermediateCRD := newCRD(mainCRDPlural)
					intermediateCRD.Spec.Version = "v1alpha2"
					intermediateCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: true,
						},
						{
							Name:    "v1alpha1",
							Served:  false,
							Storage: false,
						},
					}
					return &intermediateCRD
				}(),
				newCRD: func() *apiextensions.CustomResourceDefinition {
					newCRD := newCRD(mainCRDPlural)
					newCRD.Spec.Version = "v1alpha2"
					newCRD.Spec.Versions = []apiextensions.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: true,
						},
						{
							Name:    "v1beta1",
							Served:  true,
							Storage: false,
						},
					}
					return &newCRD
				}(),
			}),
		}

		table.DescribeTable("Test", func(tt schemaPayload) {
			defer cleaner.NotifyTestComplete(GinkgoT(), true)

			mainPackageName := genName("nginx-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			mainPackageBeta := fmt.Sprintf("%s-beta", mainPackageName)
			mainPackageDelta := fmt.Sprintf("%s-delta", mainPackageName)

			stableChannel := "stable"
			betaChannel := "beta"
			deltaChannel := "delta"

			// Create manifests
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Create a new named install strategy
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

			// Create new CSVs
			mainStableCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{*tt.oldCRD}, nil, mainNamedStrategy)
			mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{*tt.intermediateCRD}, nil, mainNamedStrategy)
			mainDeltaCSV := newCSV(mainPackageDelta, testNamespace, mainPackageBeta, semver.MustParse("0.3.0"), []apiextensions.CustomResourceDefinition{*tt.newCRD}, nil, mainNamedStrategy)

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())

			// Create the catalog source
			mainCatalogSourceName := genName("mock-ocs-main-")
			_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{*tt.oldCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV})
			defer cleanupCatalogSource()

			// Attempt to get the catalog source before creating install plan(s)
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-")

			// this subscription will be cleaned up below without the clean up function
			createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or failed before checking resource presence
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, completeOrFailedFunc)
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Ensure CRD versions are accurate
			expectedVersions := map[string]struct{}{
				"v1alpha1": {},
			}

			validateCRDVersions(GinkgoT(), c, tt.oldCRD.GetName(), expectedVersions)

			// Update the manifest
			mainManifests = []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainPackageStable},
						{Name: betaChannel, CurrentCSVName: mainPackageBeta},
					},
					DefaultChannelName: betaChannel,
				},
			}

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{*tt.intermediateCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV}, mainManifests)

			// Attempt to get the catalog source before creating install plan(s)
			_, err = fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			// Update the subscription resource to point to the beta CSV
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), *metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(GinkgoT(), err)

			// Delete orphaned csv
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), mainStableCSV.GetName(), metav1.DeleteOptions{}))

			// existing cleanup should remove this
			createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, betaChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err := fetchCSV(GinkgoT(), crc, mainBetaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Ensure CRD versions are accurate
			expectedVersions = map[string]struct{}{
				"v1alpha1": {},
				"v1alpha2": {},
			}

			validateCRDVersions(GinkgoT(), c, tt.oldCRD.GetName(), expectedVersions)

			// Update the manifest
			mainBetaCSV = newCSV(mainPackageBeta, testNamespace, "", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{*tt.intermediateCRD}, nil, mainNamedStrategy)
			mainManifests = []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: betaChannel, CurrentCSVName: mainPackageBeta},
						{Name: deltaChannel, CurrentCSVName: mainPackageDelta},
					},
					DefaultChannelName: deltaChannel,
				},
			}

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{*tt.newCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainBetaCSV, mainDeltaCSV}, mainManifests)

			// Attempt to get the catalog source before creating install plan(s)
			_, err = fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			// Update the subscription resource to point to the beta CSV
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), *metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(GinkgoT(), err)

			// Delete orphaned csv
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), mainBetaCSV.GetName(), metav1.DeleteOptions{}))

			// existing cleanup should remove this
			createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, deltaChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
			require.NoError(GinkgoT(), err)
			GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(GinkgoT(), tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err = fetchCSV(GinkgoT(), crc, mainDeltaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Ensure CRD versions are accurate
			expectedVersions = map[string]struct{}{
				"v1alpha2": {},
				"v1beta1":  {},
			}

			validateCRDVersions(GinkgoT(), c, tt.oldCRD.GetName(), expectedVersions)
			GinkgoT().Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		}, tableEntries...)

	})

	Describe("update catalog for subscription", func() {

		// crdVersionKey uniquely identifies a version within a CRD.
		type crdVersionKey struct {
			name    string
			served  bool
			storage bool
		}
		It("AmplifyPermissions", func() {

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			// Build initial catalog
			mainPackageName := genName("nginx-amplify-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			stableChannel := "stable"
			crdPlural := genName("ins-amplify-")
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
				},
			}

			// Generate permissions
			serviceAccountName := genName("nginx-sa")
			permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}
			// Generate permissions
			clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}

			// Create the catalog sources
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), permissions, clusterPermissions)
			mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, mainNamedStrategy)
			mainCatalogName := genName("mock-ocs-amplify-")
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			// Attempt to get the catalog source before creating install plan
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update-perms1")
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

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Verify CSV is created
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Update CatalogSource with a new CSV with more permissions
			updatedPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"local.cluster.com"},
							Resources: []string{"locals"},
						},
					},
				},
			}
			updatedClusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"two.cluster.com"},
							Resources: []string{"twos"},
						},
					},
				},
			}

			// Create the catalog sources
			updatedNamedStrategy := newNginxInstallStrategy(genName("dep-"), updatedPermissions, updatedClusterPermissions)
			updatedCSV := newCSV(mainPackageStable+"-next", testNamespace, mainCSV.GetName(), semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, updatedNamedStrategy)
			updatedManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: updatedCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Update catalog with updated CSV with more permissions
			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

			_, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete before checking resource presence
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedInstallPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

			// Wait for csv to update
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// If the CSV is succeeded, we successfully rolled out the RBAC changes
		})
		It("AttenuatePermissions", func() {

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			// Build initial catalog
			mainPackageName := genName("nginx-attenuate-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			stableChannel := "stable"
			crdPlural := genName("ins-attenuate-")
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
				},
			}

			// Generate permissions
			serviceAccountName := genName("nginx-sa")
			permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"local.cluster.com"},
							Resources: []string{"locals"},
						},
					},
				},
			}

			// Generate permissions
			clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"two.cluster.com"},
							Resources: []string{"twos"},
						},
					},
				},
			}

			// Create the catalog sources
			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), permissions, clusterPermissions)
			mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, mainNamedStrategy)
			mainCatalogName := genName("mock-ocs-main-update-perms1-")
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			// Attempt to get the catalog source before creating install plan
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update-perms1")
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

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Verify CSV is created
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Update CatalogSource with a new CSV with more permissions
			updatedPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"local.cluster.com"},
							Resources: []string{"locals"},
						},
					},
				},
			}
			updatedClusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"two.cluster.com"},
							Resources: []string{"twos"},
						},
					},
				},
			}

			oldSecrets, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).List(context.TODO(), metav1.ListOptions{})
			require.NoError(GinkgoT(), err, "error listing secrets")

			// Create the catalog sources
			updatedNamedStrategy := newNginxInstallStrategy(genName("dep-"), updatedPermissions, updatedClusterPermissions)
			updatedCSV := newCSV(mainPackageStable+"-next", testNamespace, mainCSV.GetName(), semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, updatedNamedStrategy)
			updatedManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: updatedCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Update catalog with updated CSV with more permissions
			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

			// Wait for subscription to update its status
			_, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete before checking resource presence
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedInstallPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

			// Wait for csv to update
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			newSecrets, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).List(context.TODO(), metav1.ListOptions{})
			require.NoError(GinkgoT(), err, "error listing secrets")

			// Assert that the number of secrets is not increased from updating service account as part of the install plan,
			assert.EqualValues(GinkgoT(), len(oldSecrets.Items), len(newSecrets.Items))

			// And that the secret list is indeed updated.
			assert.Equal(GinkgoT(), oldSecrets.Items, newSecrets.Items)

			// Wait for ServiceAccount to not have access anymore
			err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
				res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(context.TODO(), &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: "system:serviceaccount:" + testNamespace + ":" + serviceAccountName,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Group:    "cluster.com",
							Version:  "v1alpha1",
							Resource: crdPlural,
							Verb:     rbac.VerbAll,
						},
					},
				}, metav1.CreateOptions{})
				if err != nil {
					return false, err
				}
				if res == nil {
					return false, nil
				}
				GinkgoT().Log("checking serviceaccount for permission")

				// should not be allowed
				return !res.Status.Allowed, nil
			})

		})
		It("StopOnCSVModifications", func() {

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			// Build initial catalog
			mainPackageName := genName("nginx-amplify-")
			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
			stableChannel := "stable"
			crdPlural := genName("ins-amplify-")
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
				},
			}

			// Generate permissions
			serviceAccountName := genName("nginx-sa")
			permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}

			// Generate permissions
			clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
				{
					ServiceAccountName: serviceAccountName,
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{rbac.VerbAll},
							APIGroups: []string{"cluster.com"},
							Resources: []string{crdPlural},
						},
					},
				},
			}

			// Create the catalog sources
			deploymentName := genName("dep-")
			mainNamedStrategy := newNginxInstallStrategy(deploymentName, permissions, clusterPermissions)
			mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, mainNamedStrategy)
			mainCatalogName := genName("mock-ocs-stomper-")
			mainManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: mainCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}
			_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			// Attempt to get the catalog source before creating install plan
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-stompy-")
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

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Verify CSV is created
			csv, err := awaitCSV(GinkgoT(), crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			modifiedEnv := []corev1.EnvVar{{Name: "EXAMPLE", Value: "value"}}
			modifiedDetails := operatorsv1alpha1.StrategyDetailsDeployment{
				DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
					{
						Name: deploymentName,
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
										Env:             modifiedEnv,
									},
								}},
							},
						},
					},
				},
				Permissions:        permissions,
				ClusterPermissions: clusterPermissions,
			}
			csv.Spec.InstallStrategy = operatorsv1alpha1.NamedInstallStrategy{
				StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
				StrategySpec: modifiedDetails,
			}
			_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(context.TODO(), csv, metav1.UpdateOptions{})
			require.NoError(GinkgoT(), err)

			// Wait for csv to update
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, csv.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Should have the updated env var
			err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
				dep, err := c.GetDeployment(testNamespace, deploymentName)
				if err != nil {
					return false, nil
				}
				if len(dep.Spec.Template.Spec.Containers[0].Env) == 0 {
					return false, nil
				}
				return modifiedEnv[0] == dep.Spec.Template.Spec.Containers[0].Env[0], nil
			})
			require.NoError(GinkgoT(), err)

			// Create the catalog sources
			// Updated csv has the same deployment strategy as main
			updatedCSV := newCSV(mainPackageStable+"-next", testNamespace, mainCSV.GetName(), semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, mainNamedStrategy)
			updatedManifests := []registry.PackageManifest{
				{
					PackageName: mainPackageName,
					Channels: []registry.PackageChannel{
						{Name: stableChannel, CurrentCSVName: updatedCSV.GetName()},
					},
					DefaultChannelName: stableChannel,
				},
			}

			// Update catalog with updated CSV with more permissions
			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

			_, err = fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
			require.NoError(GinkgoT(), err)

			updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete before checking resource presence
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedInstallPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

			// Wait for csv to update
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
			require.NoError(GinkgoT(), err)

			// Should have created deployment and stomped on the env changes
			updatedDep, err := c.GetDeployment(testNamespace, deploymentName)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), updatedDep)

			// Should have the updated env var
			var emptyEnv []corev1.EnvVar = nil
			require.Equal(GinkgoT(), emptyEnv, updatedDep.Spec.Template.Spec.Containers[0].Env)
		})
		It("UpdateSingleExistingCRDOwner", func() {

			mainPackageName := genName("nginx-update-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)

			stableChannel := "stable"

			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

			crdPlural := genName("ins-update-")
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
				},
			}

			updatedCRD := apiextensions.CustomResourceDefinition{
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
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
						},
					},
					Names: apiextensions.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: "Namespaced",
				},
			}

			mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, mainNamedStrategy)

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			mainCatalogName := genName("mock-ocs-main-update-")

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

			// Create the catalog sources
			_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
			defer cleanupMainCatalogSource()

			// Attempt to get the catalog source before creating install plan
			_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(GinkgoT(), err)

			subscriptionName := genName("sub-nginx-update-before-")
			createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

			subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)
			require.NotNil(GinkgoT(), subscription)
			require.NotNil(GinkgoT(), subscription.Status.InstallPlanRef)
			require.Equal(GinkgoT(), mainCSV.GetName(), subscription.Status.CurrentCSV)

			installPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete before checking resource presence
			fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Fetch installplan again to check for unnecessary control loops
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				compareResources(GinkgoT(), fetchedInstallPlan, fip)
				return true
			})
			require.NoError(GinkgoT(), err)

			// Verify CSV is created
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, mainCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{updatedCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV}, mainManifests)

			// Update the subscription resource
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), *metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(GinkgoT(), err)

			// existing cleanup should remove this
			subscriptionName = genName("sub-nginx-update-after-")
			subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
			defer subscriptionCleanup()

			// Wait for subscription to update
			updatedSubscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(GinkgoT(), err)

			// Verify installplan created and installed
			fetchedUpdatedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, updatedSubscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
			require.NoError(GinkgoT(), err)
			require.NotEqual(GinkgoT(), fetchedInstallPlan.GetName(), fetchedUpdatedInstallPlan.GetName())

			// Wait for csv to update
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, mainCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			// Get the CRD to see if it is updated
			fetchedCRD, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), len(fetchedCRD.Spec.Versions), len(updatedCRD.Spec.Versions), "The CRD versions counts don't match")

			fetchedCRDVersions := map[crdVersionKey]struct{}{}
			for _, version := range fetchedCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				fetchedCRDVersions[key] = struct{}{}
			}

			for _, version := range updatedCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				_, ok := fetchedCRDVersions[key]
				require.True(GinkgoT(), ok, "couldn't find %v in fetched CRD versions: %#v", key, fetchedCRDVersions)
			}
		})
		It("UpdatePreexistingCRDFailed", func() {

			c := newKubeClient(GinkgoT())
			crc := newCRClient(GinkgoT())
			defer func() {
				require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
			}()

			mainPackageName := genName("nginx-update2-")

			mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)

			stableChannel := "stable"

			mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

			crdPlural := genName("ins-update2-")
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
				},
			}

			updatedCRD := apiextensions.CustomResourceDefinition{
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
						{
							Name:    "v1alpha2",
							Served:  true,
							Storage: false,
						},
					},
					Names: apiextensions.CustomResourceDefinitionNames{
						Plural:   crdPlural,
						Singular: crdPlural,
						Kind:     crdPlural,
						ListKind: "list" + crdPlural,
					},
					Scope: "Namespaced",
				},
			}

			expectedCRDVersions := map[crdVersionKey]struct{}{}
			for _, version := range mainCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				expectedCRDVersions[key] = struct{}{}
			}

			// Create the initial CSV
			cleanupCRD, err := createCRD(c, mainCRD)
			require.NoError(GinkgoT(), err)
			defer cleanupCRD()

			mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, mainNamedStrategy)

			mainCatalogName := genName("mock-ocs-main-update2-")

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

			// Create the catalog sources
			_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{updatedCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
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

			require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Fetch installplan again to check for unnecessary control loops
			fetchedInstallPlan, err = fetchInstallPlan(GinkgoT(), crc, fetchedInstallPlan.GetName(), func(fip *operatorsv1alpha1.InstallPlan) bool {
				compareResources(GinkgoT(), fetchedInstallPlan, fip)
				return true
			})
			require.NoError(GinkgoT(), err)

			// Verify CSV is created
			_, err = awaitCSV(GinkgoT(), crc, testNamespace, mainCSV.GetName(), csvAnyChecker)
			require.NoError(GinkgoT(), err)

			// Get the CRD to see if it is updated
			fetchedCRD, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
			require.NoError(GinkgoT(), err)
			require.Equal(GinkgoT(), len(fetchedCRD.Spec.Versions), len(mainCRD.Spec.Versions), "The CRD versions counts don't match")

			fetchedCRDVersions := map[crdVersionKey]struct{}{}
			for _, version := range fetchedCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				fetchedCRDVersions[key] = struct{}{}
			}

			for _, version := range mainCRD.Spec.Versions {
				key := crdVersionKey{
					name:    version.Name,
					served:  version.Served,
					storage: version.Storage,
				}
				_, ok := fetchedCRDVersions[key]
				require.True(GinkgoT(), ok, "couldn't find %v in fetched CRD versions: %#v", key, fetchedCRDVersions)
			}
		})
		AfterEach(func() {
			defer cleaner.NotifyTestComplete(GinkgoT(), true)
		})
	})

	// This It spec creates an InstallPlan with a CSV containing a set of permissions to be resolved.
	It("creation with permissions", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		packageName := genName("nginx")
		stableChannel := "stable"
		stableCSVName := packageName + "-stable"

		// Create manifests
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{
						Name:           stableChannel,
						CurrentCSVName: stableCSVName,
					},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create new CRDs
		crdPlural := genName("ins")
		crd := newCRD(crdPlural)

		// Generate permissions
		serviceAccountName := genName("nginx-sa")
		permissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccountName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{rbac.VerbAll},
						APIGroups: []string{"cluster.com"},
						Resources: []string{crdPlural},
					},
				},
			},
		}

		// Generate permissions
		clusterPermissions := []operatorsv1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccountName,
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{rbac.VerbAll},
						APIGroups: []string{"cluster.com"},
						Resources: []string{crdPlural},
					},
				},
			},
		}

		// Create a new NamedInstallStrategy
		namedStrategy := newNginxInstallStrategy(genName("dep-"), permissions, clusterPermissions)

		// Create new CSVs
		stableCSV := newCSV(stableCSVName, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		// Create CatalogSource
		mainCatalogSourceName := genName("nginx-catalog")
		_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{stableCSV})
		defer cleanupCatalogSource()

		// Attempt to get CatalogSource
		_, err := fetchCatalogSource(GinkgoT(), crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, mainCatalogSourceName, packageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Attempt to get InstallPlan
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed, operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		require.NotEqual(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase, "InstallPlan failed")

		// Expect correct RBAC resources to be resolved and created
		expectedSteps := map[registry.ResourceKey]struct{}{
			{Name: crd.Name, Kind: "CustomResourceDefinition"}:   {},
			{Name: stableCSVName, Kind: "ClusterServiceVersion"}: {},
			{Name: serviceAccountName, Kind: "ServiceAccount"}:   {},
			{Name: stableCSVName, Kind: "Role"}:                  {},
			{Name: stableCSVName, Kind: "RoleBinding"}:           {},
			{Name: stableCSVName, Kind: "ClusterRole"}:           {},
			{Name: stableCSVName, Kind: "ClusterRoleBinding"}:    {},
		}

		require.Equal(GinkgoT(), len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

		for _, step := range fetchedInstallPlan.Status.Plan {
			key := registry.ResourceKey{
				Name: step.Resource.Name,
				Kind: step.Resource.Kind,
			}
			for expected := range expectedSteps {
				if expected == key {
					delete(expectedSteps, expected)
				} else if strings.HasPrefix(key.Name, expected.Name) && key.Kind == expected.Kind {
					delete(expectedSteps, expected)
				} else {
					GinkgoT().Logf("%v, %v: %v && %v", key, expected, strings.HasPrefix(key.Name, expected.Name), key.Kind == expected.Kind)
				}
			}

			// This operator was installed into a global operator group, so the roles should have been lifted to clusterroles
			if step.Resource.Kind == "Role" {
				err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
					_, err = c.GetClusterRole(step.Resource.Name)
					if err != nil {
						if k8serrors.IsNotFound(err) {
							return false, nil
						}
						return false, err
					}
					return true, nil
				})
			}
			if step.Resource.Kind == "RoleBinding" {
				err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
					_, err = c.GetClusterRoleBinding(step.Resource.Name)
					if err != nil {
						if k8serrors.IsNotFound(err) {
							return false, nil
						}
						return false, err
					}
					return true, nil
				})
			}
		}

		// Should have removed every matching step
		require.Equal(GinkgoT(), 0, len(expectedSteps), "Actual resource steps do not match expected: %#v", expectedSteps)

		// the test from here out verifies created RBAC is removed after CSV deletion
		createdClusterRoles, err := c.KubernetesInterface().RbacV1().ClusterRoles().List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
		createdClusterRoleNames := map[string]struct{}{}
		for _, role := range createdClusterRoles.Items {
			createdClusterRoleNames[role.GetName()] = struct{}{}
			GinkgoT().Logf("Monitoring cluster role %v", role.GetName())
		}

		createdClusterRoleBindings, err := c.KubernetesInterface().RbacV1().ClusterRoleBindings().List(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
		createdClusterRoleBindingNames := map[string]struct{}{}
		for _, binding := range createdClusterRoleBindings.Items {
			createdClusterRoleBindingNames[binding.GetName()] = struct{}{}
			GinkgoT().Logf("Monitoring cluster role binding %v", binding.GetName())
		}

		// can't query by owner reference, so just use the name we know is in the install plan
		createdServiceAccountNames := map[string]struct{}{serviceAccountName: {}}
		GinkgoT().Logf("Monitoring service account %v", serviceAccountName)

		crWatcher, err := c.KubernetesInterface().RbacV1().ClusterRoles().Watch(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
		require.NoError(GinkgoT(), err)
		crbWatcher, err := c.KubernetesInterface().RbacV1().ClusterRoleBindings().Watch(context.TODO(), metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
		require.NoError(GinkgoT(), err)
		saWatcher, err := c.KubernetesInterface().CoreV1().ServiceAccounts(testNamespace).Watch(context.TODO(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)

		done := make(chan struct{})
		errExit := make(chan error)
		go func() {
			for {
				select {
				case evt, ok := <-crWatcher.ResultChan():
					if !ok {
						errExit <- errors.New("cr watch channel closed unexpectedly")
						return
					}
					if evt.Type == watch.Deleted {
						cr, ok := evt.Object.(*rbacv1.ClusterRole)
						if !ok {
							continue
						}
						delete(createdClusterRoleNames, cr.GetName())
						if len(createdClusterRoleNames) == 0 && len(createdClusterRoleBindingNames) == 0 && len(createdServiceAccountNames) == 0 {
							done <- struct{}{}
							return
						}
					}
				case evt, ok := <-crbWatcher.ResultChan():
					if !ok {
						errExit <- errors.New("crb watch channel closed unexpectedly")
						return
					}
					if evt.Type == watch.Deleted {
						crb, ok := evt.Object.(*rbacv1.ClusterRoleBinding)
						if !ok {
							continue
						}
						delete(createdClusterRoleBindingNames, crb.GetName())
						if len(createdClusterRoleNames) == 0 && len(createdClusterRoleBindingNames) == 0 && len(createdServiceAccountNames) == 0 {
							done <- struct{}{}
							return
						}
					}
				case evt, ok := <-saWatcher.ResultChan():
					if !ok {
						errExit <- errors.New("sa watch channel closed unexpectedly")
						return
					}
					if evt.Type == watch.Deleted {
						sa, ok := evt.Object.(*corev1.ServiceAccount)
						if !ok {
							continue
						}
						delete(createdServiceAccountNames, sa.GetName())
						if len(createdClusterRoleNames) == 0 && len(createdClusterRoleBindingNames) == 0 && len(createdServiceAccountNames) == 0 {
							done <- struct{}{}
							return
						}
					}
				case <-time.After(pollDuration):
					done <- struct{}{}
					return
				}
			}
		}()
		GinkgoT().Logf("Deleting CSV '%v' in namespace %v", stableCSVName, testNamespace)
		require.NoError(GinkgoT(), crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		select {
		case <-done:
			break
		case err := <-errExit:
			GinkgoT().Fatal(err)
		}

		require.Emptyf(GinkgoT(), createdClusterRoleNames, "unexpected cluster role remain: %v", createdClusterRoleNames)
		require.Emptyf(GinkgoT(), createdClusterRoleBindingNames, "unexpected cluster role binding remain: %v", createdClusterRoleBindingNames)
		require.Emptyf(GinkgoT(), createdServiceAccountNames, "unexpected service account remain: %v", createdServiceAccountNames)
	})

	It("CRD validation", func() {
		// Tests if CRD validation works with the "minimum" property after being
		// pulled from a CatalogSource's operator-registry.
		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		crdPlural := genName("ins")
		crdName := crdPlural + ".cluster.com"
		var min float64 = 2
		var max float64 = 256

		// Create CRD with offending property
		crd := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group:   "cluster.com",
				Version: "v1alpha1",
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
				Validation: &apiextensions.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
						Properties: map[string]apiextensions.JSONSchemaProps{
							"spec": {
								Type:        "object",
								Description: "Spec of a test object.",
								Properties: map[string]apiextensions.JSONSchemaProps{
									"scalar": {
										Type:        "number",
										Description: "Scalar value that should have a min and max.",
										Minimum:     &min,
										Maximum:     &max,
									},
								},
							},
						},
					},
				},
			},
		}

		// Create CSV
		packageName := genName("nginx-")
		stableChannel := "stable"
		packageNameStable := packageName + "-" + stableChannel
		namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		csv := newCSV(packageNameStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

		// Create PackageManifests
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: packageNameStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the CatalogSource
		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())
		catalogSourceName := genName("mock-nginx-")
		_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []operatorsv1alpha1.ClusterServiceVersion{csv})
		defer cleanupCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSource(GinkgoT(), crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		cleanupSubscription := createSubscriptionForCatalog(GinkgoT(), crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(GinkgoT(), crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete, operatorsv1alpha1.InstallPlanPhaseFailed))
		require.NoError(GinkgoT(), err)
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)
	})

	It("unpacks bundle image", func() {

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		ns, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("ns-"),
			},
		}, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		deleteOpts := &metav1.DeleteOptions{}
		defer func() {
			require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), ns.GetName(), *deleteOpts))
		}()

		catsrc := &operatorsv1alpha1.CatalogSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("kiali-"),
				Namespace: ns.GetName(),
				Labels:    map[string]string{"olm.catalogSource": "kaili-catalog"},
			},
			Spec: operatorsv1alpha1.CatalogSourceSpec{
				Image:      "quay.io/olmtest/single-bundle-index:1.0.0",
				SourceType: operatorsv1alpha1.SourceTypeGrpc,
			},
		}
		catsrc, err = crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Create(context.TODO(), catsrc, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		// Wait for the CatalogSource to be ready
		catsrc, err = fetchCatalogSource(GinkgoT(), crc, catsrc.GetName(), catsrc.GetNamespace(), catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Generate a Subscription
		subName := genName("kiali-")
		createSubscriptionForCatalog(GinkgoT(), crc, catsrc.GetNamespace(), subName, catsrc.GetName(), "kiali", stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

		sub, err := fetchSubscription(GinkgoT(), crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)

		// Wait for the expected InstallPlan's execution to either fail or succeed
		ipName := sub.Status.InstallPlanRef.Name
		ip, err := waitForInstallPlan(GinkgoT(), crc, ipName, sub.GetNamespace(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseFailed, operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, ip.Status.Phase, "InstallPlan not complete")

		// Ensure the InstallPlan contains the steps resolved from the bundle image
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

	// This It spec verifies that, in cases where there are multiple options to fulfil a dependency
	// across multiple catalogs, we only generate one installplan with one set of resolved resources.
	It("consistent generation", func() {

		// Configure catalogs:
		//  - one catalog with a package that has a dependency
		//  - several duplicate catalog with a package that satisfies the dependency
		// Install the package from the main catalog
		// Should see only 1 installplan created
		// Should see the main CSV installed

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		log := func(s string) {
			GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
		}

		ns := &corev1.Namespace{}
		ns.SetName(genName("ns-"))

		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())

		// Create a namespace an OperatorGroup
		ns, err := c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)
		deleteOpts := &metav1.DeleteOptions{}
		defer func() {
			require.NoError(GinkgoT(), c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), ns.GetName(), *deleteOpts))
		}()

		og := &operatorsv1.OperatorGroup{}
		og.SetName("og")
		_, err = crc.OperatorsV1().OperatorGroups(ns.GetName()).Create(context.TODO(), og, metav1.CreateOptions{})
		require.NoError(GinkgoT(), err)

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		crdPlural := genName("ins-")

		dependentCRD := newCRD(crdPlural)
		mainCSV := newCSV(mainPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentCSV := newCSV(dependentPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().Subscriptions(ns.GetName()).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		dependentCatalogName := genName("mock-ocs-dependent-")
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

		dependentManifests := []registry.PackageManifest{
			{
				PackageName: dependentPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: dependentPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		// Create the dependent catalog source
		_, cleanupDependentCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, dependentCatalogName, ns.GetName(), dependentManifests, []apiextensions.CustomResourceDefinition{dependentCRD}, []operatorsv1alpha1.ClusterServiceVersion{dependentCSV})
		defer cleanupDependentCatalogSource()

		// Attempt to get the catalog source before creating install plan
		dependentCatalogSource, err := fetchCatalogSource(GinkgoT(), crc, dependentCatalogName, ns.GetName(), catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		// Create the alt dependent catalog sources
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ { // Creating more increases the odds that the race condition will be triggered
			wg.Add(1)
			go func(i int) {
				// Create a CatalogSource pointing to the grpc pod
				addressSource := &operatorsv1alpha1.CatalogSource{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1alpha1.CatalogSourceKind,
						APIVersion: operatorsv1alpha1.CatalogSourceCRDAPIVersion,
					},
					Spec: operatorsv1alpha1.CatalogSourceSpec{
						SourceType: operatorsv1alpha1.SourceTypeGrpc,
						Address:    dependentCatalogSource.Status.RegistryServiceStatus.Address(),
					},
				}
				addressSource.SetName(genName("alt-dep-"))

				_, err := crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Create(context.TODO(), addressSource, metav1.CreateOptions{})
				require.NoError(GinkgoT(), err)

				// Attempt to get the catalog source before creating install plan
				_, err = fetchCatalogSource(GinkgoT(), crc, addressSource.GetName(), ns.GetName(), catalogSourceRegistryPodSynced)
				require.NoError(GinkgoT(), err)
				wg.Done()
			}(i)
		}
		wg.Wait()

		// Create the main catalog source
		_, cleanupMainCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, ns.GetName(), mainManifests, nil, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err = fetchCatalogSource(GinkgoT(), crc, mainCatalogName, ns.GetName(), catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(GinkgoT(), crc, ns.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(GinkgoT(), crc, ns.GetName(), subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(GinkgoT(), err)
		require.NotNil(GinkgoT(), subscription)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, ns.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		require.NoError(GinkgoT(), err)
		log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase))

		require.Equal(GinkgoT(), operatorsv1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Verify CSV is created
		_, err = awaitCSV(GinkgoT(), crc, ns.GetName(), mainCSV.GetName(), csvSucceededChecker)
		require.NoError(GinkgoT(), err)

		// Make sure to clean up the installed CRD
		defer func() {
			require.NoError(GinkgoT(), c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Delete(context.TODO(), dependentCRD.GetName(), *deleteOpts))
		}()

		// ensure there is only one installplan
		ips, err := crc.OperatorsV1alpha1().InstallPlans(ns.GetName()).List(context.TODO(), metav1.ListOptions{})
		require.NoError(GinkgoT(), err)
		require.Equal(GinkgoT(), 1, len(ips.Items), "If this test fails it should be taken seriously and not treated as a flake. \n%v", ips.Items)
	})

})

type checkInstallPlanFunc func(fip *operatorsv1alpha1.InstallPlan) bool

func validateCRDVersions(t GinkgoTInterface, c operatorclient.ClientInterface, name string, expectedVersions map[string]struct{}) {
	// Retrieve CRD information
	crd, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), name, metav1.GetOptions{})
	require.NoError(t, err)

	require.Equal(t, len(expectedVersions), len(crd.Spec.Versions), "number of CRD versions don't not match installed")

	for _, version := range crd.Spec.Versions {
		_, ok := expectedVersions[version.Name]
		require.True(t, ok, "couldn't find %v in expected versions: %#v", version.Name, expectedVersions)

		// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
		delete(expectedVersions, version.Name)
	}

	// Should have removed every matching version
	require.Equal(t, 0, len(expectedVersions), "Actual CRD versions do not match expected")
}

func buildInstallPlanPhaseCheckFunc(phases ...operatorsv1alpha1.InstallPlanPhase) checkInstallPlanFunc {
	return func(fip *operatorsv1alpha1.InstallPlan) bool {
		satisfiesAny := false
		for _, phase := range phases {
			satisfiesAny = satisfiesAny || fip.Status.Phase == phase
		}
		return satisfiesAny
	}
}

func buildInstallPlanCleanupFunc(crc versioned.Interface, namespace string, installPlan *operatorsv1alpha1.InstallPlan) cleanupFunc {
	return func() {
		deleteOptions := &metav1.DeleteOptions{}
		for _, step := range installPlan.Status.Plan {
			if step.Resource.Kind == operatorsv1alpha1.ClusterServiceVersionKind {
				if err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(context.TODO(), step.Resource.Name, *deleteOptions); err != nil {
					fmt.Println(err)
				}
			}
		}

		if err := crc.OperatorsV1alpha1().InstallPlans(namespace).Delete(context.TODO(), installPlan.GetName(), *deleteOptions); err != nil {
			fmt.Println(err)
		}

		err := waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().InstallPlans(namespace).Get(context.TODO(), installPlan.GetName(), metav1.GetOptions{})
			return err
		})

		if err != nil {
			fmt.Println(err)
		}
	}
}

func fetchInstallPlan(t GinkgoTInterface, c versioned.Interface, name string, checkPhase checkInstallPlanFunc) (*operatorsv1alpha1.InstallPlan, error) {
	return fetchInstallPlanWithNamespace(t, c, name, testNamespace, checkPhase)
}

func fetchInstallPlanWithNamespace(t GinkgoTInterface, c versioned.Interface, name string, namespace string, checkPhase checkInstallPlanFunc) (*operatorsv1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *operatorsv1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil || fetchedInstallPlan == nil {
			return false, err
		}

		return checkPhase(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

// do not return an error if the installplan has not been created yet
func waitForInstallPlan(t GinkgoTInterface, c versioned.Interface, name string, namespace string, checkPhase checkInstallPlanFunc) (*operatorsv1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *operatorsv1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil && !k8serrors.IsNotFound(err) {
			return false, err
		}

		return checkPhase(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

func newNginxInstallStrategy(name string, permissions []operatorsv1alpha1.StrategyDeploymentPermissions, clusterPermissions []operatorsv1alpha1.StrategyDeploymentPermissions) operatorsv1alpha1.NamedInstallStrategy {
	// Create an nginx details deployment
	details := operatorsv1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
			{
				Name: name,
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
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
	}
	namedStrategy := operatorsv1alpha1.NamedInstallStrategy{
		StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
		StrategySpec: details,
	}

	return namedStrategy
}

func newCRD(plural string) apiextensions.CustomResourceDefinition {
	crd := apiextensions.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: plural + ".cluster.com",
		},
		Spec: apiextensions.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: apiextensions.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: plural,
				Kind:     plural,
				ListKind: plural + "list",
			},
			Scope: "Namespaced",
		},
	}

	return crd
}

func newCSV(name, namespace, replaces string, version semver.Version, owned []apiextensions.CustomResourceDefinition, required []apiextensions.CustomResourceDefinition, namedStrategy operatorsv1alpha1.NamedInstallStrategy) operatorsv1alpha1.ClusterServiceVersion {
	csvType = metav1.TypeMeta{
		Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
		APIVersion: operatorsv1alpha1.GroupVersion,
	}

	csv := operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			Replaces:       replaces,
			Version:        opver.OperatorVersion{version},
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
			InstallStrategy: namedStrategy,
			CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
				Owned:    nil,
				Required: nil,
			},
		},
	}

	// Populate owned and required
	for _, crd := range owned {
		crdVersion := "v1alpha1"
		if crd.Spec.Version != "" {
			crdVersion = crd.Spec.Version
		} else {
			for _, v := range crd.Spec.Versions {
				if v.Served && v.Storage {
					crdVersion = v.Name
					break
				}
			}
		}
		desc := operatorsv1alpha1.CRDDescription{
			Name:        crd.GetName(),
			Version:     crdVersion,
			Kind:        crd.Spec.Names.Plural,
			DisplayName: crd.GetName(),
			Description: crd.GetName(),
		}
		csv.Spec.CustomResourceDefinitions.Owned = append(csv.Spec.CustomResourceDefinitions.Owned, desc)
	}

	for _, crd := range required {
		crdVersion := "v1alpha1"
		if crd.Spec.Version != "" {
			crdVersion = crd.Spec.Version
		} else {
			for _, v := range crd.Spec.Versions {
				if v.Served && v.Storage {
					crdVersion = v.Name
					break
				}
			}
		}
		desc := operatorsv1alpha1.CRDDescription{
			Name:        crd.GetName(),
			Version:     crdVersion,
			Kind:        crd.Spec.Names.Plural,
			DisplayName: crd.GetName(),
			Description: crd.GetName(),
		}
		csv.Spec.CustomResourceDefinitions.Required = append(csv.Spec.CustomResourceDefinitions.Required, desc)
	}

	return csv
}
