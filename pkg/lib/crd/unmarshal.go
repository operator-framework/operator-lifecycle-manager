package crd

import (
	"bytes"
	"fmt"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

const (
	Kind  = "CustomResourceDefinition"
	Group = "apiextensions.k8s.io/"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	// Add conversions between CRD versions
	install.Install(scheme)
}

// UnmarshalV1 takes in a CRD manifest and returns a v1 versioned CRD object.
func UnmarshalV1(manifest string) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	reader := bytes.NewReader([]byte(manifest))
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)
	if err := decoder.Decode(crd); err != nil {
		return nil, fmt.Errorf("v1 crd unmarshaling failed: %s", err)
	}

	return crd, nil
}

// UnmarshalV1 takes in a CRD manifest and returns a v1beta1 versioned CRD object.
func UnmarshalV1Beta1(manifest string) (*apiextensionsv1beta1.CustomResourceDefinition, error) {
	crd := &apiextensionsv1beta1.CustomResourceDefinition{}
	reader := bytes.NewReader([]byte(manifest))
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)
	if err := decoder.Decode(crd); err != nil {
		return nil, fmt.Errorf("v1beta1 crd unmarshaling failed: %s", err)
	}

	return crd, nil
}
