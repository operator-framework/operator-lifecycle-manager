package bundle

import (
	"context"
	"fmt"
	"github.com/containers/buildah"
	"github.com/containers/buildah/pkg/blobcache"
	"github.com/containers/image/v5/docker"
	dockerreference "github.com/containers/image/v5/docker/reference"
	"github.com/containers/image/v5/types"
	"github.com/containers/storage"
	"github.com/containers/storage/pkg/unshare"
	"os"
	"strings"
)

type Bundle struct {
	PackageName string
	// Tag for the created bundle image
	Tag string
	// Location on the registry you want the created bundle image to be uploaded
	BundleURLPath string
	// location where the manifests and metadata directories can be found
	BundleDir string
	// custom name for a manifest directory, this should be a subdirectory within BundleDir
	// If empty, the BundleManifestDirectory is assumed to be 'manifests/'
	BundleManifestDirectory string
	Channels                []string
	DefaultChannel          string
	// When set to true, GenerateAnnotations will create the annotations.yaml file in the metadata directory
	// from bundle information. If false, it will read the annotations.yaml to populate bundle fields
	GenerateAnnotations bool
}

func getDockerImageRef(destImage string) (types.ImageReference, error) {
	ref, err := dockerreference.ParseNormalizedNamed(destImage)
	if err != nil {
		return nil, fmt.Errorf("cannot parse docker image reference '%s': %v", destImage, err)
	}
	imageRef, err := docker.NewReference(ref)
	if err != nil {
		return imageRef, fmt.Errorf("cannot create docker image reference: %v", err)
	}
	return imageRef, nil
}

// Builds the bundle image onto local filesystem
func buildAndUploadBundleImage(destRef, authString string, bundleContentDirectories []string, labels map[string]string) error {
	if len(destRef) == 0 {
		return fmt.Errorf("destination image reference must not be empty")
	}

	imageRef, err := getDockerImageRef(destRef)
	if err != nil {
		return fmt.Errorf("error parsing destination image reference: %v", err)
	}

	containerName := fmt.Sprintf("%s-builder", destRef)

	storeOptions, err := storage.DefaultStoreOptions(unshare.IsRootless(), unshare.GetRootlessUID())
	if err != nil {
		return fmt.Errorf("could not get store options: %v", err)
	}

	localStore, err := storage.GetStore(storeOptions)
	if err != nil {
		return fmt.Errorf("could not get initialize local store for image builder: %v", err)
	}

	b, err := buildah.NewBuilder(context.TODO(), localStore, buildah.BuilderOptions{
		FromImage:        "scratch",
		Container:        containerName,
		ConfigureNetwork: buildah.NetworkDefault,
		SystemContext:    &types.SystemContext{},
		CommonBuildOpts:  &buildah.CommonBuildOptions{},
	})
	if err != nil {
		return fmt.Errorf("error creating image builder: %v", err)
	}
	defer func() {
		_ = b.Delete()
	}()

	for k, v := range labels {
		b.SetLabel(k, v)
	}

	for _, copydir := range bundleContentDirectories {
		if err := b.Add(copydir, false, buildah.AddAndCopyOptions{}, copydir); err != nil {
			return fmt.Errorf("failed to add layer: %v", err)
		}
	}

	layerCacheDir := ".cache"
	if _, err := os.Stat(layerCacheDir); os.IsNotExist(err) {
		_ = os.MkdirAll(layerCacheDir, 0755)
	}
	var dest types.ImageReference
	dest, err = blobcache.NewBlobCache(imageRef, layerCacheDir, types.PreserveOriginal)

	var systemContex *types.SystemContext
	if len(authString) == 0 {
		systemContex = &types.SystemContext{
			DockerInsecureSkipTLSVerify: types.OptionalBoolTrue,
		}
	} else {
		username, password := getCreds(authString)
		systemContex = &types.SystemContext{
			DockerAuthConfig: &types.DockerAuthConfig{
				Username: username,
				Password: password,
			},
		}
	}
	_, _, _, err = b.Commit(context.TODO(), dest, buildah.CommitOptions{
		SystemContext: systemContex,
		OmitTimestamp: true,
	})
	if err != nil {
		return fmt.Errorf("error creating image from container %s: %v", containerName, err)
	}

	return nil
}

func getCreds(creds string) (username string, password string) {
	if creds == "" {
		return "", ""
	}
	up := strings.SplitN(creds, ":", 2)
	if len(up) == 1 {
		return up[0], ""
	}
	if up[0] == "" {
		return "", up[1]
	}
	return up[0], up[1]
}
