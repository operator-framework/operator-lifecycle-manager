package registry

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/image"
)

type ImageInput struct {
	manifestsDir     string
	metadataDir      string
	to               image.Reference
	from             string
	annotationsFile  *AnnotationsFile
	dependenciesFile *DependenciesFile
	bundle           *Bundle
}

func NewImageInput(to image.Reference, from string) (*ImageInput, error) {
	var annotationsFound, dependenciesFound bool
	path := from
	manifests := filepath.Join(path, "manifests")
	metadata := filepath.Join(path, "metadata")
	// Get annotations file
	log := logrus.WithFields(logrus.Fields{"dir": from, "file": metadata, "load": "annotations"})
	files, err := ioutil.ReadDir(metadata)
	if err != nil {
		return nil, fmt.Errorf("unable to read directory %s: %s", metadata, err)
	}

	// Look for the metadata and manifests sub-directories to find the annotations.yaml
	// file that will inform how the manifests of the bundle should be loaded into the database.
	// If dependencies.yaml which contains operator dependencies in metadata directory
	// exists, parse and load it into the DB
	annotationsFile := &AnnotationsFile{}
	dependenciesFile := &DependenciesFile{}
	for _, f := range files {
		if !annotationsFound {
			err = DecodeFile(filepath.Join(metadata, f.Name()), annotationsFile)
			if err == nil && *annotationsFile != (AnnotationsFile{}) {
				annotationsFound = true
				continue
			}
		}

		if !dependenciesFound {
			err = DecodeFile(filepath.Join(metadata, f.Name()), &dependenciesFile)
			if err != nil {
				return nil, err
			}
			if len(dependenciesFile.Dependencies) > 0 {
				dependenciesFound = true
			}
		}
	}

	if !annotationsFound {
		return nil, fmt.Errorf("Could not find annotations file")
	}

	if !dependenciesFound {
		log.Info("Could not find optional dependencies file")
	}

	imageInput := &ImageInput{
		manifestsDir:     manifests,
		metadataDir:      metadata,
		to:               to,
		from:             from,
		annotationsFile:  annotationsFile,
		dependenciesFile: dependenciesFile,
	}

	err = imageInput.getBundleFromManifests()
	if err != nil {
		return nil, err
	}

	return imageInput, nil
}

func (i *ImageInput) getBundleFromManifests() error {
	log := logrus.WithFields(logrus.Fields{"dir": i.from, "file": i.manifestsDir, "load": "bundle"})

	csv, err := i.findCSV(i.manifestsDir)
	if err != nil {
		return err
	}

	if csv.Object == nil {
		return fmt.Errorf("csv is empty: %s", err)
	}

	log.Info("found csv, loading bundle")

	csvName := csv.GetName()

	bundle, err := loadBundle(csvName, i.manifestsDir)
	if err != nil {
		return fmt.Errorf("error loading objs in directory: %s", err)
	}

	if bundle == nil || bundle.Size() == 0 {
		return fmt.Errorf("no bundle objects found")
	}

	// set the bundleimage on the bundle
	bundle.BundleImage = i.to.String()
	// set the dependencies on the bundle
	bundle.Dependencies = i.dependenciesFile.GetDependencies()

	bundle.Name = csvName
	bundle.Package = i.annotationsFile.Annotations.PackageName
	bundle.Channels = strings.Split(i.annotationsFile.Annotations.Channels, ",")

	if err := bundle.AllProvidedAPIsInBundle(); err != nil {
		return fmt.Errorf("error checking provided apis in bundle %s: %s", bundle.Name, err)
	}

	i.bundle = bundle

	return nil
}
