package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	operatorsv2alpha1 "github.com/operator-framework/api/pkg/operators/v2alpha1"
	client "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v2alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

// Describes test specs for the Operator resource.
var _ = Describe("Operator", func() {
	var (
		deleteOpts     *metav1.DeleteOptions
		listOpts       metav1.ListOptions
		operatorClient client.OperatorInterface
		kubeClient     kubernetes.Interface
	)

	BeforeEach(func() {
		// Toggle v2alpha1 feature-gate
		toggleCVO()
		togglev2alpha1()

		// Setup common utilities
		listOpts = metav1.ListOptions{}
		deleteOpts = &metav1.DeleteOptions{}
		operatorClient = ctx.Ctx().OperatorClient().OperatorsV2alpha1().Operators()
		kubeClient = ctx.Ctx().KubeClient().KubernetesInterface()
	})

	AfterEach(func() {
		toggleCVO()
		togglev2alpha1()
	})

	// Ensures that an Operator resource can select its components by label and surface them correctly in its status.
	//
	// Steps:
	// 1. Create an Operator resource, o
	// 2. Ensure o's status eventually contains its component label selector
	// 3. Create namespaces ns-a and ns-b
	// 4. Label ns-a with o's component label
	// 5. Ensure o's status.components.refs field eventually contains a reference to ns-a
	// 6. Create ServiceAccounts sa-a and sa-b in namespaces ns-a and ns-b respectively
	// 7. Label sa-a and sa-b with o's component label
	// 8. Ensure o's status.components.refs field eventually contains references to sa-a and sa-b
	// 9. Remove the component label from sa-b
	// 10. Ensure the reference to sa-b is eventually removed from o's status.components.refs field
	// 11. Delete ns-a
	// 12. Ensure the reference to ns-a is eventually removed from o's status.components.refs field
	It("should surface components in its status", func() {
		o := &operatorsv2alpha1.Operator{}
		o.SetName(genName("o-"))
		o, err := operatorClient.Create(o)
		Expect(err).ToNot(HaveOccurred())

		defer func() {
			Expect(operatorClient.Delete(o.GetName(), deleteOpts)).To(Succeed())
		}()

		By("eventually having a status that contains its component label selector")
		w, err := operatorClient.Watch(listOpts)
		Expect(err).ToNot(HaveOccurred())
		defer w.Stop()

		deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		expectedKey := "operators.coreos.com/" + o.GetName()
		awaitPredicates(deadline, w, operatorPredicate(func(op *operatorsv2alpha1.Operator) bool {
			if op.Status.Components == nil || op.Status.Components.LabelSelector == nil {
				return false
			}

			for _, requirement := range op.Status.Components.LabelSelector.MatchExpressions {
				if requirement.Key == expectedKey && requirement.Operator == metav1.LabelSelectorOpExists {
					return true
				}
			}

			return false
		}))
		defer w.Stop()

		// Create namespaces ns-a and ns-b
		nsA := &corev1.Namespace{}
		nsA.SetName(genName("ns-a-"))
		nsB := &corev1.Namespace{}
		nsB.SetName(genName("ns-b-"))

		for _, ns := range []*corev1.Namespace{nsA, nsB} {
			_, err := kubeClient.CoreV1().Namespaces().Create(ns)
			Expect(err).ToNot(HaveOccurred())
			defer func(name string) {
				kubeClient.CoreV1().Namespaces().Delete(name, deleteOpts)
			}(ns.GetName())
		}

		// Label ns-a with o's component label
		nsA.SetLabels(map[string]string{expectedKey: ""})
		_, err = kubeClient.CoreV1().Namespaces().Update(nsA)
		Expect(err).ToNot(HaveOccurred())

		// Ensure o's status.components.refs field eventually contains a reference to ns-a
		By("eventually listing a single component reference")
		componentRefEventuallyExists(w, true, nsA.GetName())

		// Create ServiceAccounts sa-a and sa-b in namespaces ns-a and ns-b respectively
		saA := &corev1.ServiceAccount{}
		saA.SetName(genName("sa-a-"))
		saA.SetNamespace(nsA.Name)
		saB := &corev1.ServiceAccount{}
		saB.SetName(genName("sa-b-"))
		saB.SetNamespace(nsB.Name)

		for _, sa := range []*corev1.ServiceAccount{saA, saB} {
			_, err := kubeClient.CoreV1().ServiceAccounts(sa.GetNamespace()).Create(sa)
			Expect(err).ToNot(HaveOccurred())
			defer func(namespace, name string) {
				kubeClient.CoreV1().ServiceAccounts(namespace).Delete(name, deleteOpts)
			}(sa.GetNamespace(), sa.GetName())
		}

		// Label sa-a and sa-b with o's component label
		saA.SetLabels(map[string]string{expectedKey: ""})
		_, err = kubeClient.CoreV1().ServiceAccounts(saA.GetNamespace()).Update(saA)
		Expect(err).ToNot(HaveOccurred())
		saB.SetLabels(map[string]string{expectedKey: ""})
		_, err = kubeClient.CoreV1().ServiceAccounts(saB.GetNamespace()).Update(saB)
		Expect(err).ToNot(HaveOccurred())

		// Ensure o's status.components.refs field eventually contains references to sa-a and sa-b
		By("eventually listing multiple component references")
		componentRefEventuallyExists(w, true, saA.GetName())
		componentRefEventuallyExists(w, true, saB.GetName())

		// Remove the component label from sa-b
		saB.SetLabels(nil)
		_, err = kubeClient.CoreV1().ServiceAccounts(saB.GetNamespace()).Update(saB)
		Expect(err).ToNot(HaveOccurred())

		// Ensure the reference to sa-b is eventually removed from o's status.components.refs field
		By("removing a component's reference when it no longer bears the component label")
		componentRefEventuallyExists(w, false, saB.GetName())

		// Delete ns-a
		Expect(kubeClient.CoreV1().Namespaces().Delete(nsA.GetName(), deleteOpts)).To(Succeed())

		// Ensure the reference to ns-a is eventually removed from o's status.components.refs field
		By("removing a component's reference when it no longer exists")
		componentRefEventuallyExists(w, false, nsA.GetName())
	})

})

func componentRefEventuallyExists(w watch.Interface, exists bool, refName string) {
	deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	awaitPredicates(deadline, w, operatorPredicate(func(op *operatorsv2alpha1.Operator) bool {
		if op.Status.Components == nil {
			return false
		}

		for _, ref := range op.Status.Components.Refs {
			if ref.Name == refName {
				return exists
			}
		}

		return !exists
	}))
}

func operatorPredicate(fn func(*operatorsv2alpha1.Operator) bool) predicateFunc {
	return func(event watch.Event) bool {
		o, ok := event.Object.(*operatorsv2alpha1.Operator)
		if !ok {
			panic(fmt.Sprintf("unexpected event object type %T in deployment", event.Object))
		}

		return fn(o)
	}
}
