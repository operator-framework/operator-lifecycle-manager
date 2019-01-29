package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubernetes/pkg/apis/rbac"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

type checkInstallPlanFunc func(fip *v1alpha1.InstallPlan) bool

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
	var fetchedInstallPlan *v1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(testNamespace).Get(name, metav1.GetOptions{})
		if err != nil || fetchedInstallPlan == nil {
			return false, err
		}

		return checkPhase(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

func newNginxInstallStrategy(name string, permissions []install.StrategyDeploymentPermissions, clusterPermissions []install.StrategyDeploymentPermissions) v1alpha1.NamedInstallStrategy {
	// Create an nginx details deployment
	details := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: name,
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "nginx"},
					},
					Replicas: &doubleInstance,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "nginx"},
						},
						Spec: corev1.PodSpec{Containers: []corev1.Container{
							{
								Name:  genName("nginx"),
								Image: "bitnami/nginx:latest",
								Ports: []corev1.ContainerPort{{ContainerPort: 80}},
							},
						}},
					},
				},
			},
		},
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
	}
	detailsRaw, _ := json.Marshal(details)
	namedStrategy := v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: detailsRaw,
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
				ListKind: "list" + plural,
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
			Replaces: replaces,
			Version:  version,
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
				Owned:    []v1alpha1.CRDDescription{},
				Required: []v1alpha1.CRDDescription{},
			},
		},
	}

	// Populate owned and required
	for _, crd := range owned {
		desc := v1alpha1.CRDDescription{
			Name:        crd.GetName(),
			Version:     "v1alpha1",
			Kind:        crd.Spec.Names.Plural,
			DisplayName: crd.GetName(),
			Description: crd.GetName(),
		}
		csv.Spec.CustomResourceDefinitions.Owned = append(csv.Spec.CustomResourceDefinitions.Owned, desc)
	}

	for _, crd := range required {
		desc := v1alpha1.CRDDescription{
			Name:        crd.GetName(),
			Version:     "v1alpha1",
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

	mainPackageName := genName("nginx-")
	dependentPackageName := genName("nginxdep-")

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

	stableChannel := "stable"

	mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

	crdPlural := genName("ins-")

	dependentCRD := newCRD(crdPlural)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

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
	subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.Install.Name

	// Wait for InstallPlan to be status: Complete before checking resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Fetch installplan again to check for unnecessary control loops
	fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
		compareResources(t, fetchedInstallPlan, fip)
		return true
	})
	require.NoError(t, err)

	require.Equal(t, len(expectedStepSources), len(fetchedInstallPlan.Status.Plan), "Number of resolved steps matches the number of expected steps")

	// Ensure resolved step resources originate from the correct catalog sources
	t.Logf("%#v", expectedStepSources)
	for _, step := range fetchedInstallPlan.Status.Plan {
		t.Logf("checking %s", step.Resource)
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

	t.Logf("All expected resources resolved")
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
		mainStableCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, *semver.New("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, *semver.New("0.2.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

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
			registry.ResourceKey{Name: mainCRD.Name, Kind: "CustomResourceDefinition"}:                                                                             {},
			registry.ResourceKey{Name: dependentCRD.Name, Kind: "CustomResourceDefinition"}:                                                                        {},
			registry.ResourceKey{Name: dependentPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                                                           {},
			registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                                                                {},
			registry.ResourceKey{Name: strings.Join([]string{dependentPackageStable, mainCatalogSourceName, testNamespace}, "-"), Kind: v1alpha1.SubscriptionKind}: {},
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
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, v1alpha1.ApprovalAutomatic)
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

		require.Equal(t, len(fetchedInstallPlan.Status.Plan), len(expectedSteps))
		t.Logf("Number of resolved steps matches the number of expected steps")

		require.Equal(t, len(expectedSteps), len(fetchedInstallPlan.Status.Plan), "number of expected steps does not match installed")

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
		mainStableCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, *semver.New("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, *semver.New("0.2.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

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
		subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, stableChannel, v1alpha1.ApprovalAutomatic)
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

		// existing cleanup should remove this
		createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, mainPackageName, betaChannel, v1alpha1.ApprovalAutomatic)

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
	permissions := []install.StrategyDeploymentPermissions{
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
	clusterPermissions := []install.StrategyDeploymentPermissions{
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
	stableCSV := newCSV(stableCSVName, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

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
	subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogSourceName, packageName, stableChannel, v1alpha1.ApprovalAutomatic)
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
	}

	// Should have removed every matching step
	require.Equal(t, 0, len(expectedSteps), "Actual resource steps do not match expected: %#v", expectedSteps)

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
	csv := newCSV(packageNameStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

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
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.Install.Name

	// Wait for InstallPlan to be status: Complete before checking resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
	require.NoError(t, err)
	t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

}
