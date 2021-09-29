//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o fakes/fake_registry_client.go ../../../../vendor/github.com/operator-framework/operator-registry/pkg/api.RegistryClient
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o fakes/fake_registry_interface.go ../../registry/registry_client.go ClientInterface
package catalog

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

const SkipPackageAnnotationKey = "olm.skipRange"

type SourceRef struct {
	Address     string
	Client      client.Interface
	LastConnect metav1.Time
	LastHealthy metav1.Time
}

type SourceQuerier interface {
	// Deprecated: This FindReplacement function will be deprecated soon
	FindReplacement(currentVersion *semver.Version, bundleName, pkgName, channelName string, initialSource registry.CatalogKey) (*api.Bundle, *registry.CatalogKey, error)
	Queryable() error
}

type NamespaceSourceQuerier struct {
	sources map[registry.CatalogKey]registry.ClientInterface
}

var _ SourceQuerier = &NamespaceSourceQuerier{}

func NewNamespaceSourceQuerier(sources map[registry.CatalogKey]registry.ClientInterface) *NamespaceSourceQuerier {
	return &NamespaceSourceQuerier{
		sources: sources,
	}
}

func (q *NamespaceSourceQuerier) Queryable() error {
	if len(q.sources) == 0 {
		return fmt.Errorf("no catalog sources available")
	}
	return nil
}

// Deprecated: This FindReplacement function will be deprecated soon
func (q *NamespaceSourceQuerier) FindReplacement(currentVersion *semver.Version, bundleName, pkgName, channelName string, initialSource registry.CatalogKey) (*api.Bundle, *registry.CatalogKey, error) {
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
