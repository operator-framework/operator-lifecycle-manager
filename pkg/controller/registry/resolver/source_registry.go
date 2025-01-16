package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/blang/semver/v4"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"
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
	rcp          RegistryClientProvider
	catsrcLister v1alpha1listers.CatalogSourceLister
	logger       logrus.StdLogger
	invalidator  *sourceInvalidator
}

func SourceProviderFromRegistryClientProvider(rcp RegistryClientProvider, catsrcLister v1alpha1listers.CatalogSourceLister, logger logrus.StdLogger) *RegistrySourceProvider {
	return &RegistrySourceProvider{
		rcp:          rcp,
		logger:       logger,
		catsrcLister: catsrcLister,
		invalidator: &sourceInvalidator{
			validChans: make(map[cache.SourceKey]chan struct{}),
			ttl:        5 * time.Minute,
		},
	}
}

type errorSource struct {
	error
}

func (s errorSource) Snapshot(_ context.Context) (*cache.Snapshot, error) {
	return nil, s.error
}

func (a *RegistrySourceProvider) Sources(namespaces ...string) map[cache.SourceKey]cache.Source {
	result := make(map[cache.SourceKey]cache.Source)

	cats := []*operatorsv1alpha1.CatalogSource{}
	for _, ns := range namespaces {
		catsInNamespace, err := a.catsrcLister.CatalogSources(ns).List(labels.Everything())
		if err != nil {
			result[cache.SourceKey{Name: "", Namespace: ns}] = errorSource{
				error: fmt.Errorf("failed to list catalogsources for namespace %q: %w", ns, err),
			}
			return result
		}
		cats = append(cats, catsInNamespace...)
	}

	clients := a.rcp.ClientsForNamespaces(namespaces...)
	for _, cat := range cats {
		key := cache.SourceKey{Name: cat.Name, Namespace: cat.Namespace}
		if client, ok := clients[registry.CatalogKey{Name: cat.Name, Namespace: cat.Namespace}]; ok {
			result[key] = &registrySource{
				key:         key,
				client:      client,
				logger:      a.logger,
				invalidator: a.invalidator,
			}
		} else {
			result[key] = errorSource{
				error: fmt.Errorf("no registry client established for catalogsource %s/%s", cat.Namespace, cat.Name),
			}
		}
	}
	if len(result) == 0 {
		return nil
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
	s.logger.Printf("requesting snapshot for catalog source %s/%s", s.key.Namespace, s.key.Name)
	metrics.IncrementCatalogSourceSnapshotsTotal(s.key.Name, s.key.Namespace)

	// Fetching default channels this way makes many round trips
	// -- may need to either add a new API to fetch all at once,
	// or embed the information into Bundle.
	packages := make(map[string]*api.Package)

	it, err := s.client.ListBundles(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list bundles: %w", err)
	}

	var operators []*cache.Entry
	for b := it.Next(); b != nil; b = it.Next() {
		p, ok := packages[b.PackageName]
		if !ok {
			if p, err = s.client.GetPackage(ctx, b.PackageName); err != nil {
				s.logger.Printf("failed to retrieve default channel for bundle, continuing: %v", err)
				continue
			} else {
				packages[b.PackageName] = p
			}
		}
		o, err := newOperatorFromBundle(b, "", s.key, p.DefaultChannelName)
		if err != nil {
			s.logger.Printf("failed to construct operator from bundle, continuing: %v", err)
			continue
		}
		var deprecations *cache.Deprecations
		if p.Deprecation != nil {
			deprecations = &cache.Deprecations{Package: &api.Deprecation{Message: fmt.Sprintf("olm.package/%s: %s", p.Name, p.Deprecation.GetMessage())}}
		}
		for _, c := range p.Channels {
			if c.Name == b.ChannelName && c.Deprecation != nil {
				if deprecations == nil {
					deprecations = &cache.Deprecations{}
				}
				deprecations.Channel = &api.Deprecation{Message: fmt.Sprintf("olm.channel/%s: %s", c.Name, c.Deprecation.GetMessage())}
			}
		}
		if b.Deprecation != nil {
			if deprecations == nil {
				deprecations = &cache.Deprecations{}
			}
			deprecations.Bundle = &api.Deprecation{Message: fmt.Sprintf("olm.bundle/%s: %s", b.CsvName, b.Deprecation.GetMessage())}
		}
		o.SourceInfo.Deprecations = deprecations
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
