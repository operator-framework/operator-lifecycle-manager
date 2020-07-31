package e2e

import (
	"context"
	"encoding/json"
	"fmt"

	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/testdata/vpa"
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
		TearDown(testNamespace)
	})

	When("a bundle with a pdb, priorityclass, and VPA object is installed", func() {
		const (
			packageName = "busybox"
			channelName = "alpha"
			subName     = "test-subscription"
		)
		var vpaCRD unstructured.Unstructured

		BeforeEach(func() {
			By("first installing the VPA CRD on cluster")
			const (
				sourceName = "test-catalog"
				imageName  = "quay.io/olmtest/single-bundle-index:pdb"
			)

			// create VPA CRD on cluster
			y, err := vpa.Asset("test/e2e/testdata/vpa/crd.yaml")
			Expect(err).ToNot(HaveOccurred(), "could not read vpa bindata")

			data, err := yaml.YAMLToJSON(y)
			Expect(err).ToNot(HaveOccurred(), "could not convert vpa crd to json")

			err = json.Unmarshal(data, &vpaCRD)
			Expect(err).ToNot(HaveOccurred(), "could not convert vpa crd to unstructured")

			Eventually(func() error {
				err := ctx.Ctx().Client().Create(context.TODO(), &vpaCRD)
				if err != nil {
					if !k8serrors.IsAlreadyExists(err) {
						return err
					}
				}
				return nil
			}).Should(Succeed())

			// ensure vpa crd is established and accepted on the cluster before continuing
			Eventually(func() (bool, error) {
				crd, err := kubeClient.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), vpaCRD.GetName(), metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				return crdReady(&crd.Status), nil
			}).Should(BeTrue())

			var installPlanRef string
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

			Eventually(func() error {
				source, err = operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.TODO(), source, metav1.CreateOptions{})
				return err
			}).Should(Succeed())

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			ctx.Ctx().Logf("install plan %s completed", installPlanRef)
		})

		It("should create the additional bundle objects", func() {
			const (
				vpaGroup          = "autoscaling.k8s.io"
				vpaVersion        = "v1"
				vpaResource       = "verticalpodautoscalers"
				pdbName           = "busybox-pdb"
				priorityClassName = "super-priority"
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

		AfterEach(func() {
			By("Deleting the VPA CRD")
			Eventually(func() error {
				err := ctx.Ctx().Client().Delete(context.TODO(), &vpaCRD)
				if k8serrors.IsNotFound(err) {
					return nil
				}
				return err
			}).Should(Succeed())
		})
	})

	When("A bundle is installed with a CR that is associated with a provided CRD", func() {
		By("including the VPA CRD in the CSV")
		const (
			packageName = "busybox"
			channelName = "alpha"
			subName     = "test-subscription"
			vpaName     = "busybox-vpa"
			vpaGroup    = "autoscaling.k8s.io"
			vpaVersion  = "v1"
			vpaResource = "verticalpodautoscalers"
		)

		BeforeEach(func() {
			const (
				sourceName = "test-catalog"
				imageName  = "quay.io/olmtest/single-bundle-index:vpa-race"
			)
			var installPlanRef string
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
			sub, err := fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			ctx.Ctx().Logf("install plan %s completed", installPlanRef)
		})

		It("should create the additional bundle objects in the correct order: by first installing the CRD then the CR", func() {
			var vpaGVR = schema.GroupVersionResource{
				Group:    vpaGroup,
				Version:  vpaVersion,
				Resource: vpaResource,
			}

			// confirm extra bundle objects are installed
			Eventually(func() error {
				_, err := dynamicClient.Resource(vpaGVR).Namespace(testNamespace).Get(context.TODO(), vpaName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error finding vpa object associated with csv")
		})

		AfterEach(func() {
			By("Deleting the VPA CRD")
			vpaCRDName := fmt.Sprint(vpaResource, ".", vpaGroup)
			Eventually(func() error {
				err := ctx.Ctx().KubeClient().ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Delete(context.TODO(), vpaCRDName, metav1.DeleteOptions{})
				if k8serrors.IsNotFound(err) {
					return nil
				}
				return err
			}).Should(Succeed())
		})
	})

	When("A bundle is installed with a CR that has no associated CRD", func() {
		By("the API not becoming available on the api-server")
		const (
			packageName = "busybox"
			channelName = "alpha"
			subName     = "test-subscription"
		)
		var installPlanRef string

		BeforeEach(func() {
			const (
				sourceName = "test-catalog"
				imageName  = "quay.io/olmtest/single-bundle-index:missing-crd"
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
			sub, err := fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")
			installPlanRef = sub.Status.InstallPlanRef.Name

		})

		It("should fail the installplan after the timeout is exceeded", func() {
			// Wait for the installplan to fail (2 minute timeout)
			Eventually(func() bool {
				ip, err := fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete, v1alpha1.InstallPlanPhaseFailed))
				Expect(err).ToNot(HaveOccurred(), "error fetching failed install plan")
				return ip.Status.Phase == v1alpha1.InstallPlanPhaseFailed
			}).Should(BeTrue())
			ctx.Ctx().Logf("install plan %s failed", installPlanRef)
		})
	})
})

func crdReady(status *apiextensionsv1beta1.CustomResourceDefinitionStatus) bool {
	if status == nil {
		return false
	}
	established, namesAccepted := false, false
	for _, cdt := range status.Conditions {
		switch cdt.Type {
		case apiextensionsv1beta1.Established:
			if cdt.Status == apiextensionsv1beta1.ConditionTrue {
				established = true
			}
		case apiextensionsv1beta1.NamesAccepted:
			if cdt.Status == apiextensionsv1beta1.ConditionTrue {
				namesAccepted = true
			}
		}
	}
	return established && namesAccepted
}
