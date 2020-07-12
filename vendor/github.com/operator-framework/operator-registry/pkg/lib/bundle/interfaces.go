package bundle

import (
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/image"
)

// BundleImageValidator provides a toolset for pulling and then validating
// bundle container images
type BundleImageValidator interface {
	// PullBundleImage takes an imageTag to pull and a directory to push
	// the contents of the image to
	PullBundleImage(imageTag string, directory string) error
	// Validate bundle takes a directory containing the contents of a bundle image
	// and validates that the format is correct
	ValidateBundleFormat(directory string) error
	// Validate bundle takes a directory containing the contents of a bundle image
	// and validates that the content is correct
	ValidateBundleContent(directory string) error
}

// NewImageValidator is a constructor that returns an ImageValidator
func NewImageValidator(registry image.Registry, logger *logrus.Entry) BundleImageValidator {
	return imageValidator{
		registry: registry,
		logger:   logger,
	}
}
