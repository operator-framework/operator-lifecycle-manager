package operators

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("OperatorCondition", func() {
	Context("The ensureEnvVarIsPresent function", func() {
		It("returns the existing array and true if the envVar was already present", func() {
			actualEnvVars, envVarPresent := ensureEnvVarIsPresent([]corev1.EnvVar{{Name: "foo", Value: "bar"}, {Name: "foo2", Value: "bar2"}}, corev1.EnvVar{Name: "foo", Value: "bar"})
			Expect(actualEnvVars).To(Equal([]corev1.EnvVar{{Name: "foo", Value: "bar"}, {Name: "foo2", Value: "bar2"}}))
			Expect(envVarPresent).To(BeTrue())
		})

		It("appends the envVar to an empty array and return false", func() {
			actualEnvVars, envVarPresent := ensureEnvVarIsPresent([]corev1.EnvVar{}, corev1.EnvVar{Name: "foo", Value: "bar"})
			Expect(actualEnvVars).To(Equal([]corev1.EnvVar{{Name: "foo", Value: "bar"}}))
			Expect(envVarPresent).To(BeFalse())
		})

		It("appends the envVar to an EnvVar array that contains other envVars and return false", func() {
			actualEnvVars, envVarPresent := ensureEnvVarIsPresent([]corev1.EnvVar{{Name: "notFoo", Value: "bar"}}, corev1.EnvVar{Name: "foo", Value: "bar"})
			Expect(actualEnvVars).To(Equal([]corev1.EnvVar{{Name: "notFoo", Value: "bar"}, {Name: "foo", Value: "bar"}}))
			Expect(envVarPresent).To(BeFalse())
		})

		It("updates the value of an envVar in an envVar array if the envVar's key already exists but the value is different and returns false", func() {
			actualEnvVars, envVarPresent := ensureEnvVarIsPresent([]corev1.EnvVar{{Name: "foo", Value: "bar"}}, corev1.EnvVar{Name: "foo", Value: "notBar"})
			Expect(actualEnvVars).To(Equal([]corev1.EnvVar{{Name: "foo", Value: "notBar"}}))
			Expect(envVarPresent).To(BeFalse())
		})
	})

	Context("The OperatorCondition Reconciler", func() {
		var (
			ctx               context.Context
			operatorCondition *operatorsv1.OperatorCondition
			namespace         *corev1.Namespace
			namespacedName    types.NamespacedName
		)
		BeforeEach(func() {
			ctx = context.Background()
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: genName("ns-"),
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())

			namespacedName = types.NamespacedName{Name: "test", Namespace: namespace.GetName()}
		})

		When("an operatorCondition is created that specifies an array of ServiceAccounts", func() {
			BeforeEach(func() {
				operatorCondition = &operatorsv1.OperatorCondition{
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespacedName.Name,
						Namespace: namespacedName.Namespace,
					},
					Spec: operatorsv1.OperatorConditionSpec{
						ServiceAccounts: []string{"serviceaccount"},
						Deployments:     []string{},
					},
				}
				Expect(k8sClient.Create(ctx, operatorCondition)).To(Succeed())
			})

			It("creates and recreates the expected Role", func() {
				role := &rbacv1.Role{}

				Eventually(func() error {
					return k8sClient.Get(ctx, namespacedName, role)
				}, timeout, interval).Should(BeNil())

				Expect(len(role.OwnerReferences)).Should(Equal(1))

				falseBool := false
				trueBool := true

				Expect(role.OwnerReferences).Should(ContainElement(metav1.OwnerReference{
					APIVersion:         "operators.coreos.com/v1",
					Kind:               "OperatorCondition",
					Name:               "test",
					UID:                operatorCondition.UID,
					Controller:         &trueBool,
					BlockOwnerDeletion: &falseBool,
				}))
				Expect(role.Rules).Should(Equal([]rbacv1.PolicyRule{
					{
						Verbs:         []string{"get", "update", "patch"},
						APIGroups:     []string{"operators.coreos.com"},
						Resources:     []string{"operatorconditions"},
						ResourceNames: []string{namespacedName.Name},
					},
				}))
				Expect(k8sClient.Delete(ctx, role)).To(Succeed())
				Eventually(func() error {
					return k8sClient.Get(ctx, namespacedName, role)
				}, timeout, interval).Should(Succeed())

			})

			It("creates and recreates the expected RoleBinding", func() {
				roleBinding := &rbacv1.RoleBinding{}
				falseBool := false
				trueBool := true

				Eventually(func() error {
					return k8sClient.Get(ctx, namespacedName, roleBinding)
				}, timeout, interval).Should(BeNil())
				Expect(len(roleBinding.OwnerReferences)).To(Equal(1))
				Expect(roleBinding.OwnerReferences).Should(ContainElement(metav1.OwnerReference{
					APIVersion:         "operators.coreos.com/v1",
					Kind:               "OperatorCondition",
					Name:               "test",
					UID:                operatorCondition.UID,
					Controller:         &trueBool,
					BlockOwnerDeletion: &falseBool,
				}))
				Expect(len(roleBinding.Subjects)).To(Equal(1))
				Expect(roleBinding.Subjects).Should(ContainElement(rbacv1.Subject{
					Kind: "ServiceAccount",
					Name: operatorCondition.Spec.ServiceAccounts[0],
				}))
				Expect(roleBinding.RoleRef).To(Equal(rbacv1.RoleRef{
					Kind:     "Role",
					Name:     roleBinding.GetName(),
					APIGroup: "rbac.authorization.k8s.io",
				}))

				Expect(k8sClient.Delete(ctx, roleBinding)).To(Succeed())
				Eventually(func() error {
					return k8sClient.Get(ctx, namespacedName, roleBinding)
				}, timeout, interval).Should(Succeed())
			})
		})

		When("a CSV exists that owns a deployment", func() {
			var csv *operatorsv1alpha1.ClusterServiceVersion
			BeforeEach(func() {
				// Create a coppied csv used as an owner in the following tests.
				// Copied CSVs are ignored by the OperatorConditionGenerator Reconciler, which we don't want to intervine in this test.
				csv = &operatorsv1alpha1.ClusterServiceVersion{
					TypeMeta: metav1.TypeMeta{
						Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
						APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      namespacedName.Name,
						Namespace: namespace.GetName(),
						Labels: map[string]string{
							operatorsv1alpha1.CopiedLabelKey: "",
						},
					},
					Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
						InstallModes: []operatorsv1alpha1.InstallMode{
							{
								Type:      operatorsv1alpha1.InstallModeTypeOwnNamespace,
								Supported: true,
							},
							{
								Type:      operatorsv1alpha1.InstallModeTypeSingleNamespace,
								Supported: true,
							},
							{
								Type:      operatorsv1alpha1.InstallModeTypeMultiNamespace,
								Supported: true,
							},
							{
								Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
								Supported: true,
							},
						},
						InstallStrategy: newNginxInstallStrategy("deployment", nil, nil),
					},
				}
				Expect(k8sClient.Create(ctx, csv)).To(Succeed())

				// Create  the deployment
				labels := map[string]string{
					"foo": "bar",
				}
				deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment",
					Namespace: namespacedName.Namespace,
				},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: labels,
						},
						Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								GenerateName: "nginx-",
								Namespace:    namespacedName.Namespace,
								Labels:       labels,
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  "web",
										Image: "nginx",
										Ports: []corev1.ContainerPort{
											{
												Name:          "web",
												ContainerPort: 80,
												Protocol:      corev1.ProtocolTCP,
											},
										},
									},
								},
							},
						},
					},
				}
				ownerutil.AddNonBlockingOwner(deployment, csv)

				Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
			})

			Context("and an OperatorCondition with a different name than the CSV includes that deployment in its spec.Deployments array", func() {
				BeforeEach(func() {
					operatorCondition = &operatorsv1.OperatorCondition{
						ObjectMeta: metav1.ObjectMeta{
							Name:      namespacedName.Name + "different",
							Namespace: namespacedName.Namespace,
						},
						Spec: operatorsv1.OperatorConditionSpec{
							ServiceAccounts: []string{},
							Deployments:     []string{"deployment"},
						},
					}
					Expect(k8sClient.Create(ctx, operatorCondition)).To(Succeed())
				})
				It("does not inject the OperatorCondition name into the deployment's Environment Variables", func() {
					deployment := &appsv1.Deployment{}
					Consistently(func() error {
						err := k8sClient.Get(ctx, types.NamespacedName{Name: operatorCondition.Spec.Deployments[0], Namespace: namespace.GetName()}, deployment)
						if err != nil {
							return err
						}
						if len(deployment.Spec.Template.Spec.Containers) != 1 {
							return fmt.Errorf("Deployment should contain a single container")
						}
						for _, container := range deployment.Spec.Template.Spec.Containers {
							if len(container.Env) != 0 {

								return fmt.Errorf("env vars should not exist: %v", container.Env)
							}
						}
						return nil
					}, timeout, interval).Should(BeNil())
				})
			})

			Context("and an OperatorCondition with the same name as the CSV includes that deployment in its spec.Deployments array", func() {
				BeforeEach(func() {
					operatorCondition = &operatorsv1.OperatorCondition{
						ObjectMeta: metav1.ObjectMeta{
							Name:      namespacedName.Name,
							Namespace: namespacedName.Namespace,
						},
						Spec: operatorsv1.OperatorConditionSpec{
							ServiceAccounts: []string{},
							Deployments:     []string{"deployment"},
						},
					}
					ownerutil.AddNonBlockingOwner(operatorCondition, csv)
					Expect(k8sClient.Create(ctx, operatorCondition)).To(Succeed())
				})

				It("should always inject the OperatorCondition Environment Variable into containers defined in the deployment", func() {
					deployment := &appsv1.Deployment{}
					Eventually(func() error {
						err := k8sClient.Get(ctx, types.NamespacedName{Name: operatorCondition.Spec.Deployments[0], Namespace: namespace.GetName()}, deployment)
						if err != nil {
							return err
						}
						if len(deployment.Spec.Template.Spec.Containers) != 1 {
							return fmt.Errorf("Deployment should contain a single container")
						}
						for _, container := range deployment.Spec.Template.Spec.Containers {
							if len(container.Env) == 0 {
								return fmt.Errorf("env vars should exist")
							}
						}
						return nil
					}, timeout, interval).Should(BeNil())

					Expect(len(deployment.Spec.Template.Spec.Containers)).Should(Equal(1))
					for _, container := range deployment.Spec.Template.Spec.Containers {
						Expect(len(container.Env)).Should(Equal(1))
						Expect(container.Env).Should(ContainElement(corev1.EnvVar{
							Name:  "OPERATOR_CONDITION_NAME",
							Value: operatorCondition.GetName(),
						}))
					}

					// Remove the container's Environment Variables defined in the deployment
					Eventually(func() error {
						err := k8sClient.Get(ctx, types.NamespacedName{Name: operatorCondition.Spec.Deployments[0], Namespace: namespace.GetName()}, deployment)
						if err != nil {
							return err
						}
						deployment.Spec.Template.Spec.Containers[0].Env = nil
						return k8sClient.Update(ctx, deployment)
					}, timeout, interval).Should(BeNil())

					// Ensure that the OPERATOR_CONDITION_NAME Environment Variable is recreated
					Eventually(func() error {
						err := k8sClient.Get(ctx, types.NamespacedName{Name: operatorCondition.Spec.Deployments[0], Namespace: namespace.GetName()}, deployment)
						if err != nil {
							return err
						}
						if len(deployment.Spec.Template.Spec.Containers) != 1 {
							return fmt.Errorf("Deployment should contain a single container")
						}
						for _, container := range deployment.Spec.Template.Spec.Containers {
							if len(container.Env) == 0 {
								return fmt.Errorf("env vars should exist")
							}
						}
						return nil
					}, timeout, interval).Should(BeNil())

					Expect(len(deployment.Spec.Template.Spec.Containers)).Should(Equal(1))
					for _, container := range deployment.Spec.Template.Spec.Containers {
						Expect(len(container.Env)).Should(Equal(1))
						Expect(container.Env).Should(ContainElement(corev1.EnvVar{
							Name:  "OPERATOR_CONDITION_NAME",
							Value: operatorCondition.GetName(),
						}))
					}
				})
			})
		})
	})
})
