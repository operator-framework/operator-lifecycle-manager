package execregistry

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/image"
)

// CommandRunner provides some basic methods for manipulating images via an external container tool.
type CommandRunner interface {
	containertools.CommandRunner

	Unpack(image, src, dst string) error
}

// Registry enables manipulation of images via exec podman/docker commands.
type Registry struct {
	log *logrus.Entry
	cmd CommandRunner
}

// Adapt the cmd interface to the registry interface
var _ image.Registry = &Registry{}

// NewRegistry instantiates and returns a new registry which manipulates images via exec podman/docker commands.
func NewRegistry(tool containertools.ContainerTool, logger *logrus.Entry) (registry *Registry, err error) {
	return &Registry{
		log: logger,
		cmd: containertools.NewCommandRunner(tool, logger),
	}, nil
}

// Pull fetches and stores an image by reference.
func (r *Registry) Pull(ctx context.Context, ref image.Reference) error {
	return r.cmd.Pull(ref.String())
}

// Unpack writes the unpackaged content of an image to a directory.
// If the referenced image does not exist in the registry, an error is returned.
func (r *Registry) Unpack(ctx context.Context, ref image.Reference, dir string) error {
	return r.cmd.Unpack(ref.String(), "/.", dir)
}

// Labels gets the labels for an image reference.
func (r *Registry) Labels(ctx context.Context, ref image.Reference) (map[string]string, error) {
	return containertools.ImageLabelReader{
		Cmd:    r.cmd,
		Logger: r.log,
	}.GetLabelsFromImage(ref.String())
}

// Destroy is no-op for exec tools
func (r *Registry) Destroy() error {
	return nil
}
