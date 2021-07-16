package registry

import (
	"context"

	"github.com/operator-framework/operator-registry/pkg/api"
)

type Load interface {
	AddOperatorBundle(bundle *Bundle) error
	AddBundleSemver(graph *Package, bundle *Bundle) error
	AddPackageChannels(manifest PackageManifest) error
	AddBundlePackageChannels(manifest PackageManifest, bundle *Bundle) error
	RemovePackage(packageName string) error
	RemoveStrandedBundles() error
	DeprecateBundle(path string) error
	ClearNonHeadBundles() error
}

type GRPCQuery interface {
	// List all available package names in the index
	ListPackages(ctx context.Context) ([]string, error)

	// List all available bundles in the index
	ListBundles(ctx context.Context) (bundles []*api.Bundle, err error)

	// Get a package by name from the index
	GetPackage(ctx context.Context, name string) (*PackageManifest, error)

	// Get a bundle by its package name, channel name and csv name from the index
	GetBundle(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error)

	// Get the bundle in the specified package at the head of the specified channel
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
}

type Query interface {
	GRPCQuery

	ListTables(ctx context.Context) ([]string, error)
	GetDefaultPackage(ctx context.Context, name string) (string, error)
	GetChannelEntriesFromPackage(ctx context.Context, packageName string) ([]ChannelEntryAnnotated, error)
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
	// List Bundles for Package
	GetBundlesForPackage(ctx context.Context, pkgName string) (map[BundleKey]struct{}, error)
	// Get DefaultChannel for Package
	GetDefaultChannelForPackage(ctx context.Context, pkgName string) (string, error)
	// List channels for package
	ListChannels(ctx context.Context, pkgName string) ([]string, error)
	// Get CurrentCSV name for channel and package
	GetCurrentCSVNameForChannel(ctx context.Context, pkgName, channel string) (string, error)
	// Get the list of dependencies for a bundle
	GetDependenciesForBundle(ctx context.Context, name, version, path string) (dependencies []*api.Dependency, err error)
	// Get the bundle path if it exists
	GetBundlePathIfExists(ctx context.Context, csvName string) (string, error)
	// ListRegistryBundles returns a set of registry bundles.
	ListRegistryBundles(ctx context.Context) ([]*Bundle, error)
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . GraphLoader

// GraphLoader generates a graph
// GraphLoader supports multiple different loading schemes
// GraphLoader from SQL, GraphLoader from old format (filesystem), GraphLoader from SQL + input bundles
type GraphLoader interface {
	Generate(packageName string) (*Package, error)
}

// RegistryPopulator populates a registry.
type RegistryPopulator interface {
	Populate() error
}
