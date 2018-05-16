package client

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

// GetCustomResourceDefinition gets the custom resource definition.
func (c *Client) GetCustomResourceDefinition(name string) (*v1beta1ext.CustomResourceDefinition, error) {
	glog.V(4).Infof("[GET CRD ]: %s", name)
	return c.extClientset.ApiextensionsV1beta1().CustomResourceDefinitions().Get(name, metav1.GetOptions{})
}

// CreateCustomResourceDefinition creates the custom resource definition.
func (c *Client) CreateCustomResourceDefinition(crd *v1beta1ext.CustomResourceDefinition) error {
	glog.V(4).Infof("[CREATE CRD ]: %s", crd.Name)
	_, err := c.extClientset.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	return err
}

// DeleteCustomResourceDefinition deletes the custom resource definition.
func (c *Client) DeleteCustomResourceDefinition(name string, options *metav1.DeleteOptions) error {
	glog.V(4).Infof("[DELETE CRD ]: %s", name)
	return c.extClientset.ApiextensionsV1beta1().CustomResourceDefinitions().Delete(name, options)
}

// EnsureCustomResourceDefinition will creates the CRD if it doesn't exist.
// On success, the CRD is guaranteed to exist.
func (c *Client) EnsureCustomResourceDefinition(crd *v1beta1ext.CustomResourceDefinition) error {
	return wait.PollInfinite(time.Second, func() (bool, error) {
		_, err := c.GetCustomResourceDefinition(crd.Name)
		if err == nil {
			return true, nil
		}

		if !errors.IsNotFound(err) {
			return false, err
		}

		err = c.CreateCustomResourceDefinition(crd)
		if err == nil || errors.IsAlreadyExists(err) {
			return false, nil
		}

		glog.Errorf("Failed to create CRD  %q: %v", crd.Name, err)
		return false, err
	})
}

// UpdateCustomResourceDefinition will update an existign CRD instance with the new contents.
func (c *Client) UpdateCustomResourceDefinition(modified *v1beta1ext.CustomResourceDefinition) error {
	glog.V(4).Infof("[UPDATE CustomResourceDefinition]: %s", modified.GetName())
	oldCrdk, err := c.GetCustomResourceDefinition(modified.GetName())
	if err != nil {
		return err
	}
	normalized, err := cloneAndNormalizeObject(oldCrdk)
	if err != nil {
		return fmt.Errorf("Unable to normalize CustomResourceDefinition: %v", err)
	}
	patchBytes, err := createPatch(normalized, modified)
	if err != nil {
		return fmt.Errorf("error creating patch for CustomResourceDefinition: %v", err)
	}
	_, err = c.extClientset.ApiextensionsV1beta1().CustomResourceDefinitions().Patch(modified.GetName(), types.StrategicMergePatchType, patchBytes)
	return err
}
