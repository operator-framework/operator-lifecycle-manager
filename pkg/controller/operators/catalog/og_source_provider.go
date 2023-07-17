package catalog

import (
	"context"
	"fmt"

	v1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"
)

type OperatorGroupToggleSourceProvider struct {
	sp       cache.SourceProvider
	logger   *logrus.Logger
	ogLister v1listers.OperatorGroupLister
}

func NewOperatorGroupToggleSourceProvider(sp cache.SourceProvider, logger *logrus.Logger,
	ogLister v1listers.OperatorGroupLister) *OperatorGroupToggleSourceProvider {
	return &OperatorGroupToggleSourceProvider{
		sp:       sp,
		logger:   logger,
		ogLister: ogLister,
	}
}

const exclusionAnnotation string = "olm.operatorframework.io/exclude-global-namespace-resolution"

func (e *OperatorGroupToggleSourceProvider) Sources(namespaces ...string) map[cache.SourceKey]cache.Source {
	// Check if annotation is set first
	resolutionNamespaces, err := e.CheckForExclusion(namespaces...)
	if err != nil {
		e.logger.Errorf("error checking namespaces %#v for global resolution exlusion: %s", namespaces, err)
		// Fail early with a dummy Source that returns an error
		// TODO: Update the Sources interface to return an error
		m := make(map[cache.SourceKey]cache.Source)
		key := cache.SourceKey{Name: "operatorgroup-unavailable", Namespace: namespaces[0]}
		source := errorSource{err}
		m[key] = source
		return m
	}
	return e.sp.Sources(resolutionNamespaces...)
}

type errorSource struct {
	error
}

func (e errorSource) Snapshot(ctx context.Context) (*cache.Snapshot, error) {
	return nil, e.error
}

func (e *OperatorGroupToggleSourceProvider) CheckForExclusion(namespaces ...string) ([]string, error) {
	var defaultResult = namespaces
	// The first namespace provided is always the current namespace being synced
	var ownNamespace = namespaces[0]
	var toggledResult = []string{ownNamespace}

	// Check the OG on the NS provided for the exclusion annotation
	ogs, err := e.ogLister.OperatorGroups(ownNamespace).List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("listing operatorgroups in namespace %s: %s", ownNamespace, err)
	}

	if len(ogs) != 1 {
		// Problem with the operatorgroup configuration in the namespace, or the operatorgroup has not yet been persisted
		// Note: a resync will be triggered if/when the operatorgroup becomes available
		return nil, fmt.Errorf("found %d operatorgroups in namespace %s: expected 1", len(ogs), ownNamespace)
	}

	var og = ogs[0]
	if val, ok := og.Annotations[exclusionAnnotation]; ok && val == "true" {
		// Exclusion specified
		// Ignore the globalNamespace for the purposes of resolution in this namespace
		e.logger.Printf("excluding global catalogs from resolution in namespace %s", ownNamespace)
		return toggledResult, nil
	}

	return defaultResult, nil
}
