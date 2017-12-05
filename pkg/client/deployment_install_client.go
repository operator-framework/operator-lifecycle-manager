package client

import (
	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	v1beta1extensions "k8s.io/api/extensions/v1beta1"
	v1beta1rbac "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ErrNilObject = errors.New("Bad object supplied: <nil>")

type InstallStrategyDeploymentInterface interface {
	CreateRole(role *v1beta1rbac.Role) (*v1beta1rbac.Role, error)
	CreateRoleBinding(roleBinding *v1beta1rbac.RoleBinding) (*v1beta1rbac.RoleBinding, error)
	EnsureServiceAccount(serviceAccount *corev1.ServiceAccount) (*corev1.ServiceAccount, error)
	CreateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error)
	CreateOrUpdateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error)
	DeleteDeployment(name string) error
	GetServiceAccountByName(serviceAccountName string) (*corev1.ServiceAccount, error)
	GetDeployments(depNames []string) []*v1beta1extensions.Deployment
}

type InstallStrategyDeploymentClientForNamespace struct {
	opClient  opClient.Interface
	Namespace string
}

var _ InstallStrategyDeploymentInterface = &InstallStrategyDeploymentClientForNamespace{}

func NewInstallStrategyDeploymentClient(opClient opClient.Interface, namespace string) InstallStrategyDeploymentInterface {
	return &InstallStrategyDeploymentClientForNamespace{
		opClient:  opClient,
		Namespace: namespace,
	}
}

func (c *InstallStrategyDeploymentClientForNamespace) CreateRole(role *v1beta1rbac.Role) (*v1beta1rbac.Role, error) {
	return c.opClient.KubernetesInterface().RbacV1beta1().Roles(c.Namespace).Create(role)
}

func (c *InstallStrategyDeploymentClientForNamespace) CreateRoleBinding(roleBinding *v1beta1rbac.RoleBinding) (*v1beta1rbac.RoleBinding, error) {
	return c.opClient.KubernetesInterface().RbacV1beta1().RoleBindings(c.Namespace).Create(roleBinding)
}

func (c *InstallStrategyDeploymentClientForNamespace) EnsureServiceAccount(serviceAccount *corev1.ServiceAccount) (*corev1.ServiceAccount, error) {
	if serviceAccount == nil {
		return nil, ErrNilObject
	}

	foundAccount, err := c.opClient.GetServiceAccount(c.Namespace, serviceAccount.Name)
	if err == nil && foundAccount != nil {
		return foundAccount, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "checking for existing serviceacccount failed")
	}

	serviceAccount.SetNamespace(c.Namespace)
	createdAccount, err := c.opClient.CreateServiceAccount(serviceAccount)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, errors.Wrap(err, "creating serviceacccount failed")
	}
	if apierrors.IsAlreadyExists(err) {
		return serviceAccount, nil
	}
	return createdAccount, nil
}

func (c *InstallStrategyDeploymentClientForNamespace) CreateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error) {
	return c.opClient.CreateDeployment(deployment)
}

func (c *InstallStrategyDeploymentClientForNamespace) DeleteDeployment(name string) error {
	foregroundDelete := metav1.DeletePropagationForeground // cascading delete
	immediate := int64(0)
	immediateForegroundDelete := &metav1.DeleteOptions{GracePeriodSeconds: &immediate, PropagationPolicy: &foregroundDelete}
	return c.opClient.DeleteDeployment(c.Namespace, name, immediateForegroundDelete)
}

func (c *InstallStrategyDeploymentClientForNamespace) CreateOrUpdateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error) {
	_, d, err := c.opClient.CreateOrRollingUpdateDeployment(deployment)
	return d, err
}

func (c *InstallStrategyDeploymentClientForNamespace) GetServiceAccountByName(serviceAccountName string) (*corev1.ServiceAccount, error) {
	return c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Get(serviceAccountName, metav1.GetOptions{})
}

func (c *InstallStrategyDeploymentClientForNamespace) GetDeployments(depNames []string) (deployments []*v1beta1extensions.Deployment) {
	for _, depName := range depNames {
		fetchedDep, err := c.opClient.GetDeployment(c.Namespace, depName)
		if err == nil {
			deployments = append(deployments, fetchedDep)
		}
	}
	return
}
