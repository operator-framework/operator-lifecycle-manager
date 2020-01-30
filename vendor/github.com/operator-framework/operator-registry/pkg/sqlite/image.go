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

	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

// ImageLoader loads a bundle image of resources into the database
type ImageLoader struct {
	store         registry.Load
	image         string
	directory     string
	containerTool string
}

func NewSQLLoaderForImage(store registry.Load, image, containerTool string) *ImageLoader {
	return &ImageLoader{
		store:         store,
		image:         image,
		directory:     "",
		containerTool: containerTool,
	}
}

func (i *ImageLoader) Populate() error {

	log := logrus.WithField("img", i.image)

	workingDir, err := ioutil.TempDir("./", "bundle_tmp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workingDir)

	// Pull the image and get the manifests
	reader := containertools.NewImageReader(i.containerTool, log)

	err = reader.GetImageData(i.image, workingDir)
	if err != nil {
		return err
	}

	i.directory = workingDir

	log.Infof("loading Bundle %s", i.image)
	errs := make([]error, 0)
	if err := i.LoadBundleFunc(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// LoadBundleFunc walks the bundle directory. Looks for the metadata and manifests
// sub-directories to find the annotations.yaml file that will inform how the
// manifests of the bundle should be loaded into the database.
func (i *ImageLoader) LoadBundleFunc() error {
	path := i.directory
	manifests := filepath.Join(path, "manifests")
	metadata := filepath.Join(path, "metadata")

	// Get annotations file
	log := logrus.WithFields(logrus.Fields{"dir": i.directory, "file": metadata, "load": "annotations"})
	files, err := ioutil.ReadDir(metadata)
	if err != nil {
		return fmt.Errorf("unable to read directory %s: %s", metadata, err)
	}

	annotationsFile := &registry.AnnotationsFile{}
	for _, f := range files {
		fileReader, err := os.Open(filepath.Join(metadata, f.Name()))
		if err != nil {
			return fmt.Errorf("unable to read file %s: %s", f.Name(), err)
		}
		decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
		err = decoder.Decode(&annotationsFile)
		if err != nil || *annotationsFile == (registry.AnnotationsFile{}) {
			continue
		} else {
			log.Info("found annotations file searching for csv")
		}
	}

	if *annotationsFile == (registry.AnnotationsFile{}) {
		return fmt.Errorf("Could not find annotations.yaml file")
	}

	err = i.loadManifests(manifests, annotationsFile)
	if err != nil {
		return err
	}

	return nil
}

func (i *ImageLoader) loadManifests(manifests string, annotationsFile *registry.AnnotationsFile) error {
	log := logrus.WithFields(logrus.Fields{"dir": i.directory, "file": manifests, "load": "bundle"})

	csv, err := i.findCSV(manifests)
	if err != nil {
		return err
	}

	if csv.Object == nil {
		return fmt.Errorf("csv is empty: %s", err)
	}

	log.Info("found csv, loading bundle")

	// TODO: Check channels against what's in the database vs in the bundle csv

	bundle, err := loadBundle(csv.GetName(), manifests)
	if err != nil {
		return fmt.Errorf("error loading objs in directory: %s", err)
	}

	if bundle == nil || bundle.Size() == 0 {
		return fmt.Errorf("no bundle objects found")
	}

	// set the bundleimage on the bundle
	bundle.BundleImage = i.image

	if err := bundle.AllProvidedAPIsInBundle(); err != nil {
		return fmt.Errorf("error checking provided apis in bundle %s: %s", bundle.Name, err)
	}

	bcsv, err := bundle.ClusterServiceVersion()
	if err != nil {
		return fmt.Errorf("error getting csv from bundle %s: %s", bundle.Name, err)
	}

	packageManifest, err := translateAnnotationsIntoPackage(annotationsFile, bcsv)
	if err != nil {
		return fmt.Errorf("Could not translate annotations file into packageManifest %s", err)
	}

	if err := i.loadOperatorBundle(packageManifest, *bundle); err != nil {
		return fmt.Errorf("Error adding package %s", err)
	}

	// Finally let's delete all the old bundles
	if err = i.store.ClearNonDefaultBundles(packageManifest.PackageName); err != nil {
		return fmt.Errorf("Error deleting previous bundles: %s", err)
	}

	return nil
}

// findCSV looks through the bundle directory to find a csv
func (i *ImageLoader) findCSV(manifests string) (*unstructured.Unstructured, error) {
	log := logrus.WithFields(logrus.Fields{"dir": i.directory, "find": "csv"})

	files, err := ioutil.ReadDir(manifests)
	if err != nil {
		return nil, fmt.Errorf("unable to read directory %s: %s", manifests, err)
	}

	var errs []error
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

		path := filepath.Join(manifests, f.Name())
		fileReader, err := os.Open(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to read file %s: %s", path, err))
			continue
		}

		dec := yaml.NewYAMLOrJSONDecoder(fileReader, 30)
		unst := &unstructured.Unstructured{}
		if err := dec.Decode(unst); err != nil {
			continue
		}

		if unst.GetKind() != ClusterServiceVersionKind {
			continue
		}

		return unst, nil

	}

	errs = append(errs, fmt.Errorf("no csv found in bundle"))
	return nil, utilerrors.NewAggregate(errs)
}

// loadOperatorBundle adds the package information to the loader's store
func (i *ImageLoader) loadOperatorBundle(manifest registry.PackageManifest, bundle registry.Bundle) error {
	if manifest.PackageName == "" {
		return nil
	}

	if err := i.store.AddBundlePackageChannels(manifest, bundle); err != nil {
		return fmt.Errorf("error loading bundle into db: %s", err)
	}

	return nil
}

// translateAnnotationsIntoPackage attempts to translate the channels.yaml file at the given path into a package.yaml
func translateAnnotationsIntoPackage(annotations *registry.AnnotationsFile, csv *registry.ClusterServiceVersion) (registry.PackageManifest, error) {
	manifest := registry.PackageManifest{}

	channels := []registry.PackageChannel{}
	for _, ch := range annotations.GetChannels() {
		channels = append(channels,
			registry.PackageChannel{
				Name:           ch,
				CurrentCSVName: csv.GetName(),
			})
	}

	manifest = registry.PackageManifest{
		PackageName:        annotations.GetName(),
		DefaultChannelName: annotations.GetDefaultChannelName(),
		Channels:           channels,
	}

	return manifest, nil
}
