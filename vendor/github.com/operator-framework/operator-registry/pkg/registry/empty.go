package registry

import (
	"context"
	"errors"

	"github.com/operator-framework/operator-registry/pkg/api"
)

// EmptyQuery acts as a "zero value" implementation of the Query interface.
//
// EmptyQuery can be used as a substitute for any operation dependent on Query.
type EmptyQuery struct{}

func (EmptyQuery) ListTables(ctx context.Context) ([]string, error) {
	return nil, errors.New("empty querier: cannot list tables")
}

func (EmptyQuery) ListPackages(ctx context.Context) ([]string, error) {
	return nil, errors.New("empty querier: cannot list packages")
}

func (EmptyQuery) GetPackage(ctx context.Context, name string) (*PackageManifest, error) {
	return nil, errors.New("empty querier: cannot get package")
}

func (EmptyQuery) GetDefaultPackage(ctx context.Context, name string) (string, error) {
	return "", errors.New("empty querier: cannot get default package")
}

func (EmptyQuery) GetChannelEntriesFromPackage(ctx context.Context, packageName string) ([]ChannelEntryAnnotated, error) {
	return nil, errors.New("empty querier: cannot get all channel entries for package")
}

func (EmptyQuery) GetBundle(ctx context.Context, pkgName, channelName, csvName string) (*api.Bundle, error) {
	return nil, errors.New("empty querier: cannot get bundle")
}

func (EmptyQuery) GetBundleForChannel(ctx context.Context, pkgName string, channelName string) (*api.Bundle, error) {
	return nil, errors.New("empty querier: cannot get bundle for channel")
}

func (EmptyQuery) GetChannelEntriesThatReplace(ctx context.Context, name string) (entries []*ChannelEntry, err error) {
	return nil, errors.New("empty querier: cannot get channel entries that replace")
}

func (EmptyQuery) GetBundleThatReplaces(ctx context.Context, name, pkgName, channelName string) (*api.Bundle, error) {
	return nil, errors.New("empty querier: cannot get bundle that replaces")
}

func (EmptyQuery) GetChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*ChannelEntry, err error) {
	return nil, errors.New("empty querier: cannot get channel entries that provide")
}

func (EmptyQuery) GetLatestChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (entries []*ChannelEntry, err error) {
	return nil, errors.New("empty querier: cannot get latest channel entries that provide")
}

func (EmptyQuery) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
	return nil, errors.New("empty querier: cannot get bundle that provides")
}

func (EmptyQuery) ListImages(ctx context.Context) ([]string, error) {
	return nil, errors.New("empty querier: cannot get image list")
}

func (EmptyQuery) GetImagesForBundle(ctx context.Context, bundleName string) ([]string, error) {
	return nil, errors.New("empty querier: cannot get image list")
}

func (EmptyQuery) GetApisForEntry(ctx context.Context, entryId int64) (provided []*api.GroupVersionKind, required []*api.GroupVersionKind, err error) {
	return nil, nil, errors.New("empty querier: cannot apis")
}

func (EmptyQuery) GetBundleVersion(ctx context.Context, image string) (string, error) {
	return "", errors.New("empty querier: cannot get version")
}

func (EmptyQuery) GetBundlePathsForPackage(ctx context.Context, pkgName string) ([]string, error) {
	return nil, errors.New("empty querier: cannot get images")
}

func (EmptyQuery) GetBundlesForPackage(ctx context.Context, pkgName string) (map[BundleKey]struct{}, error) {
	return nil, errors.New("empty querier: cannot get bundles")
}

func (EmptyQuery) GetDefaultChannelForPackage(ctx context.Context, pkgName string) (string, error) {
	return "", errors.New("empty querier: cannot get default channel")
}

func (EmptyQuery) ListChannels(ctx context.Context, pkgName string) ([]string, error) {
	return nil, errors.New("empty querier: cannot list channels")
}

func (EmptyQuery) GetCurrentCSVNameForChannel(ctx context.Context, pkgName, channel string) (string, error) {
	return "", errors.New("empty querier: cannot get csv name for package and channel")
}

func (EmptyQuery) ListBundles(ctx context.Context) ([]*api.Bundle, error) {
	return nil, errors.New("empty querier: cannot list bundles")
}

func (EmptyQuery) GetDependenciesForBundle(ctx context.Context, name, version, path string) (dependencies []*api.Dependency, err error) {
	return nil, errors.New("empty querier: cannot get dependencies for bundle")
}

func (EmptyQuery) GetBundlePathIfExists(ctx context.Context, csvName string) (bundlePath string, err error) {
	return "", errors.New("empty querier: cannot get bundle path for bundle")
}

func (EmptyQuery) ListRegistryBundles(ctx context.Context) ([]*Bundle, error) {
	return nil, errors.New("empty querier: cannot list registry bundles")
}

var _ Query = &EmptyQuery{}

func NewEmptyQuerier() *EmptyQuery {
	return &EmptyQuery{}
}
