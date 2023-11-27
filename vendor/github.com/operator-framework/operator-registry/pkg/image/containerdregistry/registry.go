package containerdregistry

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/containerd/containerd/archive"
	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containers/image/v5/docker/reference"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/operator-framework/operator-registry/pkg/image"
)

// Registry enables manipulation of images via containerd modules.
type Registry struct {
	Store
	destroy      func() error
	log          *logrus.Entry
	resolverFunc func(repo string) (remotes.Resolver, error)
	platform     platforms.MatchComparer
}

var _ image.Registry = &Registry{}

var nonRetriablePullError = regexp.MustCompile("specified image is a docker schema v1 manifest, which is not supported")

// Pull fetches and stores an image by reference.
func (r *Registry) Pull(ctx context.Context, ref image.Reference) error {
	// Set the default namespace if unset
	ctx = ensureNamespace(ctx)

	namedRef, err := reference.ParseNamed(ref.String())
	if err != nil {
		return err
	}

	resolver, err := r.resolverFunc(namedRef.Name())
	if err != nil {
		return err
	}

	name, root, err := resolver.Resolve(ctx, ref.String())
	if err != nil {
		return fmt.Errorf("error resolving name for image ref %s: %v", ref.String(), err)
	}
	r.log.Debugf("resolved name: %s", name)

	fetcher, err := resolver.Fetcher(ctx, name)
	if err != nil {
		return err
	}

	retryBackoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.0,
		Jitter:   0.1,
		Steps:    5,
	}

	if err := retry.OnError(retryBackoff,
		func(pullErr error) bool {
			if nonRetriablePullError.MatchString(pullErr.Error()) {
				return false
			}
			r.log.Warnf("Error pulling image %q: %v. Retrying", ref.String(), pullErr)
			return true
		},
		func() error { return r.fetch(ctx, fetcher, root) },
	); err != nil {
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

	manifest, err := r.getManifest(ctx, ref)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	for _, layer := range manifest.Layers {
		r.log.Debugf("unpacking layer: %v", layer)
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

	manifest, err := r.getManifest(ctx, ref)
	if err != nil {
		return nil, err
	}
	imageConfig, err := r.getImage(ctx, *manifest)
	if err != nil {
		return nil, err
	}

	return imageConfig.Config.Labels, nil
}

// Destroy cleans up the on-disk boltdb file and other cache files, unless preserve cache is true
func (r *Registry) Destroy() (err error) {
	return r.destroy()
}

func (r *Registry) getManifest(ctx context.Context, ref image.Reference) (*ocispec.Manifest, error) {
	img, err := r.Images().Get(ctx, ref.String())
	if err != nil {
		return nil, err
	}

	manifest, err := images.Manifest(ctx, r.Content(), img.Target, r.platform)
	if err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (r *Registry) getImage(ctx context.Context, manifest ocispec.Manifest) (*ocispec.Image, error) {
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
	return &imageConfig, nil
}

func (r *Registry) fetch(ctx context.Context, fetcher remotes.Fetcher, root ocispec.Descriptor) error {
	visitor := images.HandlerFunc(func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
		r.log.WithField("digest", desc.Digest).Debug("fetched")
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

	filters := filterList{adjustPerms, dropXattrs}
	_, err = archive.Apply(ctx, dir, decompressed, archive.WithFilter(filters.and))

	return err
}

func ensureNamespace(ctx context.Context) context.Context {
	if _, namespaced := namespaces.Namespace(ctx); !namespaced {
		return namespaces.WithNamespace(ctx, namespaces.Default)
	}
	return ctx
}

type filterList []archive.Filter

func (f filterList) and(h *tar.Header) (bool, error) {
	for _, filter := range f {
		ok, err := filter(h)
		if !ok || err != nil {
			return ok, err
		}
	}

	return true, nil
}

func adjustPerms(h *tar.Header) (bool, error) {
	h.Uid = os.Getuid()
	h.Gid = os.Getgid()

	// Make all unpacked files owner-writable
	// This prevents errors when unpacking a layer that contains a read-only folder (if permissions are preserved,
	// file contents cannot be unpacked into the unpacked read-only folder).
	// This also means that "unpacked" layers cannot be "repacked" without potential information loss
	h.Mode |= 0200

	return true, nil
}

// paxSchilyXattr contains the key prefix for xattrs stored in PAXRecords (see https://golang.org/src/archive/tar/common.go for more details).
const paxSchilyXattr = "SCHILY.xattr."

// dropXattrs removes all xattrs from a Header.
// This is useful for unpacking on systems where writing certain xattrs is a restricted operation; e.g. "security.capability" on SELinux.
func dropXattrs(h *tar.Header) (bool, error) {
	h.Xattrs = nil // Deprecated, but still in use, clear anyway.
	for key := range h.PAXRecords {
		if strings.HasPrefix(key, paxSchilyXattr) { // Xattrs are stored under keys with the "Schilly.xattr." prefix.
			delete(h.PAXRecords, key)
		}
	}

	return true, nil
}
