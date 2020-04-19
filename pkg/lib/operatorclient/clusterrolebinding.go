package operatorclient

import (
	"context"
	"fmt"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
)

// CreateRoleBinding creates the roleBinding.
func (c *Client) CreateClusterRoleBinding(ig *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error) {
	return c.RbacV1().ClusterRoleBindings().Create(context.TODO(), ig, metav1.CreateOptions{})
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
