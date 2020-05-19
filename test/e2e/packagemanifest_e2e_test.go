package e2e

import (
	"context"
	"encoding/json"
	"github.com/blang/semver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	packagev1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators/v1"
	pmversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Package Manifest API lists available Operators from Catalog Sources", func() {

	var (
		crc versioned.Interface
		pmc pmversioned.Interface
		c   operatorclient.ClientInterface
	)
	BeforeEach(func() {
		crc = newCRClient()
		pmc = newPMClient()
		c = newKubeClient()
	})

	AfterEach(func() {
		cleaner.NotifyTestComplete(true)
	})

	Context("Given a CatalogSource created using the ConfigMap as catalog source type", func() {

		var (
			packageName          string
			packageStable        string
			cleanupCatalogSource cleanupFunc
			csvJSON              []byte
			csv                  v1alpha1.ClusterServiceVersion
			catsrcName           string
		)
		BeforeEach(func() {

			// create a simple catalogsource
			packageName = genName("nginx")
			stableChannel := "stable"
			packageStable = packageName + "-stable"
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
			catsrcName = genName("mock-ocs")
			namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
			csv = newCSV(packageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
			csv.SetLabels(map[string]string{"projected": "label"})
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
			csv.Spec.NativeAPIs = []metav1.GroupVersionKind{{Group: "kubenative.io", Version: "v1", Kind: "Native"}}

			var err error
			csvJSON, err = json.Marshal(csv)
			Expect(err).ToNot(HaveOccurred())

			_, cleanupCatalogSource = createInternalCatalogSource(c, crc, catsrcName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})

			// Verify catalog source was created
			_, err = fetchCatalogSourceOnStatus(crc, catsrcName, testNamespace, catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			if cleanupCatalogSource != nil {
				cleanupCatalogSource()
			}
		})

		It("retrieves the PackageManifest by package name and validates its fields", func() {

			expectedStatus := packagev1.PackageManifestStatus{
				CatalogSource:          catsrcName,
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

			pm, err := fetchPackageManifest(pmc, testNamespace, packageName, packageManifestHasStatus)
			Expect(err).ToNot(HaveOccurred(), "error getting package manifest")
			Expect(pm).ShouldNot(BeNil())
			Expect(pm.GetName()).Should(Equal(packageName))
			Expect(pm.Status).Should(Equal(expectedStatus))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.MinKubeVersion).Should(Equal("0.0.0"))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.RelatedImages[0]).Should(Equal(*dummyImage))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.NativeAPIs).Should(Equal(csv.Spec.NativeAPIs))
			Expect(pm.GetLabels()["projected"]).Should(Equal("label"))
			Expect(pm.GetLabels()["operatorframework.io/arch.amd64"]).Should(Equal("supported"))
			Expect(pm.GetLabels()["operatorframework.io/os.linux"]).Should(Equal("supported"))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.Keywords).Should(Equal([]string{"foo", "bar"}))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.Maturity).Should(Equal("foo"))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.Links).Should(Equal([]packagev1.AppLink{{Name: "foo", URL: "example.com"}}))
			Expect(pm.Status.Channels[0].CurrentCSVDesc.Maintainers).Should(Equal([]packagev1.Maintainer{{Name: "foo", Email: "example@gmail.com"}}))
		})
		It("lists PackageManifest and ensures it has valid PackageManifest item", func() {
			// Get a PackageManifestList and ensure it has the correct items
			Eventually(func() (bool, error) {
				pmList, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(context.TODO(), metav1.ListOptions{})
				return containsPackageManifest(pmList.Items, packageName), err
			}).Should(BeTrue(), "required package name not found in the list")
		})

	})

	Context("Given a CatalogSource created using gRPC catalog source type", func() {
		var (
			packageName   string
			catalogSource *v1alpha1.CatalogSource
		)
		BeforeEach(func() {
			sourceName := genName("catalog-")
			packageName = "etcd-test"
			image := "quay.io/olmtest/catsrc-update-test:related"

			catalogSource = &v1alpha1.CatalogSource{
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

			var err error
			catalogSource, err = crc.OperatorsV1alpha1().CatalogSources(catalogSource.GetNamespace()).Create(context.TODO(), catalogSource, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			err := crc.OperatorsV1alpha1().CatalogSources(catalogSource.GetNamespace()).Delete(context.TODO(), catalogSource.GetName(), metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("lists the CatalogSource contents using the PackageManifest API", func() {

			pm, err := fetchPackageManifest(pmc, testNamespace, packageName, packageManifestHasStatus)
			Expect(err).NotTo(HaveOccurred(), "error getting package manifest")
			Expect(pm).ShouldNot(BeNil())
			Expect(pm.GetName()).Should(Equal(packageName))

			// Verify related images from the package manifest
			relatedImages := pm.Status.Channels[0].CurrentCSVDesc.RelatedImages

			Expect(relatedImages).To(ConsistOf([]string{
				"quay.io/coreos/etcd@sha256:3816b6daf9b66d6ced6f0f966314e2d4f894982c6b1493061502f8c2bf86ac84",
				"quay.io/coreos/etcd@sha256:49d3d4a81e0d030d3f689e7167f23e120abf955f7d08dbedf3ea246485acee9f",
				"quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2",
			}), "Expected images to exist in the related images list\n")
		})
	})
})

type packageManifestCheckFunc func(*packagev1.PackageManifest) bool

func packageManifestHasStatus(pm *packagev1.PackageManifest) bool {
	// as long as it has a package name we consider the status non-empty
	return pm != nil && pm.Status.PackageName != ""

}

func fetchPackageManifest(pmc pmversioned.Interface, namespace, name string, check packageManifestCheckFunc) (*packagev1.PackageManifest, error) {
	var fetched *packagev1.PackageManifest
	var err error

	Eventually(func() (bool, error) {
		ctx.Ctx().Logf("Polling...")
		fetched, err = pmc.OperatorsV1().PackageManifests(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return true, err
		}
		return check(fetched), nil
	}).Should(BeTrue())

	return fetched, err
}

func containsPackageManifest(pmList []packagev1.PackageManifest, pkgName string) bool {
	for _, pm := range pmList {
		if pm.GetName() == pkgName {
			return true
		}
	}
	return false
}
