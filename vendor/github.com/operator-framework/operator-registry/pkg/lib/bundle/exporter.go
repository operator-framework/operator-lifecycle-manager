package bundle

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/containertools"
)

// BundleExporter exports the manifests of a bundle image into a directory
type BundleExporter struct {
	image         string
	directory     string
	containerTool string
}

func NewSQLExporterForBundle(image, directory, containerTool string) *BundleExporter {
	return &BundleExporter{
		image:         image,
		directory:     directory,
		containerTool: containerTool,
	}
}

func (i *BundleExporter) Export() error {

	log := logrus.WithField("img", i.image)

	tmpDir, err := ioutil.TempDir("./", "bundle_tmp")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Pull the image and get the manifests
	reader := containertools.NewImageReader(i.containerTool, log)

	err = reader.GetImageData(i.image, tmpDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(i.directory, 0777); err != nil {
		return err
	}

	return copy.Copy(filepath.Join(tmpDir, "manifests"), i.directory)
}
