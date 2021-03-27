package operators

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/testobj"
)

var _ = Describe("Operator Controller", func() {
	var (
		ctx                            context.Context
		operator                       *operatorsv1.Operator
		name                           types.NamespacedName
		expectedKey                    string
		expectedComponentLabelSelector *metav1.LabelSelector
	)

	BeforeEach(func() {
		ctx = context.Background()
		operator = newOperator(genName("ghost-")).Operator
		name = types.NamespacedName{Name: operator.GetName()}
		expectedKey = decorators.ComponentLabelKeyPrefix + operator.GetName()
		expectedComponentLabelSelector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      expectedKey,
					Operator: metav1.LabelSelectorOpExists,
				},
			},
		}

		Expect(k8sClient.Create(ctx, operator)).To(Succeed())
	})

	Describe("operator deletion", func() {
		var originalUID types.UID
		JustBeforeEach(func() {
			originalUID = operator.GetUID()
			Expect(k8sClient.Delete(ctx, operator)).To(Succeed())
		})
		Context("with components bearing its label", func() {
			var (
				objs      []runtime.Object
				namespace string
			)

			BeforeEach(func() {
				namespace = genName("ns-")
				objs = testobj.WithLabel(expectedKey, "",
					testobj.WithName(namespace, &corev1.Namespace{}),
				)

				for _, obj := range objs {
					Expect(k8sClient.Create(ctx, obj.(client.Object))).To(Succeed())
				}
			})

			AfterEach(func() {
				for _, obj := range objs {
					Expect(k8sClient.Delete(ctx, obj.(client.Object), deleteOpts)).To(Succeed())
				}
				Expect(k8sClient.Delete(ctx, operator, deleteOpts)).To(Succeed())
			})

			It("should re-create it", func() {
				// There's a race condition between this test and the controller. By the time,
				// this function is running, we may be in one of three states.
				//   1. The original deletion in the test setup has not yet finished, so the original
				//      operator resource still exists.
				//   2. The operator doesn't exist, and the controller has not yet re-created it.
				//   3. The operator has already been deleted and re-created.
				//
				// To solve this problem, we simply compare the UIDs and expect to eventually see a
				// a different UID.
				Eventually(func() (types.UID, error) {
					err := k8sClient.Get(ctx, name, operator)
					return operator.GetUID(), err
				}, timeout, interval).ShouldNot(Equal(originalUID))
			})
		})
		Context("with no components bearing its label", func() {
			It("should not re-create it", func() {
				// We expect the operator deletion to eventually complete, and then we
				// expect the operator to consistently never be found.
				Eventually(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, name, operator))
				}, timeout, interval).Should(BeTrue())
				Consistently(func() bool {
					return apierrors.IsNotFound(k8sClient.Get(ctx, name, operator))
				}, timeout, interval).Should(BeTrue())
			})
		})
	})

	Describe("component selection", func() {
		BeforeEach(func() {
			Eventually(func() (*operatorsv1.Components, error) {
				err := k8sClient.Get(ctx, name, operator)
				return operator.Status.Components, err
			}, timeout, interval).ShouldNot(BeNil())

			Eventually(func() (*metav1.LabelSelector, error) {
				err := k8sClient.Get(ctx, name, operator)
				return operator.Status.Components.LabelSelector, err
			}, timeout, interval).Should(Equal(expectedComponentLabelSelector))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, operator, deleteOpts)).To(Succeed())
		})

		Context("with no components bearing its label", func() {
			Specify("a status containing no component references", func() {
				Consistently(func() ([]operatorsv1.RichReference, error) {
					err := k8sClient.Get(ctx, name, operator)
					return operator.Status.Components.Refs, err
				}, timeout, interval).Should(BeEmpty())
			})
		})

		Context("with components bearing its label", func() {
			var (
				objs         []runtime.Object
				expectedRefs []operatorsv1.RichReference
				namespace    string
			)

			BeforeEach(func() {
				namespace = genName("ns-")
				objs = testobj.WithLabel(expectedKey, "",
					testobj.WithName(namespace, &corev1.Namespace{}),
				)

				for _, obj := range objs {
					Expect(k8sClient.Create(ctx, obj.(client.Object))).To(Succeed())
				}

				expectedRefs = toRefs(scheme, objs...)
			})

			AfterEach(func() {
				for _, obj := range objs {
					Expect(k8sClient.Delete(ctx, obj.(client.Object), deleteOpts)).To(Succeed())
				}
			})

			Specify("a status containing its component references", func() {
				Eventually(func() ([]operatorsv1.RichReference, error) {
					err := k8sClient.Get(ctx, name, operator)
					return operator.Status.Components.Refs, err
				}, timeout, interval).Should(ConsistOf(expectedRefs))
			})

			Context("when new components are labelled", func() {
				BeforeEach(func() {
					saName := &types.NamespacedName{Namespace: namespace, Name: genName("sa-")}
					newObjs := testobj.WithLabel(expectedKey, "",
						testobj.WithNamespacedName(saName, &corev1.ServiceAccount{}),
						testobj.WithName(genName("sa-admin-"), &rbacv1.ClusterRoleBinding{
							Subjects: []rbacv1.Subject{
								{
									Kind:      rbacv1.ServiceAccountKind,
									Name:      saName.Name,
									Namespace: saName.Namespace,
								},
							},
							RoleRef: rbacv1.RoleRef{
								APIGroup: rbacv1.GroupName,
								Kind:     "ClusterRole",
								Name:     "cluster-admin",
							},
						}),
					)

					for _, obj := range newObjs {
						Expect(k8sClient.Create(ctx, obj.(client.Object))).To(Succeed())
					}

					objs = append(objs, newObjs...)
					expectedRefs = append(expectedRefs, toRefs(scheme, newObjs...)...)
				})

				It("should add the component references", func() {
					Eventually(func() ([]operatorsv1.RichReference, error) {
						err := k8sClient.Get(ctx, name, operator)
						return operator.Status.Components.Refs, err
					}, timeout, interval).Should(ConsistOf(expectedRefs))
				})
			})

			Context("when component labels are removed", func() {
				BeforeEach(func() {
					for _, obj := range testobj.StripLabel(expectedKey, objs...) {
						Expect(k8sClient.Update(ctx, obj.(client.Object))).To(Succeed())
					}
				})

				It("should remove the component references", func() {
					Eventually(func() ([]operatorsv1.RichReference, error) {
						err := k8sClient.Get(ctx, name, operator)
						return operator.Status.Components.Refs, err
					}, timeout, interval).Should(BeEmpty())
				})
			})
		})
	})
})
