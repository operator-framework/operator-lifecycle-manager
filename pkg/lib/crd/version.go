package crd

import (
	"fmt"
	"strings"

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

	v := unst.GroupVersionKind().Version
	if _, ok := supportedCRDVersions[v]; !ok {
		return "", fmt.Errorf("CRD APIVersion from manifest not supported: %s", v)
	}

	return v, nil
}

