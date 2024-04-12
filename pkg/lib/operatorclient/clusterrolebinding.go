package operatorclient

import (
	"context"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	acv1 "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/klog"
)

// ApplyClusterRoleBinding applies the roleBinding.
func (c *Client) ApplyClusterRoleBinding(applyConfig *acv1.ClusterRoleBindingApplyConfiguration, applyOptions metav1.ApplyOptions) (*rbacv1.ClusterRoleBinding, error) {
	return c.RbacV1().ClusterRoleBindings().Apply(context.TODO(), applyConfig, applyOptions)
}

// CreateRoleBinding creates the roleBinding or Updates if it already exists.
func (c *Client) CreateClusterRoleBinding(ig *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error) {
	createdCRB, err := c.RbacV1().ClusterRoleBindings().Create(context.TODO(), ig, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return c.UpdateClusterRoleBinding(ig)
	}
	return createdCRB, err
}

// GetRoleBinding returns the existing roleBinding.
func (c *Client) GetClusterRoleBinding(name string) (*rbacv1.ClusterRoleBinding, error) {
	return c.RbacV1().ClusterRoleBindings().Get(context.TODO(), name, metav1.GetOptions{})
}

// DeleteRoleBinding deletes the roleBinding.
func (c *Client) DeleteClusterRoleBinding(name string, options *metav1.DeleteOptions) error {
	return c.RbacV1().ClusterRoleBindings().Delete(context.TODO(), name, *options)
}

// UpdateRoleBinding will update the given RoleBinding resource.
func (c *Client) UpdateClusterRoleBinding(crb *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error) {
	klog.V(4).Infof("[UPDATE RoleBinding]: %s", crb.GetName())
	oldCrb, err := c.GetClusterRoleBinding(crb.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldCrb, crb)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for RoleBinding: %v", err)
	}
	return c.RbacV1().ClusterRoleBindings().Patch(context.TODO(), crb.GetName(), types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
}
