package catalog

import (
	"fmt"

	log "github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
)

// InMem - catalog source implementation that stores the data in memory in golang maps
var _ Source = &InMem{}

type CRDKey struct {
	Kind    string
	Name    string
	Version string
}

func (k CRDKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Kind, k.Name, k.Version)
}

type InMem struct {
	// map ClusterServiceVersion name to to their resource definition
	clusterservices map[string]v1alpha1.ClusterServiceVersion

	// map ClusterServiceVersions by name to metadata for the CSV that replaces it
	replaces map[string]CSVMetadata

	// map CRDs to the name of the ClusterServiceVersion that manages it
	crdToCSV map[CRDKey]string

	// map CRD to their full definition
	crds map[CRDKey]v1beta1.CustomResourceDefinition
}

func NewInMemoryFromDirectory(directory string) (*InMem, error) {
	log.Infof("loading alpha entries from directory: %s", directory)
	loader := DirectoryCatalogResourceLoader{NewInMem()}
	if err := loader.LoadCatalogResources(directory); err != nil {
		return nil, err
	}
	return loader.Catalog, nil
}

func NewInMemoryFromConfigMap(cmClient client.ConfigMapClient, namespace, cmName string) (*InMem, error) {
	log.Infof("loading ui catalog entries from a configmap: %s", cmName)
	loader := ConfigMapCatalogResourceLoader{NewInMem(), namespace, cmClient}
	if err := loader.LoadCatalogResources(cmName); err != nil {
		return nil, err
	}
	return loader.Catalog, nil
}

// NewInMem returns a ptr to a new InMem instance
func NewInMem() *InMem {
	return &InMem{
		clusterservices: map[string]v1alpha1.ClusterServiceVersion{},
		replaces:        map[string]CSVMetadata{},
		crdToCSV:        map[CRDKey]string{},
		crds:            map[CRDKey]v1beta1.CustomResourceDefinition{},
	}
}

// SetCRDDefinition sets the full resource definition of a CRD in the stored map
// only sets a new definition if one is not already set
func (m *InMem) SetCRDDefinition(crd v1beta1.CustomResourceDefinition) error {
	key := CRDKey{
		Kind:    crd.Spec.Names.Kind,
		Name:    crd.GetName(),
		Version: crd.Spec.Version,
	}
	if old, exists := m.crds[key]; exists && !equality.Semantic.DeepEqual(crd, old) {
		return fmt.Errorf("invalid CRD : definition for CRD %s already set", crd.GetName())
	}
	m.crds[key] = crd
	return nil
}

// setOrReplaceCRDDefinition overwrites any existing definition with the same name
func (m *InMem) setOrReplaceCRDDefinition(crd v1beta1.CustomResourceDefinition) {
	m.crds[CRDKey{
		Kind:    crd.Spec.Names.Kind,
		Name:    crd.GetName(),
		Version: crd.Spec.Version,
	}] = crd
}

// findServiceConflicts collates a list of errors from conflicting catalog entries
func (m *InMem) findServiceConflicts(csv v1alpha1.ClusterServiceVersion) []error {
	name := csv.GetName()
	version := csv.Spec.Version.String()

	errs := []error{}

	// validate csv doesn't replace a csv that already has a replacement
	if replaces := csv.Spec.Replaces; replaces != "" {
		foundCSV, exists := m.replaces[replaces]
		if exists && (foundCSV.Name != name || foundCSV.Version != version) {
			err := fmt.Errorf("cannot replace CSV %s: already replaced by %v", replaces, foundCSV)
			errs = append(errs, err)
		}
	}
	// validate required CRDs
	for _, crdReq := range csv.Spec.CustomResourceDefinitions.Required {
		key := CRDKey{
			Kind:    crdReq.Kind,
			Name:    crdReq.Name,
			Version: crdReq.Version,
		}
		// validate CRD definition stored
		if _, ok := m.crds[key]; !ok {
			errs = append(errs, fmt.Errorf("missing definition for required CRD %v", key))
		}
	}

	// validate owned CRDs
	for _, crdReq := range csv.Spec.CustomResourceDefinitions.Owned {
		key := CRDKey{
			Kind:    crdReq.Kind,
			Name:    crdReq.Name,
			Version: crdReq.Version,
		}
		// validate crds have definitions stored
		if _, ok := m.crds[key]; !ok {
			errs = append(errs, fmt.Errorf("missing definition for owned CRD %v", key))
		}
		// validate crds not already managed by another service
		if manager, ok := m.crdToCSV[key]; ok && manager != crdReq.Name {
			errs = append(errs, fmt.Errorf("CRD %s already managed by %s", crdReq.Name, manager))
		}
	}
	return errs
}

// addService is a helper fn to register a new service into the catalog
// will error if `safe` is true and conflicts are found
func (m *InMem) addService(csv v1alpha1.ClusterServiceVersion, safe bool) error {
	name := csv.GetName()

	// find and log any conflicts; return with error if in `safe` mode
	if conflicts := m.findServiceConflicts(csv); len(conflicts) > 0 {
		log.Debugf("found conflicts for CSV %s: %v", name, conflicts)
		if safe {
			return fmt.Errorf("cannot add CSV %s safely: %v", name, conflicts)
		}
	}

	// add service
	m.clusterservices[name] = csv

	// register it as replacing CSV from its spec, if any
	if csv.Spec.Replaces != "" {
		m.replaces[csv.Spec.Replaces] = CSVMetadata{
			Name:    name,
			Version: csv.Spec.Version.String(),
		}
	}

	// register its crds
	for _, crd := range csv.Spec.CustomResourceDefinitions.Owned {
		key := CRDKey{
			Name:    crd.Name,
			Version: crd.Version,
			Kind:    crd.Kind,
		}
		m.crdToCSV[key] = name
	}
	return nil
}

// setCSVDefinition registers a new service into the catalog
// will return error if any conflicts exist
func (m *InMem) setCSVDefinition(csv v1alpha1.ClusterServiceVersion) error {
	return m.addService(csv, true)
}

// AddOrReplaceService registers service into the catalog, overwriting any existing values
func (m *InMem) AddOrReplaceService(csv v1alpha1.ClusterServiceVersion) {
	_ = m.addService(csv, false)
}

// removeService is a helper fn to delete a service from the catalog
func (m *InMem) removeService(name string) error {
	csv, exists := m.clusterservices[name]
	if !exists {
		return fmt.Errorf("not found: ClusterServiceVersion %s", name)
	}

	delete(m.clusterservices, name)
	if csv.Spec.Replaces != "" {
		delete(m.replaces, csv.Spec.Replaces)
	}

	// remove any crd's registered as managed by service
	for crd, csv := range m.crdToCSV {
		if csv == name {
			delete(m.crdToCSV, crd)
		}
	}

	return nil
}

// FindCSVByName looks up the CSV with the given name.
func (m *InMem) FindCSVByName(name string) (*v1alpha1.ClusterServiceVersion, error) {
	csv, exists := m.clusterservices[name]
	if !exists {
		return nil, fmt.Errorf("not found: ClusterServiceVersion %s", name)
	}

	return &csv, nil
}

// FindReplacementCSVForCSVName looks up any CSV in the catalog that replaces the given CSV, if any.
func (m *InMem) FindReplacementCSVForName(name string) (*v1alpha1.ClusterServiceVersion, error) {
	csvMetadata, ok := m.replaces[name]
	if !ok {
		return nil, fmt.Errorf("not found: ClusterServiceVersion that replaces %s", name)
	}

	return m.FindCSVByName(csvMetadata.Name)
}

// ListServices lists all versions of the service in the catalog
func (m *InMem) ListServices() ([]v1alpha1.ClusterServiceVersion, error) {
	services := []v1alpha1.ClusterServiceVersion{}
	for _, csv := range m.clusterservices {
		services = append(services, csv)
	}
	return services, nil
}

// FindLatestCSVForCRD looks up the latest service version (by semver) that manages a given CRD
func (m *InMem) FindLatestCSVForCRD(key CRDKey) (*v1alpha1.ClusterServiceVersion, error) {
	name, ok := m.crdToCSV[key]
	if !ok {
		return nil, fmt.Errorf("not found: CRD %s", key)
	}
	return m.FindCSVByName(name)
}

// ListCSVsForCRD lists all versions of the service that manages the given CRD
func (m *InMem) ListCSVsForCRD(key CRDKey) ([]v1alpha1.ClusterServiceVersion, error) {
	csv, _ := m.FindLatestCSVForCRD(key)
	if csv == nil {
		return []v1alpha1.ClusterServiceVersion{}, nil
	}
	return []v1alpha1.ClusterServiceVersion{*csv}, nil
}

// FindCRDByName looks up the full CustomResourceDefinition for the resource with the given name
func (m *InMem) FindCRDByKey(key CRDKey) (*v1beta1.CustomResourceDefinition, error) {
	crd, ok := m.crds[key]
	if !ok {
		return nil, fmt.Errorf("not found: CRD %s", key)
	}
	return &crd, nil
}
