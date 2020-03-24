package e2e

import (
	"encoding/json"
	"time"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	. "github.com/onsi/ginkgo"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	packagev1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators/v1"
	pmversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
)

var _ = Describe("Package Manifest", func() {
	It("loading", func() {

		// as long as it has a package name we consider the status non-empty

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

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
		c := newKubeClient(GinkgoT())
		crc := newCRClient(GinkgoT())
		pmc := newPMClient(GinkgoT())

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
			GinkgoT().Logf("Polling package-server...")
			_, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(metav1.ListOptions{})
			if err == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err, "package-server not available")

		_, cleanupCatalogSource := createInternalCatalogSource(GinkgoT(), c, crc, catalogSourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
		require.NoError(GinkgoT(), err)
		defer cleanupCatalogSource()

		_, err = fetchCatalogSource(GinkgoT(), crc, catalogSourceName, testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		pm, err := fetchPackageManifest(GinkgoT(), pmc, testNamespace, packageName, packageManifestHasStatus)
		require.NoError(GinkgoT(), err, "error getting package manifest")
		require.NotNil(GinkgoT(), pm)
		require.Equal(GinkgoT(), packageName, pm.GetName())
		require.Equal(GinkgoT(), expectedStatus, pm.Status)
		require.Equal(GinkgoT(), "0.0.0", pm.Status.Channels[0].CurrentCSVDesc.MinKubeVersion)
		require.Equal(GinkgoT(), *dummyImage, pm.Status.Channels[0].CurrentCSVDesc.RelatedImages[0])
		require.Equal(GinkgoT(), csv.Spec.NativeAPIs, pm.Status.Channels[0].CurrentCSVDesc.NativeAPIs)
		require.Equal(GinkgoT(), "label", pm.GetLabels()["projected"])
		require.Equal(GinkgoT(), "supported", pm.GetLabels()["operatorframework.io/arch.amd64"])
		require.Equal(GinkgoT(), "supported", pm.GetLabels()["operatorframework.io/os.linux"])

		// Get a PackageManifestList and ensure it has the correct items
		pmList, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(metav1.ListOptions{})
		require.NoError(GinkgoT(), err, "could not access package manifests list meta")
		require.NotNil(GinkgoT(), pmList.ListMeta, "package manifest list metadata empty")
		require.NotNil(GinkgoT(), pmList.Items)
	})
	It("loading relatedImages", func() {

		defer cleaner.NotifyTestComplete(GinkgoT(), true)

		sourceName := genName("catalog-")
		packageName := "etcd-test"
		image := "quay.io/olmtest/catsrc-update-test:related"

		crc := newCRClient(GinkgoT())
		pmc := newPMClient(GinkgoT())

		source := &v1alpha1.CatalogSource{
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
				SourceType: v1alpha1.SourceTypeGrpc,
				Image:      image,
			},
		}

		expectedRelatedImages := map[string]string{
			"quay.io/coreos/etcd@sha256:3816b6daf9b66d6ced6f0f966314e2d4f894982c6b1493061502f8c2bf86ac84":          "",
			"quay.io/coreos/etcd@sha256:49d3d4a81e0d030d3f689e7167f23e120abf955f7d08dbedf3ea246485acee9f":          "",
			"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2": "",
		}

		source, err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(source)
		require.NoError(GinkgoT(), err)
		defer func() {
			require.NoError(GinkgoT(), crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Delete(source.GetName(), &metav1.DeleteOptions{}))
		}()

		// Wait for package-server to be ready
		err = wait.Poll(pollInterval, 1*time.Minute, func() (bool, error) {
			GinkgoT().Logf("Polling package-server...")
			_, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(metav1.ListOptions{})
			if err == nil {
				return true, nil
			}
			return false, nil
		})
		require.NoError(GinkgoT(), err, "package-server not available")

		_, err = fetchCatalogSource(GinkgoT(), crc, source.GetName(), testNamespace, catalogSourceRegistryPodSynced)
		require.NoError(GinkgoT(), err)

		pm, err := fetchPackageManifest(GinkgoT(), pmc, testNamespace, packageName, packageManifestHasStatus)
		require.NoError(GinkgoT(), err, "error getting package manifest")
		require.NotNil(GinkgoT(), pm)
		require.Equal(GinkgoT(), packageName, pm.GetName())

		relatedImages := pm.Status.Channels[0].CurrentCSVDesc.RelatedImages
		require.Equal(GinkgoT(), len(expectedRelatedImages), len(relatedImages))

		for _, v := range relatedImages {
			_, ok := expectedRelatedImages[v]
			require.True(GinkgoT(), ok, "Expect this image %s to exist in the related images list\n", v)
		}
	})
})

type packageManifestCheckFunc func(*packagev1.PackageManifest) bool

func packageManifestHasStatus(pm *packagev1.PackageManifest) bool {
	// as long as it has a package name we consider the status non-empty
	return pm != nil && pm.Status.PackageName != ""

}

func fetchPackageManifest(t GinkgoTInterface, pmc pmversioned.Interface, namespace, name string, check packageManifestCheckFunc) (*packagev1.PackageManifest, error) {
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
