package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	packagev1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators/v1"
	pmversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
)

type packageManifestCheckFunc func(*packagev1.PackageManifest) bool

func packageManifestHasStatus(pm *packagev1.PackageManifest) bool {
	// as long as it has a package name we consider the status non-empty
	if pm == nil || pm.Status.PackageName == "" {
		return false
	}

	return true
}

func fetchPackageManifest(t *testing.T, pmc pmversioned.Interface, namespace, name string, check packageManifestCheckFunc) (*packagev1.PackageManifest, error) {
	var fetched *packagev1.PackageManifest
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Logf("Polling...")
		fetched, err = pmc.OperatorsV1().PackageManifests(namespace).Get(name, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return true, err
		}
		return check(fetched), nil
	})

	return fetched, err
}

func TestPackageManifestLoading(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

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
	csv := newCSV(packageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	csv.SetLabels(map[string]string{"projected": "label"})
	csv.Spec.NativeAPIs = []metav1.GroupVersionKind{{Group: "kubenative.io", Version: "v1", Kind: "Native"}}
	csvJSON, _ := json.Marshal(csv)
	csv.Spec.Keywords = []string{"foo", "bar"}
	csv.Spec.Links = []v1alpha1.AppLink{
		{
			Name: "foo",
			URL:  "example.com",
		},
	}
	csv.Spec.Maintainers = []v1alpha1.Maintainer{
		{
			Name:  "foo",
			Email: "example@gmail.com",
		},
	}
	csv.Spec.Maturity = "foo"
	c := newKubeClient(t)
	crc := newCRClient(t)
	pmc := newPMClient(t)

	expectedStatus := packagev1.PackageManifestStatus{
		CatalogSource:          catalogSourceName,
		CatalogSourceNamespace: testNamespace,
		PackageName:            packageName,
		Channels: []packagev1.PackageChannel{
			{
				Name:           stableChannel,
				CurrentCSV:     packageStable,
				CurrentCSVDesc: packagev1.CreateCSVDescription(&csv, string(csvJSON)),
			},
		},
		DefaultChannel: stableChannel,
	}

	// Wait for package-server to be ready
	err := wait.Poll(pollInterval, 1*time.Minute, func() (bool, error) {
		t.Logf("Polling package-server...")
		_, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(metav1.ListOptions{})
		if err == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "package-server not available")

	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	require.NoError(t, err)
	defer cleanupCatalogSource()

	_, err = fetchCatalogSource(t, crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	pm, err := fetchPackageManifest(t, pmc, testNamespace, packageName, packageManifestHasStatus)
	require.NoError(t, err, "error getting package manifest")
	require.NotNil(t, pm)
	require.Equal(t, packageName, pm.GetName())
	require.Equal(t, expectedStatus, pm.Status)
	require.Equal(t, "0.0.0", pm.Status.Channels[0].CurrentCSVDesc.MinKubeVersion)
	require.Equal(t, *dummyImage, pm.Status.Channels[0].CurrentCSVDesc.RelatedImages[0])
	require.Equal(t, csv.Spec.NativeAPIs, pm.Status.Channels[0].CurrentCSVDesc.NativeAPIs)
	require.Equal(t, "label", pm.GetLabels()["projected"])
	require.Equal(t, "supported", pm.GetLabels()["operatorframework.io/arch.amd64"])
	require.Equal(t, "supported", pm.GetLabels()["operatorframework.io/os.linux"])
	require.Equal(t, []string{"foo", "bar"}, pm.Status.Channels[0].CurrentCSVDesc.Keywords)
	require.Equal(t, "foo", pm.Status.Channels[0].CurrentCSVDesc.Maturity)
	require.Equal(t, []packagev1.AppLink{{Name: "foo", URL: "example.com"}}, pm.Status.Channels[0].CurrentCSVDesc.Links)
	require.Equal(t, []packagev1.Maintainer{{Name: "foo", Email: "example@gmail.com"}}, pm.Status.Channels[0].CurrentCSVDesc.Maintainers)

	// Get a PackageManifestList and ensure it has the correct items
	pmList, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(metav1.ListOptions{})
	require.NoError(t, err, "could not access package manifests list meta")
	require.NotNil(t, pmList.ListMeta, "package manifest list metadata empty")
	require.NotNil(t, pmList.Items)
}

func TestPkgManifestsFromCatsrc(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	sourceName := genName("catalog-")
	packageName := "etcd-test"
	displayName := "etcd test catalog"
	image := "quay.io/olmtest/catsrc-update-test:related"
	crc := newCRClient(t)
	pmc := newPMClient(t)

	catalogSource := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sourceName,
			Namespace: testNamespace,
			Labels:    map[string]string{"olm.catalogSource": sourceName},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType:  v1alpha1.SourceTypeGrpc,
			Image:       image,
			DisplayName: displayName,
		},
	}

	expectedRelatedImages := map[string]string{
		"quay.io/coreos/etcd@sha256:3816b6daf9b66d6ced6f0f966314e2d4f894982c6b1493061502f8c2bf86ac84":          "",
		"quay.io/coreos/etcd@sha256:49d3d4a81e0d030d3f689e7167f23e120abf955f7d08dbedf3ea246485acee9f":          "",
		"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2": "",
	}

	catalogSource, err := crc.OperatorsV1alpha1().CatalogSources(catalogSource.GetNamespace()).Create(catalogSource)
	require.NoError(t, err, "error creating Catalog Sources")
	require.NotNil(t, catalogSource)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(catalogSource.GetNamespace()).Delete(catalogSource.GetName(), &metav1.DeleteOptions{}))
	}()

	// Wait for package-server to be ready
	err = wait.Poll(pollInterval, 1*time.Minute, func() (bool, error) {
		t.Logf("Polling package-server...")
		_, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(metav1.ListOptions{})
		if err == nil {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err, "package-server not available")

	_, err = fetchCatalogSource(t, crc, catalogSource.GetName(), testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	pm, err := fetchPackageManifest(t, pmc, testNamespace, packageName, packageManifestHasStatus)
	require.NoError(t, err, "error getting package manifest")
	require.NotNil(t, pm)
	require.Equal(t, packageName, pm.GetName())
	require.Equal(t, displayName, pm.Status.CatalogSourceDisplayName)

	catalogSource, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Get(catalogSource.GetName(), metav1.GetOptions{})
	require.NoError(t, err, "error getting catalogSource")

	displayName = "updated Name"
	catalogSource.Spec.DisplayName = displayName
	catalogSource, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Update(catalogSource)
	require.NoError(t, err, "error updating catalogSource")
	require.Equal(t, displayName, catalogSource.Spec.DisplayName)

	// waiting for the update to complete
	err = wait.Poll(pollInterval, 1*time.Minute, func() (bool, error) {
		t.Logf("Polling package-server...")
		pm, err := fetchPackageManifest(t, pmc, testNamespace, packageName, packageManifestHasStatus)
		if err != nil {
			return false, err
		}
		return pm.Status.CatalogSourceDisplayName == displayName, nil
	})
	require.NoError(t, err, "error package manifest Status.CatalogSourceDisplayName is not updated to catsrc Spec.DisplayName")

	relatedImages := pm.Status.Channels[0].CurrentCSVDesc.RelatedImages
	require.Equal(t, len(expectedRelatedImages), len(relatedImages))

	for _, v := range relatedImages {
		_, ok := expectedRelatedImages[v]
		require.True(t, ok, "Expect this image %s to exist in the related images list\n", v)
	}
}
