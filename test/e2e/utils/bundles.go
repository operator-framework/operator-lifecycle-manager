package main

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
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

type Bundle struct {
	PackageName             string
	// Tag for the created bundle image
	Version                 string
	// Location on the registry you want the created bundle image to be uploaded
	BundleURLPath string
	// location where the manifests and metadata directories can be found
	BundleDir               string
	// custom name for a manifets directory
	BundleManifestDirectory string
	Channels                []string
	// Default Channel name.
	DefaultChannel      string
	GenerateAnnotations bool
}

// Build the bundle image with opm and docker binary
func (r *Registry) buildBundleImage(b *Bundle) (bundleReference string, err error){
	if len(b.DefaultChannel) == 0 && len(b.Channels) == 0 {
		return "", fmt.Errorf("missing default channel and channel list for package %s", b.PackageName)
	}
	if len(b.Channels) == 0 {
		b.Channels = append(b.Channels, b.DefaultChannel)
	}
	if len(b.DefaultChannel) == 0 {
		sort.Strings(b.Channels)
		b.DefaultChannel = b.Channels[0]
	}
	bundlePath := b.BundleURLPath
	if len(bundlePath) == 0 {
		bundlePath = fmt.Sprintf("%s/operator", b.PackageName)
	}
	bundleReference = fmt.Sprintf("%s/%s:%s", r.url, bundlePath, b.Version)

	cmd := exec.Command("opm", "alpha", "bundle", "build", "-d", path.Join(b.BundleDir, "manifests"), "-b", "docker", "-e", b.DefaultChannel, "-c", strings.Join(b.Channels, ","), "--tag", bundleReference)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return "", err
	}

	return bundleReference, nil
}

func(r *Registry) uploadBundleReferences(bundleRefs []string) error {
	if len(bundleRefs) == 0 {
		return nil
	}
	cmdArgs := append([]string{"push"}, bundleRefs...)
	cmd := exec.Command("docker", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func getDockerImageRef(destImage string) (types.ImageReference, error){
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
func buildAndUploadLocalBundleImage(destRef string, bundleContentDirectories []string, labels map[string]string, logger *io.Writer) (error) {
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
		ReportWriter:     *logger,
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
		b.SetLabel(k,v)
	}

	for _,copydir := range bundleContentDirectories {
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

	_, _, _, err = b.Commit(context.TODO(), dest, buildah.CommitOptions{
		ReportWriter:  *logger,
		SystemContext: &types.SystemContext{
			DockerInsecureSkipTLSVerify: types.OptionalBoolTrue,
		},
		OmitTimestamp: true,
	})
	if err != nil {
		return fmt.Errorf("error creating image from container %s: %v", containerName, err)
	}

	return nil
}
