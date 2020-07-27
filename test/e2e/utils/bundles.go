package main

import (
	"context"
	"fmt"
	"github.com/containers/buildah"
	"github.com/containers/buildah/pkg/blobcache"
	ocilayout "github.com/containers/image/v5/oci/layout"
	"github.com/containers/image/v5/types"
	"github.com/containers/storage"
	"github.com/containers/storage/pkg/unshare"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

type Bundle struct {
	PackageName             string
	// Tag for the created bundle image
	Version                 string
	DefaultChannel          string
	// Location on the registry you want the created bundle image to be uploaded
	BundlePath              string
	BundleManifestDirectory string
	Channels                []string
	GenerateAnnotations     bool
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
	bundlePath := b.BundlePath
	if len(bundlePath) == 0 {
		bundlePath = fmt.Sprintf("%s/operator", b.PackageName)
	}
	bundleReference = fmt.Sprintf("%s/%s:%s", r.url, bundlePath, b.Version)

	cmd := exec.Command("opm", "alpha", "bundle", "build", "-d", b.BundleManifestDirectory, "-b", "docker", "-e", b.DefaultChannel, "-c", strings.Join(b.Channels, ","), "--tag", bundleReference)
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

func getOCIImage(dir, destImage string) (types.ImageReference, error) {
	imageRef, err := ocilayout.ParseReference(fmt.Sprintf("%s:%s", dir, destImage))
	if err != nil {
		return imageRef, fmt.Errorf("could not create OCI image reference: %v", err)
	}
	return imageRef, nil
}

// Builds the bundle image onto local filesystem
func createLocalBundleImage(imageName, targetImageDir string, bundleContentDirectories []string, labels map[string]string, logger *io.Writer) (string, error) {
	if len(imageName) == 0 {
		return "", fmt.Errorf("could not create OCI image: empty image name")
	}

	ts := time.Now().Unix()
	if len(targetImageDir) == 0 {
		targetImageDir = defaultImageDir
	}
	containerName := fmt.Sprintf("%s-builder-%d", imageName, ts)

	if cwd, err := os.Getwd(); !path.IsAbs(targetImageDir) && err == nil {
		targetImageDir = path.Join(cwd, targetImageDir)
	}
	layerCacheDir := path.Join(targetImageDir, ".cache")

	if _, err := os.Stat(targetImageDir); os.IsNotExist(err) {
		_ = os.MkdirAll(targetImageDir, 0755)
	}
	if _, err := os.Stat(layerCacheDir); os.IsNotExist(err) {
		_ = os.MkdirAll(layerCacheDir, 0755)
	}

	imageRef, err := getOCIImage(targetImageDir, imageName)
	if err != nil {
		return "", fmt.Errorf("error parsing image reference: %v", err)
	}

	storeOptions, err := storage.DefaultStoreOptions(unshare.IsRootless(), unshare.GetRootlessUID())
	if err != nil {
		return "", fmt.Errorf("could not get default store options: %v", err)
	}

	localStore, err := storage.GetStore(storeOptions)
	if err != nil {
		return "", fmt.Errorf("could not get default store: %v", err)
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
		return "", fmt.Errorf("error creating image builder: %v", err)
	}
	defer func() {
		_ = b.Delete()
	}()

	for k, v := range labels {
		b.SetLabel(k,v)
	}

	for _,copydir := range bundleContentDirectories {
		if err := b.Add(copydir, false, buildah.AddAndCopyOptions{}, copydir); err != nil {
			return "", fmt.Errorf("error adding layer: %v", err)
		}
	}

	var dest types.ImageReference
	dest, err = blobcache.NewBlobCache(imageRef, layerCacheDir, types.PreserveOriginal)
	_, _, _, err = b.Commit(context.TODO(), dest, buildah.CommitOptions{
		ReportWriter:  *logger,
		SystemContext: &types.SystemContext{},
		OmitTimestamp: true,
	})
	if err != nil {
		return "", fmt.Errorf("error creating image from container %s: %v", containerName, err)
	}

	return fmt.Sprintf("oci:%s:%s", targetImageDir, imageName), nil
}


