package operator

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	operatorsv2alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v2alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/testobj"
)

var _ = Describe("Operator Reconciler", func() {
	var (
		ctx                            context.Context
		operator                       *operatorsv2alpha1.Operator
		name                           types.NamespacedName
		expectedKey                    string
		expectedComponentLabelSelector *metav1.LabelSelector
	)

	BeforeEach(func() {
		ctx = context.Background()
		operator = newOperator(genName("ghost-")).Operator
		name = types.NamespacedName{Name: operator.GetName()}
		expectedKey = ComponentLabelKeyPrefix + operator.GetName()
		expectedComponentLabelSelector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      expectedKey,
					Operator: metav1.LabelSelectorOpExists,
				},
			},
		}

		Expect(mgrClient.Create(ctx, operator)).To(Succeed())
	})

	AfterEach(func() {
		Expect(mgrClient.Get(ctx, name, operator)).To(Succeed())
		Expect(mgrClient.Delete(ctx, operator, deleteOpts)).To(Succeed())
	})

	Describe("component selection", func() {
		BeforeEach(func() {
			Eventually(func() (*operatorsv2alpha1.Components, error) {
				err := mgrClient.Get(ctx, name, operator)
				return operator.Status.Components, err
			}, timeout, interval).ShouldNot(BeNil())

			Eventually(func() (*metav1.LabelSelector, error) {
				err := mgrClient.Get(ctx, name, operator)
				return operator.Status.Components.LabelSelector, err
			}, timeout, interval).Should(Equal(expectedComponentLabelSelector))
		})

		Context("with no components bearing its label", func() {
			Specify("a status containing no component references", func() {
				Consistently(func() ([]operatorsv2alpha1.Ref, error) {
					err := mgrClient.Get(ctx, name, operator)
					return operator.Status.Components.Refs, err
				}, 4*interval, interval).Should(BeEmpty())
			})
		})

		Context("with components bearing its label", func() {
			var (
				objs         []runtime.Object
				expectedRefs []operatorsv2alpha1.Ref
				namespace    string
			)

			BeforeEach(func() {
				labels := map[string]string{"app": "app"}
				namespace = genName("ns-")
				objs = testobj.WithLabel(expectedKey, "",
					testobj.WithName(namespace, &corev1.Namespace{}),
					testobj.WithNamespacedName(
						&types.NamespacedName{Namespace: namespace, Name: genName("csv-")},
						&operatorsv1alpha1.ClusterServiceVersion{
							Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
								InstallStrategy: operatorsv1alpha1.NamedInstallStrategy{
									StrategyName: install.InstallStrategyNameDeployment,
									StrategySpecRaw: testobj.Marshal(install.StrategyDetailsDeployment{
										DeploymentSpecs: []install.StrategyDeploymentSpec{
											{
												Name: "dep",
												Spec: appsv1.DeploymentSpec{
													Selector: &metav1.LabelSelector{MatchLabels: labels},
													Template: corev1.PodTemplateSpec{
														ObjectMeta: metav1.ObjectMeta{Labels: labels},
														Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "container"}}},
													},
												},
											},
										},
									}),
								},
							},
						},
					),
				)

				for _, obj := range objs {
					Expect(mgrClient.Create(ctx, obj)).To(Succeed())
				}

				expectedRefs = toRefs(scheme, objs...)
			})

			AfterEach(func() {
				for _, obj := range objs {
					Expect(mgrClient.Get(ctx, testobj.NamespacedName(obj), obj)).To(Succeed())
					Expect(mgrClient.Delete(ctx, obj, deleteOpts)).To(Succeed())
				}
			})

			Specify("a status containing its component references", func() {
				Eventually(func() ([]operatorsv2alpha1.Ref, error) {
					err := mgrClient.Get(ctx, name, operator)
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
						Expect(mgrClient.Create(ctx, obj)).To(Succeed())
					}

					objs = append(objs, newObjs...)
					expectedRefs = append(expectedRefs, toRefs(scheme, newObjs...)...)
				})

				It("should add the component references", func() {
					Eventually(func() ([]operatorsv2alpha1.Ref, error) {
						err := mgrClient.Get(ctx, name, operator)
						return operator.Status.Components.Refs, err
					}, timeout, interval).Should(ConsistOf(expectedRefs))
				})
			})

			Context("when component labels are removed", func() {
				BeforeEach(func() {
					for _, obj := range testobj.StripLabel(expectedKey, objs...) {
						Expect(mgrClient.Update(ctx, obj)).To(Succeed())
					}
				})

				It("should remove the component references", func() {
					Eventually(func() ([]operatorsv2alpha1.Ref, error) {
						err := mgrClient.Get(ctx, name, operator)
						return operator.Status.Components.Refs, err
					}, timeout, interval).Should(BeEmpty())
				})
			})
		})
	})
})
