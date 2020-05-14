//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o fakes/fake_registry_client.go ../../../../vendor/github.com/operator-framework/operator-registry/pkg/client/client.go Interface
package resolver

import (
	"context"
	"fmt"
	"io"

	"github.com/blang/semver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/api"
	registryapi "github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

const SkipPackageAnnotationKey = "olm.skipRange"

type SourceRef struct {
	Address     string
	Client      client.Interface
	LastConnect metav1.Time
	LastHealthy metav1.Time
}

type SourceQuerier interface {
	FindProvider(api opregistry.APIKey, initialSource CatalogKey, pkgName string) (*api.Bundle, *CatalogKey, error)
	FindBundle(pkgName, channelName, bundleName string, initialSource CatalogKey) (*api.Bundle, *CatalogKey, error)
	FindLatestBundle(pkgName, channelName string, initialSource CatalogKey) (*api.Bundle, *CatalogKey, error)
	FindReplacement(currentVersion *semver.Version, bundleName, pkgName, channelName string, initialSource CatalogKey) (*api.Bundle, *CatalogKey, error)
	Queryable() error
}

type NamespaceSourceQuerier struct {
	sources map[CatalogKey]client.Interface
	clients map[CatalogKey]*client.Client
}

var _ SourceQuerier = &NamespaceSourceQuerier{}

type ChannelEntryStream interface {
	Recv() (*api.ChannelEntry, error)
}

type ChannelEntryIterator struct {
	stream ChannelEntryStream
	error  error
}

func NewChannelEntryIterator(stream ChannelEntryStream) *ChannelEntryIterator {
	return &ChannelEntryIterator{stream: stream}
}

func (ceit *ChannelEntryIterator) Next() *registryapi.ChannelEntry {
	if ceit.error != nil {
		return nil
	}
	next, err := ceit.stream.Recv()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		ceit.error = err
	}
	return next
}

func (ceit *ChannelEntryIterator) Error() error {
	return ceit.error
}

func NewNamespaceSourceQuerier(sources map[CatalogKey]client.Interface, clients map[CatalogKey]*client.Client) *NamespaceSourceQuerier {
	return &NamespaceSourceQuerier{
		sources: sources,
		clients: clients,
	}
}

func (q *NamespaceSourceQuerier) Queryable() error {
	if len(q.sources) == 0 {
		return fmt.Errorf("no catalog sources available")
	}
	return nil
}

func (q *NamespaceSourceQuerier) FindProvider(api opregistry.APIKey, initialSource CatalogKey, pkgName string) (*registryapi.Bundle, *CatalogKey, error) {
	if initialSource.Name != "" && initialSource.Namespace != "" {
		client, ok := q.clients[initialSource]
		if ok {
			if bundle, err := FindBundleThatProvides(context.TODO(), client, api.Group, api.Version, api.Kind, pkgName); err == nil {
				return bundle, &initialSource, nil
			}
			if bundle, err := FindBundleThatProvides(context.TODO(), client, api.Plural+"."+api.Group, api.Version, api.Kind, pkgName); err == nil {
				return bundle, &initialSource, nil
			}
		}
	}
	for key, client := range q.clients {
		if bundle, err := FindBundleThatProvides(context.TODO(), client, api.Group, api.Version, api.Kind, pkgName); err == nil {
			return bundle, &key, nil
		}
		if bundle, err := FindBundleThatProvides(context.TODO(), client, api.Plural+"."+api.Group, api.Version, api.Kind, pkgName); err == nil {
			return bundle, &key, nil
		}
	}
	return nil, nil, fmt.Errorf("%s not provided by a package in any CatalogSource", api)
}

func (q *NamespaceSourceQuerier) FindBundle(pkgName, channelName, bundleName string, initialSource CatalogKey) (*api.Bundle, *CatalogKey, error) {
	if initialSource.Name != "" && initialSource.Namespace != "" {
		source, ok := q.sources[initialSource]
		if !ok {
			return nil, nil, fmt.Errorf("CatalogSource %s not found", initialSource)
		}

		bundle, err := source.GetBundle(context.TODO(), pkgName, channelName, bundleName)
		if err != nil {
			return nil, nil, err
		}
		return bundle, &initialSource, nil
	}

	for key, source := range q.sources {
		bundle, err := source.GetBundle(context.TODO(), pkgName, channelName, bundleName)
		if err == nil {
			return bundle, &key, nil
		}
	}
	return nil, nil, fmt.Errorf("%s/%s/%s not found in any available CatalogSource", pkgName, channelName, bundleName)
}

func (q *NamespaceSourceQuerier) FindLatestBundle(pkgName, channelName string, initialSource CatalogKey) (*api.Bundle, *CatalogKey, error) {
	if initialSource.Name != "" && initialSource.Namespace != "" {
		source, ok := q.sources[initialSource]
		if !ok {
			return nil, nil, fmt.Errorf("CatalogSource %s not found", initialSource)
		}

		bundle, err := source.GetBundleInPackageChannel(context.TODO(), pkgName, channelName)
		if err != nil {
			return nil, nil, err
		}
		return bundle, &initialSource, nil
	}

	for key, source := range q.sources {
		bundle, err := source.GetBundleInPackageChannel(context.TODO(), pkgName, channelName)
		if err == nil {
			return bundle, &key, nil
		}
	}
	return nil, nil, fmt.Errorf("%s/%s not found in any available CatalogSource", pkgName, channelName)
}

func (q *NamespaceSourceQuerier) FindReplacement(currentVersion *semver.Version, bundleName, pkgName, channelName string, initialSource CatalogKey) (*api.Bundle, *CatalogKey, error) {
	errs := []error{}

	if initialSource.Name != "" && initialSource.Namespace != "" {
		source, ok := q.sources[initialSource]
		if !ok {
			return nil, nil, fmt.Errorf("CatalogSource %s not found", initialSource.Name)
		}

		bundle, err := q.findChannelHead(currentVersion, pkgName, channelName, source)
		if bundle != nil {
			return bundle, &initialSource, nil
		}
		if err != nil {
			errs = append(errs, err)
		}

		bundle, err = source.GetReplacementBundleInPackageChannel(context.TODO(), bundleName, pkgName, channelName)
		if bundle != nil {
			return bundle, &initialSource, nil
		}
		if err != nil {
			errs = append(errs, err)
		}

		return nil, nil, errors.NewAggregate(errs)
	}

	for key, source := range q.sources {
		bundle, err := q.findChannelHead(currentVersion, pkgName, channelName, source)
		if bundle != nil {
			return bundle, &initialSource, nil
		}
		if err != nil {
			errs = append(errs, err)
		}

		bundle, err = source.GetReplacementBundleInPackageChannel(context.TODO(), bundleName, pkgName, channelName)
		if bundle != nil {
			return bundle, &key, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return nil, nil, errors.NewAggregate(errs)
}

func (q *NamespaceSourceQuerier) findChannelHead(currentVersion *semver.Version, pkgName, channelName string, source client.Interface) (*api.Bundle, error) {
	if currentVersion == nil {
		return nil, nil
	}

	latest, err := source.GetBundleInPackageChannel(context.TODO(), pkgName, channelName)
	if err != nil {
		return nil, err
	}

	if latest.SkipRange == "" {
		return nil, nil
	}

	r, err := semver.ParseRange(latest.SkipRange)
	if err != nil {
		return nil, err
	}

	if r(*currentVersion) {
		return latest, nil
	}
	return nil, nil
}

// GetLatestChannelEntriesThatProvide uses registry client to get a list of
// latest channel entries that provide the requested API (via an iterator)
func GetLatestChannelEntriesThatProvide(ctx context.Context, c *client.Client, group, version, kind string) (*ChannelEntryIterator, error) {
	stream, err := c.Registry.GetLatestChannelEntriesThatProvide(ctx, &registryapi.GetLatestProvidersRequest{Group: group, Version: version, Kind: kind})
	if err != nil {
		return nil, err
	}
	return NewChannelEntryIterator(stream), nil
}

// FilterChannelEntries filters out a channel entries that provide the requested
// API and come from the same package with original operator and returns the
// first entry on the list
func FilterChannelEntries(it *ChannelEntryIterator, pkgName string) *opregistry.ChannelEntry {
	var entry *opregistry.ChannelEntry
	for e := it.Next(); e != nil; e = it.Next() {
		if e.PackageName != pkgName {
			entry = &opregistry.ChannelEntry{
				PackageName: e.PackageName,
				ChannelName: e.ChannelName,
				BundleName:  e.BundleName,
				Replaces:    e.Replaces,
			}
			break
		}
	}
	return entry
}

// FindBundleThatProvides returns a bundle that provides the request API and
// doesn't belong to the provided package
func FindBundleThatProvides(ctx context.Context, client *client.Client, group, version, kind, pkgName string) (*api.Bundle, error) {
	it, err := GetLatestChannelEntriesThatProvide(ctx, client, group, version, kind)
	if err != nil {
		return nil, err
	}

	entry := FilterChannelEntries(it, pkgName)
	if entry != nil {
		return nil, fmt.Errorf("Unable to find a channel entry which doesn't belong to package %s", pkgName)
	}
	bundle, err := client.Registry.GetBundle(ctx, &registryapi.GetBundleRequest{PkgName: entry.PackageName, ChannelName: entry.ChannelName, CsvName: entry.BundleName})
	if err != nil {
		return nil, err
	}
	return bundle, nil
}
