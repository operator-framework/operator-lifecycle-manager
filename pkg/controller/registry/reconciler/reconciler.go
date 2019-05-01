//go:generate counterfeiter -o ../../../fakes/fake_reconciler_factory.go . RegistryReconcilerFactory
package reconciler

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

const (
	// CatalogSourceLabelKey is the key for a label containing a CatalogSource name.
	CatalogSourceLabelKey string = "olm.catalogSource"
)

// RegistryEnsurer describes methods for ensuring a registry exists.
type RegistryEnsurer interface {
	// EnsureRegistryServer ensures a registry server exists for the given CatalogSource.
	EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error
}

// RegistryChecker describes methods for checking a registry.
type RegistryChecker interface {
	// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
	CheckRegistryServer(catalogSource *v1alpha1.CatalogSource) (healthy bool, err error)
}

// RegistryReconciler knows how to reconcile a registry.
type RegistryReconciler interface {
	RegistryChecker
	RegistryEnsurer
}

// RegistryReconcilerFactory describes factory methods for RegistryReconcilers.
type RegistryReconcilerFactory interface {
	ReconcilerForSource(source *v1alpha1.CatalogSource) RegistryReconciler
}

// RegistryReconcilerFactory is a factory for RegistryReconcilers.
type registryReconcilerFactory struct {
	Lister               operatorlister.OperatorLister
	OpClient             operatorclient.ClientInterface
	ConfigMapServerImage string
}

// ReconcilerForSource returns a RegistryReconciler based on the configuration of the given CatalogSource.
func (r *registryReconcilerFactory) ReconcilerForSource(source *v1alpha1.CatalogSource) RegistryReconciler {
	switch source.Spec.SourceType {
	case v1alpha1.SourceTypeInternal, v1alpha1.SourceTypeConfigmap:
		return &ConfigMapRegistryReconciler{
			Lister:   r.Lister,
			OpClient: r.OpClient,
			Image:    r.ConfigMapServerImage,
		}
	case v1alpha1.SourceTypeGrpc:
		if source.Spec.Image != "" {
			return &GrpcRegistryReconciler{
				Lister:   r.Lister,
				OpClient: r.OpClient,
			}
		} else if source.Spec.Address != "" {
			return &GrpcAddressRegistryReconciler{}
		}
	}
	return nil
}

// NewRegistryReconcilerFactory returns an initialized RegistryReconcilerFactory.
func NewRegistryReconcilerFactory(lister operatorlister.OperatorLister, opClient operatorclient.ClientInterface, configMapServerImage string) RegistryReconcilerFactory {
	return &registryReconcilerFactory{
		Lister:               lister,
		OpClient:             opClient,
		ConfigMapServerImage: configMapServerImage,
	}
}
