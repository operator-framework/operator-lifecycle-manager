package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/coreos-inc/tectonic-operators/operator-client/pkg/types"
)

// CreateAppVersion will create the given AppVersion resource.
func (c *Client) CreateAppVersion(av *types.AppVersion) (*types.AppVersion, error) {
	glog.V(4).Infof("[CREATE AppVersion]: %s", av.GetName())
	obj, err := unstructuredFromAppVersion(av)
	if err != nil {
		return nil, fmt.Errorf("error creating AppVersion %s: %v", av.GetName(), err)
	}
	err = c.CreateCustomResource(obj)
	if err != nil {
		return nil, err
	}
	return c.GetAppVersion(obj.GetName())
}

// GetAppVersion will return the AppVersion resource for the given name.
func (c *Client) GetAppVersion(name string) (*types.AppVersion, error) {
	glog.V(4).Infof("[GET AppVersion]: %s", name)
	obj, err := c.GetCustomResource(
		types.TectonicAPIGroup,
		types.AppVersionGroupVersion,
		types.TectonicNamespace,
		types.AppVersionKind,
		name,
	)
	if err != nil {
		return nil, err
	}
	return appVersionFromUnstructured(obj)
}

// UpdateAppVersion will update the given AppVersion resource.
func (c *Client) UpdateAppVersion(av *types.AppVersion) (*types.AppVersion, error) {
	glog.V(4).Infof("[UPDATE AppVersion]: %s", av.GetName())
	obj, err := unstructuredFromAppVersion(av)
	if err != nil {
		return nil, fmt.Errorf("error updating AppVersion %s: %v", av.GetName(), err)
	}
	err = c.UpdateCustomResource(obj)
	if err != nil {
		return nil, err
	}
	return c.GetAppVersion(obj.GetName())
}

// AtomicUpdateAppVersion takes an update function which is executed before attempting
// to update the AppVersion resource. Upon conflict, the update function is run
// again, until the update is successful or a non-conflict error is returned.
func (c *Client) AtomicUpdateAppVersion(name string, f types.AppVersionModifier) (*types.AppVersion, error) {
	glog.V(4).Infof("[ATOMIC UPDATE AppVersion]: %s", name)
	var nav *types.AppVersion
	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5,
	}, func() (bool, error) {
		av, err := c.GetAppVersion(name)
		if err != nil {
			return false, err
		}
		if err = f(av); err != nil {
			return false, err
		}
		nav, err = c.UpdateAppVersion(av)
		if err != nil {
			if errors.IsConflict(err) {
				glog.Warning("conflict updating AppVersion resource, will try again")
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	return nav, err
}

func unstructuredFromAppVersion(av *types.AppVersion) (*unstructured.Unstructured, error) {
	avb, err := json.Marshal(av)
	if err != nil {
		return nil, fmt.Errorf("error marshaling AppVersion resource: %v", err)
	}
	var r unstructured.Unstructured
	if err := json.Unmarshal(avb, &r.Object); err != nil {
		return nil, fmt.Errorf("error unmarshaling marshaled resource: %v", err)
	}
	return &r, nil
}

func appVersionFromUnstructured(r *unstructured.Unstructured) (*types.AppVersion, error) {
	avb, err := json.Marshal(r.Object)
	if err != nil {
		return nil, fmt.Errorf("error marshaling unstructured resource: %v", err)
	}
	var av types.AppVersion
	if err := json.Unmarshal(avb, &av); err != nil {
		return nil, fmt.Errorf("error unmarshmaling marshaled resource to TectonicAppVersion: %v", err)
	}
	return &av, nil
}

// SetFailureStatus sets the failure status in the AppVersion.Status.
// If nil is passed, then the failure status is cleared.
func (c *Client) SetFailureStatus(name string, failureStatus *types.FailureStatus) error {
	_, err := c.AtomicUpdateAppVersion(name, func(av *types.AppVersion) error {
		av.Status.FailureStatus = failureStatus
		return nil
	})
	return err
}

// SetTaskStatuses sets the task status list in the AppVersion.Status.
// If nil is passed, then the task status list is cleared.
func (c *Client) SetTaskStatuses(name string, ts []types.TaskStatus) error {
	_, err := c.AtomicUpdateAppVersion(name, func(av *types.AppVersion) error {
		av.Status.TaskStatuses = ts
		return nil
	})
	return err
}

// UpdateTaskStatus updates the task status in the AppVersion.Status.Taskstatues list.
// It will return the error if the name of the task is not found in the list.
func (c *Client) UpdateTaskStatus(name string, ts types.TaskStatus) error {
	_, err := c.AtomicUpdateAppVersion(name, func(av *types.AppVersion) error {
		var found bool
		for i, v := range av.Status.TaskStatuses {
			if v.Name == ts.Name {
				av.Status.TaskStatuses[i] = ts
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%q is not found in TaskStatus", ts.Name)
		}
		return nil
	})
	return err
}

// ListAppVersionsWithLabels list the AppVersion CRDs which contain the given labels.
// An empty list will be returned if no such AppVersions are found.
func (c *Client) ListAppVersionsWithLabels(labels string) (*types.AppVersionList, error) {
	glog.V(4).Infof("[LIST AppVersions] with labels: %s", labels)

	var appVersionList types.AppVersionList

	httpRestClient := c.extClientset.ApiextensionsV1beta1().RESTClient()
	uri := customResourceDefinitionURI(
		types.TectonicAPIGroup,
		types.AppVersionGroupVersion,
		types.TectonicNamespace,
		types.AppVersionKind)

	bytes, err := httpRestClient.
		Get().
		RequestURI(uri).
		DoRaw()
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(bytes, &appVersionList); err != nil {
		return nil, err
	}

	// TODO(yifan): Use ListOptions when it's supported for the CRD.

	// Assume only one label.
	kv := strings.Split(labels, "=")
	if len(kv) != 2 {
		return nil, fmt.Errorf("only support 1 label for now")
	}

	list := appVersionList.Items
	appVersionList.Items = nil

	for _, av := range list {
		if value := av.Labels[kv[0]]; value == kv[1] {
			appVersionList.Items = append(appVersionList.Items, av)
		}
	}

	return &appVersionList, nil
}

// DeleteAppVersion deletes the AppVersion with the given name.
func (c *Client) DeleteAppVersion(name string) error {
	glog.V(4).Infof("DELETE AppVersion]: %s", name)

	return c.DeleteCustomResource(
		types.TectonicAPIGroup,
		types.AppVersionGroupVersion,
		types.TectonicNamespace,
		types.AppVersionKind,
		name)
}
