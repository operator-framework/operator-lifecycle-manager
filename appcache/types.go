package appcache

import (
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
)

// AppCache
//    - Map name to AppTypeOperatorVersion
//    - Map CRD name to CRD definition
//    - Map CRD name to AppTypeOperatorVersion that manages it

type AppCache interface {
	FindCloudServiceVersionByName(name string) (v1alpha1.OperatorVersion, error)
	FindCRDDefinitionForCRD(name string) (apiextensions.CustomResourceDefinition, error)
	FindCloudServiceVersionForCRD(crdname string) (v1alpha1.OperatorVersion, error)
}
