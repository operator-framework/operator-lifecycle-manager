package schema

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
	loader := registry.ConfigMapCatalogResourceLoader{}
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
	catalog := registry.NewInMem()
	err = loader.LoadCatalogResourcesFromConfigMap(catalog, &currentConfigMap)
	if err != nil {
		return nil, err
	}
	return &LoadedCatalog{
		Registry: catalog,
		Name:     currentConfigMap.Name,
	}, nil
}

// resolveCatalogs attempts to resolve every CSV for all given catalog sources
func resolveCatalogs(t *testing.T, catalogs []registry.SourceRef, dependencyResolver resolver.DependencyResolver) error {
	var err error

	// Attempt to resolve every CSV for each catalog
	for _, catalog := range catalogs {
		t.Logf("Resolving CSVs for catalog source %s...", catalog.SourceKey.Name)

		// Get CSV names
		csvs, err := catalog.Source.ListServices()
		if err != nil {
			return err
		}
		csvNames := make([]string, len(csvs))
		for i, csv := range csvs {
			csvNames[i] = csv.GetName()
		}

		// Create an install plan that depends on all CSVs in the catalog
		plan := &v1alpha1.InstallPlan{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.InstallPlanKind,
				APIVersion: v1alpha1.InstallPlanAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "install-everything",
				Namespace: "default",
			},
			Spec: v1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: csvNames,
			},
		}

		// Attempt to resolve the install plan
		_, _, err = dependencyResolver.ResolveInstallPlan(catalogs, nil, "", plan)

	}

	return err
}

func TestReleaseCatalogs(t *testing.T) {
	manifestDirBase := os.Getenv("GOPATH") + "/src/github.com/operator-framework/operator-lifecycle-manager/deploy/"
	manifestDirs := []string{manifestDirBase + "aos-olm/manifests", manifestDirBase + "upstream/manifests"}
	for _, d := range manifestDirs {
		VerifyCatalogVersions(t, d)
	}
}

func VerifyCatalogVersions(t *testing.T, manifestDir string) {
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
	catalogVersionBundles := map[string][]registry.SourceRef{}
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

				// Store by name
				catalogNameVersions[loadedCatalog.Name] = append(catalogNameVersions[loadedCatalog.Name], loadedCatalog)

				// Store by version
				sourceRef := registry.SourceRef{
					SourceKey: registry.ResourceKey{
						Name:      loadedCatalog.Name,
						Namespace: "default", // namespace is irrelevant (everything is loaded from files)
					},
					Source: loadedCatalog.Registry,
				}
				catalogVersionBundles[loadedCatalog.Version] = append(catalogVersionBundles[loadedCatalog.Version], sourceRef)
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

	// Ensure all resources are resolvable for all catalogs of each version
	multiSourceResolver := resolver.MultiSourceResolver{}
	for version, catalogs := range catalogVersionBundles {
		// capture range variables in lexical scope
		c := catalogs
		v := version
		testName := fmt.Sprintf("ResolvingResourcesForCatalogsInVersion-%s", version)
		t.Run(testName, func(t *testing.T) {
			t.Parallel()
			t.Logf("Resolving resources for catalogs in version %s...", v)
			err := resolveCatalogs(t, c, &multiSourceResolver)
			require.NoError(t, err)
		})
	}
}
