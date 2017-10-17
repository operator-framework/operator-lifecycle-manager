package catalog

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

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
	ListServices() ([]v1alpha1.ClusterServiceVersion, error)

	FindCRDByName(name string) (*v1beta1.CustomResourceDefinition, error)
	FindLatestCSVForCRD(crdname string) (*v1alpha1.ClusterServiceVersion, error)
	ListCSVsForCRD(crdname string) ([]v1alpha1.ClusterServiceVersion, error)
}

// CSVMetadata holds the necessary information to locate a particular CSV in the catalog
type CSVMetadata struct {
	Name    string
	Version string
}
