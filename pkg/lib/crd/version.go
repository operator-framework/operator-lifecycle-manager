package crd

import (
	"fmt"
	"reflect"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

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
	if manifest == nil {
		return "", fmt.Errorf("empty CRD manifest")
	}

	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(*manifest), 10)
	unst := &unstructured.Unstructured{}
	if err := dec.Decode(unst); err != nil {
		return "", err
	}

	v := unst.GetObjectKind().GroupVersionKind().Version
	// some e2e test fixtures do not provide an version in their typemeta
	// assume these are v1beta types
	if v == "" {
		v = V1Beta1Version
	}
	if _, ok := supportedCRDVersions[v]; !ok {
		return "", fmt.Errorf("CRD APIVersion from manifest not supported: %s", v)
	}

	return v, nil
}

// V1NotEqual determines whether two v1 CRDs are equal based on the versions and validations of both.
// V1NotEqual looks at the range of the old CRD versions to ensure index out of bounds errors do not occur.
// If true, then we know we need to update the CRD on cluster.
func V1NotEqual(currentCRD *apiextensionsv1.CustomResourceDefinition, oldCRD *apiextensionsv1.CustomResourceDefinition) bool {
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
		if !equalValidation {
			return true
		}
	}

	return false
}

func V1Beta1NotEqual(currentCRD *apiextensionsv1beta1.CustomResourceDefinition, oldCRD *apiextensionsv1beta1.CustomResourceDefinition) bool {
	return !(reflect.DeepEqual(oldCRD.Spec.Version, currentCRD.Spec.Version) &&
		reflect.DeepEqual(oldCRD.Spec.Versions, currentCRD.Spec.Versions) &&
		reflect.DeepEqual(oldCRD.Spec.Validation, currentCRD.Spec.Validation))
}
