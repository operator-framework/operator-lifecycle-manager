package client

import (
	"fmt"

	"github.com/golang/glog"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// CreateIngress creates the ingress.
func (c *Client) CreateIngress(ig *extensionsv1beta1.Ingress) (*extensionsv1beta1.Ingress, error) {
	return c.ExtensionsV1beta1().Ingresses(ig.GetNamespace()).Create(ig)
}

// GetIngress returns the existing ingress.
func (c *Client) GetIngress(namespace, name string) (*extensionsv1beta1.Ingress, error) {
	return c.ExtensionsV1beta1().Ingresses(namespace).Get(name, metav1.GetOptions{})
}

// DeleteIngress deletes the ingress.
func (c *Client) DeleteIngress(namespace, name string, options *metav1.DeleteOptions) error {
	return c.ExtensionsV1beta1().Ingresses(namespace).Delete(name, options)
}

// UpdateIngress will update an ingress object by performing a 3-way patch merge between the 'current'
// `original`, and `modified` ingress object. `modified` cannot be nil. If `original` is ommitted then a
// 2-way patch between `modified` and the `current` is performed instead. Returns the latest
// ingress and true if it was updated, or an error.
func (c *Client) UpdateIngress(original, modified *extensionsv1beta1.Ingress) (*extensionsv1beta1.Ingress, bool, error) {
	if modified == nil {
		panic("modified cannot be nil")
	}
	name, namespace := modified.GetName(), modified.GetNamespace()
	glog.V(4).Infof("[UPDATE Ingress]: %s:%s", namespace, name)
	cur, err := c.GetIngress(namespace, name)
	if err != nil {
		return nil, false, fmt.Errorf("error getting existing Service %s for patch: %v", name, err)
	}
	curResourceVersion := cur.GetResourceVersion()
	if original == nil {
		original = cur // Emulate 2-way merge.
	}
	cur.TypeMeta = modified.TypeMeta // make sure the type metas won't conflict.
	patchBytes, err := createThreeWayMergePatchPreservingCommands(original, modified, cur)
	if err != nil {
		return nil, false, err
	}
	svc, err := c.ExtensionsV1beta1().Ingresses(namespace).Patch(name, types.StrategicMergePatchType, patchBytes)
	if err != nil {
		return nil, false, err
	}
	updated := svc.GetResourceVersion() != curResourceVersion
	return svc, updated, nil
}
