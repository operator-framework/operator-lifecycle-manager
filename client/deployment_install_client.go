package client

import (
	"fmt"

	opClient "github.com/coreos-inc/operator-client/pkg/client"
	"github.com/coreos/dex/pkg/log"
	"github.com/pkg/errors"
	"k8s.io/api/core/v1"
	v1beta1extensions "k8s.io/api/extensions/v1beta1"
	v1beta1rbac "k8s.io/api/rbac/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InstallStrategyDeploymentInterface interface {
	CreateRole(role *v1beta1rbac.Role) (*v1beta1rbac.Role, error)
	CreateRoleBinding(roleBinding *v1beta1rbac.RoleBinding) (*v1beta1rbac.RoleBinding, error)
	GetOrCreateServiceAccount(serviceAccount *v1.ServiceAccount) (*v1.ServiceAccount, error)
	CreateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error)
	CheckServiceAccount(serviceAccountName string) (bool, error)
	CheckOwnedDeployments(owner metav1.ObjectMeta, deploymentSpecs []v1beta1extensions.DeploymentSpec) (bool, error)
}

type InstallStrategyDeploymentClient struct {
	opClient  opClient.Interface
	Namespace string
}

var _ InstallStrategyDeploymentInterface = &InstallStrategyDeploymentClient{}

func NewInstallStrategyDeploymentClient(opClient opClient.Interface, namespace string) InstallStrategyDeploymentInterface {
	return &InstallStrategyDeploymentClient{
		opClient:  opClient,
		Namespace: namespace,
	}
}

func (c *InstallStrategyDeploymentClient) CreateRole(role *v1beta1rbac.Role) (*v1beta1rbac.Role, error) {
	return c.opClient.KubernetesInterface().RbacV1beta1().Roles(c.Namespace).Create(role)
}

func (c *InstallStrategyDeploymentClient) CreateRoleBinding(roleBinding *v1beta1rbac.RoleBinding) (*v1beta1rbac.RoleBinding, error) {
	return c.opClient.KubernetesInterface().RbacV1beta1().RoleBindings(c.Namespace).Create(roleBinding)
}

func (c *InstallStrategyDeploymentClient) GetOrCreateServiceAccount(serviceAccount *v1.ServiceAccount) (*v1.ServiceAccount, error) {
	foundAccount, err := c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Get(serviceAccount.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "checking for existing serviceacccount failed")
	}
	if foundAccount != nil {
		return foundAccount, nil
	}

	createdAccount, err := c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Create(serviceAccount)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return createdAccount, errors.Wrap(err, "creating serviceacccount failed")
	}
	return createdAccount, nil
}

func (c *InstallStrategyDeploymentClient) CreateDeployment(deployment *v1beta1extensions.Deployment) (*v1beta1extensions.Deployment, error) {
	return c.opClient.CreateDeployment(deployment)
}

func (c *InstallStrategyDeploymentClient) CheckServiceAccount(serviceAccountName string) (bool, error) {
	if _, err := c.opClient.KubernetesInterface().CoreV1().ServiceAccounts(c.Namespace).Get(serviceAccountName, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("serviceaccount %s not found: %s", serviceAccountName, err.Error())
	}
	return true, nil
}

func (c *InstallStrategyDeploymentClient) CheckOwnedDeployments(owner metav1.ObjectMeta, deploymentSpecs []v1beta1extensions.DeploymentSpec) (bool, error) {
	existingDeployments, err := c.opClient.ListDeploymentsWithLabels(c.Namespace, map[string]string{
		"alm-owner-name":      owner.Name,
		"alm-owner-namespace": owner.Namespace,
	})
	if err != nil {
		return false, fmt.Errorf("couldn't query for existing deployments: %s", err)
	}
	if len(existingDeployments.Items) != len(deploymentSpecs) {
		log.Debugf("wrong number of deployments found. want %d, got %d", len(deploymentSpecs), len(existingDeployments.Items))
		return false, nil
	}
	return true, nil
}
