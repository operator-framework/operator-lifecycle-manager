package catalog

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
)

// Catalog Source
//    - Map name to AppTypeClusterServiceVersion
//    - Map CRD to CRD definition
//    - Map CRD to AppTypeClusterServiceVersion that manages it

type Source interface {
	FindLatestCSVByServiceName(name string) (*v1alpha1.ClusterServiceVersion, error)
	FindCSVByServiceNameAndVersion(name, version string) (*v1alpha1.ClusterServiceVersion, error)
	ListCSVsForServiceName(name string) ([]v1alpha1.ClusterServiceVersion, error)
	ListServices() ([]v1alpha1.ClusterServiceVersion, error)

	FindCRDByKey(key CRDKey) (*v1beta1.CustomResourceDefinition, error)
	FindLatestCSVForCRD(key CRDKey) (*v1alpha1.ClusterServiceVersion, error)
	ListCSVsForCRD(key CRDKey) ([]v1alpha1.ClusterServiceVersion, error)
}

// CSVMetadata holds the necessary information to locate a particular CSV in the catalog
type CSVMetadata struct {
	Name    string
	Version string
}
