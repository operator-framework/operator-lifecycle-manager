package bundle

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/operator-framework/operator-registry/pkg/containertools"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// imageValidator is a struct implementation of the Indexer interface
type imageValidator struct {
	imageReader containertools.ImageReader
	logger      *log.Entry
}

// PullBundleImage shells out to a container tool and pulls a given image tag
// Then it unpacks the image layer filesystem contents and pushes the contents
// to a specified directory for further validation
func (i imageValidator) PullBundleImage(imageTag, directory string) error {
	i.logger.Debug("Pulling and unpacking container image")

	return i.imageReader.GetImageData(imageTag, directory)
}

// ValidateBundle takes a directory containing the contents of a bundle and validates
// the format of that bundle for correctness
func (i imageValidator) ValidateBundle(directory string) error {
	var manifestsFound, metadataFound bool
	var annotationsDir, manifestsDir string
	var annotationErrors []error
	var formatErrors []error

	items, _ := ioutil.ReadDir(directory)
	for _, item := range items {
		if item.IsDir() {
			switch s := item.Name(); s {
			case strings.TrimSuffix(ManifestsDir, "/"):
				i.logger.Debug("Found manifests directory")
				manifestsFound = true
				manifestsDir = filepath.Join(directory, ManifestsDir)
			case strings.TrimSuffix(MetadataDir, "/"):
				i.logger.Debug("Found metadata directory")
				metadataFound = true
				annotationsDir = filepath.Join(directory, MetadataDir)
			}
		}
	}

	if manifestsFound == false {
		formatErrors = append(formatErrors, fmt.Errorf("Unable to locate manifests directory"))
	}
	if metadataFound == false {
		formatErrors = append(formatErrors, fmt.Errorf("Unable to locate metadata directory"))
	}

	// Break here if we can't even find the files
	if len(formatErrors) > 0 {
		return NewValidationError(annotationErrors, formatErrors)
	}

	i.logger.Debug("Getting mediaType info from manifests directory")
	mediaType, err := GetMediaType(manifestsDir)
	if err != nil {
		formatErrors = append(formatErrors, err)
	}

	// Validate annotations.yaml
	annotationsFile, err := ioutil.ReadFile(filepath.Join(annotationsDir, AnnotationsFile))
	if err != nil {
		fmtErr := fmt.Errorf("Unable to read annotations.yaml file: %s", err.Error())
		formatErrors = append(formatErrors, fmtErr)
		return NewValidationError(annotationErrors, formatErrors)
	}

	var fileAnnotations AnnotationMetadata

	annotations := map[string]string{
		MediatypeLabel:      mediaType,
		ManifestsLabel:      ManifestsDir,
		MetadataLabel:       MetadataDir,
		PackageLabel:        "",
		ChannelsLabel:       "",
		ChannelDefaultLabel: "",
	}

	i.logger.Debug("Validating annotations.yaml")

	err = yaml.Unmarshal(annotationsFile, &fileAnnotations)
	if err != nil {
		formatErrors = append(formatErrors, fmt.Errorf("Unable to parse annotations.yaml file"))
	}

	for label, item := range annotations {
		val, ok := fileAnnotations.Annotations[label]
		if ok {
			i.logger.Debugf(`Found annotation "%s" with value "%s"`, label, val)
		} else {
			aErr := fmt.Errorf("Missing annotation %q", label)
			annotationErrors = append(annotationErrors, aErr)
		}

		switch label {
		case MediatypeLabel:
			if item != val {
				aErr := fmt.Errorf("Expecting annotation %q to have value %q instead of %q", label, item, val)
				annotationErrors = append(annotationErrors, aErr)
			}
		case ManifestsLabel:
			if item != ManifestsDir {
				aErr := fmt.Errorf("Expecting annotation %q to have value %q instead of %q", label, ManifestsDir, val)
				annotationErrors = append(annotationErrors, aErr)
			}
		case MetadataDir:
			if item != MetadataLabel {
				aErr := fmt.Errorf("Expecting annotation %q to have value %q instead of %q", label, MetadataDir, val)
				annotationErrors = append(annotationErrors, aErr)
			}
		case ChannelsLabel, ChannelDefaultLabel:
			if val == "" {
				aErr := fmt.Errorf("Expecting annotation %q to have non-empty value", label)
				annotationErrors = append(annotationErrors, aErr)
			} else {
				annotations[label] = val
			}
		}
	}

	_, err = ValidateChannelDefault(annotations[ChannelsLabel], annotations[ChannelDefaultLabel])
	if err != nil {
		annotationErrors = append(annotationErrors, err)
	}

	if len(annotationErrors) > 0 || len(formatErrors) > 0 {
		return NewValidationError(annotationErrors, formatErrors)
	}

	return nil
}
