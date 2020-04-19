package bundle

import (
	"github.com/operator-framework/operator-registry/pkg/containertools"

	"github.com/sirupsen/logrus"
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
func NewImageValidator(containerTool string, logger *logrus.Entry) BundleImageValidator {
	return imageValidator{
		imageReader: containertools.NewImageReader(containertools.NewContainerTool(containerTool, containertools.NoneTool), logger),
		logger:      logger,
	}
}
