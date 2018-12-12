package e2e

import (
	"encoding/json"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// Test Subscription behavior

//   I. Creating a new subscription
//      A. If package is not installed, creating a subscription should install latest version
//      B. If package is already installed, creating a subscription should upgrade it to the latest
//         version
//      C. If a subscription for the package already exists, it should be marked as a duplicate and
//         ignored by the catalog operator (?)

//  II. Updates - existing subscription for package, and a newer version is added to the channel
//      A. Test bumping up 1 version (for a normal tectonic upgrade)
//         If the new version directly replaces the existing install, the latest version should be
//         directly installed.
//      B. Test bumping up multiple versions (simulate external catalog where we might jump
//         multiple versions, or an offline install where we might upgrade services all at once)
//         If existing install is more than one version behind, it should be upgraded stepping
//         through all intermediate versions.

// III. Channel switched on the subscription
//      A. If the current csv exists in the new channel, create installplans to update to the
//         latest in the new channel
//      B. Current csv does not exist in the new channel, subscription should have an error status
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
			Replaces:        "",
			Version:         *semver.New("0.1.0"),
			InstallStrategy: installStrategy,
		},
	}
	stableCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: stable,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        outdated,
			Version:         *semver.New("0.2.0"),
			InstallStrategy: installStrategy,
		},
	}
	betaCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: beta,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        stable,
			Version:         *semver.New("0.1.1"),
			InstallStrategy: installStrategy,
		},
	}
	alphaCSV = v1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: alpha,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        beta,
			Version:         *semver.New("0.3.0"),
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
}

func initCatalog(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface) error {
	// Create configmap containing catalog
	dummyCatalogConfigMap.SetNamespace(operatorNamespace)
	_, err := c.KubernetesInterface().CoreV1().ConfigMaps(operatorNamespace).Create(dummyCatalogConfigMap)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}

	// Create catalog source custom resource pointing to ConfigMap
	dummyCatalogSource.SetNamespace(operatorNamespace)
	csUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&dummyCatalogSource)
	require.NoError(t, err)
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: csUnst})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
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

func subscriptionStateNoneChecker(subscription *v1alpha1.Subscription) bool {
	return subscription.Status.State == v1alpha1.SubscriptionStateNone
}

func subscriptionStateAny(subscription *v1alpha1.Subscription) bool {
	return subscriptionStateNoneChecker(subscription) ||
		subscriptionStateAtLatestChecker(subscription) ||
		subscriptionStateUpgradePendingChecker(subscription) ||
		subscriptionStateUpgradeAvailableChecker(subscription)
}

func fetchSubscription(t *testing.T, crc versioned.Interface, namespace, name string, checker subscriptionStateChecker) (*v1alpha1.Subscription, error) {
	var fetchedSubscription *v1alpha1.Subscription
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedSubscription, err = crc.OperatorsV1alpha1().Subscriptions(namespace).Get(name, metav1.GetOptions{})
		if err != nil || fetchedSubscription == nil {
			return false, err
		}
		t.Logf("%s (%s): %s", fetchedSubscription.Status.State, fetchedSubscription.Status.Reason, fetchedSubscription.Status.Install)
		return checker(fetchedSubscription), nil
	})
	if err != nil {
		t.Logf("never got correct status: %#v", fetchedSubscription.Status)
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
			CatalogSource:       catalogSourceName,
			Package:             testPackageName,
			Channel:             channel,
			InstallPlanApproval: approval,
		},
	}

	subscription, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Create(subscription)
	require.NoError(t, err)
	return buildSubscriptionCleanupFunc(t, crc, subscription)
}

func checkForCSV(t *testing.T, c operatorclient.ClientInterface, name string) (*v1alpha1.ClusterServiceVersion, error) {
	var csv *v1alpha1.ClusterServiceVersion
	unstrCSV, err := waitForAndFetchCustomResource(t, c, v1alpha1.GroupVersion, v1alpha1.ClusterServiceVersionKind, name)
	require.NoError(t, err)
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstrCSV.Object, &csv)
	return csv, err
}

//   I. Creating a new subscription
//      A. If package is not installed, creating a subscription should install latest version
func TestCreateNewSubscription(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)
	require.NoError(t, initCatalog(t, c, crc))

	cleanup := createSubscription(t, crc, testNamespace, testSubscriptionName, testPackageName, betaChannel, v1alpha1.ApprovalAutomatic)
	defer cleanup()

	csv, err := checkForCSV(t, c, beta)
	require.NoError(t, err)
	require.NotNil(t, csv)

	subscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Fetch subscription again to check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	compareResources(t, subscription, sameSubscription)
}

//   I. Creating a new subscription
//      B. If package is already installed, creating a subscription should upgrade it to the latest
//         version
func TestCreateNewSubscriptionAgain(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)
	require.NoError(t, initCatalog(t, c, crc))

	// Will be cleaned up by the upgrade process
	_, err := createCSV(t, c, crc, stableCSV, testNamespace, false, false)
	require.NoError(t, err)

	subscriptionCleanup := createSubscription(t, crc, testNamespace, testSubscriptionName, testPackageName, alphaChannel, v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	csv, err := checkForCSV(t, c, alpha)
	require.NoError(t, err)
	require.NotNil(t, csv)

	subscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, crc, testNamespace, testSubscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	compareResources(t, subscription, sameSubscription)
}

// If installPlanApproval is set to manual, the installplans created should be created with approval: manual
func TestCreateNewSubscriptionManualApproval(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	c := newKubeClient(t)
	crc := newCRClient(t)

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

	// Fetch subscription again to check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, crc, testNamespace, "manual-subscription", subscriptionStateUpgradePendingChecker)
	compareResources(t, subscription, sameSubscription)
}
