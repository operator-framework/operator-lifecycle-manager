package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	etcdVersion            = "3.2.13"
	prometheusVersion      = "v2.3.2"
	expectedEtcdNodes      = 3
	expectedPrometheusSize = 3
	ocsConfigMap           = "ocs"
)

type installPlanConditionChecker func(fip *v1alpha1.InstallPlan) bool

var installPlanCompleteChecker = func(fip *v1alpha1.InstallPlan) bool {
	return fip.Status.Phase == v1alpha1.InstallPlanPhaseComplete
}

var installPlanFailedChecker = func(fip *v1alpha1.InstallPlan) bool {
	return fip.Status.Phase == v1alpha1.InstallPlanPhaseFailed
}

var installPlanCompleteOrFailedChecker = func(fip *v1alpha1.InstallPlan) bool {
	return installPlanCompleteChecker(fip) || installPlanFailedChecker(fip)
}

var installPlanRequiresApprovalChecker = func(fip *v1alpha1.InstallPlan) bool {
	return fip.Status.Phase == v1alpha1.InstallPlanPhaseRequiresApproval
}

func buildInstallPlanCleanupFunc(crc versioned.Interface, namespace string, installPlan *v1alpha1.InstallPlan) cleanupFunc {
	return func() {
		for _, step := range installPlan.Status.Plan {
			if step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
				if err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(step.Resource.Name, &metav1.DeleteOptions{}); err != nil {
					fmt.Println(err)
				}
			}
		}
		if err := crc.OperatorsV1alpha1().InstallPlans(namespace).Delete(installPlan.GetName(), &metav1.DeleteOptions{}); err != nil {
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

func fetchInstallPlan(t *testing.T, c versioned.Interface, name string, checker installPlanConditionChecker) (*v1alpha1.InstallPlan, error) {
	var fetchedInstallPlan *v1alpha1.InstallPlan
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlan, err = c.OperatorsV1alpha1().InstallPlans(testNamespace).Get(name, metav1.GetOptions{})
		if err != nil || fetchedInstallPlan == nil {
			return false, err
		}
		return checker(fetchedInstallPlan), nil
	})
	return fetchedInstallPlan, err
}

func newCRClient(t *testing.T) versioned.Interface {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	crclient, err := client.NewClient(kubeconfigPath)
	require.NoError(t, err)
	return crclient
}

func TestCreateInstallPlanManualApproval(t *testing.T) {
	c := newKubeClient(t)
	crc := newCRClient(t)

	inMem, err := registry.NewInMemoryFromConfigMap(c, testNamespace, ocsConfigMap)
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

	// Create a new InstallPlan for Vault with manual approval
	cleanup, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, etcdInstallPlan)
	require.NoError(t, err)
	defer cleanup()

	// Get InstallPlan and verify status
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, etcdInstallPlan.GetName(), installPlanRequiresApprovalChecker)
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

	approvedInstallPlan, err := fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), installPlanCompleteChecker)
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

	cleanup, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
	require.NoError(t, err)
	defer cleanup()

	// Wait for InstallPlan to be status: Complete before checking for resource presence
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), installPlanFailedChecker)
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
	mainPackageName := "nginx"
	dependentPackageName := "nginxdep"

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

	stableChannel := "stable"

	// Create separate manifests for each CatalogSource
	mainManifests := []registry.PackageManifest{
		registry.PackageManifest{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				registry.PackageChannel{Name: stableChannel, CurrentCSVName: mainPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	dependentManifests := []registry.PackageManifest{
		registry.PackageManifest{
			PackageName: dependentPackageName,
			Channels: []registry.PackageChannel{
				registry.PackageChannel{Name: stableChannel, CurrentCSVName: dependentPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Generate CSVs for each package
	csvType = metav1.TypeMeta{
		Kind:       v1alpha1.ClusterServiceVersionKind,
		APIVersion: v1alpha1.GroupVersion,
	}

	// Create an install strategy
	strategy = install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
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
								Image: "nginx:1.7.9",
								Ports: []corev1.ContainerPort{{ContainerPort: 80}},
							},
						}},
					},
				},
			},
		},
	}
	strategyRaw, _ = json.Marshal(strategy)
	installStrategy = v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

	dependentCRD := extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: "CustomResourceDefinition",
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: extv1beta1.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	}

	mainCSV := v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: mainPackageStable,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        "",
			Version:         *semver.New("0.1.0"),
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Required: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: crdName,
					},
				},
			},
		},
	}

	dependentCSV := v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: dependentPackageStable,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        "",
			Version:         *semver.New("0.1.0"),
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: crdName,
					},
				},
			},
		},
	}

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create expected install plan step sources
	expectedStepSources := map[registry.ResourceKey]registry.ResourceKey{
		registry.ResourceKey{Name: crdName, Kind: "CustomResourceDefinition"}:                        registry.ResourceKey{Name: "mock-ocs-dependent", Namespace: testNamespace},
		registry.ResourceKey{Name: dependentPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}: registry.ResourceKey{Name: "mock-ocs-dependent", Namespace: testNamespace},
		registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:      registry.ResourceKey{Name: "mock-ocs-main", Namespace: testNamespace},
	}

	// Create the catalog sources
	_, cleanupDependentCatalogSource, err := createInternalCatalogSource(t, c, crc, "mock-ocs-dependent", testNamespace, dependentManifests, []extv1beta1.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{dependentCSV})
	require.NoError(t, err)
	defer cleanupDependentCatalogSource()
	_, cleanupMainCatalogSource, err := createInternalCatalogSource(t, c, crc, "mock-ocs-main", testNamespace, mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})
	require.NoError(t, err)
	defer cleanupMainCatalogSource()

	// Fetch list of catalog sources
	installPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-nginx",
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
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), installPlanCompleteChecker)
	require.NoError(t, err)
	t.Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)

	require.Equal(t, v1alpha1.InstallPlanPhaseComplete, fetchedInstallPlan.Status.Phase)

	// Fetch installplan again to check for unnecessary control loops
	fetchedInstallPlan, err = fetchInstallPlan(t, crc, fetchedInstallPlan.GetName(), func(fip *v1alpha1.InstallPlan) bool {
		compareResources(t, fetchedInstallPlan, fip)
		return true
	})
	require.NoError(t, err)

	require.Equal(t, len(fetchedInstallPlan.Status.Plan), len(expectedStepSources))
	t.Logf("Number of resolved steps matches the number of expected steps")

	// Ensure resolved step resources originate from the correct catalog sources
	for _, step := range fetchedInstallPlan.Status.Plan {
		key := registry.ResourceKey{Name: step.Resource.Name, Kind: step.Resource.Kind}
		expectedSource, ok := expectedStepSources[key]
		require.True(t, ok)
		require.Equal(t, step.Resource.CatalogSource, expectedSource.Name)
		require.Equal(t, step.Resource.CatalogSourceNamespace, expectedSource.Namespace)
	}
	t.Logf("All expected resources resolved")
}

func TestCreateInstallPlanWithPreExistingCRDOwners(t *testing.T) {
	mainPackageName := "nginx"
	dependentPackageName := "nginxdep"

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)
	dependentPackageBeta := fmt.Sprintf("%s-beta", dependentPackageName)

	stableChannel := "stable"
	betaChannel := "beta"

	// Create manifests
	mainManifests := []registry.PackageManifest{
		registry.PackageManifest{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				registry.PackageChannel{Name: stableChannel, CurrentCSVName: mainPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
		registry.PackageManifest{
			PackageName: dependentPackageName,
			Channels: []registry.PackageChannel{
				registry.PackageChannel{Name: stableChannel, CurrentCSVName: dependentPackageStable},
				registry.PackageChannel{Name: betaChannel, CurrentCSVName: dependentPackageBeta},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Generate CSVs for each package
	csvType = metav1.TypeMeta{
		Kind:       v1alpha1.ClusterServiceVersionKind,
		APIVersion: v1alpha1.GroupVersion,
	}

	// Create an install strategy
	strategy = install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
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
								Image: "nginx:1.7.9",
								Ports: []corev1.ContainerPort{{ContainerPort: 80}},
							},
						}},
					},
				},
			},
		},
	}
	strategyRaw, _ = json.Marshal(strategy)
	installStrategy = v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

	dependentCRD := extv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: crdName,
		},
		TypeMeta: metav1.TypeMeta{
			Kind: "CustomResourceDefinition",
		},
		Spec: extv1beta1.CustomResourceDefinitionSpec{
			Group:   "cluster.com",
			Version: "v1alpha1",
			Names: extv1beta1.CustomResourceDefinitionNames{
				Plural:   crdPlural,
				Singular: crdPlural,
				Kind:     crdPlural,
				ListKind: "list" + crdPlural,
			},
			Scope: "Namespaced",
		},
	}

	mainCSV := v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: mainPackageStable,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        "",
			Version:         *semver.New("0.1.0"),
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Required: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: crdName,
					},
				},
			},
		},
	}

	dependentStableCSV := v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: dependentPackageStable,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        "",
			Version:         *semver.New("0.1.0"),
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: crdName,
					},
				},
			},
		},
	}

	dependentBetaCSV := v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: dependentPackageBeta,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        dependentPackageStable,
			Version:         *semver.New("0.2.0"),
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned: []v1alpha1.CRDDescription{
					{
						Name:        crdName,
						Version:     "v1alpha1",
						Kind:        crdPlural,
						DisplayName: crdName,
						Description: crdName,
					},
				},
			},
		},
	}

	c := newKubeClient(t)
	crc := newCRClient(t)

	// Create the catalog source
	_, cleanupCatalogSource, err := createInternalCatalogSource(t, c, crc, "mock-ocs-main", testNamespace, mainManifests, []extv1beta1.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{dependentBetaCSV, dependentStableCSV, mainCSV})
	require.NoError(t, err)
	defer cleanupCatalogSource()

	// Fetch list of catalog sources
	installPlan := v1alpha1.InstallPlan{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.InstallPlanKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "install-nginx",
			Namespace: testNamespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{mainPackageStable},
			Approval:                   v1alpha1.ApprovalAutomatic,
		},
	}

	t.Run("OnePreExistingCRDOwner", func(t *testing.T) {
		expectedSteps := map[registry.ResourceKey]struct{}{
			registry.ResourceKey{Name: crdName, Kind: "CustomResourceDefinition"}:                      struct{}{},
			registry.ResourceKey{Name: dependentPackageBeta, Kind: v1alpha1.ClusterServiceVersionKind}: struct{}{},
			registry.ResourceKey{Name: mainPackageStable, Kind: v1alpha1.ClusterServiceVersionKind}:    struct{}{},
		}

		// Create the preexisting CRD and CSV
		cleanupCRD, err := createCRD(c, dependentCRD)
		require.NoError(t, err)
		defer cleanupCRD()
		cleanupCSV, err := createCSV(t, c, crc, dependentBetaCSV, testNamespace, true)
		require.NoError(t, err)
		defer cleanupCSV()
		t.Log("Dependent CRD and preexisting CSV created")

		cleanupInstallPlan, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
		require.NoError(t, err)
		t.Logf("Install plan %s created", installPlan.GetName())
		defer cleanupInstallPlan()

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), installPlanCompleteOrFailedChecker)
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
							Name:        crdName,
							Version:     "v1alpha1",
							Kind:        crdPlural,
							DisplayName: crdName,
							Description: crdName,
						},
					},
				},
			},
		}

		// Create the preexisting CRD and CSV
		cleanupCRD, err := createCRD(c, dependentCRD)
		require.NoError(t, err)
		defer cleanupCRD()
		cleanupBetaCSV, err := createCSV(t, c, crc, dependentBetaCSV, testNamespace, true)
		require.NoError(t, err)
		defer cleanupBetaCSV()
		cleanupSecondOwnerCSV, err := createCSV(t, c, crc, secondOwnerCSV, testNamespace, true)
		require.NoError(t, err)
		defer cleanupSecondOwnerCSV()
		t.Log("Dependent CRD and preexisting CSVs created")

		cleanupInstallPlan, err := decorateCommonAndCreateInstallPlan(crc, testNamespace, installPlan)
		require.NoError(t, err)
		t.Logf("Install plan %s created", installPlan.GetName())
		defer cleanupInstallPlan()

		// Wait for InstallPlan to be status: Complete or Failed before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlan.GetName(), installPlanCompleteOrFailedChecker)
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
}
