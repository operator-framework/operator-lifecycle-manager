package e2e

import (
	"encoding/json"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/require"
	"k8s.io/api/apps/v1beta2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
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
	dummyManifest = []registry.PackageManifest{registry.PackageManifest{
		PackageName: testPackageName,
		Channels: []registry.PackageChannel{
			registry.PackageChannel{Name: stableChannel, CurrentCSVName: stable},
			registry.PackageChannel{Name: betaChannel, CurrentCSVName: beta},
			registry.PackageChannel{Name: alphaChannel, CurrentCSVName: alpha},
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
				Spec: v1beta2.DeploymentSpec{
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
			Name:       catalogSourceName,
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

func initCatalog(t *testing.T, c operatorclient.ClientInterface) error {
	// create ConfigMap containing catalog
	dummyCatalogConfigMap.SetNamespace(testNamespace)
	_, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(dummyCatalogConfigMap)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	// create CatalogSource custom resource pointing to ConfigMap
	dummyCatalogSource.SetNamespace(testNamespace)
	csUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&dummyCatalogSource)
	require.NoError(t, err)
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: csUnst})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func createSubscription(t *testing.T, c operatorclient.ClientInterface, channel string, name string, approval v1alpha1.Approval) cleanupFunc {
	sub := &v1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.SubscriptionKind,
			APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      name,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:       catalogSourceName,
			Package:             testPackageName,
			Channel:             channel,
			InstallPlanApproval: approval,
		},
	}

	unstrSub, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sub)
	require.NoError(t, err)
	require.NoError(t, c.CreateCustomResource(&unstructured.Unstructured{Object: unstrSub}))
	return cleanupCustomResource(t, c, v1alpha1.GroupVersion,
		v1alpha1.SubscriptionKind, name)
}

func fetchSubscription(t *testing.T, c operatorclient.ClientInterface, name string) (*v1alpha1.Subscription, error) {
	var sub *v1alpha1.Subscription
	unstrSub, err := waitForAndFetchCustomResource(t, c, v1alpha1.GroupVersion, v1alpha1.SubscriptionKind, name)
	require.NoError(t, err)
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstrSub.Object, &sub)
	return sub, err
}

func checkForCSV(t *testing.T, c operatorclient.ClientInterface, name string) (*v1alpha1.ClusterServiceVersion, error) {
	var csv *v1alpha1.ClusterServiceVersion
	unstrCSV, err := waitForAndFetchCustomResource(t, c, v1alpha1.GroupVersion, v1alpha1.ClusterServiceVersionKind, name)
	require.NoError(t, err)
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstrCSV.Object, &csv)
	return csv, err
}

func checkForInstallPlan(t *testing.T, c operatorclient.ClientInterface, owner ownerutil.Owner) (*v1alpha1.InstallPlan, error) {
	var installPlan *v1alpha1.InstallPlan
	installPlans, err := waitForAndFetchChildren(t, c, v1alpha1.GroupVersion, v1alpha1.InstallPlanKind, owner, 1)
	require.NoError(t, err)
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(installPlans[0].Object, &installPlan)
	t.Log(err)
	return installPlan, err
}

//   I. Creating a new subscription
//      A. If package is not installed, creating a subscription should install latest version
func TestCreateNewSubscription(t *testing.T) {
	c := newKubeClient(t)
	require.NoError(t, initCatalog(t, c))

	cleanup := createSubscription(t, c, betaChannel, testSubscriptionName, v1alpha1.ApprovalAutomatic)
	defer cleanup()

	csv, err := checkForCSV(t, c, beta)
	require.NoError(t, err)
	require.NotNil(t, csv)

	subscription, err := fetchSubscription(t, c, testSubscriptionName)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Fetch subscription again to check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, c, testSubscriptionName)
	compareResources(t, subscription, sameSubscription)

	// Deleting subscription / installplan doesn't clean up the CSV
	cleanupCustomResource(t, c, v1alpha1.GroupVersion,
		v1alpha1.ClusterServiceVersionKind, csv.GetName())()
}

//   I. Creating a new subscription
//      B. If package is already installed, creating a subscription should upgrade it to the latest
//         version
func TestCreateNewSubscriptionAgain(t *testing.T) {
	c := newKubeClient(t)
	crc := newCRClient(t)
	require.NoError(t, initCatalog(t, c))

	csvCleanup, err := createCSV(t, c, crc, stableCSV, true)
	require.NoError(t, err)
	defer csvCleanup()

	subscriptionCleanup := createSubscription(t, c, alphaChannel, testSubscriptionName, v1alpha1.ApprovalAutomatic)
	defer subscriptionCleanup()

	csv, err := checkForCSV(t, c, alpha)
	require.NoError(t, err)
	require.NotNil(t, csv)

	subscription, err := fetchSubscription(t, c, testSubscriptionName)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Deleting subscription / installplan doesn't clean up the CSV
	cleanupCustomResource(t, c, v1alpha1.GroupVersion,
		v1alpha1.ClusterServiceVersionKind, csv.GetName())()
}

// If installPlanApproval is set to manual, the installplans created should be created with approval: manual
func TestCreateNewSubscriptionManualApproval(t *testing.T) {
	c := newKubeClient(t)

	require.NoError(t, initCatalog(t, c))

	subscriptionCleanup := createSubscription(t, c, stableChannel, "manual-subscription", v1alpha1.ApprovalManual)
	defer subscriptionCleanup()

	subscription, err := fetchSubscription(t, c, "manual-subscription")
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlan, err := checkForInstallPlan(t, c, subscription)
	require.NoError(t, err)
	require.NotNil(t, installPlan)

	require.Equal(t, v1alpha1.ApprovalManual, installPlan.Spec.Approval)
	require.Equal(t, v1alpha1.InstallPlanPhaseRequiresApproval, installPlan.Status.Phase)

	// Fetch subscription again to check for unnecessary control loops
	sameSubscription, err := fetchSubscription(t, c, "manual-subscription")
	compareResources(t, subscription, sameSubscription)
}
