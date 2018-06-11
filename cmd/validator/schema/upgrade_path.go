package schema

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/ghodss/yaml"
	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
)

// Files is a map of files.
type Files map[string][]byte

// Glob searches the `/manifests` directory for files matching the pattern and returns them.
func Glob(pattern string) Files {
	matching := map[string][]byte{}
	files, err := filepath.Glob(pattern)
	if err != nil {
		panic(err)
	}

	for _, name := range files {
		bytes, err := ioutil.ReadFile(name)
		if err != nil {
			panic(err)
		}
		matching[name] = bytes
	}

	return matching
}

// TestUpgradePath checks that every ClusterServiceVersion in a package directory has a valid `spec.replaces` field.
func TestUpgradePath(packageDir string) {
	replaces := map[string]string{}
	csvFiles := Glob(filepath.Join(packageDir, "**.clusterserviceversion.yaml"))

	for _, bytes := range csvFiles {
		jsonBytes, err := yaml.YAMLToJSON(bytes)
		if err != nil {
			panic(err)
		}
		var csv csvv1alpha1.ClusterServiceVersion
		err = json.Unmarshal(jsonBytes, &csv)
		if err != nil {
			panic(err)
		}
		replaces[csv.ObjectMeta.Name] = csv.Spec.Replaces
	}

	for replacing, replaced := range replaces {
		fmt.Printf("%s -> %s\n", replaced, replacing)

		if _, ok := replaces[replaced]; replaced != "" && !ok {
			err := fmt.Errorf("%s should replace %s, which does not exist", replacing, replaced)
			panic(err)
		}
	}
}
