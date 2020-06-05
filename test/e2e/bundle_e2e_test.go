package e2e

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Installing bundles with new object types", func() {
	var (
		kubeClient     operatorclient.ClientInterface
		operatorClient versioned.Interface
		dynamicClient  dynamic.Interface
	)

	BeforeEach(func() {
		kubeClient = ctx.Ctx().KubeClient()
		operatorClient = ctx.Ctx().OperatorClient()
		dynamicClient = ctx.Ctx().DynamicClient()
	})

	AfterEach(func() {
		cleaner.NotifyTestComplete(true)
	})

	When("a bundle with a pdb, priorityclass, and VPA object is installed", func() {
		By("including the VPA CRD in the CSV")
		const (
			packageName = "busybox"
			channelName = "alpha"
			subName     = "test-subscription"
		)

		BeforeEach(func() {
			const (
				sourceName = "test-catalog"
				imageName  = "quay.io/olmtest/single-bundle-index:pdb"
			)
			// create catalog source
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
					Image:      imageName,
				},
			}

			source, err := operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.TODO(), source, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred(), "could not create catalog source")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			Eventually(func() error {
				_, err = fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
				return err
			}).Should(BeNil())
		})

		It("should create the additional bundle objects", func() {
			const (
				vpaGroup          = "autoscaling.k8s.io"
				vpaVersion        = "v1"
				vpaResource       = "verticalpodautoscalers"
				pdbName           = "busybox-pdb"
				priorityClassName = "high-priority"
				vpaName           = "busybox-vpa"
			)

			var resource = schema.GroupVersionResource{
				Group:    vpaGroup,
				Version:  vpaVersion,
				Resource: vpaResource,
			}

			// confirm extra bundle objects are installed
			Eventually(func() error {
				_, err := kubeClient.KubernetesInterface().PolicyV1beta1().PodDisruptionBudgets(testNamespace).Get(context.TODO(), pdbName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error getting pdb object associated with CSV")

			Eventually(func() error {
				_, err := kubeClient.KubernetesInterface().SchedulingV1().PriorityClasses().Get(context.TODO(), priorityClassName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error getting priorityclass object associated with CSV")

			Eventually(func() error {
				_, err := dynamicClient.Resource(resource).Namespace(testNamespace).Get(context.TODO(), vpaName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error finding vpa object associated with csv")
		})
	})
})
