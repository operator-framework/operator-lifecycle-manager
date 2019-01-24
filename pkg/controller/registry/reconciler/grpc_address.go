package reconciler

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

type GrpcAddressRegistryReconciler struct{}

var _ RegistryReconciler = &GrpcAddressRegistryReconciler{}

func (g *GrpcAddressRegistryReconciler) EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error {
	return nil
}
