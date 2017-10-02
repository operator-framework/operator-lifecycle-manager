package catalog

import (
	"fmt"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
)

type MemoryMap struct {
	clusterservices map[string]v1alpha1.ClusterServiceVersion // map clusterservice name to CRDef
	crdToCSV        map[string]string                         // map CRD name to clusterservice name
	crds            map[string]apiextensions.CustomResourceDefinition
}

func (m *MemoryMap) FindClusterServiceVersionByName(name string) (*v1alpha1.ClusterServiceVersion, error) {
	csv, ok := m.clusterservices[name]
	if !ok {
		return nil, fmt.Errorf("Not found: ClusterServiceVersion %s", name)
	}
	return &csv, nil
}

func (m *MemoryMap) FindClusterServiceVersionForCRD(crdname string) (*v1alpha1.ClusterServiceVersion, error) {
	name, ok := m.crdToCSV[crdname]
	if !ok {
		return nil, fmt.Errorf("Not found: CRD %s", crdname)
	}
	return m.FindClusterServiceVersionForCRD(name)
}

func (m *MemoryMap) FindCRDByName(crdname string) (*apiextensions.CustomResourceDefinition, error) {
	crd, ok := m.crds[crdname]
	if !ok {
		return nil, fmt.Errorf("Not found: CRD %s", crdname)
	}
	return &crd, nil
}
