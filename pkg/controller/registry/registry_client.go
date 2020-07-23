//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o resolver/fakes/fake_registry_client_interface.go . ClientInterface
package registry

import (
	"context"
	"fmt"
	"io"

	"google.golang.org/grpc"

	registryapi "github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

// ChannelEntryStream interface
type ChannelEntryStream interface {
	Recv() (*registryapi.ChannelEntry, error)
}

// ClientInterface that extends client.Interface
type ClientInterface interface {
	client.Interface
	FindBundleThatProvides(ctx context.Context, group, version, kind string, excludedPackages map[string]struct{}) (*registryapi.Bundle, error)
	GetLatestChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (*ChannelEntryIterator, error)
}

// ChannelEntryIterator struct
type ChannelEntryIterator struct {
	stream ChannelEntryStream
	error  error
}

// NewChannelEntryIterator returns a new ChannelEntryIterator
func NewChannelEntryIterator(stream ChannelEntryStream) *ChannelEntryIterator {
	return &ChannelEntryIterator{stream: stream}
}

// Next returns the next Channel Entry in the grpc stream
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

// Client struct with a registry client embedded
type Client struct {
	*client.Client
}

// NewClientFromConn returns the next Channel Entry in the grpc stream
func NewClientFromConn(conn *grpc.ClientConn) *Client {
	return &Client{
		Client: client.NewClientFromConn(conn),
	}
}

var _ ClientInterface = &Client{}

// GetLatestChannelEntriesThatProvide uses registry client to get a list of
// latest channel entries that provide the requested API (via an iterator)
func (rc *Client) GetLatestChannelEntriesThatProvide(ctx context.Context, group, version, kind string) (*ChannelEntryIterator, error) {
	stream, err := rc.Client.Registry.GetLatestChannelEntriesThatProvide(ctx, &registryapi.GetLatestProvidersRequest{Group: group, Version: version, Kind: kind})
	if err != nil {
		return nil, err
	}
	return NewChannelEntryIterator(stream), nil
}

// FindBundleThatProvides returns a bundle that provides the request API and
// doesn't belong to the provided package
func (rc *Client) FindBundleThatProvides(ctx context.Context, group, version, kind string, excludedPackages map[string]struct{}) (*registryapi.Bundle, error) {
	it, err := rc.GetLatestChannelEntriesThatProvide(ctx, group, version, kind)
	if err != nil {
		return nil, err
	}
	entry := rc.filterChannelEntries(ctx, it, excludedPackages)
	if entry == nil {
		return nil, fmt.Errorf("Unable to find a channel entry that satisfies the requirements")
	}
	bundle, err := rc.Client.Registry.GetBundle(ctx, &registryapi.GetBundleRequest{PkgName: entry.PackageName, ChannelName: entry.ChannelName, CsvName: entry.BundleName})
	if err != nil {
		return nil, err
	}
	return bundle, nil
}

// FilterChannelEntries filters out channel entries that provide the requested
// API and come from the same package with original operator and returns the
// first entry on the list from the default channel of that package
func (rc *Client) filterChannelEntries(ctx context.Context, it *ChannelEntryIterator, excludedPackages map[string]struct{}) *opregistry.ChannelEntry {
	defChannels := make(map[string]string, 0)

	var entries []*opregistry.ChannelEntry
	for e := it.Next(); e != nil; e = it.Next() {
		if _, ok := excludedPackages[e.PackageName]; !ok {
			// keep track of the default channel for each package
			if _, ok := defChannels[e.PackageName]; !ok {
				defChannel, err := rc.getDefaultPackageChannel(ctx, e.PackageName)
				if err != nil {
					continue
				}
				defChannels[e.PackageName] = defChannel
			}

			// only add entry to the list if the entry is in the default channel
			if e.ChannelName == defChannels[e.PackageName] {
				entry := &opregistry.ChannelEntry{
					PackageName: e.PackageName,
					ChannelName: e.ChannelName,
					BundleName:  e.BundleName,
					Replaces:    e.Replaces,
				}
				entries = append(entries, entry)
			}
		}
	}

	if len(entries) > 0 {
		return entries[0]
	}
	return nil
}

// GetDefaultPackageChannel uses registry client to get the default
// channel name for a given package name
func (rc *Client) getDefaultPackageChannel(ctx context.Context, pkgName string) (string, error) {
	pkg, err := rc.Client.Registry.GetPackage(ctx, &registryapi.GetPackageRequest{Name: pkgName})
	if err != nil {
		return "", err
	}
	if pkg == nil {
		return "", fmt.Errorf("package %s not found in registry", pkgName)
	}

	return pkg.DefaultChannelName, nil
}
