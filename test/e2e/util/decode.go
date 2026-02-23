package util

import (
	"os"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

type clientObjectOption func(client.Object)

func WithNamespace(namespace string) clientObjectOption {
	return func(obj client.Object) {
		obj.SetNamespace(namespace)
	}
}

func DecodeFile(file string, to client.Object, options ...clientObjectOption) (client.Object, error) {
	manifest, err := yamlFromFilePath(file)
	if err != nil {
		return nil, err
	}
	dec := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 10)
	if err := dec.Decode(to); err != nil {
		return nil, err
	}

	return to, nil
}

func yamlFromFilePath(fileName string) (string, error) {
	yaml, err := os.ReadFile(fileName)
	return string(yaml), err
}
