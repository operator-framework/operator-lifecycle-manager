package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/operator-framework/operator-registry/pkg/api"
	fbccache "github.com/operator-framework/operator-registry/pkg/cache"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

// The X-Acknowledge-Experimental request header is expected when calling experimental endpoints.
// It forces the user to confront the fact that the endpoint might change, or be removed, without notice.
const headerXAcknowledgeExperimental = "x-acknowledge-experimental"

type RegistryServer struct {
	api.UnimplementedRegistryServer
	store registry.GRPCQuery
}

var _ api.RegistryServer = &RegistryServer{}

func NewRegistryServer(store registry.GRPCQuery) *RegistryServer {
	return &RegistryServer{UnimplementedRegistryServer: api.UnimplementedRegistryServer{}, store: store}
}

func (s *RegistryServer) ListPackages(req *api.ListPackageRequest, stream api.Registry_ListPackagesServer) error {
	packageNames, err := s.store.ListPackages(stream.Context())
	if err != nil {
		return err
	}
	for _, p := range packageNames {
		if err := stream.Send(&api.PackageName{Name: p}); err != nil {
			return err
		}
	}

	return nil
}

func (s *RegistryServer) ListBundles(req *api.ListBundlesRequest, stream api.Registry_ListBundlesServer) error {
	return s.store.SendBundles(stream.Context(), stream)
}

func (s *RegistryServer) GetPackage(ctx context.Context, req *api.GetPackageRequest) (*api.Package, error) {
	packageManifest, err := s.store.GetPackage(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	return registry.PackageManifestToAPIPackage(packageManifest), nil
}

func (s *RegistryServer) GetBundle(ctx context.Context, req *api.GetBundleRequest) (*api.Bundle, error) {
	return s.store.GetBundle(ctx, req.GetPkgName(), req.GetChannelName(), req.GetCsvName())
}

func (s *RegistryServer) GetBundleForChannel(ctx context.Context, req *api.GetBundleInChannelRequest) (*api.Bundle, error) {
	return s.store.GetBundleForChannel(ctx, req.GetPkgName(), req.GetChannelName())
}

func (s *RegistryServer) GetChannelEntriesThatReplace(req *api.GetAllReplacementsRequest, stream api.Registry_GetChannelEntriesThatReplaceServer) error {
	channelEntries, err := s.store.GetChannelEntriesThatReplace(stream.Context(), req.GetCsvName())
	if err != nil {
		return err
	}
	for _, e := range channelEntries {
		if err := stream.Send(registry.ChannelEntryToAPIChannelEntry(e)); err != nil {
			return err
		}
	}
	return nil
}

func (s *RegistryServer) GetBundleThatReplaces(ctx context.Context, req *api.GetReplacementRequest) (*api.Bundle, error) {
	return s.store.GetBundleThatReplaces(ctx, req.GetCsvName(), req.GetPkgName(), req.GetChannelName())
}

func (s *RegistryServer) GetChannelEntriesThatProvide(req *api.GetAllProvidersRequest, stream api.Registry_GetChannelEntriesThatProvideServer) error {
	channelEntries, err := s.store.GetChannelEntriesThatProvide(stream.Context(), req.GetGroup(), req.GetVersion(), req.GetKind())
	if err != nil {
		return err
	}
	for _, e := range channelEntries {
		if err := stream.Send(registry.ChannelEntryToAPIChannelEntry(e)); err != nil {
			return err
		}
	}
	return nil
}

func (s *RegistryServer) GetLatestChannelEntriesThatProvide(req *api.GetLatestProvidersRequest, stream api.Registry_GetLatestChannelEntriesThatProvideServer) error {
	channelEntries, err := s.store.GetLatestChannelEntriesThatProvide(stream.Context(), req.GetGroup(), req.GetVersion(), req.GetKind())
	if err != nil {
		return err
	}
	for _, e := range channelEntries {
		if err := stream.Send(registry.ChannelEntryToAPIChannelEntry(e)); err != nil {
			return err
		}
	}
	return nil
}

func (s *RegistryServer) GetDefaultBundleThatProvides(ctx context.Context, req *api.GetDefaultProviderRequest) (*api.Bundle, error) {
	return s.store.GetBundleThatProvides(ctx, req.GetGroup(), req.GetVersion(), req.GetKind())
}

type ExperimentalRegistryServer struct {
	api.UnimplementedExperimentalRegistryServer
	store registry.GRPCQuery
}

var _ api.ExperimentalRegistryServer = &ExperimentalRegistryServer{}

func NewExperimentalRegistryServer(store registry.GRPCQuery) *ExperimentalRegistryServer {
	return &ExperimentalRegistryServer{UnimplementedExperimentalRegistryServer: api.UnimplementedExperimentalRegistryServer{}, store: store}
}

func (s *ExperimentalRegistryServer) ExperimentalListPackageCustomSchemas(req *api.ExperimentalListPackageCustomSchemasRequest, stream api.ExperimentalRegistry_ExperimentalListPackageCustomSchemasServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	// Silently return an empty stream when the experimental header is absent
	// so that unaware clients see no disruption.
	if vals := md.Get(headerXAcknowledgeExperimental); len(vals) == 0 || vals[0] != "true" {
		return nil
	}

	schema, pkgName := req.GetSchema(), req.GetPackageName()
	if schema == "" {
		return status.Errorf(codes.InvalidArgument, "schema is required")
	}
	type customSchemaQuerier interface {
		ListPackageCustomSchemas(ctx context.Context, schema, packageName string, sender func(*structpb.Struct) error) error
	}
	mq, ok := s.store.(customSchemaQuerier)
	if !ok {
		return status.Errorf(codes.Unimplemented, "store does not support custom schema queries")
	}
	err := mq.ListPackageCustomSchemas(stream.Context(), schema, pkgName, stream.Send)
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		var ve *fbccache.ValidationError
		if errors.As(err, &ve) {
			return status.Errorf(codes.InvalidArgument, "%v", err)
		}
		if stream.Context().Err() != nil {
			return status.FromContextError(stream.Context().Err()).Err()
		}
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}
