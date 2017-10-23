package catalog

import (
	"fmt"
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
	log.Debugf("Load Dir     -- BEGIN %s", directory)
	if err := filepath.Walk(directory, d.LoadCRDsWalkFunc); err != nil {
		log.Debugf("Load Dir     -- ERROR %s : CRD error=%s", directory, err)
		return fmt.Errorf("error loading CRDs from directory %s: %s", directory, err)
	}
	if err := filepath.Walk(directory, d.LoadCSVsWalkFunc); err != nil {
		log.Debugf("Load Dir     -- ERROR %s : CSV error=%s", directory, err)
		return fmt.Errorf("error loading CSVs from directory %s: %s", directory, err)
	}
	log.Debugf("Load Dir     -- OK    %s", directory)
	return nil
}

func (d *DirectoryCatalogResourceLoader) LoadCRDsWalkFunc(path string, f os.FileInfo, err error) error {
	log.Debugf("Load CRD     -- BEGIN %s", path)

	if f.IsDir() {
		log.Debugf("Load CRD     -- ISDIR %s", path)
		return nil
	}
	if strings.HasSuffix(path, ".crd.yaml") {
		crd, err := LoadCRDFromFile(d.Catalog, path)
		if err != nil {
			log.Debugf("Load CRD     -- ERROR %s", path)
			return err
		}
		log.Debugf("Load CRD     -- OK    %s", crd.Name)
	}
	log.Debugf("Load CRD     -- NO OP  %s", path)
	return nil
}

func (d *DirectoryCatalogResourceLoader) LoadCSVsWalkFunc(path string, f os.FileInfo, err error) error {
	log.Debugf("Load CSV     -- BEGIN %s", path)

	if f.IsDir() {
		log.Debugf("Load CSV     -- ISDIR %s", path)
		return nil
	}
	if strings.HasSuffix(path, ".clusterserviceversion.yaml") {
		csv, err := LoadCSVFromFile(d.Catalog, path)
		if err != nil {
			log.Debugf("Load CSV     -- ERROR %s", path)
			return err
		}
		log.Debugf("Load CSV     -- OK    %s", csv.Name)
	}
	return nil
}
