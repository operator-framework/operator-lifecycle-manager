package operatorclient

import (
	"fmt"

	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
)

// CreateRoleBinding creates the roleBinding.
func (c *Client) CreateAPIService(ig *apiregistrationv1.APIService) (*apiregistrationv1.APIService, error) {
	return c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Create(ig)
}

// GetRoleBinding returns the existing roleBinding.
func (c *Client) GetAPIService(name string) (*apiregistrationv1.APIService, error) {
	return c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Get(name, metav1.GetOptions{})
}

// DeleteRoleBinding deletes the roleBinding.
func (c *Client) DeleteAPIService(name string, options *metav1.DeleteOptions) error {
	return c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Delete(name, options)
}

// UpdateRoleBinding will update the given RoleBinding resource.
func (c *Client) UpdateAPIService(crb *apiregistrationv1.APIService) (*apiregistrationv1.APIService, error) {
	glog.V(4).Infof("[UPDATE RoleBinding]: %s", crb.GetName())
	oldCrb, err := c.GetAPIService(crb.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldCrb, crb)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for RoleBinding: %v", err)
	}
	return c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Patch(crb.GetName(), types.StrategicMergePatchType, patchBytes)
}
