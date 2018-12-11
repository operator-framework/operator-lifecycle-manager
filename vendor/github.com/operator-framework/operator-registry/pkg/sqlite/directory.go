package sqlite

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/operator-framework/operator-registry/pkg/schema"
)

type SQLPopulator interface {
	Populate() error
}

// DirectoryLoader loads a directory of resources into the database
// files ending in `.crd.yaml` will be parsed as CRDs
// files ending in `.clusterserviceversion.yaml` will be parsed as CSVs
// files ending in `.package.yaml` will be parsed as Packages
type DirectoryLoader struct {
	store     registry.Load
	directory string
}

var _ SQLPopulator = &DirectoryLoader{}

func NewSQLLoaderForDirectory(store registry.Load, directory string) *DirectoryLoader {
	return &DirectoryLoader{
		store:     store,
		directory: directory,
	}
}

func (d *DirectoryLoader) Populate() error {
	log := logrus.WithField("dir", d.directory)

	log.Info("validating manifests")
	if err := schema.CheckCatalogResources(d.directory); err != nil {
		return err
	}

	log.Info("loading Bundles")
	if err := filepath.Walk(d.directory, d.LoadBundleWalkFunc); err != nil {
		return err
	}

	log.Info("loading Packages")
	if err := filepath.Walk(d.directory, d.LoadPackagesWalkFunc); err != nil {
		return err
	}

	log.Info("extracting provided API information")
	if err := d.store.AddProvidedAPIs(); err != nil {
		return err
	}
	return nil
}

// LoadBundleWalkFunc walks the directory. When it sees a `.clusterserviceversion.yaml` file, it
// attempts to load the surrounding files in the same directory as a bundle, and stores them in the
// db for querying
func (d *DirectoryLoader) LoadBundleWalkFunc(path string, f os.FileInfo, err error) error {
	if f == nil {
		return fmt.Errorf("Not a valid file")
	}

	log := logrus.WithFields(logrus.Fields{"dir": d.directory, "file": f.Name(), "load": "bundles"})

	if f.IsDir() {
		if strings.HasPrefix(f.Name(), ".") {
			log.Info("skipping hidden directory")
			return filepath.SkipDir
		}
		log.Info("directory")
		return nil
	}

	if strings.HasPrefix(f.Name(), ".") {
		log.Info("skipping hidden file")
		return nil
	}

	if !strings.HasSuffix(path, ".clusterserviceversion.yaml") {
		log.Info("skipping non-csv file")
		return nil
	}

	log.Info("found csv, loading bundle")
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("unable to load CSV from file %s: %v", path, err)
	}
	csv := v1alpha1.ClusterServiceVersion{}
	if _, _, err = scheme.Codecs.UniversalDecoder().Decode(data, nil, &csv); err != nil {
		return fmt.Errorf("could not decode contents of file %s into CSV: %v", path, err)
	}

	bundle, err := d.LoadBundle(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("error loading objs in dir: %s", err.Error())
	}

	if bundle.Size() == 0 {
		log.Warnf("no bundle objects found")
		return nil
	}

	if err := bundle.AllProvidedAPIsInBundle(); err != nil {
		return err
	}

	return d.store.AddOperatorBundle(bundle)
}

// LoadBundle takes the directory that a CSV is in and assumes the rest of the objects in that directory
// are part of the bundle.
func (d *DirectoryLoader) LoadBundle(dir string) (*registry.Bundle, error) {
	bundle := &registry.Bundle{}
	log := logrus.WithFields(logrus.Fields{"dir": d.directory, "load": "bundle"})
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		log = log.WithField("file", f.Name())
		if f.IsDir() {
			log.Info("skipping directory")
			continue
		}

		if strings.HasPrefix(f.Name(), ".") {
			log.Info("skipping hidden file")
			continue
		}

		log.Info("loading bundle file")
		data, err := ioutil.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("unable to load bundle file %s: %v", f.Name(), err)
		}

		obj := &unstructured.Unstructured{}
		if _, _, err = registry.DefaultYAMLDecoder().Decode(data, nil, obj); err != nil {
			return nil, fmt.Errorf("could not decode contents of file %s into object: %v", f.Name(), err)
		}
		if obj != nil {
			bundle.Add(obj)
		}

	}
	return bundle, nil
}

func (d *DirectoryLoader) LoadPackagesWalkFunc(path string, f os.FileInfo, err error) error {
	log := logrus.WithFields(logrus.Fields{"dir": d.directory, "file": f.Name(), "load": "package"})
	if f == nil {
		return fmt.Errorf("Not a valid file")
	}
	if f.IsDir() {
		if strings.HasPrefix(f.Name(), ".") {
			log.Info("skipping hidden directory")
			return filepath.SkipDir
		}
		log.Info("directory")
		return nil
	}

	if strings.HasPrefix(f.Name(), ".") {
		log.Info("skipping hidden file")
		return nil
	}

	if !strings.HasSuffix(path, ".package.yaml") {
		log.Info("skipping non-package file")
		return nil
	}

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to load package from file %s: %v", path, err)
	}

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
	manifest := registry.PackageManifest{}
	if err = decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("could not decode contents of file %s into package: %v", path, err)
	}

	if err := d.store.AddPackageChannels(manifest); err != nil {
		return fmt.Errorf("error loading package into db: %s", err.Error())
	}

	return nil
}
