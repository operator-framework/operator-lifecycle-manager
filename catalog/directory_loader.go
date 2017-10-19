package catalog

import (
	"os"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
)

type DirectoryCatalogResourceLoader struct {
	Catalog *InMem
}

func (d *DirectoryCatalogResourceLoader) LoadCatalogResources(directory string) error {
	if err := filepath.Walk(directory, d.LoadCRDs); err != nil {
		return err
	}
	return filepath.Walk(directory, d.LoadCSVs)
}

func (d *DirectoryCatalogResourceLoader) LoadCRDs(path string, f os.FileInfo, err error) error {
	log.Infof("checking %s", path)
	if f.IsDir() {
		return nil
	}

	if !strings.HasSuffix(path, ".yaml") {
		return nil
	}

	log.Infof("loading %s", path)

	if strings.HasSuffix(path, ".crd.yaml") {
		crd, err := LoadCRDFromFile(d.Catalog, path)
		if err != nil {
			return err
		}
		log.Infof("loaded %s", crd.Name)
	}
	return nil
}

func (d *DirectoryCatalogResourceLoader) LoadCSVs(path string, f os.FileInfo, err error) error {
	log.Infof("checking %s", path)
	if f.IsDir() {
		return nil
	}

	if !strings.HasSuffix(path, ".yaml") {
		return nil
	}

	log.Infof("loading %s", path)

	if strings.HasSuffix(path, ".clusterserviceversion.yaml") {
		csv, err := LoadCSVFromFile(d.Catalog, path)
		if err != nil {
			return err
		}
		log.Infof("loaded %s", csv.Name)
	}

	return nil
}
