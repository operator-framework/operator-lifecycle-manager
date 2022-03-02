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
		clientCtx = context.Background()
		scheme = ctx.Ctx().Scheme()
		listOpts = metav1.ListOptions{}
		operatorClient = ctx.Ctx().OperatorClient().OperatorsV1().Operators()
		client = ctx.Ctx().Client()

		var err error
		operatorFactory, err = decorators.NewSchemedOperatorFactory(scheme)
		Expect(err).ToNot(HaveOccurred())
	})
	AfterEach(func() {
		Eventually(func() error {
			return operatorClient.DeleteCollection(clientCtx, metav1.DeleteOptions{}, listOpts)
		}).Should(Succeed())
	})

	When("an Operator resource can select its components by label", func() {
		var (
			o *operatorsv1.Operator
		)
		BeforeEach(func() {
			o = &operatorsv1.Operator{}
			o.SetName(genName("o-"))

			Eventually(func() error {
				return client.Create(clientCtx, o)
			}).Should(Succeed())
		})
		AfterEach(func() {
			Eventually(func() error {
				return controllerclient.IgnoreNotFound(client.Delete(clientCtx, o))
			}).Should(Succeed())
		})

		It("should not contain copied csv status references", func() {
			Consistently(o).ShouldNot(ContainCopiedCSVReferences())
		})

		It("[FLAKE] should surface referenced components in its status", func() {
			By("eventually having a status that contains its component label selector")
			w, err := operatorClient.Watch(clientCtx, listOpts)
			Expect(err).To(BeNil())
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

			setComponentLabel := func(m metav1.Object) error {
				m.SetLabels(map[string]string{expectedKey: ""})
				return nil
			}
			Eventually(Apply(nsA, setComponentLabel)).Should(Succeed())

			By("eventually listing a single component reference")
			componentRefEventuallyExists(w, true, getReference(scheme, nsA))

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
						return controllerclient.IgnoreNotFound(client.Delete(clientCtx, sa))
					}).Should(Succeed())
				}(sa)
			}

			Eventually(Apply(saA, setComponentLabel)).Should(Succeed())
			Eventually(Apply(saB, setComponentLabel)).Should(Succeed())

			By("eventually listing multiple component references")
			componentRefEventuallyExists(w, true, getReference(scheme, saA))
			componentRefEventuallyExists(w, true, getReference(scheme, saB))

			Eventually(Apply(saB, func(m metav1.Object) error {
				m.SetLabels(nil)
				return nil
			})).Should(Succeed())

			By("removing a component's reference when it no longer bears the component label")
			componentRefEventuallyExists(w, false, getReference(scheme, saB))

			Eventually(func() error {
				return controllerclient.IgnoreNotFound(client.Delete(clientCtx, o))
			}).Should(Succeed())

			By("recreating the Operator when any components still exist")
			Eventually(func() error {
				return client.Get(clientCtx, types.NamespacedName{Name: o.GetName()}, o)
			}).Should(Succeed())

			Eventually(func() error {
				return controllerclient.IgnoreNotFound(client.Delete(clientCtx, nsA))
			}).Should(Succeed())

			By("removing a component's reference when it no longer exists")
			componentRefEventuallyExists(w, false, getReference(scheme, nsA))

			Eventually(func() error {
				return controllerclient.IgnoreNotFound(client.Delete(clientCtx, o))
			}).Should(Succeed())

			By("verifying the Operator is permanently deleted if it has no components")
			Consistently(func() error {
				return controllerclient.IgnoreNotFound(client.Get(clientCtx, types.NamespacedName{Name: o.GetName()}, o))
			}).Should(Succeed())
		})
	})

	When("a subscription to a package exists", func() {
		var (
			ns           *corev1.Namespace
			sub          *operatorsv1alpha1.Subscription
			ip           *operatorsv1alpha1.InstallPlan
			operatorName types.NamespacedName
		)

		BeforeEach(func() {
			ns = &corev1.Namespace{}
			ns.SetName(genName("ns-"))
			Eventually(func() error {
				return client.Create(clientCtx, ns)
			}).Should(Succeed())

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

			_, err := fetchCatalogSourceOnStatus(newCRClient(), cs.GetName(), cs.GetNamespace(), catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred())

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
			By("Deleting the testing namespace")
			Eventually(func() error {
				return controllerclient.IgnoreNotFound(client.Delete(clientCtx, ns))
			}).Should(Succeed())

			By("Deleting the kiali-related CRDs")
			Eventually(func() error {
				return client.DeleteAllOf(clientCtx, &apiextensionsv1.CustomResourceDefinition{}, controllerclient.MatchingLabels{
					fmt.Sprintf("operators.coreos.com/%s", operatorName.Name): "",
				})
			}).Should(Succeed())

			By("Deleting the test Operator resource")
			Eventually(func() error {
				return operatorClient.Delete(clientCtx, operatorName.Name, metav1.DeleteOptions{})
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

		When("a namespace is added", func() {
			var newNs *corev1.Namespace

			BeforeEach(func() {
				By("Subscribe to a package and await a successful install")
				newNs = &corev1.Namespace{}
				newNs.SetName(genName("ns-"))
				Eventually(func() error {
					return client.Create(clientCtx, newNs)
				}).Should(Succeed())
			})
			AfterEach(func() {
				Eventually(func() error {
					return controllerclient.IgnoreNotFound(client.Delete(clientCtx, newNs))
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
		return "copiedCSVRefMatcher matcher expects an *Operator"
	}
	return fmt.Sprintf("Expected\n\t%#v\nto contain copied CSVs in components\n\t%#v\n", operator, operator.Status.Components)
}

func (matcher *copiedCSVRefMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	operator, ok := actual.(*operatorsv1.Operator)
	if !ok {
		return "copiedCSVRefMatcher matcher expects an *Operator"
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
					ctx.Ctx().Logf("reference missing: %v - %v - %v", ref.GetObjectKind(), ref.Name, ref.Namespace)
					return false, nil
				}
			}

			return true, nil
		},
		name: fmt.Sprintf("ReferenceComponents(%v)", refs),
	}
}
