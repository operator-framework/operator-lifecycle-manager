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

type InstallStrategyDeploymentInterface interface {
	CreateRole(role *v1beta1rbac.Role) (*v1beta1rbac.Role, error)
	CreateRoleBinding(roleBinding *v1beta1rbac.RoleBinding) (*v1beta1rbac.RoleBinding, error)
	EnsureServiceAccount(serviceAccount *corev1.ServiceAccount) (*corev1.ServiceAccount, error)
	CreateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error)
	GetServiceAccountByName(serviceAccountName string) (*corev1.ServiceAccount, error)
	GetOwnedDeployments(owner metav1.ObjectMeta) (*v1beta1extensions.DeploymentList, error)
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
	foundAccount, err := c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Get(serviceAccount.Name, metav1.GetOptions{})
	if err == nil {
		return foundAccount, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "checking for existing serviceacccount failed")
	}

	createdAccount, err := c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Create(serviceAccount)
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

func (c *InstallStrategyDeploymentClientForNamespace) GetServiceAccountByName(serviceAccountName string) (*corev1.ServiceAccount, error) {
	return c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Get(serviceAccountName, metav1.GetOptions{})
}

func (c *InstallStrategyDeploymentClientForNamespace) GetOwnedDeployments(owner metav1.ObjectMeta) (*v1beta1extensions.DeploymentList, error) {
	return c.opClient.ListDeploymentsWithLabels(c.Namespace, map[string]string{
		"alm-owner-name":      owner.Name,
		"alm-owner-namespace": owner.Namespace,
	})
}
