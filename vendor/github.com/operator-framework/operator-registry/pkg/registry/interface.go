package registry

import (
	"context"

	"github.com/operator-framework/operator-registry/pkg/api"
)

type Load interface {
	AddOperatorBundle(bundle *Bundle) error
	AddBundlePackageChannels(manifest PackageManifest, bundle Bundle) error
	AddPackageChannels(manifest PackageManifest) error
	RmPackageName(packageName string) error
	ClearNonDefaultBundles(packageName string) error
}

type Query interface {
	ListTables(ctx context.Context) ([]string, error)
	ListPackages(ctx context.Context) ([]string, error)
	GetPackage(ctx context.Context, name string) (*PackageManifest, error)
	GetBundle(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error)
	GetBundleForChannel(ctx context.Context, pkgName string, channelName string) (*api.Bundle, error)
	// Get all channel entries that say they replace this one
	GetChannelEntriesThatReplace(ctx context.Context, name string) (entries []*ChannelEntry, err error)
	// Get the bundle in a package/channel that replace this one
	GetBundleThatReplaces(ctx context.Context, name, pkgName, channelName string) (*api.Bundle, error)
	// Get all channel entries that provide an api
	GetChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*ChannelEntry, err error)
	// Get latest channel entries that provide an api
	GetLatestChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*ChannelEntry, err error)
	// Get the the latest bundle that provides the API in a default channel
	GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error)
	// List all images in the database
	ListImages(ctx context.Context) ([]string, error)
	// List all images for a particular bundle
	GetImagesForBundle(ctx context.Context, bundleName string) ([]string, error)
	// Get Provided and Required APIs for a particular bundle
	GetApisForEntry(ctx context.Context, entryID int64) (provided []*api.GroupVersionKind, required []*api.GroupVersionKind, err error)
	// Get Version of a Bundle Image
	GetBundleVersion(ctx context.Context, image string) (string, error)
	// List Images for Package
	GetBundlePathsForPackage(ctx context.Context, pkgName string) ([]string, error)
	// Get DefaultChannel for Package
	GetDefaultChannelForPackage(ctx context.Context, pkgName string) (string, error)
	// List channels for package
	ListChannels(ctx context.Context, pkgName string) ([]string, error)
	// Get CurrentCSV name for channel and package
	GetCurrentCSVNameForChannel(ctx context.Context, pkgName, channel string) (string, error)
}
