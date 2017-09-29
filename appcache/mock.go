package appcache

import (
	"fmt"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
)

type Mock struct {
	cloudservices map[string]v1alpha1.OperatorVersion // map from cloudservice name to obj
	crdToCSV      map[string]string                   // map from CRD name to cloudservice name
	crds          map[string]apiextensions.CustomResourceDefinition
}

func (m *Mock) FindCloudServiceVersionByName(name string) (*v1alpha1.OperatorVersion, error) {
	csv, ok := m.cloudservices[name]
	if !ok {
		return nil, fmt.Errorf("Not found: CloudServiceVersion %s", name)
	}
	return &csv, nil
}

func (m *Mock) FindCloudServiceVersionForCRD(crdname string) (*v1alpha1.OperatorVersion, error) {
	name, ok := m.crdToCSV[crdname]
	if !ok {
		return nil, fmt.Errorf("Not found: CRD %s", crdname)
	}
	return m.FindCloudServiceVersionForCRD(name)
}

func (m *Mock) FindCRDByName(crdname string) (*apiextensions.CustomResourceDefinition, error) {
	crd, ok := m.crds[crdname]
	if !ok {
		return nil, fmt.Errorf("Not found: CRD %s", crdname)
	}
	return &crd, nil
}
