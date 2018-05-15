package client

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	optypes "github.com/coreos-inc/tectonic-operators/operator-client/pkg/types"
)

// CreateConfigMap will create the given ConfigMap resource.
func (c *Client) CreateConfigMap(namespace string, cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	glog.V(4).Infof("[CREATE ConfigMap]: %s", cm.GetName())
	return c.CoreV1().ConfigMaps(namespace).Create(cm)
}

// GetConfigMap will return the ConfigMap resource for the given name.
func (c *Client) GetConfigMap(namespace, name string) (*v1.ConfigMap, error) {
	glog.V(4).Infof("[GET ConfigMap]: %s", name)
	return c.CoreV1().ConfigMaps(namespace).Get(name, metav1.GetOptions{})
}

// UpdateConfigMap will update the given ConfigMap resource.
func (c *Client) UpdateConfigMap(cm *v1.ConfigMap) (*v1.ConfigMap, error) {
	glog.V(4).Infof("[UPDATE ConfigMap]: %s", cm.GetName())
	oldCm, err := c.GetConfigMap(cm.GetNamespace(), cm.GetName())
	if err != nil {
		return nil, err
	}
	patchBytes, err := createPatch(oldCm, cm)
	if err != nil {
		return nil, fmt.Errorf("error creating patch for ConfigMap: %v", err)
	}
	return c.CoreV1().ConfigMaps(cm.GetNamespace()).Patch(cm.GetName(), types.StrategicMergePatchType, patchBytes)
}

// AtomicUpdateConfigMap takes an update function which is executed before attempting
// to update the ConfigMap resource. Upon conflict, the update function is run
// again, until the update is successful or a non-conflict error is returned.
func (c *Client) AtomicUpdateConfigMap(namespace, name string, f optypes.ConfigMapModifier) (*v1.ConfigMap, error) {
	glog.V(4).Infof("[ATOMIC UPDATE ConfigMap]: %s", name)
	var ncm *v1.ConfigMap
	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5,
	}, func() (bool, error) {
		cm, err := c.GetConfigMap(namespace, name)
		if err != nil {
			return false, err
		}
		if err = f(cm); err != nil {
			return false, err
		}
		ncm, err = c.UpdateConfigMap(cm)
		if err != nil {
			if errors.IsConflict(err) {
				glog.Warningf("conflict updating ConfigMap resource, will try again: %v", err)
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	return ncm, err
}

// ListConfigMapsWithLabels list the ConfigMaps which contain the given labels.
// An empty list will be returned if no such ConfigMaps are found.
func (c *Client) ListConfigMapsWithLabels(namespace string, labels labels.Set) (*v1.ConfigMapList, error) {
	glog.V(4).Infof("[LIST ConfigMaps] with labels: %s", labels)
	opts := metav1.ListOptions{LabelSelector: labels.String()}
	return c.CoreV1().ConfigMaps(namespace).List(opts)
}

// DeleteConfigMap deletes the ConfigMap with the given name.
func (c *Client) DeleteConfigMap(namespace, name string, options *metav1.DeleteOptions) error {
	glog.V(4).Infof("DELETE ConfigMap]: %s", name)
	return c.CoreV1().ConfigMaps(namespace).Delete(name, options)
}
