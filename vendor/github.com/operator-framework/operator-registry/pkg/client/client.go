package client

import (
	"context"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/api/grpc_health_v1"
)

type Interface interface {
	GetBundle(ctx context.Context, packageName, channelName, csvName string) (*api.Bundle, error)
	GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*api.Bundle, error)
	GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*api.Bundle, error)
	GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error)
	ListBundles(ctx context.Context) (*BundleIterator, error)
	GetPackage(ctx context.Context, packageName string) (*api.Package, error)
	HealthCheck(ctx context.Context, reconnectTimeout time.Duration) (bool, error)
	Close() error
}

type Client struct {
	Registry api.RegistryClient
	Health   grpc_health_v1.HealthClient
	Conn     *grpc.ClientConn
}

var _ Interface = &Client{}

type BundleStream interface {
	Recv() (*api.Bundle, error)
}

type BundleIterator struct {
	stream BundleStream
	error  error
}

func NewBundleIterator(stream BundleStream) *BundleIterator {
	return &BundleIterator{stream: stream}
}

func (it *BundleIterator) Next() *api.Bundle {
	if it.error != nil {
		return nil
	}
	next, err := it.stream.Recv()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		it.error = err
	}
	return next
}

func (it *BundleIterator) Error() error {
	return it.error
}

func (c *Client) GetBundle(ctx context.Context, packageName, channelName, csvName string) (*api.Bundle, error) {
	return c.Registry.GetBundle(ctx, &api.GetBundleRequest{PkgName: packageName, ChannelName: channelName, CsvName: csvName})
}

func (c *Client) GetBundleInPackageChannel(ctx context.Context, packageName, channelName string) (*api.Bundle, error) {
	return c.Registry.GetBundleForChannel(ctx, &api.GetBundleInChannelRequest{PkgName: packageName, ChannelName: channelName})
}

func (c *Client) GetReplacementBundleInPackageChannel(ctx context.Context, currentName, packageName, channelName string) (*api.Bundle, error) {
	return c.Registry.GetBundleThatReplaces(ctx, &api.GetReplacementRequest{CsvName: currentName, PkgName: packageName, ChannelName: channelName})
}

func (c *Client) GetBundleThatProvides(ctx context.Context, group, version, kind string) (*api.Bundle, error) {
	return c.Registry.GetDefaultBundleThatProvides(ctx, &api.GetDefaultProviderRequest{Group: group, Version: version, Kind: kind})
}

func (c *Client) ListBundles(ctx context.Context) (*BundleIterator, error) {
	stream, err := c.Registry.ListBundles(ctx, &api.ListBundlesRequest{})
	if err != nil {
		return nil, err
	}
	return NewBundleIterator(stream), nil
}

func (c *Client) GetPackage(ctx context.Context, packageName string) (*api.Package, error) {
	return c.Registry.GetPackage(ctx, &api.GetPackageRequest{Name: packageName})
}

func (c *Client) Close() error {
	if c.Conn == nil {
		return nil
	}
	return c.Conn.Close()
}

func (c *Client) HealthCheck(ctx context.Context, reconnectTimeout time.Duration) (bool, error) {
	res, err := c.Health.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "Registry"})
	if err != nil {
		if c.Conn.GetState() == connectivity.TransientFailure {
			ctx, cancel := context.WithTimeout(ctx, reconnectTimeout)
			defer cancel()
			if !c.Conn.WaitForStateChange(ctx, connectivity.TransientFailure) {
				return false, NewHealthError(c.Conn, HealthErrReasonUnrecoveredTransient, "connection didn't recover from TransientFailure")
			}
		}
		return false, NewHealthError(c.Conn, HealthErrReasonConnection, err.Error())
	}
	if res.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		return false, nil
	}
	return true, nil
}

func NewClient(address string) (*Client, error) {
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	return NewClientFromConn(conn), nil
}

func NewClientFromConn(conn *grpc.ClientConn) *Client {
	return &Client{
		Registry: api.NewRegistryClient(conn),
		Health:   grpc_health_v1.NewHealthClient(conn),
		Conn:     conn,
	}
}
