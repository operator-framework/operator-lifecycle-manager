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
	return filepath.Walk(directory, d.LoadCatalogResource)
}

func (d *DirectoryCatalogResourceLoader) LoadCatalogResource(path string, f os.FileInfo, err error) error {
	if f.IsDir() {
		return nil
	}

	if !strings.HasSuffix(path, ".yaml") {
		return nil
	}

	if strings.HasSuffix(path, ".clusterserviceversion.yaml") {
		csv, err := LoadCSVFromFile(d.Catalog, path)
		if err != nil {
			return err
		}
		log.Infof("loaded %s", csv.Name)
	}
	if strings.HasSuffix(path, ".crd.yaml") {
		crd, err := LoadCRDFromFile(d.Catalog, path)
		if err != nil {
			return err
		}
		log.Infof("loaded %s", crd.Name)
	}
	return nil
}
