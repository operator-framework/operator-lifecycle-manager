package client

import (
	"encoding/json"
	"fmt"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/coreos-inc/tectonic-operators/operator-client/pkg/types"
)

// CreateMigrationStatus creates the MigrationStatus.
func (c *Client) CreateMigrationStatus(ms *types.MigrationStatus) (*types.MigrationStatus, error) {
	glog.V(4).Infof("[CREATE MigrationStatus]: %s:%s", ms.GetNamespace(), ms.GetName())
	r, err := unstructuredFromMigrationStatus(ms)
	if err != nil {
		return nil, fmt.Errorf("unable to convert MigrationStatus into unstructured.Unstructured: %v", err)
	}
	err = c.CreateCustomResource(r)
	if err != nil {
		return nil, fmt.Errorf("error creating MigrationStatus: %v", err)
	}
	return c.GetMigrationStatus(ms.GetName())
}

// GetMigrationStatus returns the MigrationStatus for the given name.
func (c *Client) GetMigrationStatus(name string) (*types.MigrationStatus, error) {
	glog.V(4).Infof("[GET MigrationStatus]: %s:%s", types.TectonicNamespace, name)
	obj, err := c.GetCustomResource(
		types.MigrationAPIGroup,
		types.MigrationGroupVersion,
		types.TectonicNamespace,
		types.MigrationStatusKind,
		name,
	)
	if err != nil {
		return nil, err
	}
	return migrationStatusFromUnstructured(obj)
}

// UpdateMigrationStatus will update the given MigrationStatus.
func (c *Client) UpdateMigrationStatus(ms *types.MigrationStatus) (*types.MigrationStatus, error) {
	glog.V(4).Infof("[UPDATE MigrationStatus]: %s:%s", ms.GetNamespace(), ms.GetName())
	r, err := unstructuredFromMigrationStatus(ms)
	if err != nil {
		return nil, fmt.Errorf("unable to convert MigrationStatus %s into unstructured.Unstructured: %v", ms.GetName(), err)
	}
	err = c.UpdateCustomResource(r)
	if err != nil {
		return nil, fmt.Errorf("error updating MigrationStatus %s: %v", ms.GetName(), err)
	}
	return c.GetMigrationStatus(ms.GetName())
}

func unstructuredFromMigrationStatus(ms *types.MigrationStatus) (*unstructured.Unstructured, error) {
	mb, err := json.Marshal(ms)
	if err != nil {
		return nil, fmt.Errorf("error marshaling MigrationStatus resource: %v", err)
	}
	var r unstructured.Unstructured
	if err := json.Unmarshal(mb, &r.Object); err != nil {
		return nil, fmt.Errorf("error unmarshaling marshaled resource: %v", err)
	}
	return &r, nil
}

func migrationStatusFromUnstructured(r *unstructured.Unstructured) (*types.MigrationStatus, error) {
	mb, err := json.Marshal(r.Object)
	if err != nil {
		return nil, fmt.Errorf("error marshaling unstructured resource: %v", err)
	}
	var ms types.MigrationStatus
	if err := json.Unmarshal(mb, &ms); err != nil {
		return nil, fmt.Errorf("error unmarshmaling marshaled resource to MigrationStatus: %v", err)
	}
	if ms.Versions == nil {
		ms.Versions = make(types.MigrationVersions)
	}
	return &ms, nil
}
