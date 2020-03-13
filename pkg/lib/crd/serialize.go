package crd

import (
	"bytes"
	"fmt"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	Kind       = "CustomResourceDefinition"
	APIVersion = "apiextensions.k8s.io/v1"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	// Add conversions between CRD versions
	install.Install(scheme)
}

// Serialize takes in a CRD manifest and returns a v1 versioned CRD object.
// Compatible with v1beta1 or v1 CRD manifests.
func Serialize(manifest string) (*apiextensionsv1.CustomResourceDefinition, error) {
	u := &unstructured.Unstructured{}
	reader := bytes.NewReader([]byte(manifest))
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)
	if err := decoder.Decode(u); err != nil {
		return nil, fmt.Errorf("crd unmarshaling failed: %s", err)
	}


	// Step through unversioned type to support v1beta1 -> v1
	unversioned := &apiextensions.CustomResourceDefinition{}
	if err := scheme.Convert(u, unversioned, nil); err != nil {
		return nil, fmt.Errorf("failed to convert crd from unstructured to internal: %s\nto v1: %s", u, err)
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := scheme.Convert(unversioned, crd, nil); err != nil {
		return nil, fmt.Errorf("failed to convert crd from internal to v1: %s\nto v1: %s", u, err)
	}

	// set CRD type meta
	// for purposes of fake client for unit tests to pass
	crd.TypeMeta.Kind = Kind
	crd.TypeMeta.APIVersion = APIVersion


	// for each version in the CRD, check and make sure there is a schema
	// if not a schema, give a default schema of props
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Schema == nil {
			schema := &apiextensionsv1.JSONSchemaProps{Type: "object"}
			crd.Spec.Versions[i].Schema = &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema:schema}
		}
	}


	return crd, nil
}
