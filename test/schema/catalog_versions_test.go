package schema

import (
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"encoding/json"

	"github.com/coreos/go-semver/semver"
	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
)

var manifestDir = os.Getenv("GOPATH") + "/src/github.com/operator-framework/operator-lifecycle-manager" +
	"/deploy/tectonic-alm-operator/manifests"

// BySemverDir lets us sort os.FileInfo by interpreting the filename as a semver version,
// which is how manifest directories are stored
type BySemverDir []os.FileInfo

func (s BySemverDir) Len() int      { return len(s) }
func (s BySemverDir) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s BySemverDir) Less(i, j int) bool {
	semverA := semver.New(s[i].Name())
	semverB := semver.New(s[j].Name())
	return semverA.LessThan(*semverB)
}

// LoadedCatalog wraps an in mem catalog with version metadata
type LoadedCatalog struct {
	Registry *registry.InMem
	Name     string
	Version  string
}

// loadCatalogFromFile loads an in memory catalog from a file path. Only used for testing.
func loadCatalogFromFile(path string) (*LoadedCatalog, error) {
	loader := registry.ConfigMapCatalogResourceLoader{
		Catalog: registry.NewInMem(),
	}
	currentBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	currentJsonBytes, err := yaml.YAMLToJSON(currentBytes)
	if err != nil {
		return nil, err
	}
	var currentConfigMap v1.ConfigMap
	err = json.Unmarshal(currentJsonBytes, &currentConfigMap)
	if err != nil {
		return nil, err
	}
	err = loader.LoadCatalogResourcesFromConfigMap(&currentConfigMap)
	if err != nil {
		return nil, err
	}
	return &LoadedCatalog{
		Registry: loader.Catalog,
		Name:     currentConfigMap.Name,
	}, nil
}

func TestCatalogVersions(t *testing.T) {
	// for each version of the catalog, load (version-1) and verify that each OCS that has a replaces field
	// points to an OCS in the previous version of the catalog
	files, err := ioutil.ReadDir(manifestDir)
	require.NoError(t, err)

	versionDirs := []os.FileInfo{}
	for _, f := range files {
		if f.IsDir() {
			versionDirs = append(versionDirs, f)
		}
	}

	// sort manifest directories by semver
	sort.Sort(BySemverDir(versionDirs))

	// versions before this don't contain the catalog
	oldestVersion := semver.New("0.2.1")

	// load all available catalogs
	catalogNameVersions := map[string][]*LoadedCatalog{}
	for _, versioned := range versionDirs {

		// ignore old versions that don't have the catalog
		semverDirName := semver.New(versioned.Name())
		if semverDirName.LessThan(*oldestVersion) {
			continue
		}

		// get the path of each version of the catalog
		manifestFiles, err := ioutil.ReadDir(path.Join(manifestDir, versioned.Name()))
		require.NoError(t, err)
		for _, f := range manifestFiles {
			if strings.HasSuffix(f.Name(), "configmap.yaml") {
				t.Logf("loading %s", f.Name())
				loadedCatalog, err := loadCatalogFromFile(path.Join(manifestDir, versioned.Name(), f.Name()))
				require.NoError(t, err)
				loadedCatalog.Version = semverDirName.String()
				if _, ok := catalogNameVersions[loadedCatalog.Name]; !ok {
					catalogNameVersions[loadedCatalog.Name] = []*LoadedCatalog{}
				}
				catalogNameVersions[loadedCatalog.Name] = append(catalogNameVersions[loadedCatalog.Name], loadedCatalog)
			}
		}
	}

	// ensure services in <version> that have a `replaces` field replace something in <version - 1>
	for catalogName, catalogVersions := range catalogNameVersions {
		for i := 0; i < len(catalogVersions)-1; i++ {
			currentCatalog := catalogVersions[i]
			nextCatalog := catalogVersions[i+1]

			t.Logf("comparing %s version %s to %s", catalogName, currentCatalog.Version, nextCatalog.Version)

			nextServices, err := nextCatalog.Registry.ListServices()
			require.NoError(t, err)
			for _, csv := range nextServices {
				if csv.Spec.Replaces != "" {
					oldCSV, err := currentCatalog.Registry.FindCSVByName(csv.Spec.Replaces)
					require.NoError(t, err)
					require.NotNil(t, oldCSV)
				}
			}
		}
	}
}
