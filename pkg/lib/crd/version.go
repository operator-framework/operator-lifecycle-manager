package crd

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// GroupVersion takes a CRD manifest and determines the APIVersion.
func GroupVersion(manifest *string) (schema.GroupVersion, error) {
	if manifest == nil {
		return schema.GroupVersion{}, fmt.Errorf("empty CRD manifest")
	}

	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(*manifest), 10)
	unst := &unstructured.Unstructured{}
	if err := dec.Decode(unst); err != nil {
		return schema.GroupVersion{}, err
	}

	v := unst.GroupVersionKind().GroupVersion()
	if v.Empty() {
		return schema.GroupVersion{}, fmt.Errorf("could not determine GroupVersion from CRD manifest")
	}
	return v, nil
}
