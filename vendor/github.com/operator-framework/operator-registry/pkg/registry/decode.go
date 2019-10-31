package registry

import (
	"errors"
	"fmt"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// DecodeUnstructured decodes a raw stream into a an
// unstructured.Unstructured instance.
func DecodeUnstructured(reader io.Reader) (obj *unstructured.Unstructured, err error) {
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)

	t := &unstructured.Unstructured{}
	if err = decoder.Decode(t); err != nil {
		return
	}

	obj = t
	return
}

// DecodePackageManifest decodes a raw stream into a a PackageManifest instance.
// If a package name is empty we consider the object invalid!
func DecodePackageManifest(reader io.Reader) (manifest *PackageManifest, err error) {
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)

	obj := &PackageManifest{}
	if decodeErr := decoder.Decode(obj); decodeErr != nil {
		err = fmt.Errorf("could not decode contents into package manifest - %v", decodeErr)
		return
	}

	if obj.PackageName == "" {
		err = errors.New("name of package (packageName) is missing")
		return
	}

	manifest = obj
	return
}
