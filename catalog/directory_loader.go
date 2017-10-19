package catalog

import (
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

// DirectoryCatalogResourceLoader loads a directory of resources into the in-memory catalog
// files ending in `.crd.yaml` will be parsed as CRDs
// files ending in`.clusterserviceversion.yaml` will be parsed as CRDs
type DirectoryCatalogResourceLoader struct {
	Catalog *InMem
}

func (d *DirectoryCatalogResourceLoader) LoadCatalogResources(directory string) error {
	if err := filepath.Walk(directory, d.LoadCRDsWalkFunc); err != nil {
		return err
	}
	return filepath.Walk(directory, d.LoadCSVsWalkFunc)
}

func (d *DirectoryCatalogResourceLoader) LoadCRDsWalkFunc(path string, f os.FileInfo, err error) error {
	log.Debugf("loading %s", path)
	if f.IsDir() {
		return nil
	}
	if strings.HasSuffix(path, ".crd.yaml") {
		crd, err := LoadCRDFromFile(d.Catalog, path)
		if err != nil {
			return err
		}
		log.Debugf("loaded %s", crd.Name)
	}
	return nil
}

func (d *DirectoryCatalogResourceLoader) LoadCSVsWalkFunc(path string, f os.FileInfo, err error) error {
	log.Debugf("loading %s", path)

	if strings.HasSuffix(path, ".clusterserviceversion.yaml") {
		csv, err := LoadCSVFromFile(d.Catalog, path)
		if err != nil {
			return err
		}
		log.Debugf("loaded %s", csv.Name)
	}

	return nil
}
