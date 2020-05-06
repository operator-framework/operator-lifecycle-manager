package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/operator-framework/operator-registry/pkg/image"
)

type Dependencies struct {
	RawMessage []map[string]string `json:"dependencies" yaml:"dependencies"`
}

// DirectoryPopulator loads an unpacked operator bundle from a directory into the database.
type DirectoryPopulator struct {
	loader      Load
	graphLoader GraphLoader
	querier     Query
	imageDirMap map[image.Reference]string
}

func NewDirectoryPopulator(loader Load, graphLoader GraphLoader, querier Query, imageDirMap map[image.Reference]string) *DirectoryPopulator {
	return &DirectoryPopulator{
		loader:      loader,
		graphLoader: graphLoader,
		querier:     querier,
		imageDirMap: imageDirMap,
	}
}

func (i *DirectoryPopulator) Populate(mode Mode) error {
	var errs []error
	imagesToAdd := make([]*ImageInput, 0)
	for to, from := range i.imageDirMap {
		imageInput, err := NewImageInput(to, from)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		imagesToAdd = append(imagesToAdd, imageInput)
	}

	if len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}

	err := i.loadManifests(imagesToAdd, mode)
	if err != nil {
		return err
	}

	return nil
}

func (i *DirectoryPopulator) loadManifests(imagesToAdd []*ImageInput, mode Mode) error {
	switch mode {
	case ReplacesMode:
		// TODO: This is relatively inefficient. Ideally, we should be able to use a replaces
		// graph loader to construct what the graph would look like with a set of new bundles
		// and use that to return an error if it's not valid, rather than insert one at a time
		// and reinspect the database.
		//
		// Additionally, it would be preferrable if there was a single database transaction
		// that took the updated graph as a whole as input, rather than inserting bundles of the
		// same package linearly.
		var err error
		var validImagesToAdd []*ImageInput
		for len(imagesToAdd) > 0 {
			validImagesToAdd, imagesToAdd, err = i.getNextReplacesImagesToAdd(imagesToAdd)
			if err != nil {
				return err
			}
			for _, image := range validImagesToAdd {
				err := i.loadManifestsReplaces(image.bundle, image.annotationsFile)
				if err != nil {
					return err
				}
			}
		}
	case SemVerMode:
		for _, image := range imagesToAdd {
			err := i.loadManifestsSemver(image.bundle, image.annotationsFile, false)
			if err != nil {
				return err
			}
		}
	case SkipPatchMode:
		for _, image := range imagesToAdd {
			err := i.loadManifestsSemver(image.bundle, image.annotationsFile, true)
			if err != nil {
				return err
			}
		}
	default:
		err := fmt.Errorf("Unsupported update mode")
		if err != nil {
			return err
		}
	}

	// Finally let's delete all the old bundles
	if err := i.loader.ClearNonHeadBundles(); err != nil {
		return fmt.Errorf("Error deleting previous bundles: %s", err)
	}

	return nil
}

func (i *DirectoryPopulator) loadManifestsReplaces(bundle *Bundle, annotationsFile *AnnotationsFile) error {
	channels, err := i.querier.ListChannels(context.TODO(), annotationsFile.GetName())
	existingPackageChannels := map[string]string{}
	for _, c := range channels {
		current, err := i.querier.GetCurrentCSVNameForChannel(context.TODO(), annotationsFile.GetName(), c)
		if err != nil {
			return err
		}
		existingPackageChannels[c] = current
	}

	bcsv, err := bundle.ClusterServiceVersion()
	if err != nil {
		return fmt.Errorf("error getting csv from bundle %s: %s", bundle.Name, err)
	}

	packageManifest, err := translateAnnotationsIntoPackage(annotationsFile, bcsv, existingPackageChannels)
	if err != nil {
		return fmt.Errorf("Could not translate annotations file into packageManifest %s", err)
	}

	if err := i.loadOperatorBundle(packageManifest, bundle); err != nil {
		return fmt.Errorf("Error adding package %s", err)
	}

	return nil
}

func (i *DirectoryPopulator) getNextReplacesImagesToAdd(imagesToAdd []*ImageInput) ([]*ImageInput, []*ImageInput, error) {
	remainingImages := make([]*ImageInput, 0)
	foundImages := make([]*ImageInput, 0)

	var errs []error

	// Separate these image sets per package, since multiple different packages have
	// separate graph
	imagesPerPackage := make(map[string][]*ImageInput, 0)
	for _, image := range imagesToAdd {
		pkg := image.bundle.Package
		if _, ok := imagesPerPackage[pkg]; !ok {
			newPkgImages := make([]*ImageInput, 0)
			newPkgImages = append(newPkgImages, image)
			imagesPerPackage[pkg] = newPkgImages
		} else {
			imagesPerPackage[pkg] = append(imagesPerPackage[pkg], image)
		}
	}

	for pkg, pkgImages := range imagesPerPackage {
		// keep a tally of valid and invalid images to ensure at least one
		// image per package is valid. If not, throw an error
		pkgRemainingImages := 0
		pkgFoundImages := 0

		// first, try to pull the existing package graph from the database if it exists
		graph, err := i.graphLoader.Generate(pkg)
		if err != nil && !errors.Is(err, ErrPackageNotInDatabase) {
			return nil, nil, err
		}

		var pkgErrs []error
		// then check each image to see if it can be a replacement
		replacesLoader := ReplacesGraphLoader{}
		for _, pkgImage := range pkgImages {
			canAdd, err := replacesLoader.CanAdd(pkgImage.bundle, graph)
			if err != nil {
				pkgErrs = append(pkgErrs, err)
			}
			if canAdd {
				pkgFoundImages++
				foundImages = append(foundImages, pkgImage)
			} else {
				pkgRemainingImages++
				remainingImages = append(remainingImages, pkgImage)
			}
		}

		// no new images can be added, the current iteration aggregates all the
		// errors that describe invalid bundles
		if pkgFoundImages == 0 && pkgRemainingImages > 0 {
			errs = append(errs, utilerrors.NewAggregate(pkgErrs))
		}
	}

	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}

	return foundImages, remainingImages, nil
}

func (i *DirectoryPopulator) loadManifestsSemver(bundle *Bundle, annotations *AnnotationsFile, skippatch bool) error {
	graph, err := i.graphLoader.Generate(bundle.Package)
	if err != nil && !errors.Is(err, ErrPackageNotInDatabase) {
		return err
	}

	// add to the graph
	bundleLoader := BundleGraphLoader{}
	updatedGraph, err := bundleLoader.AddBundleToGraph(bundle, graph, annotations.Annotations.DefaultChannelName, skippatch)
	if err != nil {
		return err
	}

	if err := i.loader.AddBundleSemver(updatedGraph, bundle); err != nil {
		return fmt.Errorf("error loading bundle into db: %s", err)
	}

	return nil
}

// loadBundle takes the directory that a CSV is in and assumes the rest of the objects in that directory
// are part of the bundle.
func loadBundle(csvName string, dir string) (*Bundle, error) {
	log := logrus.WithFields(logrus.Fields{"dir": dir, "load": "bundle"})
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	bundle := &Bundle{
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
		var (
			obj  = &unstructured.Unstructured{}
			path = filepath.Join(dir, f.Name())
		)
		if err = DecodeFile(path, obj); err != nil {
			log.WithError(err).Debugf("could not decode file contents for %s", path)
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

	return bundle, nil
}

// findCSV looks through the bundle directory to find a csv
func (i *ImageInput) findCSV(manifests string) (*unstructured.Unstructured, error) {
	log := logrus.WithFields(logrus.Fields{"dir": i.from, "find": "csv"})

	files, err := ioutil.ReadDir(manifests)
	if err != nil {
		return nil, fmt.Errorf("unable to read directory %s: %s", manifests, err)
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

		var (
			obj  = &unstructured.Unstructured{}
			path = filepath.Join(manifests, f.Name())
		)
		if err = DecodeFile(path, obj); err != nil {
			log.WithError(err).Debugf("could not decode file contents for %s", path)
			continue
		}

		if obj.GetKind() != clusterServiceVersionKind {
			continue
		}

		return obj, nil
	}

	return nil, fmt.Errorf("no csv found in bundle")
}

// loadOperatorBundle adds the package information to the loader's store
func (i *DirectoryPopulator) loadOperatorBundle(manifest PackageManifest, bundle *Bundle) error {
	if manifest.PackageName == "" {
		return nil
	}

	if err := i.loader.AddBundlePackageChannels(manifest, bundle); err != nil {
		return fmt.Errorf("error loading bundle into db: %s", err)
	}

	return nil
}

// translateAnnotationsIntoPackage attempts to translate the channels.yaml file at the given path into a package.yaml
func translateAnnotationsIntoPackage(annotations *AnnotationsFile, csv *ClusterServiceVersion, existingPackageChannels map[string]string) (PackageManifest, error) {
	manifest := PackageManifest{}

	for _, ch := range annotations.GetChannels() {
		existingPackageChannels[ch] = csv.GetName()
	}

	channels := []PackageChannel{}
	for c, current := range existingPackageChannels {
		channels = append(channels,
			PackageChannel{
				Name:           c,
				CurrentCSVName: current,
			})
	}

	manifest = PackageManifest{
		PackageName:        annotations.GetName(),
		DefaultChannelName: annotations.GetDefaultChannelName(),
		Channels:           channels,
	}

	return manifest, nil
}

// DecodeFile decodes the file at a path into the given interface.
func DecodeFile(path string, into interface{}) error {
	if into == nil {
		panic("programmer error: decode destination must be instantiated before decode")
	}

	fileReader, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to read file %s: %s", path, err)
	}
	defer fileReader.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)

	return decoder.Decode(into)
}

func parseDependenciesFile(path string, depFile *DependenciesFile) error {
	deps := Dependencies{}
	err := DecodeFile(path, &deps)
	if err != nil || len(deps.RawMessage) == 0 {
		return fmt.Errorf("Unable to decode the dependencies file %s", path)
	}
	depList := []Dependency{}
	for _, v := range deps.RawMessage {
		// convert map to json
		jsonStr, _ := json.Marshal(v)

		// Check dependency type
		dep := Dependency{}
		err := json.Unmarshal(jsonStr, &dep)
		if err != nil {
			return err
		}

		switch dep.GetType() {
		case GVKType, PackageType:
			dep.Value = string(jsonStr)
		default:
			return fmt.Errorf("Unsupported dependency type %s", dep.GetType())
		}
		depList = append(depList, dep)
	}

	depFile.Dependencies = depList

	return nil
}
