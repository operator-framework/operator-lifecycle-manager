package resolver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
)

type RegistryClientProvider interface {
	ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface
}

type registryClientAdapter struct {
	rcp    RegistryClientProvider
	logger logrus.StdLogger
}

func SourceProviderFromRegistryClientProvider(rcp RegistryClientProvider, logger logrus.StdLogger) cache.SourceProvider {
	return &registryClientAdapter{
		rcp:    rcp,
		logger: logger,
	}
}

type registrySource struct {
	key    cache.SourceKey
	client client.Interface
	logger logrus.StdLogger
}

func (s *registrySource) Snapshot(ctx context.Context) (*cache.Snapshot, error) {
	// Fetching default channels this way makes many round trips
	// -- may need to either add a new API to fetch all at once,
	// or embed the information into Bundle.
	defaultChannels := make(map[string]string)

	it, err := s.client.ListBundles(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list bundles: %w", err)
	}

	var operators []*cache.Operator
	for b := it.Next(); b != nil; b = it.Next() {
		defaultChannel, ok := defaultChannels[b.PackageName]
		if !ok {
			if p, err := s.client.GetPackage(ctx, b.PackageName); err != nil {
				s.logger.Printf("failed to retrieve default channel for bundle, continuing: %v", err)
				continue
			} else {
				defaultChannels[b.PackageName] = p.DefaultChannelName
				defaultChannel = p.DefaultChannelName
			}
		}
		o, err := cache.NewOperatorFromBundle(b, "", s.key, defaultChannel)
		if err != nil {
			s.logger.Printf("failed to construct operator from bundle, continuing: %v", err)
			continue
		}
		o.ProvidedAPIs = o.ProvidedAPIs.StripPlural()
		o.RequiredAPIs = o.RequiredAPIs.StripPlural()
		o.Replaces = b.Replaces
		EnsurePackageProperty(o, b.PackageName, b.Version)
		operators = append(operators, o)
	}
	if err := it.Error(); err != nil {
		return nil, fmt.Errorf("error encountered while listing bundles: %w", err)
	}

	return &cache.Snapshot{Entries: operators}, nil
}

func (a *registryClientAdapter) Sources(namespaces ...string) map[cache.SourceKey]cache.Source {
	result := make(map[cache.SourceKey]cache.Source)
	for key, client := range a.rcp.ClientsForNamespaces(namespaces...) {
		result[cache.SourceKey(key)] = &registrySource{
			key:    cache.SourceKey(key),
			client: client,
			logger: a.logger,
		}
	}
	return result
}

func EnsurePackageProperty(o *cache.Operator, name, version string) {
	for _, p := range o.Properties {
		if p.Type == opregistry.PackageType {
			return
		}
	}
	prop := opregistry.PackageProperty{
		PackageName: name,
		Version:     version,
	}
	bytes, err := json.Marshal(prop)
	if err != nil {
		return
	}
	o.Properties = append(o.Properties, &api.Property{
		Type:  opregistry.PackageType,
		Value: string(bytes),
	})
}
