package e2e

import (
	"testing"
	"time"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	packagev1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
	pmversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
)

type packageManifestCheckFunc func(*packagev1alpha1.PackageManifest) bool

func packageManifestHasStatus(pm *packagev1alpha1.PackageManifest) bool {
	// as long as it has a package name we consider the status non-empty
	if pm == nil || pm.Status.PackageName == "" {
		return false
	}

	return true
}

func fetchPackageManifest(t *testing.T, pmc pmversioned.Interface, namespace, name string, check packageManifestCheckFunc) (*packagev1alpha1.PackageManifest, error) {
	var fetched *packagev1alpha1.PackageManifest
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Logf("Polling...")
		fetched, err = pmc.PackagemanifestV1alpha1().PackageManifests(namespace).Get(name, metav1.GetOptions{})
		if err != nil {
			return true, err
		}
		return check(fetched), nil
	})

	return fetched, err
}

func TestPackageManifestLoading(t *testing.T) {
	// create a simple catalogsource
	packageName := genName("nginx")
	stableChannel := "stable"
	packageStable := packageName + "-stable"
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: packageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	crdPlural := genName("ins")
	crd := newCRD(crdPlural)
	catalogSourceName := genName("mock-ocs")
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csv := newCSV(packageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)
	pmc := newPMClient(t)

	expectedStatus := packagev1alpha1.PackageManifestStatus{
		CatalogSource:          catalogSourceName,
		CatalogSourceNamespace: testNamespace,
		PackageName:            packageName,
		Channels: []packagev1alpha1.PackageChannel{
			{
				Name:           stableChannel,
				CurrentCSV:     packageStable,
				CurrentCSVDesc: packagev1alpha1.CreateCSVDescription(&csv),
			},
		},
		DefaultChannel: stableChannel,
	}

	// Wait for package-server to be ready
	err := wait.Poll(pollInterval, 1*time.Minute, func() (bool, error) {
		t.Logf("Polling package-server...")
		_, err := pmc.PackagemanifestV1alpha1().PackageManifests(testNamespace).List(metav1.ListOptions{})
		if err == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "package-server not available")

	watcher, err := pmc.PackagemanifestV1alpha1().PackageManifests(testNamespace).Watch(metav1.ListOptions{})
	require.NoError(t, err)
	defer watcher.Stop()

	receivedPackage := make(chan bool)
	go func() {
		event := <-watcher.ResultChan()
		pkg := event.Object.(*packagev1alpha1.PackageManifest)

		require.Equal(t, watch.Added, event.Type)
		require.NotNil(t, pkg)
		require.Equal(t, packageName, pkg.GetName())
		require.Equal(t, expectedStatus, pkg.Status)
		receivedPackage <- true
		return
	}()

	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	require.NoError(t, err)
	defer cleanupCatalogSource()

	pm, err := fetchPackageManifest(t, pmc, testNamespace, packageName, packageManifestHasStatus)

	require.True(t, <-receivedPackage)
	require.NoError(t, err, "error getting package manifest")
	require.NotNil(t, pm)
	require.Equal(t, packageName, pm.GetName())
	require.Equal(t, expectedStatus, pm.Status)
}

func TestPackageManifestMultipleWatches(t *testing.T) {
	pmc := newPMClient(t)

	watcherA, _ := pmc.PackagemanifestV1alpha1().PackageManifests(testNamespace).Watch(metav1.ListOptions{})
	watcherB, _ := pmc.PackagemanifestV1alpha1().PackageManifests(testNamespace).Watch(metav1.ListOptions{})
	watcherC, _ := pmc.PackagemanifestV1alpha1().PackageManifests(testNamespace).Watch(metav1.ListOptions{})

	defer watcherB.Stop()
	defer watcherC.Stop()
	watcherA.Stop()

	list, err := pmc.PackagemanifestV1alpha1().PackageManifests(testNamespace).List(metav1.ListOptions{})

	require.NoError(t, err)
	require.NotEqual(t, 0, len(list.Items))
}
