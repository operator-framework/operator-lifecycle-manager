package e2e

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/kubernetes/pkg/apis/rbac"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	opver "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/version"
)

type checkInstallPlanFunc func(fip *v1alpha1.InstallPlan) bool

func validateCRDVersions(t *testing.T, c operatorclient.ClientInterface, name string, expectedVersions map[string]struct{}) {
	// Retrieve CRD information
	crd, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(name, metav1.GetOptions{})
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

func buildInstallPlanPhaseCheckFunc(phases ...v1alpha1.InstallPlanPhase) checkInstallPlanFunc {
	return func(fip *v1alpha1.InstallPlan) bool {
		satisfiesAny := false
		for _, phase := range phases {
			satisfiesAny = satisfiesAny || fip.Status.Phase == phase
		}
		return satisfiesAny
	}
}

func buildInstallPlanCleanupFunc(crc versioned.Interface, namespace string, installPlan *v1alpha1.InstallPlan) cleanupFunc {
	return func() {
		deleteOptions := &metav1.DeleteOptions{}
		for _, step := range installPlan.Status.Plan {
			if step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
				if err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(step.Resource.Name, deleteOptions); err != nil {
					fmt.Println(err)
				}
			}
		}

		if err := crc.OperatorsV1alpha1().InstallPlans(namespace).Delete(installPlan.GetName(), deleteOptions); err != nil {
			fmt.Println(err)
		}

		err := waitForDelete(func() error {
			_, err := crc.OperatorsV1alpha1().InstallPlans(namespace).Get(installPlan.GetName(), metav1.GetOptions{})
			return err
		})

		if err != nil {
			fmt.Println(err)
		}
	}
}

func fetchInstallPlan(t *testing.T, c versioned.Interface, name string, checkPhase checkInstallPlanFunc) (*v1alpha1.InstallPlan, error) {
	return fetchInstallPlanWithNamespace(t, c, name, testNamespace, checkPhase)
}

func fetchInstallPlanWithNamespace(t *testing.T, c versioned.Interface, name string, namespace string, checkPhase checkInstallPlanFunc) (*v1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *v1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(namespace).Get(name, metav1.GetOptions{})
		if err != nil || fetchedInstallPlan == nil {
			return false, err
		}

		return checkPhase(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

func newNginxInstallStrategy(name string, permissions []v1alpha1.StrategyDeploymentPermissions, clusterPermissions []v1alpha1.StrategyDeploymentPermissions) v1alpha1.NamedInstallStrategy {
	// Create an nginx details deployment
	details := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
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
	namedStrategy := v1alpha1.NamedInstallStrategy{
		StrategyName: v1alpha1.InstallStrategyNameDeployment,
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

func newCSV(name, namespace, replaces string, version semver.Version, owned []apiextensions.CustomResourceDefinition, required []apiextensions.CustomResourceDefinition, namedStrategy v1alpha1.NamedInstallStrategy) v1alpha1.ClusterServiceVersion {
	csvType = metav1.TypeMeta{
		Kind:       v1alpha1.ClusterServiceVersionKind,
		APIVersion: v1alpha1.GroupVersion,
	}

	csv := v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:       replaces,
			Version:        opver.OperatorVersion{version},
			MinKubeVersion: "0.0.0",
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			InstallStrategy: namedStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
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
		desc := v1alpha1.CRDDescription{
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
		desc := v1alpha1.CRDDescription{
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

func TestInstallPlanWithCSVsAcrossMultipleCatalogSources(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	log := func(s string) {
		t.Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
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

	c := newKubeClient(t)
	crc := newCRClient(t)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
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
	require.NotEqual(t, "", testNamespace)
	_, cleanupDependentCatalogSource := createInternalCatalogSource(t, c, crc, dependentCatalogName, testNamespace, dependentManifests, []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{dependentCSV})
	defer cleanupDependentCatalogSource()
	// Attempt to get the catalog source before creating install plan
	_, err := fetchCatalogSource(t, crc, dependentCatalogName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})
	defer cleanupMainCatalogSource()
	// Attempt to get the catalog source before creating install plan
	_, err = fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create expected install plan step sources
	expectedStepSources := map[registry.ResourceKey]registry.ResourceKey{
		registry.ResourceKey{Name: dependentCRD.Name, Kind: "CustomResourceDefinition"}:                                                                       {Name: dependentCatalogName, Namespace: testNamespace},
		registry.ResourceKey{Name: dependentPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                                                          {Name: dependentCatalogName, Namespace: testNamespace},
		registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                                                               {Name: mainCatalogName, Namespace: testNamespace},
		registry.ResourceKey{Name: strings.Join([]string{dependentPackageStable, dependentCatalogName, testNamespace}, "-"), Kind: v1alpha1.SubscriptionKind}: {Name: dependentCatalogName, Namespace: testNamespace},
	}

	subscriptionName := genName("sub-nginx-")
	subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.InstallPlanRef.Name

	// Wait for InstallPlan to be status: Complete before checking resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase))

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Fetch installplan again to check for unnecessary control loops
	fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
		compareResources(t, fetchedInstallPlan, fip)
		return true
	})
	require.NoError(t, err)

	require.Equal(t, len(expectedStepSources), len(fetchedInstallPlan.Status.Plan), "Number of resolved steps matches the number of expected steps")

	// Ensure resolved step resources originate from the correct catalog sources
	log(fmt.Sprintf("%#v", expectedStepSources))
	for _, step := range fetchedInstallPlan.Status.Plan {
		log(fmt.Sprintf("checking %s", step.Resource))
		key := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
		expectedSource, ok := expectedStepSources[key]
		require.True(t, ok, "didn't find %v", key)
		require.Equal(t, expectedSource.Name, step.Resource.CatalogSource)
		require.Equal(t, expectedSource.Namespace, step.Resource.CatalogSourceNamespace)

		// delete
	}
EXPECTED:
	for key := range expectedStepSources {
		for _, step := range fetchedInstallPlan.Status.Plan {
			if step.Resource.Name == key.Name && step.Resource.Kind == key.Kind {
				continue EXPECTED
			}
		}
		t.Fatalf("expected step %s not found in %#v", key, fetchedInstallPlan.Status.Plan)
	}

	log("All expected resources resolved")

	// Verify that the dependent subscription is in a good state
	dependentSubscription, err := fetchSubscription(t, crc, testNamespace, strings.Join([]string{dependentPackageStable, dependentCatalogName, testNamespace}, "-"), subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, dependentSubscription)
	require.NotNil(t, dependentSubscription.Status.InstallPlanRef)
	require.Equal(t, dependentCSV.GetName(), dependentSubscription.Status.CurrentCSV)

	// Verify CSV is created
	_, err = awaitCSV(t, crc, testNamespace, dependentCSV.GetName(), csvAnyChecker)
	require.NoError(t, err)

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

	updateInternalCatalog(t, c, crc, dependentCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{dependentCSV, updatedDependentCSV}, dependentManifests)

	// Wait for subscription to update
	updatedDepSubscription, err := fetchSubscription(t, crc, testNamespace, strings.Join([]string{dependentPackageStable, dependentCatalogName, testNamespace}, "-"), subscriptionHasCurrentCSV(updatedDependentCSV.GetName()))
	require.NoError(t, err)

	// Verify installplan created and installed
	fetchedUpdatedDepInstallPlan, err := fetchInstallPlan(t, crc, updatedDepSubscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	log(fmt.Sprintf("Install plan %s fetched with status %s", fetchedUpdatedDepInstallPlan.GetName(), fetchedUpdatedDepInstallPlan.Status.Phase))
	require.NotEqual(t, fetchedInstallPlan.GetName(), fetchedUpdatedDepInstallPlan.GetName())

	// Wait for csv to update
	_, err = awaitCSV(t, crc, testNamespace, updatedDependentCSV.GetName(), csvAnyChecker)
	require.NoError(t, err)
}

func TestCreateInstallPlanWithPreExistingCRDOwners(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)
	t.Run("OnePreExistingCRDOwner", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

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

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		// Create the catalog source
		mainCatalogSourceName := genName("mock-ocs-main-" + strings.ToLower(t.Name()) + "-")
		_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{dependentCRD, mainCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
		defer cleanupCatalogSource()
		// Attempt to get the catalog source before creating install plan(s)
		_, err := fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		expectedSteps := map[registry.ResourceKey]struct{}{
			registry.ResourceKey{Name: mainCRD.Name, Kind: "CustomResourceDefinition"}:              {},
			registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}: {},
		}

		// Create the preexisting CRD and CSV
		cleanupCRD, err := createCRD(c, dependentCRD)
		require.NoError(t, err)
		defer cleanupCRD()
		cleanupCSV, err := createCSV(t, c, crc, dependentBetaCSV, testNamespace, true, false)
		require.NoError(t, err)
		defer cleanupCSV()
		t.Log("Dependent CRD and preexisting CSV created")

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)

		installPlanName := subscription.Status.Install.Name

		// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Fetch installplan again to check for unnecessary control loops
		fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
			compareResources(t, fetchedInstallPlan, fip)
			return true
		})
		require.NoError(t, err)

		for _, step := range fetchedInstallPlan.Status.Plan {
			t.Logf("%#v", step)
		}
		require.Equal(t, len(fetchedInstallPlan.Status.Plan), len(expectedSteps), "number of expected steps does not match installed")
		t.Logf("Number of resolved steps matches the number of expected steps")

		for _, step := range fetchedInstallPlan.Status.Plan {
			key := registry.ResourceKey{
				Name: step.Resource.Name,
				Kind: step.Resource.Kind,
			}
			_, ok := expectedSteps[key]
			require.True(t, ok)

			// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
			delete(expectedSteps, key)
		}

		// Should have removed every matching step
		require.Equal(t, 0, len(expectedSteps), "Actual resource steps do not match expected")
	})

	t.Run("PreExistingCRDOwnerIsReplaced", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

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

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
		}()

		// Create the catalog source
		mainCatalogSourceName := genName("mock-ocs-main-" + strings.ToLower(t.Name()) + "-")
		_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{dependentCRD, mainCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
		defer cleanupCatalogSource()
		// Attempt to get the catalog source before creating install plan(s)
		_, err := fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		subscriptionName := genName("sub-nginx-")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)

		installPlanName := subscription.Status.Install.Name

		// Wait for InstallPlan to be status: Complete or failed before checking resource presence
		completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed)
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, completeOrFailedFunc)
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Ensure that the desired resources have been created
		expectedSteps := map[registry.ResourceKey]struct{}{
			registry.ResourceKey{Name: mainCRD.Name, Kind: "CustomResourceDefinition"}:                                                                             {},
			registry.ResourceKey{Name: dependentCRD.Name, Kind: "CustomResourceDefinition"}:                                                                        {},
			registry.ResourceKey{Name: dependentPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                                                           {},
			registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                                                                {},
			registry.ResourceKey{Name: strings.Join([]string{dependentPackageStable, mainCatalogSourceName, testNamespace}, "-"), Kind: v1alpha1.SubscriptionKind}: {},
		}

		require.Equal(t, len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

		for _, step := range fetchedInstallPlan.Status.Plan {
			key := registry.ResourceKey{
				Name: step.Resource.Name,
				Kind: step.Resource.Kind,
			}
			_, ok := expectedSteps[key]
			require.True(t, ok, "couldn't find %v in expected steps: %#v", key, expectedSteps)

			// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
			delete(expectedSteps, key)
		}

		// Should have removed every matching step
		require.Equal(t, 0, len(expectedSteps), "Actual resource steps do not match expected")

		// Update the subscription resource to point to the beta CSV
		err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(metav1.NewDeleteOptions(0), metav1.ListOptions{})
		require.NoError(t, err)
		// Delete orphaned csv
		require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(mainStableCSV.GetName(), &metav1.DeleteOptions{}))

		// existing cleanup should remove this
		createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, betaChannel, "", v1alpha1.ApprovalAutomatic)

		subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)

		installPlanName = subscription.Status.Install.Name

		// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
		fetchedInstallPlan, err = fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Fetch installplan again to check for unnecessary control loops
		fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
			compareResources(t, fetchedInstallPlan, fip)
			return true
		})
		require.NoError(t, err)

		// Ensure correct in-cluster resource(s)
		fetchedCSV, err := fetchCSV(t, crc, mainBetaCSV.GetName(), testNamespace, csvSucceededChecker)
		require.NoError(t, err)

		t.Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
	})
}

func TestInstallPlanWithCRDSchemaChange(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	var min float64 = 2
	var max float64 = 256
	var newMax float64 = 50
	// generated outside of the test table so that the same naming can be used for both old and new CSVs
	mainCRDPlural := "testcrd"

	// excluded: new CRD, same version, same schema - won't trigger a CRD update
	tests := []struct {
		name          string
		expectedPhase v1alpha1.InstallPlanPhase
		oldCRD        *apiextensions.CustomResourceDefinition
		newCRD        *apiextensions.CustomResourceDefinition
	}{
		{
			name:          "all existing versions are present, different (backwards compatible) schema",
			expectedPhase: v1alpha1.InstallPlanPhaseComplete,
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
		},
		{
			name:          "all existing versions are present, different (backwards incompatible) schema",
			expectedPhase: v1alpha1.InstallPlanPhaseFailed,
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
		},
		{
			name:          "missing existing versions in new CRD",
			expectedPhase: v1alpha1.InstallPlanPhaseFailed,
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
			}(),
		},
		{
			name:          "existing version is present in new CRD (deprecated field)",
			expectedPhase: v1alpha1.InstallPlanPhaseComplete,
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
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer cleaner.NotifyTestComplete(t, true)

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

			c := newKubeClient(t)
			crc := newCRClient(t)

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
			_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{*tt.oldCRD}, []v1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV})
			defer cleanupCatalogSource()

			// Attempt to get the catalog source before creating install plan(s)
			_, err := fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(t, err)

			subscriptionName := genName("sub-nginx-alpha-")
			// this subscription will be cleaned up below without the clean up function
			createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

			subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(t, err)
			require.NotNil(t, subscription)

			installPlanName := subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or failed before checking resource presence
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, completeOrFailedFunc)
			require.NoError(t, err)
			t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Ensure that the desired resources have been created
			expectedSteps := map[registry.ResourceKey]struct{}{
				registry.ResourceKey{Name: tt.oldCRD.Name, Kind: "CustomResourceDefinition"}:            {},
				registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}: {},
			}

			require.Equal(t, len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

			for _, step := range fetchedInstallPlan.Status.Plan {
				key := registry.ResourceKey{
					Name: step.Resource.Name,
					Kind: step.Resource.Kind,
				}
				_, ok := expectedSteps[key]
				require.True(t, ok, "couldn't find %v in expected steps: %#v", key, expectedSteps)

				// Remove the entry from the expected steps set (to ensure no duplicates in resolved plan)
				delete(expectedSteps, key)
			}

			// Should have removed every matching step
			require.Equal(t, 0, len(expectedSteps), "Actual resource steps do not match expected")

			// Create initial CR
			cleanupCR, err := createCR(c, existingCR, "cluster.com", "v1alpha1", testNamespace, tt.oldCRD.Spec.Names.Plural, "my-cr-1")
			require.NoError(t, err)
			defer cleanupCR()

			updateInternalCatalog(t, c, crc, mainCatalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{*tt.newCRD}, []v1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV}, mainManifests)
			// Attempt to get the catalog source before creating install plan(s)
			_, err = fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(t, err)
			// Update the subscription resource to point to the beta CSV
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(t, err)

			// existing cleanup should remove this
			subscriptionName = genName("sub-nginx-beta")
			createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, betaChannel, "", v1alpha1.ApprovalAutomatic)

			subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(t, err)
			require.NotNil(t, subscription)

			installPlanName = subscription.Status.InstallPlanRef.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
			require.NoError(t, err)
			t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(t, tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err := fetchCSV(t, crc, mainBetaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(t, err)

			t.Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		})
	}
}

func TestInstallPlanWithDeprecatedVersionCRD(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	// generated outside of the test table so that the same naming can be used for both old and new CSVs
	mainCRDPlural := genName("ins")

	// excluded: new CRD, same version, same schema - won't trigger a CRD update
	tests := []struct {
		name            string
		expectedPhase   v1alpha1.InstallPlanPhase
		oldCRD          *apiextensions.CustomResourceDefinition
		intermediateCRD *apiextensions.CustomResourceDefinition
		newCRD          *apiextensions.CustomResourceDefinition
	}{
		{
			name:          "upgrade CRD with deprecated version",
			expectedPhase: v1alpha1.InstallPlanPhaseComplete,
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer cleaner.NotifyTestComplete(t, true)

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

			c := newKubeClient(t)
			crc := newCRClient(t)

			// Create the catalog source
			mainCatalogSourceName := genName("mock-ocs-main-")
			_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogSourceName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{*tt.oldCRD}, []v1alpha1.ClusterServiceVersion{mainStableCSV})
			defer cleanupCatalogSource()

			// Attempt to get the catalog source before creating install plan(s)
			_, err := fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(t, err)

			subscriptionName := genName("sub-nginx-")
			// this subscription will be cleaned up below without the clean up function
			createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

			subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(t, err)
			require.NotNil(t, subscription)

			installPlanName := subscription.Status.Install.Name

			// Wait for InstallPlan to be status: Complete or failed before checking resource presence
			completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed)
			fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, completeOrFailedFunc)
			require.NoError(t, err)
			t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
			require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

			// Ensure CRD versions are accurate
			expectedVersions := map[string]struct{}{
				"v1alpha1": {},
			}

			validateCRDVersions(t, c, tt.oldCRD.GetName(), expectedVersions)

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

			updateInternalCatalog(t, c, crc, mainCatalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{*tt.intermediateCRD}, []v1alpha1.ClusterServiceVersion{mainStableCSV, mainBetaCSV}, mainManifests)
			// Attempt to get the catalog source before creating install plan(s)
			_, err = fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(t, err)
			// Update the subscription resource to point to the beta CSV
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(t, err)
			// Delete orphaned csv
			require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(mainStableCSV.GetName(), &metav1.DeleteOptions{}))

			// existing cleanup should remove this
			createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, betaChannel, "", v1alpha1.ApprovalAutomatic)

			subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(t, err)
			require.NotNil(t, subscription)

			installPlanName = subscription.Status.Install.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
			require.NoError(t, err)
			t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(t, tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err := fetchCSV(t, crc, mainBetaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(t, err)

			// Ensure CRD versions are accurate
			expectedVersions = map[string]struct{}{
				"v1alpha1": {},
				"v1alpha2": {},
			}

			validateCRDVersions(t, c, tt.oldCRD.GetName(), expectedVersions)

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

			updateInternalCatalog(t, c, crc, mainCatalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{*tt.newCRD}, []v1alpha1.ClusterServiceVersion{mainBetaCSV, mainDeltaCSV}, mainManifests)
			// Attempt to get the catalog source before creating install plan(s)
			_, err = fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
			require.NoError(t, err)
			// Update the subscription resource to point to the beta CSV
			err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(metav1.NewDeleteOptions(0), metav1.ListOptions{})
			require.NoError(t, err)
			// Delete orphaned csv
			require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(mainBetaCSV.GetName(), &metav1.DeleteOptions{}))

			// existing cleanup should remove this
			createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, deltaChannel, "", v1alpha1.ApprovalAutomatic)

			subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
			require.NoError(t, err)
			require.NotNil(t, subscription)

			installPlanName = subscription.Status.Install.Name

			// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
			fetchedInstallPlan, err = fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
			require.NoError(t, err)
			t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

			require.Equal(t, tt.expectedPhase, fetchedInstallPlan.Status.Phase)

			// Ensure correct in-cluster resource(s)
			fetchedCSV, err = fetchCSV(t, crc, mainDeltaCSV.GetName(), testNamespace, csvSucceededChecker)
			require.NoError(t, err)

			// Ensure CRD versions are accurate
			expectedVersions = map[string]struct{}{
				"v1alpha2": {},
				"v1beta1":  {},
			}

			validateCRDVersions(t, c, tt.oldCRD.GetName(), expectedVersions)

			t.Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
		})
	}
}

func TestUpdateCatalogForSubscription(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	// crdVersionKey uniquely identifies a version within a CRD.
	type crdVersionKey struct {
		name    string
		served  bool
		storage bool
	}

	t.Run("AmplifyPermissions", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
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
		permissions := []v1alpha1.StrategyDeploymentPermissions{
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
		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
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
		_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		subscriptionName := genName("sub-nginx-update-perms1")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)
		require.NotNil(t, subscription.Status.InstallPlanRef)
		require.Equal(t, mainCSV.GetName(), subscription.Status.CurrentCSV)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Verify CSV is created
		_, err = awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		// Update CatalogSource with a new CSV with more permissions
		updatedPermissions := []v1alpha1.StrategyDeploymentPermissions{
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
		updatedClusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
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
		updateInternalCatalog(t, c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

		_, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
		require.NoError(t, err)

		updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedUpdatedInstallPlan, err := fetchInstallPlan(t, crc, updatedInstallPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)
		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

		// Wait for csv to update
		_, err = awaitCSV(t, crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		// If the CSV is succeeded, we successfully rolled out the RBAC changes
	})

	t.Run("AttenuatePermissions", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
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
		permissions := []v1alpha1.StrategyDeploymentPermissions{
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
		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
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
		_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		subscriptionName := genName("sub-nginx-update-perms1")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)
		require.NotNil(t, subscription.Status.InstallPlanRef)
		require.Equal(t, mainCSV.GetName(), subscription.Status.CurrentCSV)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Verify CSV is created
		_, err = awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		// Update CatalogSource with a new CSV with more permissions
		updatedPermissions := []v1alpha1.StrategyDeploymentPermissions{
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
		updatedClusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
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

		oldSecrets, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).List(metav1.ListOptions{})
		require.NoError(t, err, "error listing secrets")

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
		updateInternalCatalog(t, c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

		// Wait for subscription to update its status
		_, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
		require.NoError(t, err)

		updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedUpdatedInstallPlan, err := fetchInstallPlan(t, crc, updatedInstallPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)
		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

		// Wait for csv to update
		_, err = awaitCSV(t, crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		newSecrets, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).List(metav1.ListOptions{})
		require.NoError(t, err, "error listing secrets")
		// Assert that the number of secrets is not increased from updating service account as part of the install plan,
		assert.EqualValues(t, len(oldSecrets.Items), len(newSecrets.Items))
		// And that the secret list is indeed updated.
		assert.Equal(t, oldSecrets.Items, newSecrets.Items)

		// Wait for ServiceAccount to not have access anymore
		err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
			res, err := c.KubernetesInterface().AuthorizationV1().SubjectAccessReviews().Create(&authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: "system:serviceaccount:" + testNamespace + ":" + serviceAccountName,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Group:    "cluster.com",
						Version:  "v1alpha1",
						Resource: crdPlural,
						Verb:     rbac.VerbAll,
					},
				},
			})
			if err != nil {
				return false, err
			}
			if res == nil {
				return false, nil
			}
			t.Log("checking serviceaccount for permission")

			// should not be allowed
			return !res.Status.Allowed, nil
		})

	})

	t.Run("StopOnCSVModifications", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
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
		permissions := []v1alpha1.StrategyDeploymentPermissions{
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
		clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
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
		_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		subscriptionName := genName("sub-nginx-stompy-")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)
		require.NotNil(t, subscription.Status.InstallPlanRef)
		require.Equal(t, mainCSV.GetName(), subscription.Status.CurrentCSV)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Verify CSV is created
		csv, err := awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		modifiedEnv := []corev1.EnvVar{{Name: "EXAMPLE", Value: "value"}}
		modifiedDetails := v1alpha1.StrategyDetailsDeployment{
			DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
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
		csv.Spec.InstallStrategy = v1alpha1.NamedInstallStrategy{
			StrategyName: v1alpha1.InstallStrategyNameDeployment,
			StrategySpec: modifiedDetails,
		}
		_, err = crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Update(csv)
		require.NoError(t, err)

		// Wait for csv to update
		_, err = awaitCSV(t, crc, testNamespace, csv.GetName(), csvSucceededChecker)
		require.NoError(t, err)

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
		require.NoError(t, err)

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
		updateInternalCatalog(t, c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, updatedCSV}, updatedManifests)

		_, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanDifferentChecker(fetchedInstallPlan.GetName()))
		require.NoError(t, err)

		updatedInstallPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedUpdatedInstallPlan, err := fetchInstallPlan(t, crc, updatedInstallPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)
		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedUpdatedInstallPlan.Status.Phase)

		// Wait for csv to update
		_, err = awaitCSV(t, crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		// Should have created deployment and stomped on the env changes
		updatedDep, err := c.GetDeployment(testNamespace, deploymentName)
		require.NoError(t, err)
		require.NotNil(t, updatedDep)

		// Should have the updated env var
		var emptyEnv []corev1.EnvVar = nil
		require.Equal(t, emptyEnv, updatedDep.Spec.Template.Spec.Containers[0].Env)
	})

	t.Run("UpdateSingleExistingCRDOwner", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

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

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
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
		_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		subscriptionName := genName("sub-nginx-update-before-")
		createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)
		require.NotNil(t, subscription.Status.InstallPlanRef)
		require.Equal(t, mainCSV.GetName(), subscription.Status.CurrentCSV)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Fetch installplan again to check for unnecessary control loops
		fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
			compareResources(t, fetchedInstallPlan, fip)
			return true
		})
		require.NoError(t, err)

		// Verify CSV is created
		_, err = awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvAnyChecker)
		require.NoError(t, err)

		updateInternalCatalog(t, c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{updatedCRD}, []v1alpha1.ClusterServiceVersion{mainCSV}, mainManifests)

		// Update the subscription resource
		err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(metav1.NewDeleteOptions(0), metav1.ListOptions{})
		require.NoError(t, err)

		// existing cleanup should remove this
		subscriptionName = genName("sub-nginx-update-after-")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		// Wait for subscription to update
		updatedSubscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)

		// Verify installplan created and installed
		fetchedUpdatedInstallPlan, err := fetchInstallPlan(t, crc, updatedSubscription.Status.InstallPlanRef.Name, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)
		require.NotEqual(t, fetchedInstallPlan.GetName(), fetchedUpdatedInstallPlan.GetName())

		// Wait for csv to update
		_, err = awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvAnyChecker)
		require.NoError(t, err)

		// Get the CRD to see if it is updated
		fetchedCRD, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(crdName, metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, len(fetchedCRD.Spec.Versions), len(updatedCRD.Spec.Versions), "The CRD versions counts don't match")

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
			require.True(t, ok, "couldn't find %v in fetched CRD versions: %#v", key, fetchedCRDVersions)
		}
	})

	t.Run("UpdatePreexistingCRDFailed", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		c := newKubeClient(t)
		crc := newCRClient(t)
		defer func() {
			require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
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
		require.NoError(t, err)
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
		_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{updatedCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		// Attempt to get the catalog source before creating install plan
		_, err = fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		require.NoError(t, err)
		require.NotNil(t, subscription)
		require.NotNil(t, subscription.Status.InstallPlanRef)
		require.Equal(t, mainCSV.GetName(), subscription.Status.CurrentCSV)

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
		require.NoError(t, err)

		require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

		// Fetch installplan again to check for unnecessary control loops
		fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
			compareResources(t, fetchedInstallPlan, fip)
			return true
		})
		require.NoError(t, err)

		// Verify CSV is created
		_, err = awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvAnyChecker)
		require.NoError(t, err)

		// Get the CRD to see if it is updated
		fetchedCRD, err := c.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(crdName, metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, len(fetchedCRD.Spec.Versions), len(mainCRD.Spec.Versions), "The CRD versions counts don't match")

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
			require.True(t, ok, "couldn't find %v in fetched CRD versions: %#v", key, fetchedCRDVersions)
		}
	})
}

// TestCreateInstallPlanWithPermissions creates an InstallPlan with a CSV containing a set of permissions to be resolved.
func TestCreateInstallPlanWithPermissions(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

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
	permissions := []v1alpha1.StrategyDeploymentPermissions{
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
	clusterPermissions := []v1alpha1.StrategyDeploymentPermissions{
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

	c := newKubeClient(t)
	crc := newCRClient(t)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
	}()

	// Create CatalogSource
	mainCatalogSourceName := genName("nginx-catalog")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{stableCSV})
	defer cleanupCatalogSource()

	// Attempt to get CatalogSource
	_, err := fetchCatalogSource(t, crc, mainCatalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("sub-nginx-")
	subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, packageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.Install.Name

	// Attempt to get InstallPlan
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed, v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	require.NotEqual(t, v1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase, "InstallPlan failed")

	// Expect correct RBAC resources to be resolved and created
	expectedSteps := map[registry.ResourceKey]struct{}{
		registry.ResourceKey{Name: crd.Name, Kind: "CustomResourceDefinition"}:   {},
		registry.ResourceKey{Name: stableCSVName, Kind: "ClusterServiceVersion"}: {},
		registry.ResourceKey{Name: serviceAccountName, Kind: "ServiceAccount"}:   {},
		registry.ResourceKey{Name: stableCSVName, Kind: "Role"}:                  {},
		registry.ResourceKey{Name: stableCSVName, Kind: "RoleBinding"}:           {},
		registry.ResourceKey{Name: stableCSVName, Kind: "ClusterRole"}:           {},
		registry.ResourceKey{Name: stableCSVName, Kind: "ClusterRoleBinding"}:    {},
	}

	require.Equal(t, len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

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
				t.Logf("%v, %v: %v && %v", key, expected, strings.HasPrefix(key.Name, expected.Name), key.Kind == expected.Kind)
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
	require.Equal(t, 0, len(expectedSteps), "Actual resource steps do not match expected: %#v", expectedSteps)

	// the test from here out verifies created RBAC is removed after CSV deletion
	createdClusterRoles, err := c.KubernetesInterface().RbacV1().ClusterRoles().List(metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
	createdClusterRoleNames := map[string]struct{}{}
	for _, role := range createdClusterRoles.Items {
		createdClusterRoleNames[role.GetName()] = struct{}{}
		t.Logf("Monitoring cluster role %v", role.GetName())
	}

	createdClusterRoleBindings, err := c.KubernetesInterface().RbacV1().ClusterRoleBindings().List(metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
	createdClusterRoleBindingNames := map[string]struct{}{}
	for _, binding := range createdClusterRoleBindings.Items {
		createdClusterRoleBindingNames[binding.GetName()] = struct{}{}
		t.Logf("Monitoring cluster role binding %v", binding.GetName())
	}

	// can't query by owner reference, so just use the name we know is in the install plan
	createdServiceAccountNames := map[string]struct{}{serviceAccountName: struct{}{}}
	t.Logf("Monitoring service account %v", serviceAccountName)

	crWatcher, err := c.KubernetesInterface().RbacV1().ClusterRoles().Watch(metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
	require.NoError(t, err)
	crbWatcher, err := c.KubernetesInterface().RbacV1().ClusterRoleBindings().Watch(metav1.ListOptions{LabelSelector: fmt.Sprintf("%v=%v", ownerutil.OwnerKey, stableCSVName)})
	require.NoError(t, err)
	saWatcher, err := c.KubernetesInterface().CoreV1().ServiceAccounts(testNamespace).Watch(metav1.ListOptions{})
	require.NoError(t, err)

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

	t.Logf("Deleting CSV '%v' in namespace %v", stableCSVName, testNamespace)
	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
	select {
	case <-done:
		break
	case err := <-errExit:
		t.Fatal(err)
	}

	require.Emptyf(t, createdClusterRoleNames, "unexpected cluster role remain: %v", createdClusterRoleNames)
	require.Emptyf(t, createdClusterRoleBindingNames, "unexpected cluster role binding remain: %v", createdClusterRoleBindingNames)
	require.Emptyf(t, createdServiceAccountNames, "unexpected service account remain: %v", createdServiceAccountNames)
}

func TestInstallPlanCRDValidation(t *testing.T) {
	// Tests if CRD validation works with the "minimum" property after being
	// pulled from a CatalogSource's operator-registry.
	defer cleaner.NotifyTestComplete(t, true)

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
	c := newKubeClient(t)
	crc := newCRClient(t)
	catalogSourceName := genName("mock-nginx-")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	defer cleanupCatalogSource()

	// Attempt to get the catalog source before creating install plan
	_, err := fetchCatalogSource(t, crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("sub-nginx-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.InstallPlanRef.Name

	// Wait for InstallPlan to be status: Complete before checking resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
	require.NoError(t, err)
	t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)
}
