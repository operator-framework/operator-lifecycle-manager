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
	FindLatestCSVByServiceName(name string) (*v1alpha1.ClusterServiceVersion, error)
	FindCSVByServiceNameAndVersion(name, version string) (*v1alpha1.ClusterServiceVersion, error)
	ListCSVsForServiceName(name string) ([]v1alpha1.ClusterServiceVersion, error)

	FindCRDByName(name string) (*apiextensions.CustomResourceDefinition, error)
	FindLatestCSVForCRD(crdname string) ([]v1alpha1.ClusterServiceVersion, error)
	ListCSVsForCRD(crdname string) ([]v1alpha1.ClusterServiceVersion, error)
}
