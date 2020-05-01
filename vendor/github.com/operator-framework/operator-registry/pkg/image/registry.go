package image

import (
	"context"
)

// Registry knows how to Pull and Unpack Operator Bundle images to the filesystem.
// Note: In the future, Registry will know how to Build and Push Operator Bundle images as well.
type Registry interface {
	// Pull fetches and stores an image by reference.
	Pull(ctx context.Context, ref Reference) error

	// Push uploads an image to the remote registry of its reference.
	// If the referenced image does not exist in the registry, an error is returned.
	// Push(ctx context.Context, ref string) error

	// Unpack writes the unpackaged content of an image to a directory.
	// If the referenced image does not exist in the registry, an error is returned.
	Unpack(ctx context.Context, ref Reference, dir string) error

	// Labels gets the labels for an image that is already stored.
	Labels(ctx context.Context, ref Reference) (map[string]string, error)

	// Destroy cleans up any on-disk resources used to track images
	Destroy() error

	// Pack creates and stores an image based on the given reference and returns a reference to the new image.
	// If the referenced image does not exist in the registry, a new image is created from scratch.
	// If it exists, it's used as the base image.
	// Pack(ctx context.Context, ref Reference, from io.Reader) (next string, err error)
}

