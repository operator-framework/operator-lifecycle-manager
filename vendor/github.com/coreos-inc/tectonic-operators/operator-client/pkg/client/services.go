package client

import (
	"errors"
	"fmt"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
)

// GetService returns the Service object for the given namespace and name.
func (c *Client) GetService(namespace, name string) (*v1.Service, error) {
	glog.V(4).Infof("[GET Service]: %s:%s", namespace, name)
	return c.CoreV1().Services(namespace).Get(name, metav1.GetOptions{})
}

// CreateService creates the Service object.
func (c *Client) CreateService(svc *v1.Service) (*v1.Service, error) {
	glog.V(4).Infof("[CREATE Service]: %s:%s", svc.Namespace, svc.Name)
	return c.CoreV1().Services(svc.Namespace).Create(svc)
}

// DeleteService deletes the Service object for the given namespace and name.
func (c *Client) DeleteService(namespace, name string, options *metav1.DeleteOptions) error {
	glog.V(4).Infof("[DELETE Service]: %s:%s", namespace, name)
	return c.CoreV1().Services(namespace).Delete(name, options)
}

// UpdateService updates a Service object by performing a 2-way patch between the existing Service
// and `svc`.
//
// Returns the latest Service and true if it was updated, or an error.
func (c *Client) UpdateService(svc *v1.Service) (*v1.Service, bool, error) {
	return c.PatchService(nil, svc)
}

// PatchService updates a Service object by performing a 3-way patch merge between the existing
// Service and `original` and `modified` manifests.
//
// Returns the latest Service and true if it was updated, or an error.
func (c *Client) PatchService(original, modified *v1.Service) (*v1.Service, bool, error) {
	return c.PatchServiceMigrations(modified.Name, modified.Namespace, Patch(original, modified), UpdateOpts{})
}

// UpdateServiceMigrations updates a Service object by performing a 3-way patch merge between the
// existing Service and `original` and `modified` manifests.
//
// This function gets the Service with the given name and namespace, and then passes it into `f`.
// `f` should return the original and modified manifests (in that order) which are then used to
// compute the 3-way patch.
//
// Returns the latest Service and true if it was updated, or an error.
func (c *Client) UpdateServiceMigrations(name, namespace string, f UpdateFunction, opts UpdateOpts) (*v1.Service, bool, error) {
	return c.PatchServiceMigrations(name, namespace, updateToPatch(f), opts)
}

// PatchServiceMigrations updates a Service object by performing a 3-way patch merge between the
// existing Service and `original` and `modified` manifests.
//
// This function gets the Service with the given name and namespace, and then passes it into `f`.
// `f` should return the original and modified manifests (in that order) which are then used to
// compute the 3-way patch.
//
// Returns the latest Service and true if it was updated, or an error.
func (c *Client) PatchServiceMigrations(name, namespace string, f PatchFunction, opts UpdateOpts) (*v1.Service, bool, error) {
	glog.V(4).Infof("[PATCH MIGRATIONS Service]: %s:%s", namespace, name)

	current, err := c.GetService(namespace, name)
	if err != nil {
		return nil, false, fmt.Errorf("error getting existing Service %s for patch: %v", name, err)
	}

	if opts.Migrations != nil && len(opts.Migrations.Before) != 0 {
		if err := opts.Migrations.RunBeforeMigrations(c, namespace, name); err != nil {
			return nil, false, err
		}
		// Get object again as things may have changed during migrations.
		current, err = c.GetService(namespace, name)
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
	original, modified := originalObj.(*v1.Service), modifiedObj.(*v1.Service)
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
	updated, err := c.CoreV1().Services(namespace).Patch(name, apitypes.StrategicMergePatchType, patchBytes)
	if err != nil {
		return nil, false, err
	}

	if opts.Migrations != nil && len(opts.Migrations.After) != 0 {
		if err := opts.Migrations.RunAfterMigrations(c, namespace, name); err != nil {
			return nil, false, err
		}
		// Get object again as things may have changed during migrations.
		updated, err = c.GetService(namespace, name)
		if err != nil {
			return nil, false, err
		}
	}

	return updated, current.GetResourceVersion() != updated.GetResourceVersion(), nil
}
