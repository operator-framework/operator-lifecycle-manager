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
	to          image.Reference
	from        string
}

func NewDirectoryPopulator(loader Load, graphLoader GraphLoader, querier Query, to image.Reference, from string) *DirectoryPopulator {
	return &DirectoryPopulator{
		loader:      loader,
		graphLoader: graphLoader,
		querier:     querier,
		to:          to,
		from:        from,
	}
}

func (i *DirectoryPopulator) Populate(mode Mode) error {
	path := i.from
	manifests := filepath.Join(path, "manifests")
	metadata := filepath.Join(path, "metadata")
	// Get annotations file
	log := logrus.WithFields(logrus.Fields{"dir": i.from, "file": metadata, "load": "annotations"})
	files, err := ioutil.ReadDir(metadata)
	if err != nil {
		return fmt.Errorf("unable to read directory %s: %s", metadata, err)
	}

	// Look for the metadata and manifests sub-directories to find the annotations.yaml
	// file that will inform how the manifests of the bundle should be loaded into the database.
	// If dependencies.yaml which contains operator dependencies in metadata directory
	// exists, parse and load it into the DB
	annotationsFile := &AnnotationsFile{}
	dependenciesFile := &DependenciesFile{}
	for _, f := range files {
		err = decodeFile(filepath.Join(metadata, f.Name()), annotationsFile)
		if err != nil || *annotationsFile == (AnnotationsFile{}) {
			log.Info("found annotations file searching for csv")
			continue
		}

		err = parseDependenciesFile(filepath.Join(metadata, f.Name()), dependenciesFile)
		if err != nil || len(dependenciesFile.Dependencies) < 1 {
			continue
		} else {
			log.Info("found dependencies file searching for csv")
		}
	}

	if *annotationsFile == (AnnotationsFile{}) {
		return fmt.Errorf("Could not find annotations.yaml file")
	}

	err = i.loadManifests(manifests, annotationsFile, dependenciesFile, mode)
	if err != nil {
		return err
	}

	return nil
}

func (i *DirectoryPopulator) loadManifests(manifests string, annotationsFile *AnnotationsFile, dependenciesFile *DependenciesFile, mode Mode) error {
	log := logrus.WithFields(logrus.Fields{"dir": i.from, "file": manifests, "load": "bundle"})

	csv, err := i.findCSV(manifests)
	if err != nil {
		return err
	}

	if csv.Object == nil {
		return fmt.Errorf("csv is empty: %s", err)
	}

	log.Info("found csv, loading bundle")

	csvName := csv.GetName()

	bundle, err := loadBundle(csvName, manifests)
	if err != nil {
		return fmt.Errorf("error loading objs in directory: %s", err)
	}

	if bundle == nil || bundle.Size() == 0 {
		return fmt.Errorf("no bundle objects found")
	}

	// set the bundleimage on the bundle
	bundle.BundleImage = i.to.String()
	// set the dependencies on the bundle
	bundle.Dependencies = dependenciesFile.GetDependencies()

	bundle.Name = csvName
	bundle.Package = annotationsFile.Annotations.PackageName
	bundle.Channels = strings.Split(annotationsFile.Annotations.Channels, ",")

	if err := bundle.AllProvidedAPIsInBundle(); err != nil {
		return fmt.Errorf("error checking provided apis in bundle %s: %s", bundle.Name, err)
	}

	switch mode {
	case ReplacesMode:
		err = i.loadManifestsReplaces(bundle, annotationsFile)
		if err != nil {
			return err
		}
	case SemVerMode:
		err = i.loadManifestsSemver(bundle, annotationsFile, false)
		if err != nil {
			return err
		}
	case SkipPatchMode:
		err = i.loadManifestsSemver(bundle, annotationsFile, true)
		if err != nil {
			return err
		}
	default:
		err = fmt.Errorf("Unsupported update mode")
	}

	// Finally let's delete all the old bundles
	if err = i.loader.ClearNonHeadBundles(); err != nil {
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
		if err = decodeFile(path, obj); err != nil {
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
func (i *DirectoryPopulator) findCSV(manifests string) (*unstructured.Unstructured, error) {
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
		if err = decodeFile(path, obj); err != nil {
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

// decodeFile decodes the file at a path into the given interface.
func decodeFile(path string, into interface{}) error {
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
	err := decodeFile(path, &deps)
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
		case "olm.gvk", "olm.package":
			dep.Value = string(jsonStr)
		default:
			return fmt.Errorf("Unsupported dependency type %s", dep.GetType())
		}
		depList = append(depList, dep)
	}

	depFile.Dependencies = depList

	return nil
}
