package main

import (
	"context"
	"fmt"
	"os"

	"github.com/containers/image/v5/docker"
	"github.com/containers/image/v5/docker/reference"
	"github.com/containers/image/v5/manifest"
	"github.com/containers/image/v5/types"
)

// This is a simple tool to resolve canonical reference of the image.
// E.g. this resolves quay.io/operator-framework/olm:v0.28.0 to
// quay.io/operator-framework/olm@sha256:40d0363f4aa684319cd721c2fcf3321785380fdc74de8ef821317cd25a10782a
func main() {
	ctx := context.Background()

	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <image reference>\n", os.Args[0])
		os.Exit(1)
	}

	ref := os.Args[1]

	if err := run(ctx, ref); err != nil {
		fmt.Fprintf(os.Stderr, "error running the tool: %s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, ref string) error {
	imgRef, err := reference.ParseNamed(ref)
	if err != nil {
		return fmt.Errorf("error parsing image reference %q: %w", ref, err)
	}

	sysCtx := &types.SystemContext{}
	canonicalRef, err := resolveCanonicalRef(ctx, imgRef, sysCtx)
	if err != nil {
		return fmt.Errorf("error resolving canonical reference: %w", err)
	}

	fmt.Println(canonicalRef.String())
	return nil
}

func resolveCanonicalRef(ctx context.Context, imgRef reference.Named, sysCtx *types.SystemContext) (reference.Canonical, error) {
	if canonicalRef, ok := imgRef.(reference.Canonical); ok {
		return canonicalRef, nil
	}

	srcRef, err := docker.NewReference(imgRef)
	if err != nil {
		return nil, fmt.Errorf("error creating reference: %w", err)
	}

	imgSrc, err := srcRef.NewImageSource(ctx, sysCtx)
	if err != nil {
		return nil, fmt.Errorf("error creating image source: %w", err)
	}
	defer imgSrc.Close()

	imgManifestData, _, err := imgSrc.GetManifest(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error getting manifest: %w", err)
	}
	imgDigest, err := manifest.Digest(imgManifestData)
	if err != nil {
		return nil, fmt.Errorf("error getting digest of manifest: %w", err)
	}
	canonicalRef, err := reference.WithDigest(reference.TrimNamed(imgRef), imgDigest)
	if err != nil {
		return nil, fmt.Errorf("error creating canonical reference: %w", err)
	}
	return canonicalRef, nil
}
