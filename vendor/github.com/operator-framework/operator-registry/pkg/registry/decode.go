package registry

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// DecodeUnstructured decodes a raw stream into a an
// unstructured.Unstructured instance.
func DecodeUnstructured(reader io.Reader) (*unstructured.Unstructured, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)

	t := &unstructured.Unstructured{}
	if err := decoder.Decode(t); err != nil {
		return nil, err
	}

	return t, nil
}

// DecodePackageManifest decodes a raw stream into a a PackageManifest instance.
// If a package name is empty we consider the object invalid!
func DecodePackageManifest(reader io.Reader) (*PackageManifest, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(reader, 30)

	obj := &PackageManifest{}
	if decodeErr := decoder.Decode(obj); decodeErr != nil {
		err := fmt.Errorf("could not decode contents into package manifest - %v", decodeErr)
		return nil, err
	}

	if obj.PackageName == "" {
		err := errors.New("name of package (packageName) is missing")
		return nil, err
	}

	return obj, nil
}

func decodeFileFS(root fs.FS, path string, into interface{}, log *logrus.Entry) error {
	fileReader, err := root.Open(path)
	if err != nil {
		return fmt.Errorf("unable to read file %s: %s", path, err)
	}
	defer fileReader.Close()

	decoder := yaml.NewYAMLOrJSONDecoder(fileReader, 30)

	errRet := decoder.Decode(into)

	if errRet == nil {
		// Look for and warn about extra documents
		extraDocument := &map[string]interface{}{}
		err = decoder.Decode(extraDocument)
		if err == nil && log != nil {
			log.Warnf("found more than one document inside %s, using only the first one", path)
		}
	}

	return errRet
}
