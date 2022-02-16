package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
)

// todo: move to pkg/controller/operators/catalog

type RegistryClientProvider interface {
	ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface
}

type sourceInvalidator struct {
	m          sync.Mutex
	validChans map[cache.SourceKey]chan struct{}
	ttl        time.Duration // auto-invalidate after this ttl
}

func (i *sourceInvalidator) Invalidate(key cache.SourceKey) {
	i.m.Lock()
	defer i.m.Unlock()
	if c, ok := i.validChans[key]; ok {
		close(c)
		delete(i.validChans, key)
	}
}

func (i *sourceInvalidator) GetValidChannel(key cache.SourceKey) <-chan struct{} {
	i.m.Lock()
	defer i.m.Unlock()

	if c, ok := i.validChans[key]; ok {
		return c
	}
	c := make(chan struct{})
	i.validChans[key] = c

	go func() {
		<-time.After(i.ttl)

		// be careful to avoid closing c (and panicking) after
		// it has already been invalidated via Invalidate
		i.m.Lock()
		defer i.m.Unlock()

		if saved := i.validChans[key]; saved == c {
			close(c)
			delete(i.validChans, key)
		}
	}()

	return c
}

type RegistrySourceProvider struct {
	rcp         RegistryClientProvider
	logger      logrus.StdLogger
	invalidator *sourceInvalidator
}

func SourceProviderFromRegistryClientProvider(rcp RegistryClientProvider, logger logrus.StdLogger) *RegistrySourceProvider {
	return &RegistrySourceProvider{
		rcp:    rcp,
		logger: logger,
		invalidator: &sourceInvalidator{
			validChans: make(map[cache.SourceKey]chan struct{}),
			ttl:        5 * time.Minute,
		},
	}
}

func (a *RegistrySourceProvider) Sources(namespaces ...string) map[cache.SourceKey]cache.Source {
	result := make(map[cache.SourceKey]cache.Source)
	for key, client := range a.rcp.ClientsForNamespaces(namespaces...) {
		result[cache.SourceKey(key)] = &registrySource{
			key:         cache.SourceKey(key),
			client:      client,
			logger:      a.logger,
			invalidator: a.invalidator,
		}
	}
	return result
}

func (a *RegistrySourceProvider) Invalidate(key cache.SourceKey) {
	a.invalidator.Invalidate(key)
}

type registrySource struct {
	key         cache.SourceKey
	client      client.Interface
	logger      logrus.StdLogger
	invalidator *sourceInvalidator
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

	var operators []*cache.Entry
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
		o, err := newOperatorFromBundle(b, "", s.key, defaultChannel)
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

	return &cache.Snapshot{
		Entries: operators,
		Valid:   s.invalidator.GetValidChannel(s.key),
	}, nil
}

func EnsurePackageProperty(o *cache.Entry, name, version string) {
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

func newOperatorFromBundle(bundle *api.Bundle, startingCSV string, sourceKey cache.SourceKey, defaultChannel string) (*cache.Entry, error) {
	parsedVersion, err := semver.ParseTolerant(bundle.Version)
	version := &parsedVersion
	if err != nil {
		version = nil
	}
	provided := cache.APISet{}
	for _, gvk := range bundle.ProvidedApis {
		provided[opregistry.APIKey{Plural: gvk.Plural, Group: gvk.Group, Kind: gvk.Kind, Version: gvk.Version}] = struct{}{}
	}
	required := cache.APISet{}
	for _, gvk := range bundle.RequiredApis {
		required[opregistry.APIKey{Plural: gvk.Plural, Group: gvk.Group, Kind: gvk.Kind, Version: gvk.Version}] = struct{}{}
	}
	sourceInfo := &cache.OperatorSourceInfo{
		Package:     bundle.PackageName,
		Channel:     bundle.ChannelName,
		StartingCSV: startingCSV,
		Catalog:     sourceKey,
	}
	sourceInfo.DefaultChannel = sourceInfo.Channel == defaultChannel

	// legacy support - if the api doesn't contain properties/dependencies, build them from required/provided apis
	properties := bundle.Properties
	if len(properties) == 0 {
		properties, err = providedAPIsToProperties(provided)
		if err != nil {
			return nil, err
		}
	}
	if len(bundle.Dependencies) > 0 {
		ps, err := legacyDependenciesToProperties(bundle.Dependencies)
		if err != nil {
			return nil, fmt.Errorf("failed to translate legacy dependencies to properties: %w", err)
		}
		properties = append(properties, ps...)
	} else {
		ps, err := requiredAPIsToProperties(required)
		if err != nil {
			return nil, err
		}
		properties = append(properties, ps...)
	}

	o := &cache.Entry{
		Name:         bundle.CsvName,
		Replaces:     bundle.Replaces,
		Version:      version,
		ProvidedAPIs: provided,
		RequiredAPIs: required,
		SourceInfo:   sourceInfo,
		Properties:   properties,
		Skips:        bundle.Skips,
		BundlePath:   bundle.BundlePath,
	}

	if r, err := semver.ParseRange(bundle.SkipRange); err == nil {
		o.SkipRange = r
	}

	if o.BundlePath == "" {
		// This bundle's content is embedded within the Bundle
		// proto message, not specified via image reference.
		o.Bundle = bundle
	}

	return o, nil
}

func legacyDependenciesToProperties(dependencies []*api.Dependency) ([]*api.Property, error) {
	var result []*api.Property
	for _, dependency := range dependencies {
		switch dependency.Type {
		case "olm.gvk":
			type gvk struct {
				Group   string `json:"group"`
				Version string `json:"version"`
				Kind    string `json:"kind"`
			}
			var vfrom gvk
			if err := json.Unmarshal([]byte(dependency.Value), &vfrom); err != nil {
				return nil, fmt.Errorf("failed to unmarshal legacy 'olm.gvk' dependency: %w", err)
			}
			vto := gvk{
				Group:   vfrom.Group,
				Version: vfrom.Version,
				Kind:    vfrom.Kind,
			}
			vb, err := json.Marshal(&vto)
			if err != nil {
				return nil, fmt.Errorf("unexpected error marshaling generated 'olm.package.required' property: %w", err)
			}
			result = append(result, &api.Property{
				Type:  "olm.gvk.required",
				Value: string(vb),
			})
		case "olm.package":
			var vfrom struct {
				PackageName  string `json:"packageName"`
				VersionRange string `json:"version"`
			}
			if err := json.Unmarshal([]byte(dependency.Value), &vfrom); err != nil {
				return nil, fmt.Errorf("failed to unmarshal legacy 'olm.package' dependency: %w", err)
			}
			vto := struct {
				PackageName  string `json:"packageName"`
				VersionRange string `json:"versionRange"`
			}{
				PackageName:  vfrom.PackageName,
				VersionRange: vfrom.VersionRange,
			}
			vb, err := json.Marshal(&vto)
			if err != nil {
				return nil, fmt.Errorf("unexpected error marshaling generated 'olm.package.required' property: %w", err)
			}
			result = append(result, &api.Property{
				Type:  "olm.package.required",
				Value: string(vb),
			})
		case "olm.label":
			result = append(result, &api.Property{
				Type:  "olm.label.required",
				Value: dependency.Value,
			})
		case "olm.constraint":
			result = append(result, &api.Property{
				Type:  "olm.constraint",
				Value: dependency.Value,
			})
		}
	}
	return result, nil
}
