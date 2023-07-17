package e2e

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/ghodss/yaml"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

//go:embed testdata/vpa/crd.yaml
var vpaCRDRaw []byte

var _ = Describe("Installing bundles with new object types", func() {
	var (
		kubeClient         operatorclient.ClientInterface
		operatorClient     versioned.Interface
		dynamicClient      dynamic.Interface
		generatedNamespace corev1.Namespace
	)

	BeforeEach(func() {
		kubeClient = ctx.Ctx().KubeClient()
		operatorClient = ctx.Ctx().OperatorClient()
		dynamicClient = ctx.Ctx().DynamicClient()

		By("creating a test namespace")
		generatedNamespace = SetupGeneratedTestNamespace(genName("bundle-e2e-"))
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
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
				imageName  = "quay.io/olmtest/single-bundle-index:pdb-v1"
			)

			// create VPA CRD on cluster
			Expect(vpaCRDRaw).ToNot(BeEmpty(), "could not read vpa bindata")
			data, err := yaml.YAMLToJSON(vpaCRDRaw)
			Expect(err).ToNot(HaveOccurred(), "could not convert vpa crd to json")

			err = json.Unmarshal(data, &vpaCRD)
			Expect(err).ToNot(HaveOccurred(), "could not convert vpa crd to unstructured")

			Eventually(func() error {
				err := ctx.Ctx().Client().Create(context.Background(), &vpaCRD)
				if err != nil {
					if !apierrors.IsAlreadyExists(err) {
						return err
					}
				}
				return nil
			}).Should(Succeed())

			// ensure vpa crd is established and accepted on the cluster before continuing
			Eventually(func() (bool, error) {
				crd, err := kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.Background(), vpaCRD.GetName(), metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				return crdReady(&crd.Status), nil
			}).Should(BeTrue())

			source := &v1alpha1.CatalogSource{
				TypeMeta: metav1.TypeMeta{
					Kind:       v1alpha1.CatalogSourceKind,
					APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      sourceName,
					Namespace: generatedNamespace.GetName(),
					Labels:    map[string]string{"olm.catalogSource": sourceName},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      imageName,
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			}

			Eventually(func() error {
				source, err = operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
				return err
			}).Should(Succeed())

			// Wait for the CatalogSource to be ready
			_, err = fetchCatalogSourceOnStatus(operatorClient, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, generatedNamespace.GetName(), subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef := sub.Status.InstallPlanRef

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlanWithNamespace(GinkgoT(), operatorClient, installPlanRef.Name, installPlanRef.Namespace, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
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
				_, err := kubeClient.KubernetesInterface().SchedulingV1().PriorityClasses().Get(context.Background(), priorityClassName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error getting priorityclass object associated with CSV")

			Eventually(func() error {
				_, err := dynamicClient.Resource(resource).Namespace(generatedNamespace.GetName()).Get(context.Background(), vpaName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error finding vpa object associated with csv")

			Eventually(func() error {
				_, err := kubeClient.KubernetesInterface().PolicyV1().PodDisruptionBudgets(generatedNamespace.GetName()).Get(context.Background(), pdbName, metav1.GetOptions{})
				return err
			}).Should(Succeed(), "expected no error getting pdb object associated with CSV")
		})

		AfterEach(func() {
			By("Deleting the VPA CRD")
			Eventually(func() error {
				err := ctx.Ctx().Client().Delete(context.Background(), &vpaCRD)
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}).Should(Succeed())
		})
	})
})

func crdReady(status *apiextensionsv1.CustomResourceDefinitionStatus) bool {
	if status == nil {
		return false
	}
	established, namesAccepted := false, false
	for _, cdt := range status.Conditions {
		switch cdt.Type {
		case apiextensionsv1.Established:
			if cdt.Status == apiextensionsv1.ConditionTrue {
				established = true
			}
		case apiextensionsv1.NamesAccepted:
			if cdt.Status == apiextensionsv1.ConditionTrue {
				namesAccepted = true
			}
		}
	}
	return established && namesAccepted
}
