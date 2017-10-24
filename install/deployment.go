package install

import (
	"fmt"

	"github.com/coreos-inc/operator-client/pkg/client"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	rbac "k8s.io/api/rbac/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/apis"
	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

const (
	InstallStrategyNameDeployment = "deployment"
)

var BlockOwnerDeletion = true
var Controller = false

// StrategyDeploymentPermissions describe the rbac rules and service account needed by the install strategy
type StrategyDeploymentPermissions struct {
	ServiceAccountName string            `json:"serviceAccountName"`
	Rules              []rbac.PolicyRule `json:"rules"`
}

// StrategyDetailsDeployment represents the parsed details of a Deployment
// InstallStrategy.
type StrategyDetailsDeployment struct {
	DeploymentSpecs []v1beta1.DeploymentSpec        `json:"deployments"`
	Permissions     []StrategyDeploymentPermissions `json:"permissions"`
}

func (d *StrategyDetailsDeployment) Install(
	client client.Interface,
	owner metav1.ObjectMeta,
	ownerType metav1.TypeMeta,
) error {
	ownerReferences := []metav1.OwnerReference{
		{
			APIVersion:         apis.GroupName,
			Kind:               v1alpha1.ClusterServiceVersionKind,
			Name:               owner.GetName(),
			UID:                owner.UID,
			Controller:         &Controller,
			BlockOwnerDeletion: &BlockOwnerDeletion,
		},
	}
	for _, permission := range d.Permissions {
		// create role
		role := &rbac.Role{
			Rules: permission.Rules,
		}
		role.SetOwnerReferences(ownerReferences)
		role.SetGenerateName(fmt.Sprintf("%s-role-", owner.Name))
		createdRole, err := client.KubernetesInterface().RbacV1beta1().Roles(owner.Namespace).Create(role)
		if err != nil {
			return err
		}

		// create serviceaccount if necessary
		serviceAccount := &v1.ServiceAccount{}
		serviceAccount.SetOwnerReferences(ownerReferences)
		serviceAccount.SetName(permission.ServiceAccountName)
		serviceAccount, err = client.KubernetesInterface().CoreV1().ServiceAccounts(owner.Namespace).Create(serviceAccount)
		if err != nil && !errors.IsAlreadyExists(err) {
			return err
		}

		// create rolebinding
		roleBinding := &rbac.RoleBinding{
			RoleRef: rbac.RoleRef{
				Kind:     "Role",
				Name:     createdRole.GetName(),
				APIGroup: rbac.GroupName},
			Subjects: []rbac.Subject{{
				Kind:      "ServiceAccount",
				Name:      permission.ServiceAccountName,
				Namespace: owner.Namespace,
			}},
		}
		roleBinding.SetOwnerReferences(ownerReferences)
		roleBinding.SetGenerateName(fmt.Sprintf("%s-%s-rolebinding-", createdRole.Name, serviceAccount.Name))

		if _, err = client.KubernetesInterface().RbacV1beta1().RoleBindings(owner.Namespace).Create(roleBinding); err != nil {
			return err
		}
	}

	for _, spec := range d.DeploymentSpecs {
		dep := v1beta1.Deployment{Spec: spec}
		dep.SetNamespace(owner.Namespace)
		dep.SetOwnerReferences(ownerReferences)
		dep.SetGenerateName(fmt.Sprintf("%s-", owner.Name))
		if dep.Labels == nil {
			dep.SetLabels(map[string]string{})
		}
		dep.Labels["alm-owned"] = "true"
		dep.Labels["alm-owner-name"] = owner.Name
		dep.Labels["alm-owner-namespace"] = owner.Namespace
		_, err := client.CreateDeployment(&dep)
		return err
	}
	return nil
}

func (d *StrategyDetailsDeployment) CheckInstalled(client client.Interface, owner metav1.ObjectMeta) (bool, error) {
	existingDeployments, err := client.ListDeploymentsWithLabels(
		owner.Namespace,
		map[string]string{
			"alm-owned":           "true",
			"alm-owner-name":      owner.Name,
			"alm-owner-namespace": owner.Namespace,
		},
	)
	if err != nil {
		return false, fmt.Errorf("couldn't query for existing deployments: %s", err)
	}
	if len(existingDeployments.Items) == len(d.DeploymentSpecs) {
		return true, nil
	}
	return false, nil
}
