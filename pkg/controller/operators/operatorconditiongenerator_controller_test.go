package operators

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

var _ = Describe("The OperatorConditionsGenerator Controller", func() {
	const (
		timeout  = time.Second * 20
		interval = time.Millisecond * 100
	)

	var (
		ctx       context.Context
		namespace *corev1.Namespace
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: genName("ns-"),
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	})

	It("creates an OperatorCondition for a CSV without a ServiceAccount", func() {
		depName := genName("dep-")
		csv := &operatorsv1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
				APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("csv-"),
				Namespace: namespace.GetName(),
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
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			},
		}

		Expect(k8sClient.Create(ctx, csv)).To(Succeed())
		namespacedName := types.NamespacedName{Name: csv.GetName(), Namespace: csv.GetNamespace()}
		operatorCondition := &operatorsv2.OperatorCondition{}
		// Check that an OperatorCondition was created
		Eventually(func() error {
			err := k8sClient.Get(ctx, namespacedName, operatorCondition)
			if err != nil {
				return err
			}
			if len(operatorCondition.Spec.ServiceAccounts) != 1 || operatorCondition.Spec.ServiceAccounts[0] != "default" {
				return fmt.Errorf("operatorCondition should only include the default ServiceAccount for CSVs without ClusterPermissions or Permissions")
			}

			if len(operatorCondition.Spec.Deployments) != 1 || operatorCondition.Spec.Deployments[0] != depName {
				return fmt.Errorf("operatorCondition should only include the CSV's deployments")
			}

			return nil
		}, timeout, interval).Should(Succeed())

		Expect(k8sClient.Delete(ctx, operatorCondition)).Should(Succeed())
		Eventually(func() error {
			return k8sClient.Get(ctx, namespacedName, operatorCondition)
		}, timeout, interval).Should(Succeed())
	})

	It("does not create an OperatorCondition for a Copied CSV", func() {
		depName := genName("dep-")
		csv := &operatorsv1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
				APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("csv-"),
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
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			},
		}

		Expect(k8sClient.Create(ctx, csv)).To(Succeed())
		namespacedName := types.NamespacedName{Name: csv.GetName(), Namespace: csv.GetNamespace()}
		operatorCondition := &operatorsv2.OperatorCondition{}

		// Wait 10 seconds
		// Background: This test could pass simply because the controller hasn't reconciled the Copied CSV yet.
		// However, this test does sound an alarm if the controller ever reconciles a copied CSV.
		// TODO: Improve this test by identifying a way to identify that the controller has not reconciling a resource.
		time.Sleep(time.Second * 10)
		err := k8sClient.Get(ctx, namespacedName, operatorCondition)
		Expect(err).ToNot(BeNil())
		Expect(k8serrors.IsNotFound(err)).To(BeTrue())
	})

	It("creates an OperatorCondition for a CSV with multiple ServiceAccounts and Deployments", func() {
		depName := genName("dep-")
		csv := &operatorsv1alpha1.ClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
				APIVersion: operatorsv1alpha1.ClusterServiceVersionAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("csv-"),
				Namespace: namespace.GetName(),
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
				InstallStrategy: newNginxInstallStrategy(depName, nil, nil),
			},
		}

		// Add additional ServiceAccounts
		serviceAccountName := genName("sa-")
		csv.Spec.InstallStrategy.StrategySpec.ClusterPermissions = []operatorsv1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccountName + "1",
				Rules:              []rbacv1.PolicyRule{},
			},
			{
				ServiceAccountName: serviceAccountName + "2",
				Rules:              []rbacv1.PolicyRule{},
			},
		}
		csv.Spec.InstallStrategy.StrategySpec.Permissions = []operatorsv1alpha1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: serviceAccountName + "3",
				Rules:              []rbacv1.PolicyRule{},
			},
			{
				ServiceAccountName: serviceAccountName + "4",
				Rules:              []rbacv1.PolicyRule{},
			},
		}

		// Duplicate the deployment
		csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs = append(csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs, csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[0])
		csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[1].Name = csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[1].Name + "2"

		Expect(k8sClient.Create(ctx, csv)).To(Succeed())

		namespacedName := types.NamespacedName{Name: csv.GetName(), Namespace: csv.GetNamespace()}
		operatorCondition := &operatorsv2.OperatorCondition{}
		// Check that an OperatorCondition was created
		Eventually(func() error {
			err := k8sClient.Get(ctx, namespacedName, operatorCondition)
			if err != nil {
				return err
			}
			if len(operatorCondition.Spec.ServiceAccounts) != 4 ||
				operatorCondition.Spec.ServiceAccounts[0] != serviceAccountName+"1" ||
				operatorCondition.Spec.ServiceAccounts[1] != serviceAccountName+"2" ||
				operatorCondition.Spec.ServiceAccounts[2] != serviceAccountName+"3" ||
				operatorCondition.Spec.ServiceAccounts[3] != serviceAccountName+"4" {
				return fmt.Errorf("operatorCondition should include the ServiceAccounts owned by the csv")
			}

			if len(operatorCondition.Spec.Deployments) != 2 ||
				operatorCondition.Spec.Deployments[0] != depName ||
				operatorCondition.Spec.Deployments[1] != depName+"2" {
				return fmt.Errorf("operatorCondition should include both of the CSV's deployments")
			}

			return nil
		}, timeout, interval).Should(Succeed())
	})
})

var singleInstance = int32(1)

func newNginxInstallStrategy(name string, permissions []operatorsv1alpha1.StrategyDeploymentPermissions, clusterPermissions []operatorsv1alpha1.StrategyDeploymentPermissions) operatorsv1alpha1.NamedInstallStrategy {
	// Create an nginx details deployment
	details := operatorsv1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
			{
				Name: name,
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "nginx"},
					},
					Replicas: &singleInstance,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "nginx"},
						},
						Spec: corev1.PodSpec{Containers: []corev1.Container{
							{
								Name:            genName("nginx"),
								Image:           "bitnami/nginx:latest",
								Ports:           []corev1.ContainerPort{{ContainerPort: 80}},
								ImagePullPolicy: corev1.PullIfNotPresent,
							},
						}},
					},
				},
			},
		},
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
	}
	namedStrategy := operatorsv1alpha1.NamedInstallStrategy{
		StrategyName: operatorsv1alpha1.InstallStrategyNameDeployment,
		StrategySpec: details,
	}

	return namedStrategy
}
