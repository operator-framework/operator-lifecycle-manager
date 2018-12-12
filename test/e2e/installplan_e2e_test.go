package e2e

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
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

func decorateCommonAndCreateInstallPlan(crc versioned.Interface, namespace string, plan v1alpha1.InstallPlan) (cleanupFunc, error) {
	plan.Kind = v1alpha1.InstallPlanKind
	plan.APIVersion = v1alpha1.SchemeGroupVersion.String()

	_, err := crc.OperatorsV1alpha1().InstallPlans(namespace).Create(&plan)
	if err != nil {
		return nil, err
	}
	return buildInstallPlanCleanupFunc(crc, namespace, &plan), nil
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

func newCRD(name, plural string) extv1beta1.CustomResourceDefinition {
	crd := extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: "CustomResourceDefinition",
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: extv1beta1.CustomResourceDefinitionNames{
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

func newCSV(name, namespace, replaces string, version semver.Version, owned []extv1beta1.CustomResourceDefinition, required []extv1beta1.CustomResourceDefinition, namedStrategy v1alpha1.NamedInstallStrategy) v1alpha1.ClusterServiceVersion {
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
			Replaces:        replaces,
			Version:         version,
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

func TestCreateInstallPlanManualApproval(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

	inMem, err := registry.NewInMemoryFromConfigMap(c, operatorNamespace, ocsConfigMap)
	require.NoError(t, err)
	require.NotNil(t, inMem)
	latestEtcdCSV, err := inMem.FindCSVForPackageNameUnderChannel("etcd", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latestEtcdCSV)

	etcdInstallPlan := v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name: "install-manual-" + latestEtcdCSV.GetName(),
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{latestEtcdCSV.GetName()},
			Approval:                   v1alpha1.ApprovalManual,
			Approved:                   false,
		},
	}

	// Attempt to get the catalog source before creating install plan
	_, err = fetchCatalogSource(t, crc, ocsConfigMap, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create a new InstallPlan for Vault with manual approval
	cleanup, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, etcdInstallPlan)
	require.NoError(t, err)
	defer cleanup()

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, etcdInstallPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseRequiresApproval))
	require.NoError(t, err)
	require.NotNil(t, fetchedInstallPlan)

	var verifyResources = func(installPlan *v1alpha1.InstallPlan, shouldBeCreated bool) int {
		resourcesPresent := 0
		// Step through the InstallPlan and check if resources have been created or not
		for _, step := range installPlan.Status.Plan {
			t.Logf("Verifiying that %s %s is not present", step.Resource.Kind, step.Resource.Name)
			if step.Resource.Kind == "CustomResourceDefinition" {
				// _, err := c.GetCustomResourceDefinition(step.Resource.Name)

				// FIXME: CI cluster will already have the CRDs so this will always fail
				if shouldBeCreated {
					// require.NoError(t, err)
					// resourcesPresent = resourcesPresent + 1
				} else {
					// require.Error(t, err)
				}
			} else if step.Resource.Kind == "ClusterServiceVersion" {
				_, err := c.GetCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, testNamespace, step.Resource.Kind, step.Resource.Name)

				if shouldBeCreated {
					require.NoError(t, err)
					resourcesPresent = resourcesPresent + 1
				} else {
					require.Error(t, err)
				}
			} else if step.Resource.Kind == "Secret" {
				_, err := c.KubernetesInterface().CoreV1().Secrets(testNamespace).Get(step.Resource.Name, metav1.GetOptions{})

				if shouldBeCreated {
					require.NoError(t, err)
					resourcesPresent = resourcesPresent + 1
				} else {
					require.Error(t, err)
				}
			}
		}
		return resourcesPresent
	}

	etcdResourcesPresent := verifyResources(fetchedInstallPlan, false)
	// Result: Ensure that the InstallPlan does not actually create Etcd resources
	t.Logf("%d Etcd Resources present", etcdResourcesPresent)
	require.Zero(t, etcdResourcesPresent)

	// Approve InstallPlan and update
	fetchedInstallPlan.Spec.Approved = true
	_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(fetchedInstallPlan)
	require.NoError(t, err)

	approvedInstallPlan, err := fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)

	etcdResourcesPresent = verifyResources(approvedInstallPlan, true)
	// Result: Ensure that the InstallPlan actually creates Etcd resources
	t.Logf("%d Etcd Resources present", etcdResourcesPresent)
	require.NotZero(t, etcdResourcesPresent)

	// Fetch installplan again to check for unnecessary control loops
	_, err = fetchInstallPlan(t, crc, approvedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
		compareResources(t, approvedInstallPlan, fip)
		return true
	})
	require.NoError(t, err)

}

// As an infra owner, creating an installplan with a clusterServiceVersionName that does not exist in the catalog should result in a “Failed” status
func TestCreateInstallPlanFromInvalidClusterServiceVersionName(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	crc := newCRClient(t)

	installPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-dogecoin-miner",
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{"Dogecoin-miner-0.1"},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}

	// Attempt to get the catalog source before creating install plan
	_, err := fetchCatalogSource(t, crc, ocsConfigMap, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	cleanup, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
	require.NoError(t, err)
	defer cleanup()

	// Wait for InstallPlan to be status: Failed before checking for resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed))
	require.NoError(t, err)

	require.Equal(t, v1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase)

	// Fetch installplan again to check for unnecessary control loops
	_, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
		compareResources(t, fetchedInstallPlan, fip)
		return true
	})
	require.NoError(t, err)
}

func TestCreateInstallPlanWithCSVsAcrossMultipleCatalogSources(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	mainPackageName := genName("nginx")
	dependentPackageName := genName("nginxdep")

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

	stableChannel := "stable"

	mainNamedStrategy := newNginxInstallStrategy("dep-", nil, nil)
	dependentNamedStrategy := newNginxInstallStrategy("dep-", nil, nil)

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

	dependentCRD := newCRD(crdName, crdPlural)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), nil, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	dependentCatalogName := genName("mock-ocs-dependent")
	mainCatalogName := genName("mock-ocs-main")

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
	_, cleanupDependentCatalogSource, err := createInternalCatalogSource(t, c, crc, dependentCatalogName, operatorNamespace, dependentManifests, []extv1beta1.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{dependentCSV})
	require.NoError(t, err)
	defer cleanupDependentCatalogSource()
	// Attempt to get the catalog source before creating install plan
	_, err = fetchCatalogSource(t, crc, dependentCatalogName, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	_, cleanupMainCatalogSource, err := createInternalCatalogSource(t, c, crc, mainCatalogName, operatorNamespace, mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})
	require.NoError(t, err)
	defer cleanupMainCatalogSource()
	// Attempt to get the catalog source before creating install plan
	_, err = fetchCatalogSource(t, crc, mainCatalogName, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create expected install plan step sources
	expectedStepSources := map[registry.ResourceKey]registry.ResourceKey{
		registry.ResourceKey{Name: crdName, Kind: "CustomResourceDefinition"}:                           {Name: dependentCatalogName, Namespace: operatorNamespace},
		registry.ResourceKey{Name: fmt.Sprintf("edit-%s-%s", crdName, "v1alpha1"), Kind: "ClusterRole"}: {Name: dependentCatalogName, Namespace: operatorNamespace},
		registry.ResourceKey{Name: fmt.Sprintf("view-%s-%s", crdName, "v1alpha1"), Kind: "ClusterRole"}: {Name: dependentCatalogName, Namespace: operatorNamespace},
		registry.ResourceKey{Name: dependentPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:    {Name: dependentCatalogName, Namespace: operatorNamespace},
		registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:         {Name: mainCatalogName, Namespace: operatorNamespace},
	}

	// Fetch list of catalog sources
	installPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      genName("install-nginx"),
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{mainPackageStable},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}

	cleanup, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
	require.NoError(t, err)
	t.Logf("Install plan %s created", installPlan.GetName())
	defer cleanup()

	// Wait for InstallPlan to be status: Complete before checking resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Fetch installplan again to check for unnecessary control loops
	fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
		compareResources(t, fetchedInstallPlan, fip)
		return true
	})
	require.NoError(t, err)

	require.Equal(t, len(expectedStepSources), len(fetchedInstallPlan.Status.Plan))
	t.Logf("Number of resolved steps matches the number of expected steps")

	// Ensure resolved step resources originate from the correct catalog sources
	for _, step := range fetchedInstallPlan.Status.Plan {
		t.Logf("checking %s", step.Resource.Name)
		key := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
		expectedSource, ok := expectedStepSources[key]
		require.True(t, ok, "didn't find %v", key)
		require.Equal(t, expectedSource.Name, step.Resource.CatalogSource)
		require.Equal(t, expectedSource.Namespace, step.Resource.CatalogSourceNamespace)

		// delete
	}
	t.Logf("All expected resources resolved")
}

func TestCreateInstallPlanWithPreExistingCRDOwners(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)
	t.Run("OnePreExistingCRDOwner", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		mainPackageName := genName("nginx")
		dependentPackageName := genName("nginxdep")

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
		mainCRDPlural := genName("ins")
		mainCRDName := mainCRDPlural + ".cluster.com"
		mainCRD := newCRD(mainCRDName, mainCRDPlural)

		// Create a new named install strategy
		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		dependentCRDPlural := genName("ins")
		dependentCRDName := dependentCRDPlural + ".cluster.com"
		dependentCRD := newCRD(dependentCRDName, dependentCRDPlural)

		// Create new CSVs
		mainStableCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

		c := newKubeClient(t)
		crc := newCRClient(t)

		// Create default test installplan
		installPlan := v1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.InstallPlanKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("install-nginx"),
				Namespace: testNamespace,
			},
			Spec: v1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{mainPackageStable},
				Approval:                   v1alpha1.ApprovalAutomatic,
			},
		}

		// Create the catalog source
		catalogSourceName := genName("mock-ocs-main")
		_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, mainManifests, []extv1beta1.CustomResourceDefinition{dependentCRD, mainCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
		require.NoError(t, err)
		defer cleanupCatalogSource()
		// Attempt to get the catalog source before creating install plan(s)
		_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		expectedSteps := map[registry.ResourceKey]struct{}{
			registry.ResourceKey{Name: mainCRDName, Kind: "CustomResourceDefinition"}:                                {},
			registry.ResourceKey{Name: fmt.Sprintf("edit-%s-%s", mainCRDName, "v1alpha1"), Kind: "ClusterRole"}:      {},
			registry.ResourceKey{Name: fmt.Sprintf("view-%s-%s", mainCRDName, "v1alpha1"), Kind: "ClusterRole"}:      {},
			registry.ResourceKey{Name: dependentCRDName, Kind: "CustomResourceDefinition"}:                           {},
			registry.ResourceKey{Name: fmt.Sprintf("edit-%s-%s", dependentCRDName, "v1alpha1"), Kind: "ClusterRole"}: {},
			registry.ResourceKey{Name: fmt.Sprintf("view-%s-%s", dependentCRDName, "v1alpha1"), Kind: "ClusterRole"}: {},
			registry.ResourceKey{Name: dependentPackageBeta, Kind: v1alpha1.ClusterServiceVersionKind}:               {},
			registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                  {},
		}

		// Create the preexisting CRD and CSV
		cleanupCRD, err := createCRD(c, dependentCRD)
		require.NoError(t, err)
		defer cleanupCRD()
		cleanupCSV, err := createCSV(t, c, crc, dependentBetaCSV, testNamespace, true, false)
		require.NoError(t, err)
		defer cleanupCSV()
		t.Log("Dependent CRD and preexisting CSV created")

		cleanupInstallPlan, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
		require.NoError(t, err)
		t.Logf("Install plan %s created", installPlan.GetName())
		defer cleanupInstallPlan()

		// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
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

		// Ensure resolved step resources originate from the correct catalog sources
		for _, step := range fetchedInstallPlan.Status.Plan {
			key := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
			_, ok := expectedSteps[key]
			require.True(t, ok)
		}
		t.Logf("All expected resources resolved")
	})

	t.Run("TwoPreExistingCRDOwners", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		mainPackageName := genName("nginx")
		dependentPackageName := genName("nginxdep")

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
		mainCRDPlural := genName("ins")
		mainCRDName := mainCRDPlural + ".cluster.com"
		mainCRD := newCRD(mainCRDName, mainCRDPlural)

		// Create a new named install strategy
		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		dependentCRDPlural := genName("ins")
		dependentCRDName := dependentCRDPlural + ".cluster.com"
		dependentCRD := newCRD(dependentCRDName, dependentCRDPlural)

		// Create new CSVs
		mainStableCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

		c := newKubeClient(t)
		crc := newCRClient(t)

		// Create default test installplan
		installPlan := v1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.InstallPlanKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("install-nginx"),
				Namespace: testNamespace,
			},
			Spec: v1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{mainPackageStable},
				Approval:                   v1alpha1.ApprovalAutomatic,
			},
		}

		// Create the catalog source
		catalogSourceName := genName("mock-ocs-main")
		_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, mainManifests, []extv1beta1.CustomResourceDefinition{dependentCRD, mainCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
		require.NoError(t, err)
		defer cleanupCatalogSource()
		// Attempt to get the catalog source before creating install plan(s)
		_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		secondOwnerCSV := v1alpha1.ClusterServiceVersion{
			TypeMeta: csvType,
			ObjectMeta: metav1.ObjectMeta{
				Name: "second-owner",
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces:        "",
				Version:         *semver.New("0.2.0"),
				InstallStrategy: installStrategy,
				CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
					Owned: []v1alpha1.CRDDescription{
						{
							Name:        dependentCRDName,
							Version:     "v1alpha1",
							Kind:        dependentCRDPlural,
							DisplayName: dependentCRDName,
							Description: dependentCRDName,
						},
					},
				},
			},
		}

		// Create the preexisting CRD and CSV
		cleanupCRD, err := createCRD(c, dependentCRD)
		require.NoError(t, err)
		defer cleanupCRD()
		cleanupBetaCSV, err := createCSV(t, c, crc, dependentBetaCSV, testNamespace, true, false)
		require.NoError(t, err)
		defer cleanupBetaCSV()
		cleanupSecondOwnerCSV, err := createCSV(t, c, crc, secondOwnerCSV, testNamespace, true, false)
		require.NoError(t, err)
		defer cleanupSecondOwnerCSV()
		t.Log("Dependent CRD and preexisting CSVs created")

		cleanupInstallPlan, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
		require.NoError(t, err)
		defer cleanupInstallPlan()
		t.Logf("Install plan %s created", installPlan.GetName())

		// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

		require.Equal(t, v1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase)

		// Fetch installplan again to check for unnecessary control loops
		_, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
			compareResources(t, fetchedInstallPlan, fip)
			return true
		})
		require.NoError(t, err)
	})

	t.Run("PreExistingCRDOwnerIsReplaced", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		mainPackageName := genName("nginx")
		dependentPackageName := genName("nginxdep")

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
		mainCRDPlural := genName("ins")
		mainCRDName := mainCRDPlural + ".cluster.com"
		mainCRD := newCRD(mainCRDName, mainCRDPlural)

		// Create a new named install strategy
		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		dependentCRDPlural := genName("ins")
		dependentCRDName := dependentCRDPlural + ".cluster.com"
		dependentCRD := newCRD(dependentCRDName, dependentCRDPlural)

		// Create new CSVs
		mainStableCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

		c := newKubeClient(t)
		crc := newCRClient(t)

		// Create the catalog source
		catalogSourceName := genName("mock-ocs-main")
		_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, mainManifests, []extv1beta1.CustomResourceDefinition{dependentCRD, mainCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
		require.NoError(t, err)
		defer cleanupCatalogSource()
		// Attempt to get the catalog source before creating install plan(s)
		_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		// Create default test installplan
		installPlan := v1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.InstallPlanKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("install-nginx"),
				Namespace: testNamespace,
			},
			Spec: v1alpha1.InstallPlanSpec{
				CatalogSource:              catalogSourceName,
				CatalogSourceNamespace:     testNamespace,
				ClusterServiceVersionNames: []string{mainPackageStable},
				Approval:                   v1alpha1.ApprovalAutomatic,
			},
		}

		// Create a stable installplan
		installPlanCleanup, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
		require.NoError(t, err)
		defer installPlanCleanup()

		// Wait for InstallPlan to be status: Complete or failed before checking resource presence
		completeOrFailedFunc := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed)
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), completeOrFailedFunc)
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		require.True(t, completeOrFailedFunc(fetchedInstallPlan))

		// Ensure that the desired resources have been created
		expectedSteps := map[registry.ResourceKey]struct{}{
			registry.ResourceKey{Name: mainCRDName, Kind: "CustomResourceDefinition"}:                                {},
			registry.ResourceKey{Name: fmt.Sprintf("edit-%s-%s", mainCRDName, "v1alpha1"), Kind: "ClusterRole"}:      {},
			registry.ResourceKey{Name: fmt.Sprintf("view-%s-%s", mainCRDName, "v1alpha1"), Kind: "ClusterRole"}:      {},
			registry.ResourceKey{Name: dependentCRDName, Kind: "CustomResourceDefinition"}:                           {},
			registry.ResourceKey{Name: fmt.Sprintf("edit-%s-%s", dependentCRDName, "v1alpha1"), Kind: "ClusterRole"}: {},
			registry.ResourceKey{Name: fmt.Sprintf("view-%s-%s", dependentCRDName, "v1alpha1"), Kind: "ClusterRole"}: {},
			registry.ResourceKey{Name: dependentPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:             {},
			registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:                  {},
		}

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

		// Update the step resource to point to the beta CSV
		installPlanBeta := fetchedInstallPlan
		installPlanBeta.Spec.ClusterServiceVersionNames = []string{mainBetaCSV.GetName()}
		updated, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(installPlanBeta)
		require.NoError(t, err)

		// Update the status subresource with a preresolved set of resources
		csvStepResource, err := resolver.NewStepResourceFromCSV(&mainBetaCSV)
		require.NoError(t, err)

		crdStepResources, err := resolver.NewStepResourcesFromCRD(&mainCRD)
		require.NoError(t, err)

		dependentCSVStepResource, err := resolver.NewStepResourceFromCSV(&dependentStableCSV)
		require.NoError(t, err)

		dependentCRDStepResources, err := resolver.NewStepResourcesFromCRD(&dependentCRD)
		require.NoError(t, err)

		updated.Status.Plan = []*v1alpha1.Step{{
			Resource: dependentCSVStepResource,
			Status:   v1alpha1.StepStatusPresent,
		},
			{
				Resource: csvStepResource,
				Status:   v1alpha1.StepStatusNotPresent,
			},
		}
		for _, step := range dependentCRDStepResources {
			updated.Status.Plan = append(updated.Status.Plan, &v1alpha1.Step{
				Resource: step,
				Status:   v1alpha1.StepStatusPresent,
			})
		}
		for _, step := range crdStepResources {
			updated.Status.Plan = append(updated.Status.Plan, &v1alpha1.Step{
				Resource: step,
				Status:   v1alpha1.StepStatusPresent,
			})
		}

		updated.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		updated, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).UpdateStatus(updated)
		require.NoError(t, err)

		// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
		fetchedInstallPlan, err = fetchInstallPlan(t, crc, updated.GetName(), completeOrFailedFunc)
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

		require.True(t, completeOrFailedFunc(fetchedInstallPlan))

		// Ensure correct in-cluster resource(s)
		fetchedCSV, err := fetchCSV(t, crc, mainBetaCSV.GetName(), csvSucceededChecker)
		require.NoError(t, err)

		t.Logf("All expected resources resolved %s", fetchedCSV.Status.Phase)
	})

	t.Run("PreExistingCRDOwnerFailsPlanExecution", func(t *testing.T) {
		defer cleaner.NotifyTestComplete(t, true)

		mainPackageName := genName("nginx")
		dependentPackageName := genName("nginxdep")

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
		mainCRDPlural := genName("ins")
		mainCRDName := mainCRDPlural + ".cluster.com"
		mainCRD := newCRD(mainCRDName, mainCRDPlural)

		// Create a new named install strategy
		mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
		dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

		dependentCRDPlural := genName("ins")
		dependentCRDName := dependentCRDPlural + ".cluster.com"
		dependentCRD := newCRD(dependentCRDName, dependentCRDPlural)

		// Create new CSVs
		mainStableCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		mainBetaCSV := newCSV(mainPackageBeta, testNamespace, mainPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{mainCRD}, []extv1beta1.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
		dependentStableCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)
		dependentBetaCSV := newCSV(dependentPackageBeta, testNamespace, dependentPackageStable, *semver.New("0.2.0"), []extv1beta1.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

		c := newKubeClient(t)
		crc := newCRClient(t)

		// Create the catalog source
		catalogSourceName := genName("mock-ocs-main")
		_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, mainManifests, []extv1beta1.CustomResourceDefinition{dependentCRD, mainCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainStableCSV, mainBetaCSV})
		require.NoError(t, err)
		defer cleanupCatalogSource()
		// Attempt to get the catalog source before creating install plan(s)
		_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
		require.NoError(t, err)

		// Create a dummy installplan with a non-existent csv
		dummyInstallPlan := v1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.InstallPlanKind,
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("install-nginx"),
				Namespace: testNamespace,
			},
			Spec: v1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{mainPackageStable},
				Approval:                   v1alpha1.ApprovalAutomatic,
			},
		}
		dummyInstallPlan.Spec.ClusterServiceVersionNames = []string{"non-existent"}
		cleanupDummyInstallPlan, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, dummyInstallPlan)
		require.NoError(t, err)
		defer cleanupDummyInstallPlan()

		// Add pre-existing dependencies
		dependentCRDCleanup, err := createCRD(c, dependentCRD)
		require.NoError(t, err)
		defer dependentCRDCleanup()

		dependentBetaCSVCleanup, err := createCSV(t, c, crc, dependentBetaCSV, testNamespace, true, false)
		require.NoError(t, err)
		defer dependentBetaCSVCleanup()

		// Fetch the dummy installplan
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, dummyInstallPlan.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, err)

		// Update spec to point at a valid CSV
		fetchedInstallPlan.Spec.ClusterServiceVersionNames = []string{mainStableCSV.GetName()}
		updated, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(fetchedInstallPlan)
		require.NoError(t, err)

		// Update the status subresource with a pre-resolved set of resources
		csvStepResource, err := resolver.NewStepResourceFromCSV(&mainStableCSV)
		require.NoError(t, err)

		crdStepResources, err := resolver.NewStepResourcesFromCRD(&mainCRD)
		require.NoError(t, err)

		dependentCSVStepResource, err := resolver.NewStepResourceFromCSV(&dependentStableCSV)
		require.NoError(t, err)

		dependentCRDStepResources, err := resolver.NewStepResourcesFromCRD(&dependentCRD)
		require.NoError(t, err)

		updated.Status.Plan = []*v1alpha1.Step{
			{
				Resource: dependentCSVStepResource,
				Status:   v1alpha1.StepStatusUnknown,
			},
			{
				Resource: csvStepResource,
				Status:   v1alpha1.StepStatusUnknown,
			},
		}

		for _, step := range dependentCRDStepResources {
			updated.Status.Plan = append(updated.Status.Plan, &v1alpha1.Step{
				Resource: step,
				Status:   v1alpha1.StepStatusUnknown,
			})
		}
		for _, step := range crdStepResources {
			updated.Status.Plan = append(updated.Status.Plan, &v1alpha1.Step{
				Resource: step,
				Status:   v1alpha1.StepStatusUnknown,
			})
		}

		updated, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).UpdateStatus(updated)
		require.NoError(t, err)

		// Wait for InstallPlan to be status: Failed
		updated, err = fetchInstallPlan(t, crc, updated.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed))
		require.NoError(t, err)
		t.Logf("Install plan %s fetched with status %s", updated.GetName(), updated.Status.Phase)
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
	crdName := crdPlural + ".cluster.com"
	crd := newCRD(crdName, crdPlural)

	// Generate permissions
	serviceAccountName := genName("nginx-sa")
	permissions := []install.StrategyDeploymentPermissions{
		{
			ServiceAccountName: serviceAccountName,
			Rules: []rbac.PolicyRule{
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
			Rules: []rbac.PolicyRule{
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
	stableCSV := newCSV(stableCSVName, testNamespace, "", *semver.New("0.1.0"), []extv1beta1.CustomResourceDefinition{crd}, nil, namedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create CatalogSource
	catalogSourceName := genName("nginx-catalog")
	_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, manifests, []extv1beta1.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{stableCSV})
	require.NoError(t, err)
	defer cleanupCatalogSource()

	// Attempt to get CatalogSource
	_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create InstallPlan
	installPlanName := genName("install-nginx")
	installPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      installPlanName,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{stableCSVName},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}
	cleanupInstallPlan, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
	require.NoError(t, err)
	defer cleanupInstallPlan()

	// Attempt to get InstallPlan
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed, v1alpha1.InstallPlanPhaseComplete))
	require.NoError(t, err)
	require.NotEqual(t, v1alpha1.InstallPlanPhaseFailed, fetchedInstallPlan.Status.Phase, "InstallPlan failed")

	// Expect correct RBAC resources to be resolved and created
	expectedSteps := map[registry.ResourceKey]struct{}{
		registry.ResourceKey{Name: crdName, Kind: "CustomResourceDefinition"}:                                             {},
		registry.ResourceKey{Name: fmt.Sprintf("edit-%s-%s", crdName, "v1alpha1"), Kind: "ClusterRole"}:                   {},
		registry.ResourceKey{Name: fmt.Sprintf("view-%s-%s", crdName, "v1alpha1"), Kind: "ClusterRole"}:                   {},
		registry.ResourceKey{Name: stableCSVName, Kind: "ClusterServiceVersion"}:                                          {},
		registry.ResourceKey{Name: serviceAccountName, Kind: "ServiceAccount"}:                                            {},
		registry.ResourceKey{Name: fmt.Sprintf("%s-0", stableCSVName), Kind: "Role"}:                                      {},
		registry.ResourceKey{Name: fmt.Sprintf("%s-0-%s", stableCSVName, serviceAccountName), Kind: "RoleBinding"}:        {},
		registry.ResourceKey{Name: fmt.Sprintf("%s-0", stableCSVName), Kind: "ClusterRole"}:                               {},
		registry.ResourceKey{Name: fmt.Sprintf("%s-0-%s", stableCSVName, serviceAccountName), Kind: "ClusterRoleBinding"}: {},
	}

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

}
