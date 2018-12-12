package client

import (
	"context"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"google.golang.org/grpc"
)

type Interface interface {
	GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*registry.Bundle, error)
	GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*registry.Bundle, error)
	GetBundleThatProvides(ctx context.Context, groupOrName, version, kind string) (*registry.Bundle, error)
}

type Client struct {
	client api.RegistryClient
	health api.HealthClient
	conn *grpc.ClientConn
}

var _ Interface = &Client{}

func (c *Client) GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*registry.Bundle, error) {
	bundle, err := c.client.GetBundleForChannel(ctx, &api.GetBundleInChannelRequest{PkgName:packageName, ChannelName: channelName})
	if err!=nil {
		return nil, err
	}
	return registry.NewBundleFromStrings(bundle.CsvName, packageName, channelName, bundle.Object)
}

func (c *Client) GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*registry.Bundle, error) {
	bundle, err := c.client.GetBundleThatReplaces(ctx, &api.GetReplacementRequest{CsvName:currentName, PkgName:packageName, ChannelName:channelName})
	if err!=nil {
		return nil, err
	}
	return registry.NewBundleFromStrings(bundle.CsvName, packageName, channelName, bundle.Object)
}

func (c *Client) GetBundleThatProvides(ctx context.Context, groupOrName, version, kind string) (*registry.Bundle, error) {
	bundle, err := c.client.GetDefaultBundleThatProvides(ctx, &api.GetDefaultProviderRequest{GroupOrName:groupOrName, Version:version, Kind:kind})
	if err!=nil {
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
	if err!=nil {
		return nil, err
	}
	return &Client{
		client: api.NewRegistryClient(conn),
		health: api.NewHealthClient(conn),
		conn: conn,
	}, nil
}

