package crd

import (
	"fmt"
	"reflect"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// V1Beta1 refers to the deprecated v1beta1 APIVersion of CRD objects
const V1Beta1Version string = "v1beta1"

// V1 refers to the new v1 APIVersion of CRD objects
const V1Version string = "v1"

var supportedCRDVersions = map[string]struct{}{
	V1Beta1Version: {},
	V1Version:      {},
}

// Version takes a CRD manifest and determines whether it is v1 or v1beta1 type based on the APIVersion.
func Version(manifest *string) (string, error) {
	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(*manifest), 10)
	unst := &unstructured.Unstructured{}
	if err := dec.Decode(unst); err != nil {
		return "", err
	}

	v := unst.GetObjectKind().GroupVersionKind().Version
	if _, ok := supportedCRDVersions[v]; !ok {
		return "", fmt.Errorf("could not determine CRD version from manifest")
	}

	return v, nil
}

// NotEqual determines whether two CRDs are equal based on the versions and validations of both.
// NotEqual looks at the range of the old CRD versions to ensure index out of bounds errors do not occur.
// If true, then we know we need to update the CRD on cluster.
func NotEqual(currentCRD *apiextensionsv1.CustomResourceDefinition, oldCRD *apiextensionsv1.CustomResourceDefinition) bool {
	var equalVersions bool
	var equalValidation bool
	var oldRange = len(oldCRD.Spec.Versions) - 1

	equalVersions = reflect.DeepEqual(currentCRD.Spec.Versions, oldCRD.Spec.Versions)
	if !equalVersions {
		return true
	}

	for i := range currentCRD.Spec.Versions {
		if i > oldRange {
			return true
		}
		equalValidation = reflect.DeepEqual(currentCRD.Spec.Versions[i].Schema, oldCRD.Spec.Versions[i].Schema)
		if equalValidation == false {
			return true
		}
	}

	return false
}
