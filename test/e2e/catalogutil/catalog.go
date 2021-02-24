package catalogutil

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/onsi/ginkgo"
	"github.com/pkg/errors"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-registry/pkg/registry"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// log is used for logging operations within the catalogutil package
func log(s string) {
	ginkgo.GinkgoT().Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
}

// TargetCatalogType represents how a catalog should be built
type TargetCatalogType string

func (c TargetCatalogType) String() string {
	return string(c)
}

const (
	// Image represents catalog operations based on bundles (i.e. opm index commands)
	Image TargetCatalogType = "image"
	// Registry represents catalog operations based on operator-registry database updates (i.e. opm registry commands)
	Registry TargetCatalogType = "registry"
)

// GraphUpdateMode is the string argument to opm registry add --mode <string>
type GraphUpdateMode string

func (c GraphUpdateMode) String() string {
	return string(c)
}

const (
	// Replaces tells opm registry add to use replaces mode
	Replaces GraphUpdateMode = "replaces"
	// Semver tells opm registry add to use semver mode
	Semver GraphUpdateMode = "semver"
	// SemverSkipPatch tells opm registry add to use semver-skippatch mode
	SemverSkipPatch GraphUpdateMode = "semver-skippatch"
)

// CRDInformationList is an array of CRDInformation
type CRDInformationList []CRDInformation

// GetCRDDescription extracts the underlying operatorsv1alpha1.CRDDescription from the array of CRDInformationList
func (c CRDInformationList) GetCRDDescription() []operatorsv1alpha1.CRDDescription {
	crds := []operatorsv1alpha1.CRDDescription{}
	for _, crdDescription := range c {
		crds = append(crds, crdDescription.Description)
	}
	return crds
}

// CRDInformation groups together CRD description (used in CSV owned and dependency GVK) along with CRD specific information
type CRDInformation struct {
	Description  operatorsv1alpha1.CRDDescription // provides GVK and other info to CSV and CRD
	PluralName   string                           // the plural name to use for a CRD
	SingluarName string                           // the singular name to use for a CRD
	Group        string                           // the group name
}

// constants (or as close as we can get) for CatalogEntry.OwnedGVKs and CatalogEntry.DependencyGVKs.
var (
	A1v1CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorA", Name: "testoperatoras.sample.ibm.com", Version: "v1", DisplayName: "TestOperatorA", Description: "TestOperatorA Description",
			},
			PluralName:   "testoperatoras",
			SingluarName: "testoperatora",
			Group:        "sample.ibm.com",
		},
	}
	A1v2CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorA", Name: "testoperatoras.sample.ibm.com", Version: "v2", DisplayName: "TestOperatorA", Description: "TestOperatorA Description",
			},
			PluralName:   "testoperatoras",
			SingluarName: "testoperatora",
			Group:        "sample.ibm.com",
		},
	}
	A1v3CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorA", Name: "testoperatoras.sample.ibm.com", Version: "v3", DisplayName: "TestOperatorA", Description: "TestOperatorA Description",
			},
			PluralName:   "testoperatoras",
			SingluarName: "testoperatora",
			Group:        "sample.ibm.com",
		},
	}
	A1v4CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorA", Name: "testoperatoras.sample.ibm.com", Version: "v4", DisplayName: "TestOperatorA", Description: "TestOperatorA Description",
			},
			PluralName:   "testoperatoras",
			SingluarName: "testoperatora",
			Group:        "sample.ibm.com",
		},
	}
	B1v1CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorB", Name: "testoperatorbs.sample.ibm.com", Version: "v1", DisplayName: "TestOperatorB", Description: "TestOperatorB Description",
			},
			PluralName:   "testoperatorbs",
			SingluarName: "testoperatorb",
			Group:        "sample.ibm.com",
		},
	}
	B1v2CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorB", Name: "testoperatorbs.sample.ibm.com", Version: "v2", DisplayName: "TestOperatorB", Description: "TestOperatorB Description",
			},
			PluralName:   "testoperatorbs",
			SingluarName: "testoperatorb",
			Group:        "sample.ibm.com",
		},
	}
	B1v3CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorB", Name: "testoperatorbs.sample.ibm.com", Version: "v3", DisplayName: "TestOperatorB", Description: "TestOperatorB Description",
			},
			PluralName:   "testoperatorbs",
			SingluarName: "testoperatorb",
			Group:        "sample.ibm.com",
		},
	}
	B1v4CRDDescription = CRDInformationList{
		{
			Description: operatorsv1alpha1.CRDDescription{
				Kind: "TestOperatorB", Name: "testoperatorbs.sample.ibm.com", Version: "v4", DisplayName: "TestOperatorB", Description: "TestOperatorB Description",
			},
			PluralName:   "testoperatorbs",
			SingluarName: "testoperatorb",
			Group:        "sample.ibm.com",
		},
	}
)

// CRD v1
var (
	// V1CRDVersion - initial version of CRD
	V1CRDVersion = []apiextensionsv1.CustomResourceDefinitionVersion{
		{
			Name:    "v1",
			Served:  true,
			Storage: true,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
	}
	// V2CRDVersion - second version of CRD (v1 is served and v2 stored)
	V2CRDVersion = []apiextensionsv1.CustomResourceDefinitionVersion{
		{
			Name:    "v1",
			Served:  true,
			Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
		{
			Name:    "v2",
			Served:  true,
			Storage: true,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
	}
	// V3CRDVersion - third version of CRD (v1 is present only, v2 is served and v3 is stored)
	V3CRDVersion = []apiextensionsv1.CustomResourceDefinitionVersion{
		{
			Name:    "v1",
			Served:  false,
			Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
		{
			Name:    "v2",
			Served:  true,
			Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
		{
			Name:    "v3",
			Served:  true,
			Storage: true,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
	}
	// V4CRDVersion - fourth version of CRD (v1 is removed, v2 is present only, v3 is served, and v4 is stored)
	V4CRDVersion = []apiextensionsv1.CustomResourceDefinitionVersion{
		{
			Name:    "v2",
			Served:  false,
			Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
		{
			Name:    "v3",
			Served:  true,
			Storage: false,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
		{
			Name:    "v4",
			Served:  true,
			Storage: true,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {
							Type:     "object",
							Required: []string{"requiredString"},
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"requiredString": {
									Type: "string",
								},
							},
						},
					},
				},
			},
		},
	}
)

// CRD v1beta1
var (
	// V1CRDVersionV1beta1 - initial version of CRD
	V1CRDVersionV1beta1 = []apiextensionsv1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1",
			Served:  true,
			Storage: true,
		},
	}
	// V2CRDVersionV1beta1 - second version of CRD (v1 is served and v2 stored)
	V2CRDVersionV1beta1 = []apiextensionsv1beta1.CustomResourceDefinitionVersion{

		{
			Name:    "v1",
			Served:  true,
			Storage: false,
		},
		{
			Name:    "v2",
			Served:  true,
			Storage: true,
		},
	}
	// V3CRDVersionV1beta1 - third version of CRD (v1 is present only, v2 is served and v3 is stored)
	V3CRDVersionV1beta1 = []apiextensionsv1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v1",
			Served:  false,
			Storage: false,
		},
		{
			Name:    "v2",
			Served:  true,
			Storage: false,
		},
		{
			Name:    "v3",
			Served:  true,
			Storage: true,
		},
	}
	// V4CRDVersionV1beta1 - fourth version of CRD (v1 is removed, v2 is present only, v3 is served, and v4 is stored)
	V4CRDVersionV1beta1 = []apiextensionsv1beta1.CustomResourceDefinitionVersion{
		{
			Name:    "v2",
			Served:  false,
			Storage: false,
		},
		{
			Name:    "v3",
			Served:  true,
			Storage: false,
		},
		{
			Name:    "v4",
			Served:  true,
			Storage: true,
		},
	}
)

// CatalogEntry provides information needed for creating a bundle image in a catalog
type CatalogEntry struct {
	Version                 semver.Version                   // CSV version
	ReplacesVersion         string                           // Prior CSV version this CSV replaces
	SkipRange               string                           // SemVer range used in olm.skipRange annotation
	DefaultChannel          string                           // Default channel for the package
	Channels                []string                         // One or more channels that the bundle image should belong to
	NewIndex                bool                             // Flag to indicate if a new empty catalog index should be built
	PackageName             string                           // The OLM package name
	OwnedGVKs               CRDInformationList               // CRDs that are owned by the CSV
	DependencyGVKs          CRDInformationList               // CRDs that are dependencies (used in CSV required section and dependencies.yaml)
	DependencyPackages      []registry.PackageDependency     // OLM package dependencies (used in dependencies.yaml)
	CrdVersions             interface{}                      // CRD versions ([]apiextensionsv1.CustomResourceDefinitionVersion or []apiextensionsv1beta1.CustomResourceDefinitionVersion)
	Addmode                 GraphUpdateMode                  // graph update mode that defines how channel graphs are updated. One of: [replaces, semver, semver-skippatch]
	ConfigMap               *corev1.ConfigMap                // an optional config map to add to a catalog bundle
	Secret                  *corev1.Secret                   // an optional secret to add to a catalog bundle
	OperatorImage           string                           // the operator image to use in the CSV deployment section
	OperatorCommand         []string                         // the operator command to execute in the CSV deployment section
	GenerateAnnotationsYaml bool                             // Flag to indicate if a annotations.yaml file should be generated manually or via "opm alpha bundle build"
	Skips                   []string                         // An optional list of name(s) of one or more CSV(s) that should be skipped in the upgrade graph (see operatorsv1alpha1.Skips doc for more info)
	RelatedImages           []operatorsv1alpha1.RelatedImage // an optional list of related images (see operatorsv1alpha1.RelatedImage docs for more info)

	BundleImageWithDigest string // Storage of the resulting bundle image reference (i.e. output variable set after bundle creation)
}

// OpmBinarySourceImage defines the docker image to use for catalog operations (usually as a builder stage)
type OpmBinarySourceImage string

func (c OpmBinarySourceImage) String() string {
	return string(c)
}

const (
	// Upstream1_15 is the docker image for opm 1.15 from the upstream OLM project
	Upstream1_15 OpmBinarySourceImage = "quay.io/operator-framework/upstream-opm-builder:v1.15.0"
	// Downstream4_5 is the docker image for operator registry 4.5 from the downstream OLM project
	Downstream4_5 OpmBinarySourceImage = "registry.redhat.io/openshift4/ose-operator-registry:v4.5"
	// Downstream4_6 is the docker image for operator registry 4.5 from the downstream OLM project
	Downstream4_6 OpmBinarySourceImage = "registry.redhat.io/openshift4/ose-operator-registry:v4.6"
)

// CatalogFromImage defines the docker image to use in a FROM instruction within a dockerfile (usually as the final stage image)
type CatalogFromImage string

func (c CatalogFromImage) String() string {
	return string(c)
}

const (
	// Ubi7 is the docker image used as a FROM instruction within a dockerfile (usually as the final stage image)
	Ubi7 CatalogFromImage = "registry.redhat.io/ubi7/ubi"
	// Ubi8 is the docker image used as a FROM instruction within a dockerfile (usually as the final stage image)
	Ubi8 CatalogFromImage = "registry.redhat.io/ubi8/ubi"
)

// Oc defines the version for the oc command
type Oc string

func (c Oc) String() string {
	return string(c)
}

const (
	// Ocv4_5_0 is the 4.5 version of the oc command
	Ocv4_5_0 Oc = "ocv4.5.0"
)

// Opmup defines the version for the upstream opm command
type Opmup string

func (c Opmup) String() string {
	return string(c)
}

// upstream opm versions
const (
	Opmupv1_15_1 Opmup = "opmupv1.15.1"
	Opmupv1_15_2 Opmup = "opmupv1.15.2"
)

// Opmdown defines the version for the downstream opm command
type Opmdown string

func (c Opmdown) String() string {
	return string(c)
}

// downstream opm versions
const (
	Opmdownv1_14_3 Opmdown = "opmdownv1.14.3"
)

// ContainerCLI defines the version for the container (docker or podman) command
type ContainerCLI string

func (c ContainerCLI) String() string {
	return string(c)
}

// container commands
const (
	Docker ContainerCLI = "docker"
	Podman ContainerCLI = "podman"
)

// Stack represents the combination of arguments needed to generate a catalog for a specific environment
type Stack struct {
	OpmBinarySourceImage OpmBinarySourceImage // docker image to use for catalog operations (usually as a builder stage)
	CatalogFromImage     CatalogFromImage     // docker image to use in a FROM instruction within a catalog dockerfile
	CatalogName          string               // name of the catalog to build
	CatalogTag           string               // tag of the catalog to build
	Oc                   Oc                   // name and version of oc command
	Opmup                Opmup                // name and version of upstream opm command
	Opmdown              Opmdown              // name and version of downstream opm command
	OpmDebug             bool                 // should opm be run in debug mode
	ContainerCLI         ContainerCLI         // The container tool to use
	TargetRegistry       string               // the target image registry
	TargetCatalogType    TargetCatalogType    // IMAGE | REGISTRY
}

/*
GetOPM returns the desired opm executable string to use (i.e. either Stack.Opmup or Stack.Opmdown must be set).
If both Stack.Opmup and Stack.Opmdown are set Stack.Opmup is preferred.
Can return error if neither of these are set.
*/
func (s *Stack) GetOPM() (string, error) {
	// check which one is provided and return it otherwise error
	if s.Opmup != "" {
		return s.Opmup.String(), nil
	} else if s.Opmdown != "" {
		return s.Opmdown.String(), nil
	} else {
		return "", errors.New("OPM is not configured correctly")
	}
}

/*
IsUpstream returns true if Stack.Opmup is configured otherwise false.
Function checks both Stack.Opmup and Stack.Opmdown.
Can return error if neither of these are set.
*/
func (s *Stack) IsUpstream() (bool, error) {
	// check which one is provided and return it otherwise error
	if s.Opmup != "" {
		return true, nil
	} else if s.Opmdown != "" {
		return false, nil
	} else {
		return false, errors.New("OPM is not configured correctly")
	}
}

// GetCatalogLatest is a helper function to get the configured catalog in the target registry using "latest" tag
func (s *Stack) GetCatalogLatest() string {
	catalogLatest := fmt.Sprintf("%s/%s:latest", s.TargetRegistry, s.CatalogName)
	return catalogLatest
}

// DependenciesFile uses embedding to enhance the operator registry DependenciesFile to provide additional functionality
type DependenciesFile struct {
	*registry.DependenciesFile
}

// NewDependenceiesFile should be used to create instances of DependenciesFile
func NewDependenceiesFile() DependenciesFile {
	return DependenciesFile{&registry.DependenciesFile{}}
}

/*
AddDependency allows callers to add instances of the following types via the dependencies argument:

github.com/operator-framework/operator-registry/pkg/registry.GVKDependency

github.com/operator-framework/operator-registry/pkg/registry.PackageDependency

Using any other type will result in an error. Validation will occur for each dependency provided
and invalid dependencies will not be added.

This function will accumulate errors encountered and return a single return separated error string.
Errors with malformed or unknown dependency types will not be added to the list of dependencies.

Example with error messages:

AddDependency(
		true,
		registry.GVKDependency{Group: "abc.com", Kind: "foobar", Version: "v1"},
		registry.GVKDependency{Group: "abc.com"},
		registry.PackageDependency{PackageName: "testoperatora", Version: "1.2.3"},
		1)

Collected errors:
	Dependency 1 Error 0: API Version is empty
	Dependency 1 Error 1: API Kind is empty
	Dependency 3 Error 0: unknown type: int
*/
func (depFile *DependenciesFile) AddDependency(enforceDependencyValidation bool, dependencies ...interface{}) error {
	errorsMap := map[int][]error{}

	addErr := func(argumentIndex int, errs ...error) {
		if errorList, ok := errorsMap[argumentIndex]; ok {
			errorList = append(errorList, errs...)
		} else {
			errorList = append(errorList, errs...)
			errorsMap[argumentIndex] = errorList
		}
	}

	for depIndex, dependency := range dependencies {
		discoveredType := ""

		switch dependency.(type) {
		case registry.GVKDependency:
			discoveredType = registry.GVKType
			gvk := dependency.(registry.GVKDependency)
			errs := gvk.Validate()
			if enforceDependencyValidation && len(errs) > 0 {
				addErr(depIndex, errs...)
				continue
			}
		case registry.PackageDependency:
			discoveredType = registry.PackageType
			gvk := dependency.(registry.PackageDependency)
			errs := gvk.Validate()
			if enforceDependencyValidation && len(errs) > 0 {
				addErr(depIndex, errs...)
				continue
			}
		default:
			addErr(depIndex, fmt.Errorf("unknown type: %T", dependency))
			continue
		}
		b, err := json.Marshal(dependency)
		if err != nil {
			addErr(depIndex, fmt.Errorf("Unable to marshal %s: %v", discoveredType, err))
			continue
		}
		depFile.Dependencies = append(depFile.Dependencies, registry.Dependency{Type: discoveredType, Value: b})
	}
	if len(errorsMap) > 0 {
		collectedErrorMsg := "Collected errors:\n"
		for argumentIndex, errorList := range errorsMap {
			for errorIndex, e := range errorList {
				collectedErrorMsg += fmt.Sprintf("\tDependency %d Error %d: %s\n", argumentIndex, errorIndex, e.Error())
			}
		}
		return errors.Errorf("%s", collectedErrorMsg)
	}
	return nil
}

// WriteFile writes contents to <destinationDir>/dependencies.yaml if dependencies are present
func (depFile *DependenciesFile) WriteFile(destinationDir string) error {
	// if there are no dependencies defined, there's no point in writing out the file
	if len(depFile.Dependencies) == 0 {
		return nil
	}

	yamlBytes, err := yaml.Marshal(depFile)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(destinationDir, "dependencies.yaml"), yamlBytes, os.FileMode(0644))
	if err != nil {
		return err
	}
	return nil
}

/*
CreateTemporaryCatalog is the main entry point for generating a temporary catalog for use with e2e tests.

Arguments:

• toolsBin: The absolute path to the directory that contains the tools needed to generate a catalog

• catalogEntries: a slice of catalog entries that need to be added into a catalog

• stack: contains arguments needed to generate a catalog for a specific environment

Returns:

error is non nil if anything prevented the catalog from being generated and pushed to the target registry
*/
func CreateTemporaryCatalog(toolsBin string, catalogEntries []CatalogEntry, stack Stack) error {
	// create the destination directory
	destDir, err := ioutil.TempDir("", "catalog")
	if err != nil {
		return err
	}

	// this is necessary for running on darwin systems
	destDir, err = filepath.EvalSymlinks(destDir)

	defer os.RemoveAll(destDir) // clean up

	olmCatalogDirectory := filepath.Join("deploy", "olm-catalog")

	for index := range catalogEntries {
		// since catalog entry is used for input/output, use address of array index
		catalogEntry := &catalogEntries[index]
		packageNameLower := strings.ToLower(catalogEntry.PackageName)

		version := catalogEntry.Version.String()

		bundlePath := filepath.Join(destDir, olmCatalogDirectory, packageNameLower, version)

		manifestDirectory := filepath.Join(bundlePath, "manifests")
		os.MkdirAll(manifestDirectory, os.ModePerm)
		if err != nil {
			return err
		}

		metadataDirectory := filepath.Join(bundlePath, "metadata")
		os.MkdirAll(metadataDirectory, os.ModePerm)
		if err != nil {
			return err
		}

		// Create CSV
		csv, err := createCSVTemplate(catalogEntry)
		if err != nil {
			return err
		}

		WriteCSV(csv, manifestDirectory, catalogEntry.PackageName, version)

		// Create CRD
		for _, ownedGVK := range catalogEntry.OwnedGVKs {
			crd, err := createCRD(ownedGVK, catalogEntry.CrdVersions)
			if err != nil {
				return err
			}

			err = WriteCRD(crd, manifestDirectory, ownedGVK.Description.Name)
			if err != nil {
				return err
			}
		}

		// Create ConfigMap
		if catalogEntry.ConfigMap != nil {
			err = WriteConfigMap(catalogEntry.ConfigMap, manifestDirectory, packageNameLower)
			if err != nil {
				return err
			}
		}

		// Create Secret
		if catalogEntry.Secret != nil {
			err = WriteSecret(catalogEntry.Secret, manifestDirectory, packageNameLower)
			if err != nil {
				return err
			}
		}

		if catalogEntry.GenerateAnnotationsYaml {
			// Create annotations.yaml
			err = WriteAnnotationsYaml(metadataDirectory, catalogEntry.PackageName, catalogEntry.Channels, catalogEntry.DefaultChannel)
			if err != nil {
				return err
			}
		}

		// Create dependency.yaml
		dep := NewDependenceiesFile()

		// add all dependencies for GVK and packages
		for _, dependencyGVK := range catalogEntry.DependencyGVKs {
			err = dep.AddDependency(true, registry.GVKDependency{Group: dependencyGVK.Description.Name, Kind: dependencyGVK.Description.Kind, Version: dependencyGVK.Description.Version})
			if err != nil {
				return err
			}
		}

		for _, dependencyPackage := range catalogEntry.DependencyPackages {
			err = dep.AddDependency(true, dependencyPackage)
			if err != nil {
				return err
			}
		}
		// attempt to write out content (if any present)
		err = dep.WriteFile(metadataDirectory)
		if err != nil {
			return err
		}

		err = CreateBundleImage(toolsBin, destDir, manifestDirectory, catalogEntry, stack)
		if err != nil {
			return err
		}
	}
	// execute opm with either index or registry mode
	if stack.TargetCatalogType == Image {
		for _, catalogEntry := range catalogEntries {
			err = IndexAdd(toolsBin, destDir, catalogEntry.BundleImageWithDigest, catalogEntry.NewIndex, stack)
			if err != nil {
				return err
			}
			// push the image to its destination registry after each bundle is added
			// this is needed for subsequent entries after the first
			err = pushCommand(stack, stack.GetCatalogLatest())
			if err != nil {
				return err
			}
		}
	} else if stack.TargetCatalogType == Registry {
		for index := range catalogEntries {
			catalogEntry := &catalogEntries[index]
			err = RegistryAdd(toolsBin, destDir, catalogEntry, stack)
			if err != nil {
				return err
			}
		}
		err = CreateIndexFromDatabase(destDir, stack)
		if err != nil {
			return err
		}
		// finally push the image to its destination registry
		err = pushCommand(stack, stack.GetCatalogLatest())
		if err != nil {
			return err
		}
	}

	return nil
}

/*
CreateBundleImage is a helper function that creates a bundle image

Arguments:

• toolsbin: The absolute path to the directory that contains the tools needed to generate a catalog

• destinationDir: The "root destination" directory that contains the manifest files (where the docker file will end up)

• manifestDirectory: The "manifest" directory

• catalogEntry: a single catalog entry that contains the information needed to create a bundle image. Note that catalogEntry is modified
so that the catalogEntry.BundleImageWithDigest contains the bundle image that was created

• stack: contains arguments needed to generate a bundle for a specific environment

Returns:

error is non nil if anything prevented the bundle image from being generated and pushed to the target registry

*/
func CreateBundleImage(toolsbin string, destinationDir string, manifestDirectory string, catalogEntry *CatalogEntry, stack Stack) error {

	opm, err := stack.GetOPM()
	if err != nil {
		return err
	}

	packageNameLower := strings.ToLower(catalogEntry.PackageName)

	destinationImage := fmt.Sprintf("%s/%s:v%s", stack.TargetRegistry, packageNameLower, catalogEntry.Version)
	// abc, err := os.Getwd()
	// _ = abc

	// before we run "opm alpha bundle build" command blow away the docker file that might be left over from previous executions
	// this will get recreated with the correct content when opm runs
	os.Remove(filepath.Join(destinationDir, "bundle.Dockerfile"))

	cmd := exec.Command(
		filepath.Join(toolsbin, opm), "alpha", "bundle", "build",
		"--tag", destinationImage,
		"--directory", manifestDirectory,
		"-b", stack.ContainerCLI.String(),
		"--package", packageNameLower,
		"--channels", strings.Join(catalogEntry.Channels, ","),
		"--default", catalogEntry.DefaultChannel,
	)

	if !catalogEntry.GenerateAnnotationsYaml {
		// we're configured to allow the annotations.yaml file to be generated by opm alpha bundle build
		cmd.Args = append(cmd.Args, "--overwrite")
	}
	// this will be the destination for the generated docker file for the bundle
	cmd.Dir = destinationDir
	log(fmt.Sprintf("Executing command: %v", cmd))
	cmdOutputBytes, err := cmd.CombinedOutput()
	log(string(cmdOutputBytes))
	if err != nil {
		return err
	}

	err = pushCommand(stack, destinationImage)
	if err != nil {
		return err
	}

	cmd = exec.Command("skopeo", "inspect", "--tls-verify=false", fmt.Sprintf("docker://%s", destinationImage))
	log(fmt.Sprintf("Executing command: %v", cmd))
	cmdOutputBytes, err = cmd.Output()
	if err != nil {
		return err
	}

	cmdOutputMap := map[string]interface{}{}
	err = json.Unmarshal(cmdOutputBytes, &cmdOutputMap)
	if err != nil {
		return err
	}
	digest, ok := cmdOutputMap["Digest"]
	if !ok {
		return errors.New("Unable to get digest from skopeo inspect")
	}
	destinationImageWithDigest := fmt.Sprintf("%s/%s@%s", stack.TargetRegistry, packageNameLower, digest)
	catalogEntry.BundleImageWithDigest = destinationImageWithDigest
	return nil
}

/*
RegistryAdd is a function that uses "opm registry add" to add a catalog entry to a catalog

Arguments:

• toolsbin: The absolute path to the directory that contains the tools needed to generate a catalog

• destinationDir: The "root destination" directory that contains the manifest files (where the bundles.db will end up)

• catalogEntry: a single catalog entry that contains the information needed to add a bundle image to a catalog. Note that
catalogEntry.NewIndex controls when a database should be created from scratch before adding an entry

• stack: contains arguments needed to generate a catalog for a specific environment

Returns:

error is non nil if anything prevented the bundle image from being added to the database
*/
func RegistryAdd(toolsbin string, destinationDir string, catalogEntry *CatalogEntry, stack Stack) error {

	opm, err := stack.GetOPM()
	if err != nil {
		return err
	}

	// Build using opm registry commands
	databaseFile := filepath.Join(destinationDir, "bundles.db")

	// if we want a new index, blow away any database file that might be present
	if catalogEntry.NewIndex {
		// remove any existing database
		os.Remove(databaseFile)
	}
	cmd := exec.Command(
		filepath.Join(toolsbin, opm), "registry", "add",
		"--skip-tls",
		"-b", catalogEntry.BundleImageWithDigest,
		"-c", stack.ContainerCLI.String(),
		"--mode", catalogEntry.Addmode.String(),
	)
	if stack.OpmDebug {
		setOPMDebug(cmd)
	}
	cmd.Dir = destinationDir
	log(fmt.Sprintf("Executing command: %v", cmd))
	cmdOutputBytes, err := cmd.CombinedOutput()
	log(string(cmdOutputBytes))
	if err != nil {
		return err
	}

	sqlLiteCommand(databaseFile, "--package--", "select * from package")
	sqlLiteCommand(databaseFile, "--channel--", "select * from channel")
	sqlLiteCommand(databaseFile, "--channel-entry--", "select * from channel_entry")
	sqlLiteCommand(databaseFile, "--operatorbundle--", "select name,bundlepath,replaces,skips,skiprange,length(csv),length(bundle) from operatorbundle;")
	return nil
}

/*
CreateIndexFromDatabase is a function that generates a catalog image using a bundles.db. The
dockerfile is configured for either upstream or downstream use depending on the values provided in stack.

Arguments:

• destinationDir: The "root destination" directory that contains the manifest files (where the bundles.db is located)

• stack: contains arguments needed to generate a catalog for a specific environment

Returns:

error is non nil if anything prevented the catalog image from being created (NOTE: pushing occurs independently)
*/
func CreateIndexFromDatabase(destinationDir string, stack Stack) error {

	// if not supplied, default to a scratch image
	if stack.CatalogFromImage == "" {
		stack.CatalogFromImage = "scratch"
	}
	isUpstream, err := stack.IsUpstream()
	if err != nil {
		return err
	}

	var copyFromLine string
	var additionalLabel string
	var entryPointLine string
	var cmdLine string
	if isUpstream {
		copyFromLine = "COPY --from=builder /bin/opm /bin/opm"
		additionalLabel = ""
		entryPointLine = `ENTRYPOINT ["/bin/opm"]`
		cmdLine = `CMD ["registry", "serve", "--database", "/database/index.db"]`
	} else {
		copyFromLine = "COPY --from=builder /usr/bin/registry-server /registry-server"
		additionalLabel = "LABEL sample.ibm.com.catalog.version=v4.6"
		entryPointLine = `ENTRYPOINT ["/registry-server"]`
		cmdLine = `CMD ["--database", "/database/index.db"]`
	}

	dockerFileAsString := fmt.Sprintf(`FROM %s AS builder
FROM %s
COPY bundles.db /database/index.db
%s
LABEL operators.operatorframework.io.index.database.v1=/database/index.db
%s
COPY --from=builder /bin/grpc_health_probe /bin/grpc_health_probe
EXPOSE 50051
%s
%s`,
		stack.OpmBinarySourceImage,
		stack.CatalogFromImage,
		additionalLabel,
		copyFromLine,
		entryPointLine,
		cmdLine)

	log(dockerFileAsString)

	// write the dockerfile to stdin to build the image
	cmd := exec.Command(stack.ContainerCLI.String(), "build", "-t", stack.GetCatalogLatest(), "-f", "-", destinationDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer stdin.Close()
		io.WriteString(stdin, dockerFileAsString)
	}()
	cmdOutputBytes, err := cmd.CombinedOutput()
	log(string(cmdOutputBytes))
	if err != nil {
		return err
	}
	return nil
}

/*
IndexAdd is a function that uses "opm index add" to add a catalog entry to a catalog

Arguments:

• toolsbin: The absolute path to the directory that contains the tools needed to generate a catalog

• destinationDir: The "root destination" directory that contains the manifest files

• bundleImageWithDigest: the bundle image to add to the catalog

• newIndex: controls when a database should be created from scratch before adding an entry

• stack: contains arguments needed to generate a catalog for a specific environment

Returns:

error is non nil if anything prevented the bundle image from being added to the database
*/
func IndexAdd(toolsbin string, destinationDir string, bundleImageWithDigest string, newIndex bool, stack Stack) error {

	opm, err := stack.GetOPM()
	if err != nil {
		return err
	}
	catalogLatest := stack.GetCatalogLatest()
	// Build using opm index commands
	if newIndex {
		// build a new index
		cmd := exec.Command(
			filepath.Join(toolsbin, opm), "index", "add", "--skip-tls",
			"-b", bundleImageWithDigest,
			"-c", stack.ContainerCLI.String(),
			"--tag", catalogLatest,
			"--binary-image", stack.OpmBinarySourceImage.String())
		if stack.OpmDebug {
			setOPMDebug(cmd)
		}
		cmd.Dir = destinationDir
		log(fmt.Sprintf("Executing command: %v", cmd))
		cmdOutputBytes, err := cmd.CombinedOutput()
		log(string(cmdOutputBytes))
		if err != nil {
			return err
		}
	} else {
		cmd := exec.Command(
			filepath.Join(toolsbin, opm), "index", "add",
			"-b", bundleImageWithDigest,
			"-c", stack.ContainerCLI.String(),
			"--tag", catalogLatest,
			"--from-index", catalogLatest,
			"--binary-image", stack.OpmBinarySourceImage.String())
		if stack.OpmDebug {
			setOPMDebug(cmd)
		}
		cmd.Dir = destinationDir
		log(fmt.Sprintf("Executing command: %v", cmd))
		cmdOutputBytes, err := cmd.CombinedOutput()
		log(string(cmdOutputBytes))
		if err != nil {
			return err
		}
	}
	return nil
}

// pushCommand is a helper function to push an image to a target registry
func pushCommand(stack Stack, destinationImage string) error {
	var pushCommand []string
	if stack.ContainerCLI == "podman" {
		// clear cache in home directory
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		// ignore errors during removal
		os.Remove(filepath.Join("var", "lib", "containers", "cache", "blob-info-cache-v1.boltdb"))
		os.Remove(filepath.Join(homeDir, ".local", "share", "containers", "cache", "blob-info-cache-v1.boltdb"))

		pushCommand = []string{stack.ContainerCLI.String(), "push", "--format", "v2s2", destinationImage}
	} else {
		pushCommand = []string{stack.ContainerCLI.String(), "push", destinationImage}
	}
	cmd := exec.Command(pushCommand[0], pushCommand[1:]...)
	log(fmt.Sprintf("Executing command: %v", cmd))
	cmdOutputBytes, err := cmd.CombinedOutput()
	log(string(cmdOutputBytes))
	if err != nil {
		return err
	}
	return nil
}

// sqlLiteCommand is a helper function that executes a sqllite3 command
func sqlLiteCommand(bundleDatabaseFile string, message string, selectStatement string) {
	log(message)
	cmd := exec.Command("sqlite3", bundleDatabaseFile, selectStatement)
	log(fmt.Sprintf("Executing command: %v", cmd))
	// run command but ignore errors
	cmdOutputBytes, _ := cmd.CombinedOutput()
	log(string(cmdOutputBytes))
}

// setOPMDebug is a helper function to add the debug flag to an opm executable command
func setOPMDebug(cmd *exec.Cmd) {
	cmd.Args = append(cmd.Args, "--debug")
}

/*
WriteAnnotationsYaml creates an annotations.yaml file with a combination of default values and the provided arguments

Arguments:

• destinationDir: The metadata directory where the file will be written to

• packageName: the OLM package name used in the package label

• channels: a slice of channels used in the channels label

• channelDefault: the default channel to be used in the default channel label

Returns:

error is non nil if anything prevented the bundle image from being added to the database
*/
func WriteAnnotationsYaml(destinationDir string, packageName string, channels []string, channelDefault string) error {
	yamlBytes, err := bundle.GenerateAnnotations(bundle.RegistryV1Type, bundle.ManifestsDir, bundle.MetadataDir, packageName, strings.Join(channels, ","), channelDefault)
	if err != nil {
		return err
	}

	err = ioutil.WriteFile(filepath.Join(destinationDir, bundle.AnnotationsFile), yamlBytes, os.FileMode(0644))
	if err != nil {
		return err
	}
	return nil
}

// createCRD is a helper function that creates a CRD using either v1 or v1beta1 apis
func createCRD(ownedCRD CRDInformation, versions interface{}) (interface{}, error) {

	switch versions.(type) {
	case []apiextensionsv1.CustomResourceDefinitionVersion:
		return createV1CRD(ownedCRD, versions.([]apiextensionsv1.CustomResourceDefinitionVersion)), nil
	case []apiextensionsv1beta1.CustomResourceDefinitionVersion:
		return createV1Beta1CRD(ownedCRD, versions.([]apiextensionsv1beta1.CustomResourceDefinitionVersion)), nil
	default:
		return nil, errors.New("Incorrect datatype for versions argument. Must be []apiextensionsv1.CustomResourceDefinitionVersion or []apiextensionsv1beta1.CustomResourceDefinitionVersion")
	}
}

// createV1CRD creates a v1 CRD
func createV1CRD(ownedCRD CRDInformation, versions []apiextensionsv1.CustomResourceDefinitionVersion) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: apiextensionsv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: ownedCRD.Description.Name,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: ownedCRD.Group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     ownedCRD.Description.Kind,
				Plural:   ownedCRD.PluralName,
				Singular: ownedCRD.SingluarName,
			},
			Scope:    apiextensionsv1.NamespaceScoped,
			Versions: versions,
		},
	}
}

// createV1Beta1CRD creates a v1beta1 CRD
func createV1Beta1CRD(ownedCRD CRDInformation, versions []apiextensionsv1beta1.CustomResourceDefinitionVersion) *apiextensionsv1beta1.CustomResourceDefinition {
	result := apiextensionsv1beta1.CustomResourceDefinition{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CustomResourceDefinition",
			APIVersion: apiextensionsv1beta1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: ownedCRD.Description.Name,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group: ownedCRD.Group,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Kind:     ownedCRD.Description.Kind,
				Plural:   ownedCRD.PluralName,
				Singular: ownedCRD.SingluarName,
			},
			Scope: apiextensionsv1beta1.NamespaceScoped,
			Subresources: &apiextensionsv1beta1.CustomResourceSubresources{
				Status: &apiextensionsv1beta1.CustomResourceSubresourceStatus{},
			},
			Versions: versions,
		},
	}
	return &result
}

/*
WriteCRD takes a CRD (whose type is either *apiextensionsv1beta1.CustomResourceDefinition or *apiextensionsv1.CustomResourceDefinition)
and writes its contents to a file at destinationDir using crdName as a portion of the file name.
*/
func WriteCRD(crd interface{}, destinationDir string, crdName string) error {

	switch crd.(type) {
	case *apiextensionsv1beta1.CustomResourceDefinition, *apiextensionsv1.CustomResourceDefinition:
		yamlBytes, err := yaml.Marshal(crd)
		if err != nil {
			return err
		}
		err = ioutil.WriteFile(filepath.Join(destinationDir, fmt.Sprintf("%s-crd.yaml", crdName)), yamlBytes, os.FileMode(0644))
		if err != nil {
			return err
		}
	default:
		return errors.New("Incorrect datatype for crd argument. Must be *apiextensionsv1beta1.CustomResourceDefinition or *apiextensionsv1.CustomResourceDefinition")
	}
	return nil
}

// createCSVTemplate is a helper function for creating a CSV
func createCSVTemplate(catalogEntry *CatalogEntry) (*operatorsv1alpha1.ClusterServiceVersion, error) {
	if catalogEntry == nil {
		return nil, errors.New("Expected catalog entry to be non-nil")
	}

	packageNameLower := strings.ToLower(catalogEntry.PackageName)

	replacesString := ""
	if catalogEntry.ReplacesVersion != "" {
		replacesString = fmt.Sprintf("%s.v%s", packageNameLower, catalogEntry.ReplacesVersion)
	}

	singleInstance := int32(1)

	// setup annotations
	annotationMap := map[string]string{
		"capabilities": "Full Lifecycle",
	}

	if catalogEntry.SkipRange != "" {
		annotationMap["olm.skipRange"] = catalogEntry.SkipRange
	}

	// setup static environment vars
	envVars := []corev1.EnvVar{
		{
			Name: "WATCH_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.annotations['olm.targetNamespaces']",
				},
			},
		},
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name:  "OPERATOR_NAME",
			Value: packageNameLower,
		},
	}
	// add dynamic environment vars for related images
	for _, relatedImage := range catalogEntry.RelatedImages {
		envVars = append(envVars, corev1.EnvVar{
			// hack together the related image syntax
			Name:  fmt.Sprintf("RELATED_IMAGE_%s", strings.ToUpper(relatedImage.Name)),
			Value: relatedImage.Image,
		})
	}

	// setup CSV
	result := operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
			APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: annotationMap,
			Name:        fmt.Sprintf("%s.v%s", packageNameLower, catalogEntry.Version.String()),
			Namespace:   "placeholder",
		},
		Spec: operatorsv1alpha1.ClusterServiceVersionSpec{
			APIServiceDefinitions: operatorsv1alpha1.APIServiceDefinitions{},
			CustomResourceDefinitions: operatorsv1alpha1.CustomResourceDefinitions{
				Owned:    catalogEntry.OwnedGVKs.GetCRDDescription(),
				Required: catalogEntry.DependencyGVKs.GetCRDDescription(),
			},
			DisplayName: catalogEntry.PackageName,
			InstallStrategy: operatorsv1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: operatorsv1alpha1.StrategyDetailsDeployment{
					DeploymentSpecs: []operatorsv1alpha1.StrategyDeploymentSpec{
						{
							Name: packageNameLower,
							Spec: v1.DeploymentSpec{
								Replicas: &singleInstance,
								Selector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"name": packageNameLower},
								},
								Strategy: v1.DeploymentStrategy{},
								Template: corev1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{"name": packageNameLower},
									},
									Spec: corev1.PodSpec{
										Containers: []corev1.Container{
											{
												Command:         catalogEntry.OperatorCommand,
												Env:             envVars,
												Image:           catalogEntry.OperatorImage,
												ImagePullPolicy: corev1.PullAlways,
												Name:            packageNameLower,
												Resources:       corev1.ResourceRequirements{},
											},
										},
										ServiceAccountName: packageNameLower,
									},
								},
							},
						},
					},
					Permissions: []operatorsv1alpha1.StrategyDeploymentPermissions{
						{
							ServiceAccountName: packageNameLower,
							Rules: []rbacv1.PolicyRule{
								{
									APIGroups: []string{""},
									Resources: []string{"pods", "services", "services/finalizers", "endpoints", "persistentvolumeclaims", "events", "configmaps", "secrets"},
									Verbs:     []string{"*"},
								},
								{
									APIGroups: []string{"apps"},
									Resources: []string{"deployments", "daemonsets", "replicasets", "statefulsets"},
									Verbs:     []string{"*"},
								},
								{
									APIGroups: []string{"monitoring.coreos.com"},
									Resources: []string{"servicemonitors"},
									Verbs:     []string{"get", "create"},
								},
								{
									APIGroups:     []string{"apps"},
									ResourceNames: []string{packageNameLower},
									Resources:     []string{"deployments/finalizers"},
									Verbs:         []string{"update"},
								},
								{
									APIGroups: []string{""},
									Resources: []string{"pods"},
									Verbs:     []string{"get"},
								},
								{
									APIGroups: []string{"apps"},
									Resources: []string{"replicasets", "deployments"},
									Verbs:     []string{"get"},
								},
							},
						},
					},
				},
			},
			InstallModes: []operatorsv1alpha1.InstallMode{
				{
					Type:      operatorsv1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeMultiNamespace,
					Supported: false,
				},
				{
					Type:      operatorsv1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			Maturity: "alpha",
			Provider: operatorsv1alpha1.AppLink{},
			Replaces: replacesString,
			Version: version.OperatorVersion{
				Version: catalogEntry.Version,
			},
			RelatedImages: catalogEntry.RelatedImages,
			Skips:         catalogEntry.Skips,
		},
	}
	return &result, nil
}

// WriteCSV takes a ClusterServiceVersion and writes its contents to a file at destinationDir using packageName and version as a portion of the file name.
func WriteCSV(csv *operatorsv1alpha1.ClusterServiceVersion, destinationDir string, packageName string, version string) error {
	yamlBytes, err := yaml.Marshal(csv)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(destinationDir, fmt.Sprintf("%s-v%s-csv.yaml", packageName, version)), yamlBytes, os.FileMode(0644))
	if err != nil {
		return err
	}
	return nil
}

// WriteConfigMap takes a configmap and writes its contents to a file at destinationDir using configMapName as a portion of the file name.
func WriteConfigMap(configMap *corev1.ConfigMap, destinationDir string, configMapName string) error {
	yamlBytes, err := yaml.Marshal(configMap)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(destinationDir, fmt.Sprintf("%s-cm.yaml", configMapName)), yamlBytes, os.FileMode(0644))
	if err != nil {
		return err
	}
	return nil
}

// WriteSecret takes a secret and writes its contents to a file at destinationDir using secretName as a portion of the file name.
func WriteSecret(secret *corev1.Secret, destinationDir string, secretName string) error {
	yamlBytes, err := yaml.Marshal(secret)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(destinationDir, fmt.Sprintf("%s-cm.yaml", secretName)), yamlBytes, os.FileMode(0644))
	if err != nil {
		return err
	}
	return nil
}

// split is a helper function for splitting a plural.group into its separate parts
func split(pluralName string) (group string, plural string, err error) {
	parts := strings.SplitN(pluralName, ".", 2)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("couldn't parse plural.group from crd name: %s", pluralName)
	}
	return parts[1], parts[0], nil
}
