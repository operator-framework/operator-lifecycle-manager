package e2e

import (
	"encoding/json"
	"testing"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/coreos/go-semver/semver"
	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/require"
	"k8s.io/api/apps/v1beta2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	catalogsourcev1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/catalogsource/v1alpha1"
	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	subscriptionv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/subscription/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
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
	alpha    = "myapp-beta"
	beta     = "myapp-alpha"
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
		Kind:       csvv1alpha1.ClusterServiceVersionKind,
		APIVersion: csvv1alpha1.GroupVersion,
	}
	installStrategy = csvv1alpha1.NamedInstallStrategy{
		StrategyName: install.InstallStrategyNameDeployment,
	}
	outdatedCSV = csvv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: outdated,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Replaces:        "",
			Version:         *semver.New("0.1.0"),
			InstallStrategy: installStrategy,
		},
	}
	stableCSV = csvv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: stable,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Replaces:        outdated,
			Version:         *semver.New("0.2.0"),
			InstallStrategy: installStrategy,
		},
	}
	betaCSV = csvv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: beta,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Replaces:        stable,
			Version:         *semver.New("0.1.1"),
			InstallStrategy: installStrategy,
		},
	}
	alphaCSV = csvv1alpha1.ClusterServiceVersion{
		TypeMeta: csvType,
		ObjectMeta: metav1.ObjectMeta{
			Name: alpha,
		},
		Spec: csvv1alpha1.ClusterServiceVersionSpec{
			Replaces:        beta,
			Version:         *semver.New("0.3.0"),
			InstallStrategy: installStrategy,
		},
	}
	csvList = []csvv1alpha1.ClusterServiceVersion{outdatedCSV, stableCSV, betaCSV, alphaCSV}

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

	dummyCatalogSource = catalogsourcev1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       catalogsourcev1alpha1.CatalogSourceKind,
			APIVersion: catalogsourcev1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: catalogSourceName,
		},
		Spec: catalogsourcev1alpha1.CatalogSourceSpec{
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

func initCatalog(t *testing.T, c opClient.Interface) error {
	// create ConfigMap containing catalog
	dummyCatalogConfigMap.SetNamespace(testNamespace)
	_, err := c.CreateConfigMap(testNamespace, dummyCatalogConfigMap)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	// create CatalogSource custom resource pointing to ConfigMap
	dummyCatalogSource.SetNamespace(testNamespace)
	csUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&dummyCatalogSource)
	if err != nil {
		return err
	}
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: csUnst})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func createSubscription(t *testing.T, c opClient.Interface, channel string) cleanupFunc {
	sub := &subscriptionv1alpha1.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       subscriptionv1alpha1.SubscriptionKind,
			APIVersion: subscriptionv1alpha1.SubscriptionCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testSubscriptionName,
		},
		Spec: &subscriptionv1alpha1.SubscriptionSpec{
			CatalogSource: catalogSourceName,
			Package:       testPackageName,
			Channel:       channel,
		},
	}

	unstrSub, err := runtime.DefaultUnstructuredConverter.ToUnstructured(sub)
	require.NoError(t, err)
	require.NoError(t, c.CreateCustomResource(&unstructured.Unstructured{Object: unstrSub}))
	return cleanupCustomResource(c, subscriptionv1alpha1.GroupVersion,
		subscriptionv1alpha1.SubscriptionKind, testSubscriptionName)
}

func fetchSubscription(t *testing.T, c opClient.Interface, name string) (*subscriptionv1alpha1.Subscription, error) {
	var sub *subscriptionv1alpha1.Subscription
	unstrSub, err := waitForAndFetchCustomResource(t, c, subscriptionv1alpha1.GroupVersion, subscriptionv1alpha1.SubscriptionKind, name)
	if err != nil {
		return nil, err
	}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstrSub.Object, &sub)
	return sub, err
}

func checkForCSV(t *testing.T, c opClient.Interface, name string) (*csvv1alpha1.ClusterServiceVersion, error) {
	var csv *csvv1alpha1.ClusterServiceVersion
	unstrCSV, err := waitForAndFetchCustomResource(t, c, csvv1alpha1.GroupVersion, csvv1alpha1.ClusterServiceVersionKind, name)
	if err != nil {
		return nil, err
	}
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstrCSV.Object, &csv)
	return csv, err
}

//   I. Creating a new subscription
//      A. If package is not installed, creating a subscription should install latest version
func TestCreateNewSubscription(t *testing.T) {
	c := newKubeClient(t)
	require.NoError(t, initCatalog(t, c))

	cleanup := createSubscription(t, c, betaChannel)
	defer cleanup()

	csv, err := checkForCSV(t, c, beta)
	require.NoError(t, err)
	require.NotNil(t, csv)

	subscription, err := fetchSubscription(t, c, testSubscriptionName)
	require.NoError(t, err)
	require.NotNil(t, subscription)
}

//   I. Creating a new subscription
//      B. If package is already installed, creating a subscription should upgrade it to the latest
//         version
func TestCreateNewSubscriptionAgain(t *testing.T) {
	c := newKubeClient(t)

	require.NoError(t, initCatalog(t, c))

	csvCleanup, err := createCSV(c, stableCSV)
	require.NoError(t, err)
	defer csvCleanup()

	subscriptionCleanup := createSubscription(t, c, alphaChannel)
	defer subscriptionCleanup()

	csv, err := checkForCSV(t, c, alpha)
	require.NoError(t, err)
	require.NotNil(t, csv)

	subscription, err := fetchSubscription(t, c, testSubscriptionName)
	require.NoError(t, err)
	require.NotNil(t, subscription)
}
