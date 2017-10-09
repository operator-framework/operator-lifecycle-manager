package catalog

import (
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// MemoryMap - catalog source implementation that stores the data in memory in golang maps
var _ Source = &MemoryMap{}

type MemoryMap struct {
	// map ClusterServiceVersion name to it's full resource definition
	clusterservices map[string]v1alpha1.ClusterServiceVersion

	// map CRDs by name to the ClusterServiceVersion that manages it
	crdToCSV map[string]string

	// map CRD names to their full definition
	crds map[string]apiextensions.CustomResourceDefinition
}

// NewMemoryMap returns a ptr to a new MemoryMap instance
// currently a no-op wrapper
func NewMemoryMap() *MemoryMap {
	return &MemoryMap{
		clusterservices: map[string]v1alpha1.ClusterServiceVersion{},
		crdToCSV:        map[string]string{},
		crds:            map[string]apiextensions.CustomResourceDefinition{},
	}
}

// addService is a helper fn to register a new service into the catalog
func (m *MemoryMap) addService(csv v1alpha1.ClusterServiceVersion, managedcrds []apiextensions.CustomResourceDefinition) error {
	name := csv.GetName()

	// validate csv doesn't already exist and no other csv manages the same crds
	if _, exists := m.clusterservices[name]; exists {
		return fmt.Errorf("Already exists: ClusterServiceVersion %s", name)
	}
	// validate crd's not already managed by another service
	invalidCRDs := []string{}
	for _, crdef := range managedcrds {
		crd := crdef.GetName()
		if _, exists := m.crdToCSV[crd]; exists {
			invalidCRDs = append(invalidCRDs, crd)
		}
	}
	if len(invalidCRDs) > 0 {
		return fmt.Errorf("Invalid CRDs: %v", invalidCRDs)
	}
	// add service
	m.clusterservices[name] = csv
	// register it's crds
	for _, crd := range managedcrds {
		m.crdToCSV[crd.GetName()] = name
		m.crds[crd.GetName()] = crd
	}
	return nil
}

// removeService is a helper fn to delete a service from the catalog
func (m *MemoryMap) removeService(name string) error {
	if _, exists := m.clusterservices[name]; !exists {
		return fmt.Errorf("Not found: ClusterServiceVersion %s", name)
	}
	delete(m.clusterservices, name)
	// remove any crd's registered as managed by service
	for crd, csv := range m.crdToCSV {
		if csv == name {
			delete(m.crdToCSV, crd)
			delete(m.crds, crd)
		}
	}
	return nil
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
