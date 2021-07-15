package registry

import (
	"os"
	"path/filepath"

	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/sirupsen/logrus"
)

type ImageInput struct {
	to     image.Reference
	from   string
	Bundle *Bundle
}

func NewImageInput(to image.Reference, from string) (*ImageInput, error) {
	parser := newBundleParser(logrus.WithFields(logrus.Fields{"with": from, "file": filepath.Join(from, "metadata"), "load": "annotations"}))
	bundle, err := parser.Parse(os.DirFS(from))
	if err != nil {
		return nil, err
	}
	bundle.BundleImage = to.String()

	return &ImageInput{
		to:     to,
		from:   from,
		Bundle: bundle,
	}, nil
}
