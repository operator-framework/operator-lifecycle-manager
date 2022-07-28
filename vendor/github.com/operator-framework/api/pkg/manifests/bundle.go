package manifests

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

type Bundle struct {
	Name           string
	Objects        []*unstructured.Unstructured
	Package        string
	Channels       []string
	DefaultChannel string
	BundleImage    string
	CSV            *operatorsv1alpha1.ClusterServiceVersion
	V1beta1CRDs    []*apiextensionsv1beta1.CustomResourceDefinition
	V1CRDs         []*apiextensionsv1.CustomResourceDefinition
	Dependencies   []*Dependency
	// CompressedSize stores the gzip size of the bundle
	CompressedSize int64
	// Size stores the size of the bundle
	Size int64
}

func (b *Bundle) ObjectsToValidate() []interface{} {
	objs := []interface{}{}
	for _, crd := range b.V1CRDs {
		objs = append(objs, crd)
	}
	for _, crd := range b.V1beta1CRDs {
		objs = append(objs, crd)
	}
	objs = append(objs, b.CSV)

	for _, o := range b.Objects {
		objs = append(objs, o)
	}
	objs = append(objs, b)

	return objs
}
