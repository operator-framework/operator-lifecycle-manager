package operator

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	operatorsv2alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v2alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/testobj"
)

var _ = Describe("Operator Decorator", func() {
	DescribeTable("getting operator names from labels",
		func(labels map[string]string, names []types.NamespacedName) {
			Expect(OperatorNames(labels)).To(ConsistOf(names))
		},
		Entry("should handle nil labels", nil, nil),
		Entry("should handle empty labels", map[string]string{}, nil),
		Entry("should ignore non-component labels",
			map[string]string{
				"":                            "",
				"operators.coreos.com/ghost":  "ooooooooo",
				"operator/ghoul":              "",
				"operators.coreos.com/goblin": "",
				"operator":                    "wizard",
			},
			[]types.NamespacedName{
				{Name: "ghost"},
				{Name: "goblin"},
			},
		),
	)

	Describe("component selection", func() {
		var (
			operator                       *Operator
			expectedKey                    string
			expectedComponentLabelSelector *metav1.LabelSelector
		)

		BeforeEach(func() {
			operator = newOperator("ghost")
			expectedKey = ComponentLabelKeyPrefix + operator.GetName()
			expectedComponentLabelSelector = &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      expectedKey,
						Operator: metav1.LabelSelectorOpExists,
					},
				},
			}
		})

		Describe("selector generation", func() {
			var expectedComponentSelector labels.Selector

			BeforeEach(func() {
				expectedComponentSelector, _ = metav1.LabelSelectorAsSelector(expectedComponentLabelSelector)
			})

			It("can generate a valid component label key", func() {
				key, err := operator.ComponentLabelKey()
				Expect(err).ToNot(HaveOccurred())
				Expect(key).To(Equal(expectedKey))
			})

			It("can generate a valid component label selector", func() {
				labelSelector, err := operator.ComponentLabelSelector()
				Expect(err).ToNot(HaveOccurred())
				Expect(labelSelector).To(Equal(expectedComponentLabelSelector))
			})

			It("can generate a valid component selector", func() {
				componentSelector, err := operator.ComponentSelector()
				Expect(err).ToNot(HaveOccurred())
				Expect(componentSelector).To(Equal(expectedComponentSelector))
			})

			Specify("component label selector in the operator's status upon reset", func() {
				err := operator.ResetComponents()
				Expect(err).ToNot(HaveOccurred())
				Expect(operator.Status.Components).ToNot(BeNil())
				Expect(operator.Status.Components.LabelSelector).To(Equal(expectedComponentLabelSelector))
			})
		})

		Describe("adding components", func() {
			var (
				components []runtime.Object
				err        error
			)

			BeforeEach(func() {
				components = testobj.WithLabel(expectedKey, "",
					testobj.WithName("imp", &corev1.ServiceAccount{}),
					testobj.WithName("spectre", &rbacv1.Role{}),
					testobj.WithName("zombie", &appsv1.Deployment{}),
					testobj.WithName("boggart", &apiextensionsv1beta1.CustomResourceDefinition{}),
					testobj.WithName("dragon", &apiregistrationv1.APIService{}),
					testobj.WithName("hobbit", &operatorsv1alpha1.ClusterServiceVersion{}),
					testobj.WithName("elf", &operatorsv1alpha1.Subscription{}),
					testobj.WithName("toad", &operatorsv1alpha1.InstallPlan{}),
					testobj.WithName("selkie", &operatorsv1.OperatorGroup{}),
					testobj.WithName("ent", &operatorsv2alpha1.Operator{}),
				)
			})

			JustBeforeEach(func() {
				err = operator.AddComponents(components...)
			})

			Context("associated with the operator", func() {

				It("should not error", func() {
					Expect(err).ToNot(HaveOccurred())
				})

				Specify("non-nil components", func() {
					Expect(operator.Status.Components).ToNot(BeNil())
				})

				It("should be referenced in its status", func() {
					Expect(operator.Status.Components.Refs).To(ConsistOf(toRefs(scheme, components...)))
				})

				It("should retain existing references on further addition", func() {
					component := testobj.WithLabel(expectedKey, "", testobj.WithName("orc", &rbacv1.ClusterRoleBinding{}))
					err := operator.AddComponents(component...)
					Expect(err).ToNot(HaveOccurred())
					Expect(operator.Status.Components.Refs).To(ConsistOf(toRefs(scheme, append(component, components...)...)))
				})

				It("should be removed from its status upon reset", func() {
					err := operator.ResetComponents()
					Expect(err).ToNot(HaveOccurred())
					Expect(operator.Status.Components).ToNot(BeNil())
					Expect(operator.Status.Components.Refs).To(HaveLen(0))
				})

				Context("with nested list elements", func() {
					var (
						nested []runtime.Object
						list   runtime.Object
					)

					BeforeEach(func() {
						nested = testobj.WithLabel(expectedKey, "",
							testobj.WithName("nessie", &rbacv1.RoleBinding{}),
							testobj.WithName("troll", &rbacv1.RoleBinding{}),
						)
						list = testobj.WithItems(&rbacv1.RoleBindingList{}, nested...)
					})

					Specify("references for nested list elements", func() {
						err = operator.AddComponents(list)
						Expect(err).ToNot(HaveOccurred())
						Expect(operator.Status.Components.Refs).To(ConsistOf(toRefs(scheme, append(components, nested...)...)))
					})
				})

				It("should drop existing references when set", func() {
					components := testobj.WithLabel(expectedKey, "",
						testobj.WithName("imp", &corev1.ServiceAccount{}),
						testobj.WithName("spectre", &rbacv1.Role{}),
						testobj.WithName("zombie", &appsv1.Deployment{}),
					)
					err := operator.SetComponents(components...)
					Expect(err).ToNot(HaveOccurred())
					Expect(operator.Status.Components.Refs).To(ConsistOf(toRefs(scheme, components...)))
				})
			})

			Context("not associated with the operator", func() {
				var (
					expectedStatusComponents *operatorsv2alpha1.Components
				)

				BeforeEach(func() {
					expectedStatusComponents = &operatorsv2alpha1.Components{
						LabelSelector: expectedComponentLabelSelector,
						Refs:          toRefs(scheme, components...),
					}

					err = operator.AddComponents(components...)
					Expect(err).ToNot(HaveOccurred())

					// Append an unlabelled resource
					components = append(components, testobj.WithName("satyr", &corev1.Service{}))
				})

				It("should error", func() {
					Expect(err).To(HaveOccurred())
				})

				It("should still contain good component references", func() {
					Expect(operator.Status.Components).To(Equal(expectedStatusComponents))
				})

				It("should error and reset component references when set", func() {
					err := operator.SetComponents(components...)
					Expect(err).To(HaveOccurred())
					Expect(operator.Status.Components).ToNot(BeNil())

					// Should be nil after reset
					Expect(operator.Status.Components.Refs).To(BeNil())
				})
			})
		})

	})

})

func newOperator(name string) *Operator {
	return &Operator{
		Operator: &operatorsv2alpha1.Operator{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		},
	}
}

func toRefs(scheme *runtime.Scheme, objs ...runtime.Object) (refs []operatorsv2alpha1.Ref) {
	for _, ref := range testobj.GetReferences(scheme, objs...) {
		componentRef := operatorsv2alpha1.Ref{
			ObjectReference: ref,
		}
		refs = append(refs, componentRef)
	}

	return
}
