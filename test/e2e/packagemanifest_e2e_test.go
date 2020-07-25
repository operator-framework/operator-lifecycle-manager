package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/blang/semver"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	opver "github.com/operator-framework/api/pkg/lib/version"
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
		TearDown(testNamespace)
	})

	Context("Given a CatalogSource created using the ConfigMap as catalog source type", func() {

		var (
			catsrcName           string
			packageName          string
			packageAlpha         string
			alphaChannel         string
			packageStable        string
			stableChannel        string
			csvAlpha             v1alpha1.ClusterServiceVersion
			csv                  v1alpha1.ClusterServiceVersion
			cleanupCatalogSource cleanupFunc
		)
		BeforeEach(func() {

			// create a simple catalogsource
			packageName = genName("nginx")
			alphaChannel = "alpha"
			packageAlpha = packageName + "-alpha"
			stableChannel = "stable"
			packageStable = packageName + "-stable"
			manifests := []registry.PackageManifest{
				{
					PackageName: packageName,
					Channels: []registry.PackageChannel{
						{Name: alphaChannel, CurrentCSVName: packageAlpha},
						{Name: stableChannel, CurrentCSVName: packageStable},
					},
					DefaultChannelName: stableChannel,
				},
			}

			crdPlural := genName("ins")
			crd := newCRD(crdPlural)
			catsrcName = genName("mock-ocs")
			csv = newCSV(packageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
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
			csv.Spec.Icon = []v1alpha1.Icon{
				{
					Data:      "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",
					MediaType: "image/png",
				},
			}

			csvAlpha = *csv.DeepCopy()
			csvAlpha.SetName(packageAlpha)
			csvAlpha.Spec.Version = opver.OperatorVersion{semver.MustParse("0.1.1")}
			csvAlpha.Spec.Replaces = csv.GetName()
			csvAlpha.Spec.Icon = []v1alpha1.Icon{
				{
					Data:      base64.StdEncoding.EncodeToString([]byte(csvAlpha.GetName())),
					MediaType: "image/png",
				},
			}

			_, cleanupCatalogSource = createInternalCatalogSource(c, crc, catsrcName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv, csvAlpha})

			// Verify catalog source was created
			_, err := fetchCatalogSourceOnStatus(crc, catsrcName, testNamespace, catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			if cleanupCatalogSource != nil {
				cleanupCatalogSource()
			}
		})

		It("retrieves the PackageManifest by package name and validates its fields", func() {
			// Drop icons to account for pruning
			csvAlpha.Spec.Icon = nil
			csv.Spec.Icon = nil

			csvAlphaJSON, err := json.Marshal(csvAlpha)
			Expect(err).ToNot(HaveOccurred())
			csvJSON, err := json.Marshal(csv)
			Expect(err).ToNot(HaveOccurred())

			expectedStatus := packagev1.PackageManifestStatus{
				CatalogSource:          catsrcName,
				CatalogSourceNamespace: testNamespace,
				PackageName:            packageName,
				Channels: []packagev1.PackageChannel{
					{
						Name:           alphaChannel,
						CurrentCSV:     packageAlpha,
						CurrentCSVDesc: packagev1.CreateCSVDescription(&csvAlpha, string(csvAlphaJSON)),
					},
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
			Expect(pm.GetLabels()["projected"]).Should(Equal("label"))
			Expect(pm.GetLabels()["operatorframework.io/arch.amd64"]).Should(Equal("supported"))
			Expect(pm.GetLabels()["operatorframework.io/os.linux"]).Should(Equal("supported"))
		})

		It("lists PackageManifest and ensures it has valid PackageManifest item", func() {
			// Get a PackageManifestList and ensure it has the correct items
			Eventually(func() (bool, error) {
				pmList, err := pmc.OperatorsV1().PackageManifests(testNamespace).List(context.TODO(), metav1.ListOptions{})
				return containsPackageManifest(pmList.Items, packageName), err
			}).Should(BeTrue(), "required package name not found in the list")
		})

		It("gets the icon from the default channel", func() {
			var res rest.Result
			Eventually(func() error {
				res = pmc.OperatorsV1().RESTClient().Get().Resource("packagemanifests").SubResource("icon").Namespace(testNamespace).Name(packageName).Do(context.Background())
				return res.Error()
			}).Should(Succeed(), "error getting icon")

			data, err := res.Raw()
			Expect(err).ToNot(HaveOccurred())

			// Match against icon from the default
			expected, err := base64.StdEncoding.DecodeString(csv.Spec.Icon[0].Data)
			Expect(err).ToNot(HaveOccurred())
			Expect(data).To(Equal(expected))
		})
	})

	Context("Given a CatalogSource created using gRPC catalog source type", func() {
		var (
			packageName, displayName string
			catalogSource            *v1alpha1.CatalogSource
		)
		BeforeEach(func() {
			sourceName := genName("catalog-")
			packageName = "etcd-test"
			displayName = "etcd test catalog"
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
					SourceType:  v1alpha1.SourceTypeGrpc,
					Image:       image,
					DisplayName: displayName,
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

		When("the display name for catalog source is updated", func() {

			BeforeEach(func() {

				pm, err := fetchPackageManifest(pmc, testNamespace, packageName, packageManifestHasStatus)
				Expect(err).NotTo(HaveOccurred(), "error getting package manifest")
				Expect(pm).ShouldNot(BeNil())
				Expect(pm.GetName()).Should(Equal(packageName))
				Expect(pm.Status.CatalogSourceDisplayName).Should(Equal(displayName))

				catalogSource, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Get(context.TODO(), catalogSource.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "error getting catalogSource")

				displayName = "updated Name"
				catalogSource.Spec.DisplayName = displayName
				catalogSource, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Update(context.TODO(), catalogSource, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred(), "error updating catalogSource")
				Expect(catalogSource.Spec.DisplayName).Should(Equal(displayName))
			})
			It("should successfully update the CatalogSource field", func() {

				Eventually(func() (string, error) {
					pm, err := fetchPackageManifest(pmc, testNamespace, packageName,
						packageManifestHasStatus)
					if err != nil {
						return "", err
					}
					return pm.Status.CatalogSourceDisplayName, nil
				}).Should(Equal(displayName))
			})
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

	EventuallyWithOffset(1, func() (bool, error) {
		ctx.Ctx().Logf("Polling...")
		fetched, err = pmc.OperatorsV1().PackageManifests(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
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
