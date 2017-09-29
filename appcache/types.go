package appcache

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// AppCache
//    - Map name to AppTypeClusterServiceVersion
//    - Map CRD name to CRD definition
//    - Map CRD name to AppTypeClusterServiceVersion that manages it

type AppCache interface {
	FindClusterServiceVersionByName(name string) (*v1alpha1.ClusterServiceVersion, error)
	FindCRDByName(name string) (*apiextensions.CustomResourceDefinition, error)
	FindClusterServiceVersionForCRD(crdname string) (*v1alpha1.ClusterServiceVersion, error)
}
