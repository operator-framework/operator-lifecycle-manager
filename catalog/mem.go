package catalog

import (
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	"reflect"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// InMem - catalog source implementation that stores the data in memory in golang maps
var _ Source = &InMem{}

type InMem struct {
	// map ClusterServiceVersion name to it's full resource definition
	clusterservices map[string]v1alpha1.ClusterServiceVersion

	// map replaces value to a ClusterServiceVersion that replaces it
	replaces map[string]v1alpha1.ClusterServiceVersion

	// map CRDs by name to the ClusterServiceVersion that manages it
	crdToCSV map[string]string

	// map CRD names to their full definition
	crds map[string]apiextensions.CustomResourceDefinition
}

// NewInMem returns a ptr to a new InMem instance
// currently a no-op wrapper
func NewInMem() *InMem {
	return &InMem{
		clusterservices: map[string]v1alpha1.ClusterServiceVersion{},
		crdToCSV:        map[string]string{},
		crds:            map[string]apiextensions.CustomResourceDefinition{},
	}
}

// addService is a helper fn to register a new service into the catalog
func (m *InMem) addService(csv v1alpha1.ClusterServiceVersion, managedCRDs []apiextensions.CustomResourceDefinition) error {
	name := csv.GetName()

	// validate csv doesn't already exist and no other csv manages the same crds
	if _, exists := m.clusterservices[name]; exists {
		return fmt.Errorf("already exists: ClusterServiceVersion %s", name)
	}

	// validate csv doesn't replace a csv that already has a replacement
	if foundCSV, exists := m.replaces[csv.Spec.Replaces]; exists {
		return fmt.Errorf("%s tries to replace %s, which has a replacement already: %s", name, csv.Spec.Replaces, foundCSV.Name)
	}

	// validate crd's not already managed by another service
	invalidCRDs := []string{}
	for _, crdef := range managedCRDs {
		crd := crdef.GetName()
		foundCRD, exists := m.crdToCSV[crd]
		if !exists {
			continue
		}
		// only error if the added crd is different from what we've stored already
		if !reflect.DeepEqual(foundCRD, crdef) {
			invalidCRDs = append(invalidCRDs, crd)
		}

	}
	if len(invalidCRDs) > 0 {
		return fmt.Errorf("invalid CRDs: %v", invalidCRDs)
	}

	// add service
	m.clusterservices[name] = csv
	if csv.Spec.Replaces != "" {
		m.replaces[csv.Spec.Replaces] = csv
	}

	// register its crds
	for _, crd := range managedCRDs {
		m.crdToCSV[crd.GetName()] = name
		m.crds[crd.GetName()] = crd
	}
	return nil
}

// removeService is a helper fn to delete a service from the catalog
func (m *InMem) removeService(name string) error {
	foundCSV, exists := m.clusterservices[name]
	if !exists {
		return fmt.Errorf("not found: ClusterServiceVersion %s", name)
	}
	delete(m.clusterservices, name)

	if foundCSV.Spec.Replaces != "" {
		delete(m.replaces, foundCSV.Spec.Replaces)
	}

	// remove any crd's registered as managed by service
	for crd, csv := range m.crdToCSV {
		if csv == name {
			delete(m.crdToCSV, crd)
			delete(m.crds, crd)
		}
	}
	return nil
}

func (m *InMem) FindClusterServiceVersionByServiceName(name string) (*v1alpha1.ClusterServiceVersion, error) {
	csv, ok := m.clusterservices[name]
	if !ok {
		return nil, fmt.Errorf("not found: ClusterServiceVersion %s", name)
	}
	return &csv, nil
}
func (m *InMem) FindClusterServiceVersionForServiceNameAndVersion(name, version string) (*v1alpha1.ClusterServiceVersion, error) {
	csv, ok := m.clusterservices[name]
	if !ok {
		return nil, fmt.Errorf("not found: ClusterServiceVersion %s", name)
	}
	return &csv, nil
}

func (m *InMem) FindClusterServiceByReplaces(name string) (*v1alpha1.ClusterServiceVersion, error) {
	csv, ok := m.replaces[name]
	if !ok {
		return nil, fmt.Errorf("not found: ClusterServiceVersion that replaces %s", name)
	}
	return &csv, nil
}

func (m *InMem) FindClusterServiceVersionForCRD(crdname string) (*v1alpha1.ClusterServiceVersion, error) {
	name, ok := m.crdToCSV[crdname]
	if !ok {
		return nil, fmt.Errorf("not found: CRD %s", crdname)
	}
	return m.FindClusterServiceVersionForCRD(name)
}

func (m *InMem) FindCRDByName(crdname string) (*apiextensions.CustomResourceDefinition, error) {
	crd, ok := m.crds[crdname]
	if !ok {
		return nil, fmt.Errorf("not found: CRD %s", crdname)
	}
	return &crd, nil
}
