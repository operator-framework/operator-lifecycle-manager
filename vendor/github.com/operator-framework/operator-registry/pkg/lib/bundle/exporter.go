package bundle

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/image/containerdregistry"
	"github.com/operator-framework/operator-registry/pkg/image/execregistry"
)

// BundleExporter exports the manifests of a bundle image into a directory
type BundleExporter struct {
	image         string
	directory     string
	containerTool containertools.ContainerTool
}

func NewExporterForBundle(image, directory string, containerTool containertools.ContainerTool) *BundleExporter {
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

	var reg image.Registry
	var rerr error
	switch i.containerTool {
	case containertools.NoneTool:
		reg, rerr = containerdregistry.NewRegistry(containerdregistry.WithLog(log))
	case containertools.PodmanTool:
		fallthrough
	case containertools.DockerTool:
		reg, rerr = execregistry.NewRegistry(i.containerTool, log)
	}
	if rerr != nil {
		return rerr
	}
	defer func() {
		if err := reg.Destroy(); err != nil {
			log.WithError(err).Warn("error destroying local cache")
		}
	}()

	if err := reg.Pull(context.TODO(), image.SimpleReference(i.image)); err != nil {
		return err
	}

	if err := reg.Unpack(context.TODO(), image.SimpleReference(i.image), tmpDir); err != nil {
		return err
	}

	if err := os.MkdirAll(i.directory, 0777); err != nil {
		return err
	}

	return copy.Copy(filepath.Join(tmpDir, "manifests"), i.directory)
}
