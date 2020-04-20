package e2e

import (
	"context"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	crdName       = "test"
	crdNamePlural = "tests"
	crdGroup      = "test.k8s.io"
)

var _ = Describe("CRD APIVersion upgrades", func() {
	It("Handles CRD versioning changes as expected", func() {
		By("CRDs changed APIVersions from v1beta1 to v1 and OLM must support both versions. " +
			"Upgrading from a v1beta1 to a v1 version of the same CRD should be seamless because the client always returns the latest version.")

		c := ctx.Ctx().KubeClient()

		oldv1beta1CRD := &apiextensionsv1beta1.CustomResourceDefinition{
			TypeMeta: metav1.TypeMeta{APIVersion: "apiextensions.k8s.io/v1beta1", Kind: "CustomResourceDefinition"},
			ObjectMeta: metav1.ObjectMeta{
				Name: crdNamePlural + "." + crdGroup,
			},
			Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
				Group:   crdGroup,
				Version: "v1",
				Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
					Singular: crdName,
					Plural:   crdNamePlural,
					Kind: "test",
				},
			},
		}

		// create v1beta1 CRD on server
		oldcrd, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), oldv1beta1CRD, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		By("created CRD")

		// poll for CRD to be ready (using the v1 client)
		Eventually(func() (bool, error) {
			fetchedCRD, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), oldcrd.GetName(), metav1.GetOptions{})
			if err != nil || fetchedCRD == nil {
				return false, err
			}
			return checkCRD(fetchedCRD), nil
		}, 5*time.Minute, 10*time.Second).Should(Equal(true))

		oldCRDConvertedToV1, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), oldcrd.GetName(), metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())

		// confirm the v1 crd as is as expected
		// run ensureCRDV1Versions on results
		newCRD := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdNamePlural + "." + crdGroup,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: crdGroup,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:   "v1",
						Served: true,
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Singular: crdName,
					Plural:   crdNamePlural,
				},
			},
		}

		err = catalog.EnsureCRDVersions(oldCRDConvertedToV1, newCRD)
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() { cleaner.NotifyTestComplete(GinkgoT(), true) }, float64(10))
})

func checkCRD(v1crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, condition := range v1crd.Status.Conditions {
		if condition.Type == apiextensionsv1.Established {
			return true
		}
	}

	return false
}
