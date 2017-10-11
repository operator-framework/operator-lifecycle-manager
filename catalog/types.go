package catalog

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// Catalog Source
//    - Map name to AppTypeClusterServiceVersion
//    - Map CRD name to CRD definition
//    - Map CRD name to AppTypeClusterServiceVersion that manages it

type Source interface {
	FindLatestClusterServiceVersionByServiceName(name string) (*v1alpha1.ClusterServiceVersion, error)
	FindClusterServiceVersionByServiceNameAndVersion(name, version string) (*v1alpha1.ClusterServiceVersion, error)
	ListClusterServiceVersionsForServiceName(name string) ([]v1alpha1.ClusterServiceVersion, error)

	FindCRDByName(name string) (*apiextensions.CustomResourceDefinition, error)
	FindLatestClusterServiceVersionForCRD(crdname string) ([]v1alpha1.ClusterServiceVersion, error)
	ListClusterServiceVersionForCRD(crdname string) ([]v1alpha1.ClusterServiceVersion, error)
}
