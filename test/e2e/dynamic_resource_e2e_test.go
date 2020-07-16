package e2e

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Subscriptions create required objects from Catalogs", func() {

	var (
		c             operatorclient.ClientInterface
		crc           versioned.Interface
		dynamicClient dynamic.Interface
		deleteOpts    *metav1.DeleteOptions
	)

	BeforeEach(func() {
		c = newKubeClient()
		crc = newCRClient()
		dynamicClient = ctx.Ctx().DynamicClient()

		deleteOpts = &metav1.DeleteOptions{}
	})

	AfterEach(func() {
		TearDown(testNamespace)
	})

	Context("Given a Namespace", func() {
		When("a CatalogSource is created with a bundle that contains prometheus objects", func() {
			Context("creating a subscription using the CatalogSource", func() {

				var (
					ns         *corev1.Namespace
					catsrc     *v1alpha1.CatalogSource
					subName    string
					cleanupSub cleanupFunc
				)

				BeforeEach(func() {

					// Create Namespace
					var err error
					ns, err = c.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: genName("ns-"),
						},
					}, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					// Create CatalogSource
					catsrc = &v1alpha1.CatalogSource{
						ObjectMeta: metav1.ObjectMeta{
							Name:      genName("dynamic-catalog-"),
							Namespace: ns.GetName(),
						},
						Spec: v1alpha1.CatalogSourceSpec{
							Image:      "quay.io/olmtest/catsrc_dynamic_resources:e2e-test",
							SourceType: v1alpha1.SourceTypeGrpc,
						},
					}

					catsrc, err = crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Create(context.TODO(), catsrc, metav1.CreateOptions{})
					Expect(err).NotTo(HaveOccurred())

					// Wait for the CatalogSource to be ready
					_, err = fetchCatalogSourceOnStatus(crc, catsrc.GetName(), catsrc.GetNamespace(), catalogSourceRegistryPodSynced)
					Expect(err).NotTo(HaveOccurred())

					// Generate a Subscription
					subName = genName("dynamic-resources")
					cleanupSub = createSubscriptionForCatalog(crc, catsrc.GetNamespace(), subName, catsrc.GetName(), "etcd", "singlenamespace-alpha", "", v1alpha1.ApprovalAutomatic)

				})

				AfterEach(func() {

					// clean up subscription
					if cleanupSub != nil {
						cleanupSub()
					}

					// Delete CatalogSource
					if catsrc != nil {
						err := crc.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Delete(context.TODO(), catsrc.GetName(), *deleteOpts)
						Expect(err).NotTo(HaveOccurred())
					}

					// Delete Namespace
					if ns != nil {
						err := c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), ns.GetName(), *deleteOpts)
						Expect(err).NotTo(HaveOccurred())
					}

				})

				It("should install the operator successfully", func() {
					Skip("this test disabled pending fix of the v1 CRD feature")
					_, err := fetchSubscription(crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should have created the expected prometheus objects", func() {
					Skip("this test disabled pending fix of the v1 CRD feature")
					sub, err := fetchSubscription(crc, catsrc.GetNamespace(), subName, subscriptionHasInstallPlanChecker)
					Expect(err).NotTo(HaveOccurred())

					ipName := sub.Status.InstallPlanRef.Name
					ip, err := waitForInstallPlan(crc, ipName, sub.GetNamespace(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseFailed, v1alpha1.InstallPlanPhaseComplete))
					Expect(err).NotTo(HaveOccurred())

					// Ensure the InstallPlan contains the steps resolved from the bundle image
					expectedSteps := map[registry.ResourceKey]struct{}{
						{Name: "my-prometheusrule", Kind: "PrometheusRule"}: {},
						{Name: "my-servicemonitor", Kind: "ServiceMonitor"}: {},
					}

					// Verify Resource steps match expected steps
					for _, step := range ip.Status.Plan {
						key := registry.ResourceKey{
							Name: step.Resource.Name,
							Kind: step.Resource.Kind,
						}
						for expected := range expectedSteps {
							if strings.HasPrefix(key.Name, expected.Name) && key.Kind == expected.Kind {
								delete(expectedSteps, expected)
							}
						}
					}
					Expect(len(expectedSteps)).To(BeZero(), "Resource steps do not match expected: %#v", expectedSteps)

					// Confirm that the expected types exist
					gvr := schema.GroupVersionResource{
						Group:    "monitoring.coreos.com",
						Version:  "v1",
						Resource: "prometheusrules",
					}

					err = waitForGVR(dynamicClient, gvr, "my-prometheusrule", ns.GetName())
					Expect(err).NotTo(HaveOccurred())

					gvr.Resource = "servicemonitors"
					err = waitForGVR(dynamicClient, gvr, "my-servicemonitor", ns.GetName())
					Expect(err).NotTo(HaveOccurred())
				})
			})
		})

	})

})
