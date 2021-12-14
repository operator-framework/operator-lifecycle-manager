package e2e

import (
	"context"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
)

const catalogSourceLabel = "olm.catalogSource"

var _ = By

var _ = Describe("CatalogSource Grpc Pod Config", func() {
	var (
		generatedNamespace corev1.Namespace
	)

	BeforeEach(func() {
		generatedNamespace = SetupGeneratedTestNamespace(genName("catsrc-grpc-pod-config-e2e-"))
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
	})

	When("the user wants more control over where the grpc catalog source pod gets scheduled", func() {
		var (
			client              k8scontrollerclient.Client
			catalogSource       *v1alpha1.CatalogSource
			defaultNodeSelector = map[string]string{
				"kubernetes.io/os": "linux",
			}
			defaultTolerations       []corev1.Toleration = nil
			catalogSourceName                            = "test-catsrc"
			defaultPriorityClassName                     = ""
		)

		BeforeEach(func() {
			client = ctx.Ctx().Client()

			// must be a grpc source type with spec.image defined
			catalogSource = &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      catalogSourceName,
					Namespace: generatedNamespace.GetName(),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
				},
			}
		})

		AfterEach(func() {
			// assume the catalog source was created and just delete it
			_ = client.Delete(context.TODO(), catalogSource)

			// wait for it to go away
			Expect(waitForDelete(func() error {
				return client.Get(context.TODO(), k8scontrollerclient.ObjectKey{
					Name:      catalogSource.GetName(),
					Namespace: catalogSource.GetNamespace(),
				}, &v1alpha1.CatalogSource{})
			})).To(BeNil())
		})

		It("should override the pod's spec.priorityClassName", func() {
			var overridenPriorityClassName = "system-node-critical"

			// create catalog source
			catalogSource.Spec.GrpcPodConfig = &v1alpha1.GrpcPodConfig{
				PriorityClassName: &overridenPriorityClassName,
			}
			mustCreateCatalogSource(client, catalogSource)

			// Check overrides are present in the spec
			catalogSourcePod := mustGetCatalogSourcePod(client, catalogSource)
			Expect(catalogSourcePod).ToNot(BeNil())
			Expect(catalogSourcePod.Spec.NodeSelector).To(BeEquivalentTo(defaultNodeSelector))
			Expect(catalogSourcePod.Spec.Tolerations).To(ContainElements(defaultTolerations))
			Expect(catalogSourcePod.Spec.PriorityClassName).To(Equal(overridenPriorityClassName))
		})

		It("should override the pod's spec.priorityClassName when it is empty", func() {
			var overridenPriorityClassName = ""

			// create catalog source
			catalogSource.Spec.GrpcPodConfig = &v1alpha1.GrpcPodConfig{
				PriorityClassName: &overridenPriorityClassName,
			}
			mustCreateCatalogSource(client, catalogSource)

			// Check overrides are present in the spec
			catalogSourcePod := mustGetCatalogSourcePod(client, catalogSource)
			Expect(catalogSourcePod).ToNot(BeNil())
			Expect(catalogSourcePod.Spec.NodeSelector).To(BeEquivalentTo(defaultNodeSelector))
			Expect(catalogSourcePod.Spec.Tolerations).To(ContainElements(defaultTolerations))
			Expect(catalogSourcePod.Spec.PriorityClassName).To(Equal(overridenPriorityClassName))
		})

		It("should override the pod's spec.nodeSelector", func() {
			var overridenNodeSelector = map[string]string{
				"kubernetes.io/os": "linux",
				"some":             "tag",
			}

			// create catalog source
			catalogSource.Spec.GrpcPodConfig = &v1alpha1.GrpcPodConfig{
				NodeSelector: overridenNodeSelector,
			}
			mustCreateCatalogSource(client, catalogSource)

			// Check overrides are present in the spec
			catalogSourcePod := mustGetCatalogSourcePod(client, catalogSource)
			Expect(catalogSourcePod).ToNot(BeNil())
			Expect(catalogSourcePod.Spec.NodeSelector).To(BeEquivalentTo(overridenNodeSelector))
			Expect(catalogSourcePod.Spec.Tolerations).To(ContainElements(defaultTolerations))
			Expect(catalogSourcePod.Spec.PriorityClassName).To(Equal(defaultPriorityClassName))
		})

		It("should override the pod's spec.tolerations", func() {
			var tolerationSeconds int64 = 120
			var overriddenTolerations = []corev1.Toleration{
				{
					Key:               "some/key",
					Operator:          corev1.TolerationOpExists,
					Effect:            corev1.TaintEffectNoExecute,
					TolerationSeconds: &tolerationSeconds,
				},
				{
					Key:      "someother/key",
					Operator: corev1.TolerationOpEqual,
					Effect:   corev1.TaintEffectNoSchedule,
				},
			}

			// create catalog source
			catalogSource.Spec.GrpcPodConfig = &v1alpha1.GrpcPodConfig{
				Tolerations: overriddenTolerations,
			}
			mustCreateCatalogSource(client, catalogSource)

			// Check overrides are present in the spec
			catalogSourcePod := mustGetCatalogSourcePod(client, catalogSource)
			Expect(catalogSourcePod).ToNot(BeNil())
			Expect(catalogSourcePod.Spec.NodeSelector).To(BeEquivalentTo(defaultNodeSelector))
			Expect(catalogSourcePod.Spec.Tolerations).To(ContainElements(overriddenTolerations))
			Expect(catalogSourcePod.Spec.PriorityClassName).To(Equal(defaultPriorityClassName))
		})
	})
})

func mustGetCatalogSourcePod(client k8scontrollerclient.Client, catalogSource *v1alpha1.CatalogSource) *corev1.Pod {
	var podList = corev1.PodList{}

	var opts = []k8scontrollerclient.ListOption{
		k8scontrollerclient.InNamespace(catalogSource.GetNamespace()),
		// NOTE: this will fail if we stop setting the label on the catalog source pod
		k8scontrollerclient.MatchingLabels{
			catalogSourceLabel: catalogSource.GetName(),
		},
	}

	// Try to get a pod until its found and there's only one of them
	Eventually(func() error {
		if err := client.List(context.TODO(), &podList, opts...); err != nil {
			return err
		}
		if len(podList.Items) != 1 {
			return errors.New(fmt.Sprintf("expecting one catalog source pod but found %d", len(podList.Items)))
		}
		return nil
	}).Should(BeNil())

	return &podList.Items[0]
}

func mustCreateCatalogSource(client k8scontrollerclient.Client, catalogSource *v1alpha1.CatalogSource) {
	// create the object
	Expect(client.Create(context.TODO(), catalogSource)).To(BeNil())

	// wait for object to be appear
	Eventually(func() error {
		return client.Get(context.TODO(), k8scontrollerclient.ObjectKey{
			Name:      catalogSource.Name,
			Namespace: catalogSource.GetNamespace(),
		}, &v1alpha1.CatalogSource{})
	}).Should(BeNil())
}
