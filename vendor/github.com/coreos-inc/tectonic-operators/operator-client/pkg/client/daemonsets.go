package client

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang/glog"
	appsv1beta2 "k8s.io/api/apps/v1beta2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	daemonsetRolloutPollInterval = time.Second
)

// GetDaemonSet returns the DaemonSet object for the given namespace and name.
func (c *Client) GetDaemonSet(namespace, name string) (*appsv1beta2.DaemonSet, error) {
	glog.V(4).Infof("[GET DaemonSet]: %s:%s", namespace, name)
	return c.AppsV1beta2().DaemonSets(namespace).Get(name, metav1.GetOptions{})
}

// CreateDaemonSet creates the DaemonSet object.
func (c *Client) CreateDaemonSet(ds *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, error) {
	glog.V(4).Infof("[CREATE DaemonSet]: %s:%s", ds.Namespace, ds.Name)
	return c.AppsV1beta2().DaemonSets(ds.Namespace).Create(ds)
}

// DeleteDaemonSet deletes the DaemonSet object.
func (c *Client) DeleteDaemonSet(namespace, name string, options *metav1.DeleteOptions) error {
	glog.V(4).Infof("[DELETE DaemonSet]: %s:%s", namespace, name)
	return c.AppsV1beta2().DaemonSets(namespace).Delete(name, options)
}

// UpdateDaemonSet updates a DaemonSet object by performing a 2-way patch between the existing
// DaemonSet and the result of the UpdateFunction.
//
// Returns the latest DaemonSet and true if it was updated, or an error.
func (c *Client) UpdateDaemonSet(ds *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error) {
	return c.PatchDaemonSet(nil, ds)
}

// PatchDaemonSet updates a DaemonSet object by performing a 3-way patch merge between the existing
// DaemonSet and `original` and `modified` manifests.
//
// Returns the latest DaemonSet and true if it was updated, or an error.
func (c *Client) PatchDaemonSet(original, modified *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error) {
	namespace, name := modified.Namespace, modified.Name
	glog.V(4).Infof("[PATCH DaemonSet]: %s:%s", namespace, name)

	current, err := c.AppsV1beta2().DaemonSets(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("error getting existing DaemonSet %s for patch: %v", name, err)
	}
	if modified == nil {
		return nil, false, errors.New("modified cannot be nil")
	}
	if original == nil {
		original = current // Emulate 2-way merge.
	}
	current.TypeMeta = modified.TypeMeta // make sure the type metas won't conflict.
	patchBytes, err := createThreeWayMergePatchPreservingCommands(original, modified, current)
	if err != nil {
		return nil, false, err
	}
	updated, err := c.AppsV1beta2().DaemonSets(namespace).Patch(name, types.StrategicMergePatchType, patchBytes)
	if err != nil {
		return nil, false, err
	}
	return updated, current.GetResourceVersion() != updated.GetResourceVersion(), nil
}

// RollingUpdateDaemonSet performs a rolling update on the given DaemonSet. It requires that the
// DaemonSet uses the RollingUpdateDaemonSetStrategyType update strategy.
func (c *Client) RollingUpdateDaemonSet(ds *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error) {
	return c.RollingUpdateDaemonSetMigrations(ds.Namespace, ds.Name, Update(ds), UpdateOpts{})
}

// RollingUpdateDaemonSetMigrations performs a rolling update on the given DaemonSet. It
// requires that the DaemonSet uses the RollingUpdateDaemonSetStrategyType update strategy.
//
// RollingUpdateDaemonSetMigrations will run any before / during / after migrations that have been
// specified in the upgrade options.
func (c *Client) RollingUpdateDaemonSetMigrations(namespace, name string, f UpdateFunction, opts UpdateOpts) (*appsv1beta2.DaemonSet, bool, error) {
	glog.V(4).Infof("[ROLLING UPDATE DaemonSet]: %s:%s", namespace, name)
	return c.RollingPatchDaemonSetMigrations(namespace, name, updateToPatch(f), opts)
}

// RollingPatchDaemonSet performs a 3-way patch merge followed by rolling update on the given
// DaemonSet. It requires that the DaemonSet uses the RollingUpdateDaemonSetStrategyType update
// strategy.
//
// RollingPatchDaemonSet will run any before / after migrations that have been specified in the
// upgrade options.
func (c *Client) RollingPatchDaemonSet(original, modified *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error) {
	return c.RollingPatchDaemonSetMigrations(modified.Namespace, modified.Name, Patch(original, modified), UpdateOpts{})
}

// RollingPatchDaemonSetMigrations performs a 3-way patch merge followed by rolling update on
// the given DaemonSet. It requires that the DaemonSet uses the RollingUpdateDaemonSetStrategyType
// update strategy.
//
// RollingPatchDaemonSetMigrations will run any before / after migrations that have been specified
// in the upgrade options.
func (c *Client) RollingPatchDaemonSetMigrations(namespace, name string, f PatchFunction, opts UpdateOpts) (*appsv1beta2.DaemonSet, bool, error) {
	glog.V(4).Infof("[ROLLING PATCH DaemonSet]: %s:%s", namespace, name)

	current, err := c.AppsV1beta2().DaemonSets(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("error getting existing DaemonSet %s for patch: %v", name, err)
	}
	if err := checkDaemonSetRollingUpdateEnabled(current); err != nil {
		return nil, false, err
	}

	if opts.Migrations != nil && len(opts.Migrations.Before) != 0 {
		if err := opts.Migrations.RunBeforeMigrations(c, namespace, name); err != nil {
			return nil, false, err
		}
		// Get object again as things may have changed during migrations.
		current, err = c.GetDaemonSet(namespace, name)
		if err != nil {
			return nil, false, err
		}
	}

	originalObj, modifiedObj, err := f(current.DeepCopy())
	if err != nil {
		return nil, false, err
	}
	// Check for nil interfaces.
	if modifiedObj == nil {
		return nil, false, errors.New("modified cannot be nil")
	}
	if originalObj == nil {
		originalObj = current // Emulate 2-way merge.
	}
	original, modified := originalObj.(*appsv1beta2.DaemonSet), modifiedObj.(*appsv1beta2.DaemonSet)
	// Check for nil pointers.
	if modified == nil {
		return nil, false, errors.New("modified cannot be nil")
	}
	if original == nil {
		original = current // Emulate 2-way merge.
	}
	current.TypeMeta = modified.TypeMeta // make sure the type metas won't conflict.
	patchBytes, err := createThreeWayMergePatchPreservingCommands(original, modified, current)
	if err != nil {
		return nil, false, err
	}
	updated, err := c.AppsV1beta2().DaemonSets(namespace).Patch(name, types.StrategicMergePatchType, patchBytes)
	if err != nil {
		return nil, false, err
	}
	if err = c.waitForDaemonSetRollout(updated); err != nil {
		return nil, false, err
	}

	if opts.Migrations != nil && len(opts.Migrations.After) != 0 {
		if err := opts.Migrations.RunAfterMigrations(c, namespace, name); err != nil {
			return nil, false, err
		}
		// Get object again as things may have changed during migrations.
		updated, err = c.GetDaemonSet(namespace, name)
		if err != nil {
			return nil, false, err
		}
	}

	return updated, current.GetResourceVersion() != updated.GetResourceVersion(), nil
}

func checkDaemonSetRollingUpdateEnabled(obj *appsv1beta2.DaemonSet) error {
	if obj.Spec.UpdateStrategy.Type != appsv1beta2.RollingUpdateDaemonSetStrategyType {
		return fmt.Errorf("DaemonSet %s/%s does not have rolling update strategy enabled", obj.Namespace, obj.Name)
	}
	return nil
}

func (c *Client) waitForDaemonSetRollout(ds *appsv1beta2.DaemonSet) error {
	return wait.PollInfinite(daemonsetRolloutPollInterval, func() (bool, error) {
		d, err := c.GetDaemonSet(ds.Namespace, ds.Name)
		if err != nil {
			// Do not return error here, as we could be updating the API Server itself, in which case we
			// want to continue waiting.
			glog.Errorf("error getting DaemonSet %s during rollout: %v", ds.Name, err)
			return false, nil
		}
		if d.Generation <= d.Status.ObservedGeneration && d.Status.UpdatedNumberScheduled == d.Status.DesiredNumberScheduled && d.Status.NumberUnavailable == 0 {
			return true, nil
		}
		return false, nil
	})
}

// CreateOrRollingUpdateDaemonSet creates the DaemonSet if it doesn't exist. If the DaemonSet
// already exists, it will update the DaemonSet and wait for it to rollout. Returns true if the
// DaemonSet was created or updated, false if there was no update.
func (c *Client) CreateOrRollingUpdateDaemonSet(ds *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error) {
	glog.V(4).Infof("[CREATE OR ROLLING UPDATE DaemonSet]: %s:%s", ds.Namespace, ds.Name)

	_, err := c.GetDaemonSet(ds.Namespace, ds.Name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}
		created, err := c.CreateDaemonSet(ds)
		if err != nil {
			return nil, false, err
		}
		return created, true, err
	}
	return c.RollingUpdateDaemonSet(ds)
}

// NumberOfDesiredPodsForDaemonSet returns the number of Pods the DaemonSet should
// be running.
func (c *Client) NumberOfDesiredPodsForDaemonSet(ds *appsv1beta2.DaemonSet) (int, error) {
	glog.V(4).Infof("[GET Available Pods DaemonSet]: %s:%s", ds.Namespace, ds.Name)

	ds, err := c.GetDaemonSet(ds.Namespace, ds.Name)
	if err != nil {
		return 0, err
	}
	return int(ds.Status.DesiredNumberScheduled), nil
}

// ListDaemonSetsWithLabels returns a list of daemonsets that matches the label selector.
// An empty list will be returned if no such daemonsets is found.
func (c *Client) ListDaemonSetsWithLabels(namespace string, labels labels.Set) (*appsv1beta2.DaemonSetList, error) {
	glog.V(4).Infof("[LIST DaemonSets] in %s, labels: %v", namespace, labels)

	opts := metav1.ListOptions{LabelSelector: labels.String()}
	return c.AppsV1beta2().DaemonSets(namespace).List(opts)
}
