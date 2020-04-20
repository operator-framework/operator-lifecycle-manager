package sqlite

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

const ClusterServiceVersionKind = "ClusterServiceVersion"

type SQLPopulator interface {
	Populate() error
}

// DirectoryLoader loads a directory of resources into the database
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

	log.Info("loading Bundles")
	errs := make([]error, 0)
	if err := filepath.Walk(d.directory, collectWalkErrs(d.LoadBundleWalkFunc, &errs)); err != nil {
		errs = append(errs, err)
	}

	log.Info("loading Packages and Entries")
	if err := filepath.Walk(d.directory, collectWalkErrs(d.LoadPackagesWalkFunc, &errs)); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// collectWalkErrs calls the given walk func and appends any non-nil, non skip dir error returned to the given errors slice.
func collectWalkErrs(walk filepath.WalkFunc, errs *[]error) filepath.WalkFunc {
	return func(path string, f os.FileInfo, err error) (walkErr error) {
		if walkErr = walk(path, f, err); walkErr != nil && walkErr != filepath.SkipDir {
			*errs = append(*errs, walkErr)
			return nil
		}

		return walkErr
	}
}

// LoadBundleWalkFunc walks the directory. When it sees a `.clusterserviceversion.yaml` file, it
// attempts to load the surrounding files in the same directory as a bundle, and stores them in the
// db for querying
func (d *DirectoryLoader) LoadBundleWalkFunc(path string, f os.FileInfo, err error) error {
	if f == nil {
		return fmt.Errorf("invalid file: %v", f)
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

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to load file %s: %s", path, err)
	}

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
	csv := unstructured.Unstructured{}

	if err = decoder.Decode(&csv); err != nil {
		return nil
	}

	if csv.GetKind() != ClusterServiceVersionKind {
		return nil
	}

	log.Info("found csv, loading bundle")

	var errs []error
	bundle, err := loadBundle(csv.GetName(), filepath.Dir(path))
	if err != nil {
		errs = append(errs, fmt.Errorf("error loading objs in directory: %s", err))
	}

	if bundle == nil || bundle.Size() == 0 {
		errs = append(errs, fmt.Errorf("no bundle objects found"))
		return utilerrors.NewAggregate(errs)
	}

	if err := bundle.AllProvidedAPIsInBundle(); err != nil {
		errs = append(errs, fmt.Errorf("error checking provided apis in bundle %s: %s", bundle.Name, err))
	}

	if err := d.store.AddOperatorBundle(bundle); err != nil {
		version, _ := bundle.Version()
		errs = append(errs, fmt.Errorf("error adding operator bundle %s/%s/%s: %s", csv.GetName(), version, bundle.BundleImage, err))
	}

	return utilerrors.NewAggregate(errs)
}

// LoadPackagesWalkFunc attempts to unmarshal the file at the given path into a PackageManifest resource.
// If unmarshaling is successful, the PackageManifest is added to the loader's store.
func (d *DirectoryLoader) LoadPackagesWalkFunc(path string, f os.FileInfo, err error) error {
	if f == nil {
		return fmt.Errorf("invalid file: %v", f)
	}

	log := logrus.WithFields(logrus.Fields{"dir": d.directory, "file": f.Name(), "load": "package"})
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

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to load package from file %s: %s", path, err)
	}

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
	manifest := registry.PackageManifest{}
	if err = decoder.Decode(&manifest); err != nil {
		if err != nil {
			return fmt.Errorf("could not decode contents of file %s into package: %s", path, err)
		}

	}
	if manifest.PackageName == "" {
		return nil
	}

	if err := d.store.AddPackageChannels(manifest); err != nil {
		return fmt.Errorf("error loading package into db: %s", err)
	}

	return nil
}

// loadBundle takes the directory that a CSV is in and assumes the rest of the objects in that directory
// are part of the bundle.
func loadBundle(csvName string, dir string) (*registry.Bundle, error) {
	log := logrus.WithFields(logrus.Fields{"dir": dir, "load": "bundle", "name": csvName})
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var errs []error
	bundle := &registry.Bundle{
		Name: csvName,
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
		path := filepath.Join(dir, f.Name())
		fileReader, err := os.Open(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to load file %s: %s", path, err))
			continue
		}

		decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
		obj := &unstructured.Unstructured{}
		if err = decoder.Decode(obj); err != nil {
			logrus.WithError(err).Debugf("could not decode file contents for %s", path)
			continue
		}

		// Don't include other CSVs in the bundle
		if obj.GetKind() == "ClusterServiceVersion" && obj.GetName() != csvName {
			continue
		}

		if obj.Object != nil {
			bundle.Add(obj)
		}
	}

	return bundle, utilerrors.NewAggregate(errs)
}
