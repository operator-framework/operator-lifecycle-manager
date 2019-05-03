package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/version"
)

var doubleInstance = int32(2)

const (
	catalogSourceName    = "mock-ocs"
	catalogConfigMapName = "mock-ocs"
	testSubscriptionName = "mysubscription"
	testPackageName      = "myapp"

	stableChannel = "stable"
	betaChannel   = "beta"
	alphaChannel  = "alpha"

	outdated = "myapp-outdated"
	stable   = "myapp-stable"
	alpha    = "myapp-alpha"
	beta     = "myapp-beta"
)

var (
	dummyManifest = []registry.PackageManifest{{
		PackageName: testPackageName,
		Channels: []registry.PackageChannel{
			{Name: stableChannel, CurrentCSVName: stable},
			{Name: betaChannel, CurrentCSVName: beta},
			{Name: alphaChannel, CurrentCSVName: alpha},
		},
		DefaultChannelName: stableChannel,
	}}
	csvType = metav1.TypeMeta{
		Kind:       v1alpha1.ClusterServiceVersionKind,
		APIVersion: v1alpha1.GroupVersion,
	}

	strategy = install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
				Spec: newNginxDeployment(genName("nginx-")),
			},
		},
	}
	strategyRaw, _  = json.Marshal(strategy)
	installStrategy = v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}
	outdatedCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: outdated,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:       "",
			Version:        version.OperatorVersion{semver.MustParse("0.1.0")},
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
			InstallStrategy: installStrategy,
		},
	}
	stableCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: stable,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:       outdated,
			Version:        version.OperatorVersion{semver.MustParse("0.2.0")},
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
			InstallStrategy: installStrategy,
		},
	}
	betaCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: beta,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: stable,
			Version:  version.OperatorVersion{semver.MustParse("0.1.1")},
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
			InstallStrategy: installStrategy,
		},
	}
	alphaCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: alpha,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces: beta,
			Version:  version.OperatorVersion{semver.MustParse("0.3.0")},
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
			InstallStrategy: installStrategy,
		},
	}
	csvList = []v1alpha1.ClusterServiceVersion{outdatedCSV, stableCSV, betaCSV, alphaCSV}

	strategyNew = install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: genName("dep-"),
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
								Image:           "bitnami/nginx:latest",
								Ports:           []corev1.ContainerPort{{ContainerPort: 80}},
								ImagePullPolicy: corev1.PullIfNotPresent,
							},
						}},
					},
				},
			},
		},
	}

	dummyCatalogConfigMap = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: catalogConfigMapName,
		},
		Data: map[string]string{},
	}

	dummyCatalogSource = v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: catalogSourceName,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: "internal",
			ConfigMap:  catalogConfigMapName,
		},
	}
)

func init() {
	strategyNewRaw, err := json.Marshal(strategyNew)
	if err != nil {
		panic(err)
	}
	for i := 0; i < len(csvList); i++ {
		csvList[i].Spec.InstallStrategy.StrategySpecRaw = strategyNewRaw
	}

	manifestsRaw, err := yaml.Marshal(dummyManifest)
	if err != nil {
		panic(err)
	}
	dummyCatalogConfigMap.Data[registry.ConfigMapPackageName] = string(manifestsRaw)
	csvsRaw, err := yaml.Marshal(csvList)
	if err != nil {
		panic(err)
	}
	dummyCatalogConfigMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)
	dummyCatalogConfigMap.Data[registry.ConfigMapCRDName] = ""
}

func initCatalog(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface) error {
	// Create configmap containing catalog
	dummyCatalogConfigMap.SetNamespace(testNamespace)
	if _, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(dummyCatalogConfigMap); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("E2E bug detected: %v", err)
		}
		return err
	}

	// Create catalog source custom resource pointing to ConfigMap
	dummyCatalogSource.SetNamespace(testNamespace)
	if _, err := crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(&dummyCatalogSource); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return fmt.Errorf("E2E bug detected: %v", err)
		}
		return err
	}

	// Wait for the catalog source to be created
	fetched, err := fetchCatalogSource(t, crc, dummyCatalogSource.GetName(), dummyCatalogSource.GetNamespace(), catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	require.NotNil(t, fetched)

	return nil
}

type subscriptionStateChecker func(subscription *v1alpha1.Subscription) bool

func subscriptionStateUpgradeAvailableChecker(subscription *v1alpha1.Subscription) bool {
	return subscription.Status.State == v1alpha1.SubscriptionStateUpgradeAvailable
}

func subscriptionStateUpgradePendingChecker(subscription *v1alpha1.Subscription) bool {
	return subscription.Status.State == v1alpha1.SubscriptionStateUpgradePending
}

func subscriptionStateAtLatestChecker(subscription *v1alpha1.Subscription) bool {
	return subscription.Status.State == v1alpha1.SubscriptionStateAtLatest
}

func subscriptionHasInstallPlanChecker(subscription *v1alpha1.Subscription) bool {
	return subscription.Status.Install != nil
}

func subscriptionStateNoneChecker(subscription *v1alpha1.Subscription) bool {
	return subscription.Status.State == v1alpha1.SubscriptionStateNone
}

func subscriptionStateAny(subscription *v1alpha1.Subscription) bool {
	return subscriptionStateNoneChecker(subscription) ||
		subscriptionStateAtLatestChecker(subscription) ||
		subscriptionStateUpgradePendingChecker(subscription) ||
		subscriptionStateUpgradeAvailableChecker(subscription)
}

func subscriptionHasCurrentCSV(currentCSV string) subscriptionStateChecker {
	return func(subscription *v1alpha1.Subscription) bool {
		return subscription.Status.CurrentCSV == currentCSV
	}
}

func fetchSubscription(t *testing.T, crc versioned.Interface, namespace, name string, checker subscriptionStateChecker) (*v1alpha1.Subscription, error) {
	var fetchedSubscription *v1alpha1.Subscription
	var err error

	log := func(s string) {
		t.Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedSubscription, err = crc.OperatorsV1alpha1().Subscriptions(namespace).Get(name, metav1.GetOptions{})
		if err != nil || fetchedSubscription == nil {
			return false, err
		}
		log(fmt.Sprintf("%s (%s): %s", fetchedSubscription.Status.State, fetchedSubscription.Status.CurrentCSV, fetchedSubscription.Status.InstallPlanRef))
		return checker(fetchedSubscription), nil
	})
	if err != nil {
		log(fmt.Sprintf("never got correct status: %#v", fetchedSubscription.Status))
		log(fmt.Sprintf("subscription spec: %#v", fetchedSubscription.Spec))
	}
	return fetchedSubscription, err
}

func buildSubscriptionCleanupFunc(t *testing.T, crc versioned.Interface, subscription *v1alpha1.Subscription) cleanupFunc {
	return func() {
		// Check for an installplan
		if installPlanRef := subscription.Status.Install; installPlanRef != nil {
			// Get installplan and create/execute cleanup function
			installPlan, err := crc.OperatorsV1alpha1().InstallPlans(subscription.GetNamespace()).Get(installPlanRef.Name, metav1.GetOptions{})
			if err == nil {
				buildInstallPlanCleanupFunc(crc, subscription.GetNamespace(), installPlan)()
			} else {
				t.Logf("Could not get installplan %s while building subscription %s's cleanup function", installPlan.GetName(), subscription.GetName())
			}
		}

		// Delete the subscription
		err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Delete(subscription.GetName(), &metav1.DeleteOptions{})
		require.NoError(t, err)
	}
}

func createSubscription(t *testing.T, crc versioned.Interface, namespace, name, packageName, channel string, approval v1alpha1.Approval) cleanupFunc {
	subscription := &v1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.SubscriptionKind,
			APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:          catalogSourceName,
			CatalogSourceNamespace: namespace,
			Package:                packageName,
			Channel:                channel,
			InstallPlanApproval:    approval,
		},
	}

	subscription, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Create(subscription)
	require.NoError(t, err)
	return buildSubscriptionCleanupFunc(t, crc, subscription)
}

func createSubscriptionForCatalog(t *testing.T, crc versioned.Interface, namespace, name, catalog, packageName, channel, startingCSV string, approval v1alpha1.Approval) cleanupFunc {
	subscription := &v1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.SubscriptionKind,
			APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:          catalog,
			CatalogSourceNamespace: namespace,
			Package:                packageName,
			Channel:                channel,
			StartingCSV:            startingCSV,
			InstallPlanApproval:    approval,
		},
	}

	subscription, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Create(subscription)
	require.NoError(t, err)
	return buildSubscriptionCleanupFunc(t, crc, subscription)
}

//   I. Creating a new subscription
//      A. If package is not installed, creating a subscription should install latest version
func TestCreateNewSubscriptionNotInstalled(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
	}()
	require.NoError(t, initCatalog(t, c, crc))

	cleanup := createSubscription(t, crc, testNamespace, testSubscriptionName, testPackageName, betaChannel, v1alpha1.ApprovalAutomatic)
	defer cleanup()

	subscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)

	// Fetch subscription again to check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	compareResources(t, subscription, sameSubscription)
}

//   I. Creating a new subscription
//      B. If package is already installed, creating a subscription should upgrade it to the latest
//         version
func TestCreateNewSubscriptionExistingCSV(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
	}()
	require.NoError(t, initCatalog(t, c, crc))

	// Will be cleaned up by the upgrade process
	_, err := createCSV(t, c, crc, stableCSV, testNamespace, false, false)
	require.NoError(t, err)

	subscriptionCleanup := createSubscription(t, crc, testNamespace, testSubscriptionName, testPackageName, alphaChannel, v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	subscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)

	// check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	compareResources(t, subscription, sameSubscription)
}

func TestSubscriptionSkipRange(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"
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
		},
	}

	mainPackageName := genName("nginx-")
	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	updatedPackageStable := fmt.Sprintf("%s-updated", mainPackageName)
	stableChannel := "stable"
	mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0-1556661347"), []apiextensions.CustomResourceDefinition{crd}, nil, mainNamedStrategy)
	updatedCSV := newCSV(updatedPackageStable, testNamespace, "", semver.MustParse("0.1.0-1556661832"), []apiextensions.CustomResourceDefinition{crd}, nil, mainNamedStrategy)
	updatedCSV.SetAnnotations(map[string]string{resolver.SkipPackageAnnotationKey: ">=0.1.0-1556661347 <0.1.0-1556661832"})

	c := newKubeClient(t)
	crc := newCRClient(t)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
	}()

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
	updatedManifests := []registry.PackageManifest{
		{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: updatedPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create catalog source
	_, cleanupMainCatalogSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{mainCSV})
	defer cleanupMainCatalogSource()
	// Attempt to get the catalog source before creating subscription
	_, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create a subscription
	subscriptionName := genName("sub-nginx-")
	subscriptionCleanup := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	// Wait for csv to install
	firstCSV, err := awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Update catalog with a new csv in the channel with a skip range
	updateInternalCatalog(t, c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{updatedCSV}, updatedManifests)

	// Wait for csv to update
	finalCSV, err := awaitCSV(t, crc, testNamespace, updatedCSV.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Ensure we set the replacement field based on the registry data
	require.Equal(t, firstCSV.GetName(), finalCSV.Spec.Replaces)
}

// If installPlanApproval is set to manual, the installplans created should be created with approval: manual
func TestCreateNewSubscriptionManualApproval(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(testNamespace).DeleteCollection(&metav1.DeleteOptions{}, metav1.ListOptions{}))
	}()
	require.NoError(t, initCatalog(t, c, crc))

	subscriptionCleanup := createSubscription(t, crc, testNamespace, "manual-subscription", testPackageName, stableChannel, v1alpha1.ApprovalManual)
	defer subscriptionCleanup()

	subscription, err := fetchSubscription(t, crc, testNamespace, "manual-subscription", subscriptionStateUpgradePendingChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlan, err := fetchInstallPlan(t, crc, subscription.Status.Install.Name, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseRequiresApproval))
	require.NoError(t, err)
	require.NotNil(t, installPlan)

	require.Equal(t, v1alpha1.ApprovalManual, installPlan.Spec.Approval)
	require.Equal(t, v1alpha1.InstallPlanPhaseRequiresApproval, installPlan.Status.Phase)

	installPlan.Spec.Approved = true
	_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(installPlan)
	require.NoError(t, err)

	subscription, err = fetchSubscription(t, crc, testNamespace, "manual-subscription", subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)
}

func TestSusbcriptionWithStartingCSV(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

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
		},
	}

	// Create CSV
	packageName := genName("nginx-")
	stableChannel := "stable"

	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csvA := newCSV("nginx-a", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	csvB := newCSV("nginx-b", testNamespace, "nginx-a", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	// Create PackageManifests
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: csvB.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create the CatalogSource
	c := newKubeClient(t)
	crc := newCRClient(t)
	catalogSourceName := genName("mock-nginx-")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csvA, csvB})
	defer cleanupCatalogSource()

	// Attempt to get the catalog source before creating install plan
	_, err := fetchCatalogSource(t, crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("sub-nginx-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), v1alpha1.ApprovalManual)
	defer cleanupSubscription()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.Install.Name

	// Wait for InstallPlan to be status: Complete before checking resource presence
	requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseRequiresApproval)
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, requiresApprovalChecker)
	require.NoError(t, err)

	// Ensure that only 1 installplan was created
	ips, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).List(metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, ips.Items, 1)

	// Ensure that csvA and its crd are found in the plan
	csvFound := false
	crdFound := false
	for _, s := range fetchedInstallPlan.Status.Plan {
		require.Equal(t, csvA.GetName(), s.Resolving, "unexpected resolution found")
		require.Equal(t, v1alpha1.StepStatusUnknown, s.Status, "status should be unknown")
		require.Equal(t, catalogSourceName, s.Resource.CatalogSource, "incorrect catalogsource on step resource")
		switch kind := s.Resource.Kind; kind {
		case v1alpha1.ClusterServiceVersionKind:
			require.Equal(t, csvA.GetName(), s.Resource.Name, "unexpected csv found")
			csvFound = true
		case "CustomResourceDefinition":
			require.Equal(t, crdName, s.Resource.Name, "unexpected crd found")
			crdFound = true
		default:
			t.Fatalf("unexpected resource kind found in installplan: %s", kind)
		}
	}
	require.True(t, csvFound, "expected csv not found in installplan")
	require.True(t, crdFound, "expected crd not found in installplan")

	// Ensure that csvB is not found in the plan
	csvFound = false
	for _, s := range fetchedInstallPlan.Status.Plan {
		require.Equal(t, csvA.GetName(), s.Resolving, "unexpected resolution found")
		require.Equal(t, v1alpha1.StepStatusUnknown, s.Status, "status should be unknown")
		require.Equal(t, catalogSourceName, s.Resource.CatalogSource, "incorrect catalogsource on step resource")
		switch kind := s.Resource.Kind; kind {
		case v1alpha1.ClusterServiceVersionKind:
			if s.Resource.Name == csvB.GetName() {
				csvFound = true
			}
		}
	}
	require.False(t, csvFound, "expected csv not found in installplan")

	// Approve the installplan and wait for csvA to be installed
	fetchedInstallPlan.Spec.Approved = true
	_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(fetchedInstallPlan)
	require.NoError(t, err)

	_, err = awaitCSV(t, crc, testNamespace, csvA.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Wait for the subscription to begin upgrading to csvB
	subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateUpgradePendingChecker)
	require.NoError(t, err)
	require.NotEqual(t, fetchedInstallPlan.GetName(), subscription.Status.Install.Name, "expected new installplan for upgraded csv")

	upgradeInstallPlan, err := fetchInstallPlan(t, crc, subscription.Status.Install.Name, requiresApprovalChecker)
	require.NoError(t, err)

	// Approve the upgrade installplan and wait for
	upgradeInstallPlan.Spec.Approved = true
	_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(upgradeInstallPlan)
	require.NoError(t, err)

	_, err = awaitCSV(t, crc, testNamespace, csvB.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Ensure that 2 installplans were created
	ips, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).List(metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, ips.Items, 2)
}

func TestSubscriptionUpdatesMultipleIntermediates(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	crdPlural := genName("ins")
	crdName := crdPlural + ".cluster.com"

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
		},
	}

	// Create CSV
	packageName := genName("nginx-")
	stableChannel := "stable"

	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csvA := newCSV("nginx-a", testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	csvB := newCSV("nginx-b", testNamespace, "nginx-a", semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	csvC := newCSV("nginx-c", testNamespace, "nginx-b", semver.MustParse("0.3.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	// Create PackageManifests
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: csvA.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create the CatalogSource with just one version
	c := newKubeClient(t)
	crc := newCRClient(t)
	catalogSourceName := genName("mock-nginx-")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csvA})
	defer cleanupCatalogSource()

	// Attempt to get the catalog source before creating install plan
	_, err := fetchCatalogSource(t, crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionName := genName("sub-nginx-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, catalogSourceName, packageName, stableChannel, csvA.GetName(), v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Wait for csvA to be installed
	_, err = awaitCSV(t, crc, testNamespace, csvA.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Set up async watches that will fail the test if csvB doesn't get created in between csvA and csvC
	var wg sync.WaitGroup
	go func(t *testing.T) {
		wg.Add(1)
		defer wg.Done()
		_, err := awaitCSV(t, crc, testNamespace, csvB.GetName(), csvReplacingChecker)
		require.NoError(t, err)
	}(t)
	// Update the catalog to include multiple updates
	packages := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: csvC.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
	}

	updateInternalCatalog(t, c, crc, catalogSourceName, testNamespace, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csvA, csvB, csvC}, packages)

	// wait for checks on intermediate csvs to succeed
	wg.Wait()

	// Wait for csvC to be installed
	_, err = awaitCSV(t, crc, testNamespace, csvC.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Should eventually GC the CSVs
	err = waitForCSVToDelete(t, crc, csvA.Name)
	require.NoError(t, err)
	err = waitForCSVToDelete(t, crc, csvB.Name)
	require.NoError(t, err)

	// TODO: check installplans, subscription status, etc
}

func updateInternalCatalog(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, catalogSourceName, namespace string, crds []apiextensions.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion, packages []registry.PackageManifest) {
	fetchedInitialCatalog, err := fetchCatalogSource(t, crc, catalogSourceName, namespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Get initial configmap
	configMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
	require.NoError(t, err)

	// Update package to point to new csv
	manifestsRaw, err := yaml.Marshal(packages)
	require.NoError(t, err)
	configMap.Data[registry.ConfigMapPackageName] = string(manifestsRaw)

	// Update raw CRDs
	var crdsRaw []byte
	crdStrings := []string{}
	for _, crd := range crds {
		crdStrings = append(crdStrings, serializeCRD(t, crd))
	}
	crdsRaw, err = yaml.Marshal(crdStrings)
	require.NoError(t, err)
	configMap.Data[registry.ConfigMapCRDName] = strings.Replace(string(crdsRaw), "- |\n  ", "- ", -1)

	// Update raw CSVs
	csvsRaw, err := yaml.Marshal(csvs)
	require.NoError(t, err)
	configMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)

	// Update configmap
	_, err = c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Update(configMap)
	require.NoError(t, err)

	// wait for catalog to update
	_, err = fetchCatalogSource(t, crc, catalogSourceName, testNamespace, func(catalog *v1alpha1.CatalogSource) bool {
		if catalog.Status.LastSync != fetchedInitialCatalog.Status.LastSync && catalog.Status.ConfigMapResource.ResourceVersion != fetchedInitialCatalog.Status.ConfigMapResource.ResourceVersion {
			fmt.Println("catalog updated")
			return true
		}
		fmt.Println("waiting for catalog pod to be available")
		return false
	})
	require.NoError(t, err)
}
