package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/api/grpc_health_v1"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

type Interface interface {
	GetBundle(ctx context.Context, packageName, channelName, csvName string) (*registry.Bundle, error)
	GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*registry.Bundle, error)
	GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*registry.Bundle, error)
	GetBundleThatProvides(ctx context.Context, group, version, kind string) (*registry.Bundle, error)
}

type Client struct {
	Registry api.RegistryClient
	Health   grpc_health_v1.HealthClient
	Conn     *grpc.ClientConn
}

var _ Interface = &Client{}

func (c *Client) GetBundle(ctx context.Context, packageName, channelName, csvName string) (*registry.Bundle, error) {
	bundle, err := c.Registry.GetBundle(ctx, &api.GetBundleRequest{PkgName: packageName, ChannelName: channelName, CsvName: csvName})
	if err != nil {
		return nil, err
	}
	return registry.NewBundleFromStrings(bundle.CsvName, bundle.PackageName, bundle.ChannelName, bundle.Object)
}

func (c *Client) GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*registry.Bundle, error) {
	bundle, err := c.Registry.GetBundleForChannel(ctx, &api.GetBundleInChannelRequest{PkgName: packageName, ChannelName: channelName})
	if err != nil {
		return nil, err
	}
	return registry.NewBundleFromStrings(bundle.CsvName, packageName, channelName, bundle.Object)
}

func (c *Client) GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*registry.Bundle, error) {
	bundle, err := c.Registry.GetBundleThatReplaces(ctx, &api.GetReplacementRequest{CsvName: currentName, PkgName: packageName, ChannelName: channelName})
	if err != nil {
		return nil, err
	}
	return registry.NewBundleFromStrings(bundle.CsvName, packageName, channelName, bundle.Object)
}

func (c *Client) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*registry.Bundle, error) {
	bundle, err := c.Registry.GetDefaultBundleThatProvides(ctx, &api.GetDefaultProviderRequest{Group: group, Version: version, Kind: kind})
	if err != nil {
		return nil, err
	}
	parsedBundle, err := registry.NewBundleFromStrings(bundle.CsvName, bundle.PackageName, bundle.ChannelName, bundle.Object)
	if err != nil {
		return nil, err
	}
	return parsedBundle, nil
}

func NewClient(address string) (*Client, error) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	return &Client{
		Registry: api.NewRegistryClient(conn),
		Health:   grpc_health_v1.NewHealthClient(conn),
		Conn:     conn,
	}, nil
}
