package containerdregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"os"

	"github.com/operator-framework/operator-registry/pkg/image"
)

// Registry enables manipulation of images via containerd modules.
type Registry struct {
	Store
	destroy  func() error
	log      *logrus.Entry
	resolver remotes.Resolver
	platform platforms.MatchComparer
}

var _ image.Registry = &Registry{}

// Pull fetches and stores an image by reference.
func (r *Registry) Pull(ctx context.Context, ref image.Reference) error {
	// Set the default namespace if unset
	ctx = ensureNamespace(ctx)

	name, root, err := r.resolver.Resolve(ctx, ref.String())
	if err != nil {
		return fmt.Errorf("error resolving name %s: %v", name, err)
	}
	r.log.Infof("resolved name: %s", name)

	fetcher, err := r.resolver.Fetcher(ctx, name)
	if err != nil {
		return err
	}

	if err := r.fetch(ctx, fetcher, root); err != nil {
		return err
	}

	img := images.Image{
		Name:   ref.String(),
		Target: root,
	}
	if _, err = r.Images().Create(ctx, img); err != nil {
		if errdefs.IsAlreadyExists(err) {
			_, err = r.Images().Update(ctx, img)
		}
	}

	return err
}

// Unpack writes the unpackaged content of an image to a directory.
// If the referenced image does not exist in the registry, an error is returned.
func (r *Registry) Unpack(ctx context.Context, ref image.Reference, dir string) error {
	// Set the default namespace if unset
	ctx = ensureNamespace(ctx)

	img, err := r.Images().Get(ctx, ref.String())
	if err != nil {
		return err
	}

	manifest, err := images.Manifest(ctx, r.Content(), img.Target, r.platform)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	for _, layer := range manifest.Layers {
		r.log.Infof("unpacking layer: %v", layer)
		if err := r.unpackLayer(ctx, layer, dir); err != nil {
			return err
		}
	}

	return nil
}

// Labels gets the labels for an image reference.
func (r *Registry) Labels(ctx context.Context, ref image.Reference) (map[string]string, error) {
	// Set the default namespace if unset
	ctx = ensureNamespace(ctx)
	tmpDir, err := ioutil.TempDir("./", "bundle_tmp")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	img, err := r.Images().Get(ctx, ref.String())
	if err != nil {
		return nil, err
	}

	manifest, err := images.Manifest(ctx, r.Content(), img.Target, r.platform)
	if err != nil {
		return nil, err
	}

	ra, err := r.Content().ReaderAt(ctx, manifest.Config)
	if err != nil {
		return nil, err
	}
	defer ra.Close()

	decompressed, err := compression.DecompressStream(io.NewSectionReader(ra, 0, ra.Size()))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, decompressed); err != nil {
		return nil, err
	}
	r.log.Warn(buf.String())

	var imageConfig ocispec.Image

	if err := json.Unmarshal(buf.Bytes(), &imageConfig); err != nil {
		return nil, err
	}

	return imageConfig.Config.Labels, nil
}


// Destroy cleans up the on-disk boltdb file and other cache files, unless preserve cache is true
func (r *Registry) Destroy() (err error) {
	return r.destroy()
}

func (r *Registry) fetch(ctx context.Context, fetcher remotes.Fetcher, root ocispec.Descriptor) error {
	visitor := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		r.log.WithField("digest", desc.Digest).Info("fetched")
		r.log.Debug(desc)
		return nil, nil
	})

	if root.MediaType == images.MediaTypeDockerSchema1Manifest {
		return fmt.Errorf("specified image is a docker schema v1 manifest, which is not supported")
	}

	handler := images.Handlers(
		visitor,
		remotes.FetchHandler(r.Content(), fetcher),
		images.ChildrenHandler(r.Content()),
	)

	return images.Dispatch(ctx, handler, nil, root)
}

func (r *Registry) unpackLayer(ctx context.Context, layer ocispec.Descriptor, dir string) error {
	ra, err := r.Content().ReaderAt(ctx, layer)
	if err != nil {
		return err
	}
	defer ra.Close()

	// TODO(njhale): Chunk layer reading
	decompressed, err := compression.DecompressStream(io.NewSectionReader(ra, 0, ra.Size()))
	if err != nil {
		return err
	}
	_, err = archive.Apply(ctx, dir, decompressed, archive.WithFilter(adjustPerms))

	return err
}

func ensureNamespace(ctx context.Context) context.Context {
	if _, namespaced := namespaces.Namespace(ctx); !namespaced {
		return namespaces.WithNamespace(ctx, namespaces.Default)
	}
	return ctx
}

func adjustPerms(h *tar.Header) (bool, error) {
	h.Uid = os.Getuid()
	h.Gid = os.Getgid()

	return true, nil
}
