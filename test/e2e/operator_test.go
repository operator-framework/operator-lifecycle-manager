package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	gomegatypes "github.com/onsi/gomega/types"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/reference"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	clientv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/testobj"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

// Describes test specs for the Operator resource.
var _ = Describe("Operator API", func() {
	var (
		clientCtx       context.Context
		scheme          *runtime.Scheme
		listOpts        metav1.ListOptions
		operatorClient  clientv1.OperatorInterface
		client          controllerclient.Client
		operatorFactory decorators.OperatorFactory
	)

	BeforeEach(func() {
		// Setup common utilities
		clientCtx = context.Background()
		scheme = ctx.Ctx().Scheme()
		listOpts = metav1.ListOptions{}
		operatorClient = ctx.Ctx().OperatorClient().OperatorsV1().Operators()
		client = ctx.Ctx().Client()

		var err error
		operatorFactory, err = decorators.NewSchemedOperatorFactory(scheme)
		Expect(err).ToNot(HaveOccurred())
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
	// 11. Delete o
	// 12. Ensure o is re-created
	// 13. Delete ns-a
	// 14. Ensure the reference to ns-a is eventually removed from o's status.components.refs field
	// 15. Delete o
	// 16. Ensure o is not re-created
	It("should surface components in its status", func() {
		o := &operatorsv1.Operator{}
		o.SetName(genName("o-"))

		Consistently(o).ShouldNot(ContainCopiedCSVReferences())

		Eventually(func() error {
			return client.Create(clientCtx, o)
		}).Should(Succeed())

		defer func() {
			Eventually(func() error {
				err := client.Delete(clientCtx, o)
				if apierrors.IsNotFound(err) {
					return nil
				}

				return err
			}).Should(Succeed())
		}()

		By("eventually having a status that contains its component label selector")
		w, err := operatorClient.Watch(clientCtx, listOpts)
		Expect(err).ToNot(HaveOccurred())
		defer w.Stop()

		deadline, cancel := context.WithTimeout(clientCtx, 1*time.Minute)
		defer cancel()

		expectedKey := "operators.coreos.com/" + o.GetName()
		awaitPredicates(deadline, w, operatorPredicate(func(op *operatorsv1.Operator) bool {
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
			Eventually(func() error {
				return client.Create(clientCtx, ns)
			}).Should(Succeed())

			defer func(n *corev1.Namespace) {
				Eventually(func() error {
					err := client.Delete(clientCtx, n)
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}).Should(Succeed())
			}(ns)
		}

		// Label ns-a with o's component label
		setComponentLabel := func(m metav1.Object) error {
			m.SetLabels(map[string]string{expectedKey: ""})
			return nil
		}
		Eventually(Apply(nsA, setComponentLabel)).Should(Succeed())

		// Ensure o's status.components.refs field eventually contains a reference to ns-a
		By("eventually listing a single component reference")
		componentRefEventuallyExists(w, true, getReference(scheme, nsA))

		// Create ServiceAccounts sa-a and sa-b in namespaces ns-a and ns-b respectively
		saA := &corev1.ServiceAccount{}
		saA.SetName(genName("sa-a-"))
		saA.SetNamespace(nsA.GetName())
		saB := &corev1.ServiceAccount{}
		saB.SetName(genName("sa-b-"))
		saB.SetNamespace(nsB.GetName())

		for _, sa := range []*corev1.ServiceAccount{saA, saB} {
			Eventually(func() error {
				return client.Create(clientCtx, sa)
			}).Should(Succeed())
			defer func(sa *corev1.ServiceAccount) {
				Eventually(func() error {
					err := client.Delete(clientCtx, sa)
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}).Should(Succeed())
			}(sa)
		}

		// Label sa-a and sa-b with o's component label
		Eventually(Apply(saA, setComponentLabel)).Should(Succeed())
		Eventually(Apply(saB, setComponentLabel)).Should(Succeed())

		// Ensure o's status.components.refs field eventually contains references to sa-a and sa-b
		By("eventually listing multiple component references")
		componentRefEventuallyExists(w, true, getReference(scheme, saA))
		componentRefEventuallyExists(w, true, getReference(scheme, saB))

		// Remove the component label from sa-b
		Eventually(Apply(saB, func(m metav1.Object) error {
			m.SetLabels(nil)
			return nil
		})).Should(Succeed())

		// Ensure the reference to sa-b is eventually removed from o's status.components.refs field
		By("removing a component's reference when it no longer bears the component label")
		componentRefEventuallyExists(w, false, getReference(scheme, saB))

		// Delete o
		Eventually(func() error {
			err := client.Delete(clientCtx, o)
			if err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			return nil
		}).Should(Succeed())

		// Ensure that o is eventually recreated (because some of its components still exist).
		By("recreating the Operator when any components still exist")
		Eventually(func() error {
			return client.Get(clientCtx, types.NamespacedName{Name: o.GetName()}, o)
		}).Should(Succeed())

		// Delete ns-a
		Eventually(func() error {
			err := client.Delete(clientCtx, nsA)
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}).Should(Succeed())

		// Ensure the reference to ns-a is eventually removed from o's status.components.refs field
		By("removing a component's reference when it no longer exists")
		componentRefEventuallyExists(w, false, getReference(scheme, nsA))

		// Delete o
		Eventually(func() error {
			err := client.Delete(clientCtx, o)
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}).Should(Succeed())

		// Ensure that o is consistently not found
		By("verifying the Operator is permanently deleted if it has no components")
		Consistently(func() error {
			err := client.Get(clientCtx, types.NamespacedName{Name: o.GetName()}, o)
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}).Should(Succeed())
	})

	Context("when a subscription to a package exists", func() {
		var (
			ns           *corev1.Namespace
			sub          *operatorsv1alpha1.Subscription
			ip           *operatorsv1alpha1.InstallPlan
			operatorName types.NamespacedName
		)

		BeforeEach(func() {
			// Subscribe to a package and await a successful install
			ns = &corev1.Namespace{}
			ns.SetName(genName("ns-"))
			Eventually(func() error {
				return client.Create(clientCtx, ns)
			}).Should(Succeed())

			// Default to AllNamespaces
			og := &operatorsv1.OperatorGroup{}
			og.SetNamespace(ns.GetName())
			og.SetName(genName("og-"))
			Eventually(func() error {
				return client.Create(clientCtx, og)
			}).Should(Succeed())

			cs := &operatorsv1alpha1.CatalogSource{
				Spec: operatorsv1alpha1.CatalogSourceSpec{
					SourceType: operatorsv1alpha1.SourceTypeGrpc,
					Image:      "quay.io/operator-framework/ci-index:latest",
				},
			}
			cs.SetNamespace(ns.GetName())
			cs.SetName(genName("cs-"))
			Eventually(func() error {
				return client.Create(clientCtx, cs)
			}).Should(Succeed())

			sub = &operatorsv1alpha1.Subscription{
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					CatalogSource:          cs.GetName(),
					CatalogSourceNamespace: cs.GetNamespace(),
					Package:                "kiali",
					Channel:                "stable",
					InstallPlanApproval:    operatorsv1alpha1.ApprovalAutomatic,
				},
			}
			sub.SetNamespace(cs.GetNamespace())
			sub.SetName(genName("sub-"))
			Eventually(func() error {
				return client.Create(clientCtx, sub)
			}).Should(Succeed())

			Eventually(func() (operatorsv1alpha1.SubscriptionState, error) {
				s := sub.DeepCopy()
				if err := client.Get(clientCtx, testobj.NamespacedName(s), s); err != nil {
					return operatorsv1alpha1.SubscriptionStateNone, err
				}

				return s.Status.State, nil
			}).Should(BeEquivalentTo(operatorsv1alpha1.SubscriptionStateAtLatest))

			var ipRef *corev1.ObjectReference
			Eventually(func() (*corev1.ObjectReference, error) {
				if err := client.Get(clientCtx, testobj.NamespacedName(sub), sub); err != nil {
					return nil, err
				}
				ipRef = sub.Status.InstallPlanRef

				return ipRef, nil
			}).ShouldNot(BeNil())

			ip = &operatorsv1alpha1.InstallPlan{}
			Eventually(func() error {
				return client.Get(clientCtx, types.NamespacedName{Namespace: ipRef.Namespace, Name: ipRef.Name}, ip)
			}).Should(Succeed())

			operator, err := operatorFactory.NewPackageOperator(sub.Spec.Package, sub.GetNamespace())
			Expect(err).ToNot(HaveOccurred())
			operatorName = testobj.NamespacedName(operator)
		})

		AfterEach(func() {
			Eventually(func() error {
				err := client.Delete(clientCtx, ns)
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}).Should(Succeed())
		})

		It("should automatically adopt components", func() {
			Consistently(func() (*operatorsv1.Operator, error) {
				o := &operatorsv1.Operator{}
				err := client.Get(clientCtx, operatorName, o)
				return o, err
			}).ShouldNot(ContainCopiedCSVReferences())

			Eventually(func() (*operatorsv1.Operator, error) {
				o := &operatorsv1.Operator{}
				err := client.Get(clientCtx, operatorName, o)
				return o, err
			}).Should(ReferenceComponents([]*corev1.ObjectReference{
				getReference(scheme, sub),
				getReference(scheme, ip),
				getReference(scheme, testobj.WithNamespacedName(
					&types.NamespacedName{Namespace: sub.GetNamespace(), Name: "kiali-operator.v1.4.2"},
					&operatorsv1alpha1.ClusterServiceVersion{},
				)),
				getReference(scheme, testobj.WithNamespacedName(
					&types.NamespacedName{Namespace: sub.GetNamespace(), Name: "kiali-operator"},
					&corev1.ServiceAccount{},
				)),
				getReference(scheme, testobj.WithName("kialis.kiali.io", &apiextensionsv1.CustomResourceDefinition{})),
				getReference(scheme, testobj.WithName("monitoringdashboards.monitoring.kiali.io", &apiextensionsv1.CustomResourceDefinition{})),
			}))
		})

		Context("when a namespace is added", func() {
			var newNs *corev1.Namespace

			BeforeEach(func() {
				// Subscribe to a package and await a successful install
				newNs = &corev1.Namespace{}
				newNs.SetName(genName("ns-"))
				Eventually(func() error {
					return client.Create(clientCtx, newNs)
				}).Should(Succeed())
			})
			AfterEach(func() {
				Eventually(func() error {
					err := client.Delete(clientCtx, newNs)
					if apierrors.IsNotFound(err) {
						return nil
					}
					return err
				}).Should(Succeed())
			})
			It("should not adopt copied csvs", func() {
				Consistently(func() (*operatorsv1.Operator, error) {
					o := &operatorsv1.Operator{}
					err := client.Get(clientCtx, operatorName, o)
					return o, err
				}).ShouldNot(ContainCopiedCSVReferences())
			})
		})
	})
})

func getReference(scheme *runtime.Scheme, obj runtime.Object) *corev1.ObjectReference {
	ref, err := reference.GetReference(scheme, obj)
	if err != nil {
		panic(fmt.Sprintf("unable to get object reference: %s", err))
	}
	ref.UID = ""
	ref.ResourceVersion = ""

	return ref
}

func componentRefEventuallyExists(w watch.Interface, exists bool, ref *corev1.ObjectReference) {
	deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	awaitPredicates(deadline, w, operatorPredicate(func(op *operatorsv1.Operator) bool {
		if op.Status.Components == nil {
			return false
		}

		for _, r := range op.Status.Components.Refs {
			if r.APIVersion == ref.APIVersion && r.Kind == ref.Kind && r.Namespace == ref.Namespace && r.Name == ref.Name {
				return exists
			}
		}

		return !exists
	}))
}

func ContainCopiedCSVReferences() gomegatypes.GomegaMatcher {
	return &copiedCSVRefMatcher{}
}

type copiedCSVRefMatcher struct {
}

func (matcher *copiedCSVRefMatcher) Match(actual interface{}) (success bool, err error) {
	if actual == nil {
		return false, nil
	}
	operator, ok := actual.(*operatorsv1.Operator)
	if !ok {
		return false, fmt.Errorf("copiedCSVRefMatcher matcher expects an *Operator")
	}
	if operator.Status.Components == nil {
		return false, nil
	}
	for _, ref := range operator.Status.Components.Refs {
		if ref.Kind != operatorsv1alpha1.ClusterServiceVersionKind {
			continue
		}
		for _, c := range ref.Conditions {
			if c.Reason == string(operatorsv1alpha1.CSVReasonCopied) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (matcher *copiedCSVRefMatcher) FailureMessage(actual interface{}) (message string) {
	operator, ok := actual.(*operatorsv1.Operator)
	if !ok {
		return fmt.Sprintf("copiedCSVRefMatcher matcher expects an *Operator")
	}
	return fmt.Sprintf("Expected\n\t%#v\nto contain copied CSVs in components\n\t%#v\n", operator, operator.Status.Components)
}

func (matcher *copiedCSVRefMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	operator, ok := actual.(*operatorsv1.Operator)
	if !ok {
		return fmt.Sprintf("copiedCSVRefMatcher matcher expects an *Operator")
	}
	return fmt.Sprintf("Expected\n\t%#v\nto not contain copied CSVs in components\n\t%#v\n", operator, operator.Status.Components)
}

func operatorPredicate(fn func(*operatorsv1.Operator) bool) predicateFunc {
	return func(event watch.Event) bool {
		o, ok := event.Object.(*operatorsv1.Operator)
		if !ok {
			panic(fmt.Sprintf("unexpected event object type %T in deployment", event.Object))
		}

		return fn(o)
	}
}

type OperatorMatcher struct {
	matches func(*operatorsv1.Operator) (bool, error)
	name    string
}

func (o OperatorMatcher) Match(actual interface{}) (bool, error) {
	operator, ok := actual.(*operatorsv1.Operator)
	if !ok {
		return false, fmt.Errorf("OperatorMatcher expects Operator (got %T)", actual)
	}

	return o.matches(operator)
}

func (o OperatorMatcher) String() string {
	return o.name
}

func (o OperatorMatcher) FailureMessage(actual interface{}) string {
	return format.Message(actual, "to satisfy", o)
}

func (o OperatorMatcher) NegatedFailureMessage(actual interface{}) string {
	return format.Message(actual, "not to satisfy", o)
}

func ReferenceComponents(refs []*corev1.ObjectReference) gomegatypes.GomegaMatcher {
	return &OperatorMatcher{
		matches: func(operator *operatorsv1.Operator) (bool, error) {
			actual := map[corev1.ObjectReference]struct{}{}
			for _, ref := range operator.Status.Components.Refs {
				actual[*ref.ObjectReference] = struct{}{}
			}

			for _, ref := range refs {
				if _, ok := actual[*ref]; !ok {
					return false, nil
				}
			}

			return true, nil
		},
		name: fmt.Sprintf("ReferenceComponents(%v)", refs),
	}
}
