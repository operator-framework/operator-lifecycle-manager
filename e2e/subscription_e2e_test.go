package e2e

import (
	"encoding/json"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/ghodss/yaml"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/coreos-inc/alm/pkg/apis"
	catalogsourcev1alpha1 "github.com/coreos-inc/alm/pkg/apis/catalogsource/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	subscriptionv1alpha1 "github.com/coreos-inc/alm/pkg/apis/subscription/v1alpha1"
	uiv1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
	"github.com/coreos-inc/alm/pkg/install"
	"github.com/coreos-inc/alm/pkg/registry"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
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

const (
	catalogSourceName    = "mock-ocs"
	catalogConfigMap     = "mock-ocs"
	testSubscriptionName = "mysubscription"
	testPackageName      = "myapp"
	alphaChannel         = "alpha"
	betaChannel          = "beta"
	alphaLatest          = "myapp-v1.1"
	betaLatest           = "myapp-v0.3"
)

var (
	packageVersions = []string{"myapp-v0.1", "myapp-v0.2", betaLatest, alphaLatest}
)

func initCatalog(t *testing.T, c opClient.Interface) {
	manifests := []uiv1alpha1.PackageManifest{uiv1alpha1.PackageManifest{
		PackageName: testPackageName,
		Channels: []uiv1alpha1.PackageChannel{
			uiv1alpha1.PackageChannel{Name: alphaChannel, CurrentCSVName: alphaLatest},
			uiv1alpha1.PackageChannel{Name: betaChannel, CurrentCSVName: betaLatest},
		},
		DefaultChannelName: betaChannel,
	}}
	raw, err := yaml.Marshal(manifests)
	require.NoError(t, err)
	manifestStr := string(raw)
	strategyNew := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				// Same name
				Name: "dep1",
				// Different spec
				Spec: newNginxDeployment(),
			},
		},
	}
	csvList := []csvv1alpha1.ClusterServiceVersion{}
	lastVersion := ""
	for _, nextVersion := range packageVersions {
		strategyNewRaw, err := json.Marshal(strategyNew)
		require.NoError(t, err)

		c := csvv1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       csvv1alpha1.ClusterServiceVersionKind,
				APIVersion: csvv1alpha1.GroupVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: nextVersion,
			},
			Spec: csvv1alpha1.ClusterServiceVersionSpec{
				Replaces: lastVersion,
				Version:  *semver.New("0.0.0"),
				InstallStrategy: csvv1alpha1.NamedInstallStrategy{
					StrategyName:    install.InstallStrategyNameDeployment,
					StrategySpecRaw: strategyNewRaw,
				},
			},
		}
		lastVersion = nextVersion
		csvList = append(csvList, c)
	}
	rawcsvs, err := yaml.Marshal(csvList)
	require.NoError(t, err)
	csvStr := string(rawcsvs)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      catalogConfigMap,
			Namespace: testNamespace,
		},
		Data: map[string]string{
			registry.ConfigMapPackageName: manifestStr,
			registry.ConfigMapCSVName:     csvStr,
		},
	}
	_, err = c.CreateConfigMap(testNamespace, configMap)
	require.NoError(t, err)

	cs := catalogsourcev1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       catalogsourcev1alpha1.CatalogSourceKind,
			APIVersion: catalogsourcev1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      catalogSourceName,
		},
		Spec: catalogsourcev1alpha1.CatalogSourceSpec{
			Name:      catalogSourceName,
			ConfigMap: catalogConfigMap,
		},
	}
	unstructuredConverter := conversion.NewConverter(true)
	csUnst, err := unstructuredConverter.ToUnstructured(&cs)
	require.NoError(t, err)
	require.NoError(t, c.CreateCustomResource(&unstructured.Unstructured{Object: csUnst}))
}

func createSubscription(t *testing.T, c opClient.Interface, channel string) *subscriptionv1alpha1.Subscription {
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

	unstructuredConverter := conversion.NewConverter(true)
	ipUnst, err := unstructuredConverter.ToUnstructured(sub)
	require.NoError(t, err)
	require.NoError(t, c.CreateCustomResource(&unstructured.Unstructured{Object: ipUnst}))
	return sub
}
func fetchSubscription(t *testing.T, c opClient.Interface, name string) *subscriptionv1alpha1.Subscription {
	var fetched *subscriptionv1alpha1.Subscription
	var err error

	unstructuredConverter := conversion.NewConverter(true)
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Logf("polling for subscription %s...", name)
		fetchedUnst, err := c.GetCustomResource(apis.GroupName, subscriptionv1alpha1.GroupVersion,
			testNamespace, subscriptionv1alpha1.SubscriptionKind, name)
		if err != nil {
			return false, err
		}
		err = unstructuredConverter.FromUnstructured(fetchedUnst.Object, &fetched)
		if err != nil {
			return false, err
		}
		t.Logf("Subscription fetched (%s): %#v", name, fetched)
		return true, nil
	})
	require.NoError(t, err)
	return fetched
}
func checkForCSV(t *testing.T, c opClient.Interface, name string) (*csvv1alpha1.ClusterServiceVersion, error) {
	var fetched *csvv1alpha1.ClusterServiceVersion
	var err error

	unstructuredConverter := conversion.NewConverter(true)
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedInstallPlanUnst, err := c.GetCustomResource(apis.GroupName, csvv1alpha1.GroupVersion,
			testNamespace, csvv1alpha1.ClusterServiceVersionKind, name)
		if err != nil {
			t.Logf("FETCH CSV (%s) ERROR: %v", name, err)
			return false, nil
		}

		err = unstructuredConverter.FromUnstructured(fetchedInstallPlanUnst.Object, &fetched)
		require.NoError(t, err)
		t.Logf("%s (%s): %s", fetched.Status.Phase, fetched.Status.Reason, fetched.Status.Message)
		return true, nil
	})
	return fetched, err
}

func TestCreateNewSubscription(t *testing.T) {
	c := newKubeClient(t)
	initCatalog(t, c)

	s := createSubscription(t, c, alphaChannel)
	require.NotNil(t, s)
	csv, err := checkForCSV(t, c, alphaLatest)
	require.NoError(t, err)
	require.NotNil(t, csv)
	s2 := fetchSubscription(t, c, testSubscriptionName)
	require.NotNil(t, s2)
}
