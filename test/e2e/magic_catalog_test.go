package e2e

import (
	"context"
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("MagicCatalog", func() {
	var (
		generatedNamespace corev1.Namespace
		c                  client.Client
	)

	BeforeEach(func() {
		c = ctx.Ctx().Client()
		generatedNamespace = SetupGeneratedTestNamespace(genName("magic-catalog-e2e-"))
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
	})

	It("Deploys and Undeploys a File-based Catalog", func() {
		// create dependencies
		const catalogName = "test"
		namespace := generatedNamespace.GetName()
		provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "magiccatalog/fbc_catalog.json"))
		Expect(err).To(BeNil())

		// create and deploy and undeploy the magic catalog
		magicCatalog := NewMagicCatalog(c, namespace, catalogName, provider)

		// deployment blocks until the catalog source has reached a READY status
		Expect(magicCatalog.DeployCatalog(context.Background())).To(BeNil())
		Expect(magicCatalog.UndeployCatalog(context.Background())).To(BeNil())
	})

	When("an existing magic catalog exists", func() {
		var (
			mc          *MagicCatalog
			catalogName string
		)

		BeforeEach(func() {
			provider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "magiccatalog/fbc_initial.yaml"))
			Expect(err).To(BeNil())

			catalogName = genName("mc-e2e-catalog-")

			mc = NewMagicCatalog(c, generatedNamespace.GetName(), catalogName, provider)
			Expect(mc.DeployCatalog(context.Background())).To(BeNil())
		})

		AfterEach(func() {
			Expect(mc.UndeployCatalog(context.Background())).To(BeNil())
		})

		It("should succeed when the magic catalog is updated", func() {
			updatedProvider, err := NewFileBasedFiledBasedCatalogProvider(filepath.Join(testdataDir, "magiccatalog/fbc_updated.yaml"))
			Expect(err).To(BeNil())

			updatedMC := NewMagicCatalog(c, generatedNamespace.GetName(), catalogName, updatedProvider)
			Expect(updatedMC.UpdateCatalog(context.Background(), updatedProvider)).To(BeNil())

			Eventually(func() (*corev1.ConfigMap, error) {
				cm := &corev1.ConfigMap{}
				err := c.Get(context.Background(), types.NamespacedName{
					Name:      fmt.Sprintf("%s-configmap", catalogName),
					Namespace: generatedNamespace.GetName(),
				}, cm)
				if err != nil {
					return nil, err
				}
				return cm, nil
			}).Should(And(
				Not(BeNil()),
				WithTransform(func(c *corev1.ConfigMap) string {
					data, ok := c.Data["catalog.json"]
					if !ok {
						return ""
					}
					return data
				}, ContainSubstring(`---
schema: olm.package
name: test-package
defaultChannel: stable
---
schema: olm.channel
package: test-package
name: stable
entries:
  - name: busybox.v2.0.0
    replaces: busybox.v1.0.0
---
schema: olm.bundle
name: busybox.v2.0.0
package: test-package
image: quay.io/olmtest/busybox-bundle:2.0.0
properties:
  - type: olm.gvk
    value:
      group: example.com
      kind: TestA
      version: v1alpha1
  - type: olm.package
    value:
      packageName: test-package
      version: 1.0.0
`)),
			))
		})
		It("should fail when the magic catalog is re-created", func() {
			Expect(mc.DeployCatalog(context.Background())).ToNot(BeNil())
		})
	})
})
