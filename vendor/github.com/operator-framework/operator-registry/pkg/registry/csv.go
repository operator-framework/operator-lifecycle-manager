package registry

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/operator-framework/api/pkg/operators"
)

const (
	// Name of the CSV's kind
	clusterServiceVersionKind = "ClusterServiceVersion"

	// Name of the section under which the list of owned and required list of
	// CRD(s) is specified inside an operator manifest.
	customResourceDefinitions = "customresourcedefinitions"

	// Name of the section under which the list of owned and required list of
	// apiservices is specified inside an operator manifest.
	apiServiceDefinitions = "apiservicedefinitions"

	// The yaml attribute that points to the name of an older
	// ClusterServiceVersion object that the current ClusterServiceVersion
	// replaces.
	replaces = "replaces"

	// The yaml attribute that points to the names of older
	// ClusterServiceVersion objects that the current ClusterServiceVersion
	// skips
	skips = "skips"

	// The yaml attribute that points to the icon for the ClusterServiceVersion
	icon = "icon"

	// The yaml attribute that points to the icon.base64data for the ClusterServiceVersion
	base64data = "base64data"

	// The yaml attribute that points to the icon.mediatype for the ClusterServiceVersion
	mediatype = "mediatype"
	// The yaml attribute that points to the description for the ClusterServiceVersion
	description = "description"

	// The yaml attribute that specifies the version of the ClusterServiceVersion
	// expected to be semver and parseable by blang/semver
	version = "version"

	// The yaml attribute that specifies the related images of the ClusterServiceVersion
	relatedImages = "relatedImages"

	// The yaml attribute that specifies the skipRange of the ClusterServiceVersion
	skipRangeAnnotationKey = "olm.skipRange"

	// The yaml attribute that specifies the optional substitutesfor of the ClusterServiceVersion
	substitutesForAnnotationKey = "olm.substitutesFor"
)

// ClusterServiceVersion is a structured representation of cluster service
// version object(s) specified inside the 'clusterServiceVersions' section of
// an operator manifest.
type ClusterServiceVersion struct {
	// Type metadata.
	metav1.TypeMeta `json:",inline"`

	// Object metadata.
	metav1.ObjectMeta `json:"metadata"`

	// Spec is the raw representation of the 'spec' element of
	// ClusterServiceVersion object. Since we are
	// not interested in the content of spec we are not parsing it.
	Spec json.RawMessage `json:"spec"`
}

// ReadCSVFromBundleDirectory tries to parse every YAML file in the directory without inspecting sub-directories and
// returns a CSV. According to the strict one CSV per bundle rule, func returns an error if more than one CSV is found.
func ReadCSVFromBundleDirectory(bundleDir string) (*ClusterServiceVersion, error) {
	dirContent, err := ioutil.ReadDir(bundleDir)
	if err != nil {
		return nil, fmt.Errorf("error reading bundle directory %s, %v", bundleDir, err)
	}

	files := []string{}
	for _, f := range dirContent {
		if !f.IsDir() {
			files = append(files, f.Name())
		}
	}

	csv := ClusterServiceVersion{}
	foundCSV := false
	for _, file := range files {
		yamlReader, err := os.Open(path.Join(bundleDir, file))
		if err != nil {
			continue
		}
		defer yamlReader.Close()

		unstructuredCSV := unstructured.Unstructured{}

		decoder := yaml.NewYAMLOrJSONDecoder(yamlReader, 30)
		if err = decoder.Decode(&unstructuredCSV); err != nil {
			continue
		}

		if unstructuredCSV.GetKind() != operators.ClusterServiceVersionKind {
			continue
		}

		if foundCSV {
			return nil, fmt.Errorf("more than one ClusterServiceVersion is found in bundle")
		}

		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredCSV.UnstructuredContent(),
			&csv); err != nil {
			return nil, err
		}
		foundCSV = true
	}

	if foundCSV {
		return &csv, nil
	}
	return nil, fmt.Errorf("no ClusterServiceVersion object found in %s", bundleDir)

}

// GetReplaces returns the name of the older ClusterServiceVersion object that
// is replaced by this ClusterServiceVersion object.
//
// If not defined, the function returns an empty string.
func (csv *ClusterServiceVersion) GetReplaces() (string, error) {
	var objmap map[string]*json.RawMessage
	if err := json.Unmarshal(csv.Spec, &objmap); err != nil {
		return "", err
	}

	rawValue, ok := objmap[replaces]
	if !ok || rawValue == nil {
		return "", nil
	}

	var replaces string
	if err := json.Unmarshal(*rawValue, &replaces); err != nil {
		return "", err
	}

	return replaces, nil
}

// GetVersion returns the version of the CSV
//
// If not defined, the function returns an empty string.
func (csv *ClusterServiceVersion) GetVersion() (string, error) {
	var objmap map[string]*json.RawMessage
	if err := json.Unmarshal(csv.Spec, &objmap); err != nil {
		return "", err
	}

	rawValue, ok := objmap[version]
	if !ok || rawValue == nil {
		return "", nil
	}

	var v string
	if err := json.Unmarshal(*rawValue, &v); err != nil {
		return "", err
	}

	return v, nil
}

// GetSkipRange returns the skiprange of the CSV
//
// If not defined, the function returns an empty string.
func (csv *ClusterServiceVersion) GetSkipRange() string {
	skipRange, ok := csv.Annotations[skipRangeAnnotationKey]
	if !ok {
		return ""
	}
	return skipRange
}

// GetSkips returns the name of the older ClusterServiceVersion objects that
// are skipped by this ClusterServiceVersion object.
//
// If not defined, the function returns an empty string.
func (csv *ClusterServiceVersion) GetSkips() ([]string, error) {
	var objmap map[string]*json.RawMessage
	if err := json.Unmarshal(csv.Spec, &objmap); err != nil {
		return nil, err
	}

	rawValue, ok := objmap[skips]
	if !ok || rawValue == nil {
		return nil, nil
	}

	var skips []string
	if err := json.Unmarshal(*rawValue, &skips); err != nil {
		return nil, err
	}

	return skips, nil
}

// GetCustomResourceDefintions returns a list of owned and required
// CustomResourceDefinition object(s) specified inside the
// 'customresourcedefinitions' section of a ClusterServiceVersion 'spec'.
//
// owned represents the list of CRD(s) managed by this ClusterServiceVersion
// object.
// required represents the list of CRD(s) that this ClusterServiceVersion
// object depends on.
//
// If owned or required is not defined in the spec then an empty list is
// returned respectively.
func (csv *ClusterServiceVersion) GetCustomResourceDefintions() (owned []*DefinitionKey, required []*DefinitionKey, err error) {
	var objmap map[string]*json.RawMessage

	if err = json.Unmarshal(csv.Spec, &objmap); err != nil {
		return
	}

	rawValue, ok := objmap[customResourceDefinitions]
	if !ok || rawValue == nil {
		return
	}

	var definitions struct {
		Owned    []*DefinitionKey `json:"owned"`
		Required []*DefinitionKey `json:"required"`
	}

	if err = json.Unmarshal(*rawValue, &definitions); err != nil {
		return
	}

	owned = definitions.Owned
	required = definitions.Required
	return
}

// GetApiServiceDefinitions returns a list of owned and required
// APISerivces specified inside the
// 'apiservicedefinitions' section of a ClusterServiceVersion 'spec'.
//
// owned represents the list of apiservices managed by this ClusterServiceVersion
// object.
// required represents the list of apiservices that this ClusterServiceVersion
// object depends on.
//
// If owned or required is not defined in the spec then an empty list is
// returned respectively.
func (csv *ClusterServiceVersion) GetApiServiceDefinitions() (owned []*DefinitionKey, required []*DefinitionKey, err error) {
	var objmap map[string]*json.RawMessage

	if err = json.Unmarshal(csv.Spec, &objmap); err != nil {
		return nil, nil, fmt.Errorf("error unmarshaling into object map: %s", err)
	}

	rawValue, ok := objmap[apiServiceDefinitions]
	if !ok || rawValue == nil {
		return
	}

	var definitions struct {
		Owned    []*DefinitionKey `json:"owned"`
		Required []*DefinitionKey `json:"required"`
	}

	if err = json.Unmarshal(*rawValue, &definitions); err != nil {
		return
	}

	owned = definitions.Owned
	required = definitions.Required
	return
}

// GetRelatedImage returns the list of associated images for the operator
func (csv *ClusterServiceVersion) GetRelatedImages() (imageSet map[string]struct{}, err error) {
	var objmap map[string]*json.RawMessage
	imageSet = make(map[string]struct{})

	if err = json.Unmarshal(csv.Spec, &objmap); err != nil {
		return
	}

	rawValue, ok := objmap[relatedImages]
	if !ok || rawValue == nil {
		return
	}

	type relatedImage struct {
		Name string `json:"name"`
		Ref  string `json:"image"`
	}
	var relatedImages []relatedImage
	if err = json.Unmarshal(*rawValue, &relatedImages); err != nil {
		return
	}

	for _, img := range relatedImages {
		imageSet[img.Ref] = struct{}{}
	}

	return
}

// GetOperatorImages returns a list of any images used to run the operator.
// Currently this pulls any images in the pod specs of operator deployments.
func (csv *ClusterServiceVersion) GetOperatorImages() (map[string]struct{}, error) {
	type dep struct {
		Name string
		Spec v1.DeploymentSpec
	}
	type strategySpec struct {
		Deployments []dep
	}
	type strategy struct {
		Name string       `json:"strategy"`
		Spec strategySpec `json:"spec"`
	}
	type csvSpec struct {
		Install strategy
	}

	var spec csvSpec
	if err := json.Unmarshal(csv.Spec, &spec); err != nil {
		return nil, err
	}

	// this is the only install strategy we know about
	if spec.Install.Name != "deployment" {
		return nil, nil
	}

	images := map[string]struct{}{}
	for _, d := range spec.Install.Spec.Deployments {
		for _, c := range d.Spec.Template.Spec.Containers {
			images[c.Image] = struct{}{}
		}
		for _, c := range d.Spec.Template.Spec.InitContainers {
			images[c.Image] = struct{}{}
		}
	}

	return images, nil
}

type Icon struct {
	MediaType  string `json:"mediatype"`
	Base64data []byte `json:"base64data"`
}

// GetIcons returns the icons from the ClusterServiceVersion
func (csv *ClusterServiceVersion) GetIcons() ([]Icon, error) {
	var objmap map[string]*json.RawMessage
	if err := json.Unmarshal(csv.Spec, &objmap); err != nil {
		return nil, err
	}

	rawValue, ok := objmap[icon]
	if !ok || rawValue == nil {
		return nil, nil
	}
	var icons []Icon
	if err := json.Unmarshal(*rawValue, &icons); err != nil {
		return nil, err
	}
	return icons, nil
}

// GetDescription returns the description from the ClusterServiceVersion
// If not defined, the function returns an empty string.
func (csv *ClusterServiceVersion) GetDescription() (string, error) {
	var objmap map[string]*json.RawMessage

	if err := json.Unmarshal(csv.Spec, &objmap); err != nil {
		return "", err
	}

	rawValue, ok := objmap[description]
	if !ok || rawValue == nil {
		return "", nil
	}

	var desc string
	if err := json.Unmarshal(*rawValue, &desc); err != nil {
		return "", err
	}

	return desc, nil
}

// GetSubstitutesFor returns the name of the ClusterServiceVersion object that
// is substituted by this ClusterServiceVersion object.
//
// If not defined, the function returns an empty string.
func (csv *ClusterServiceVersion) GetSubstitutesFor() string {
	substitutesFor, ok := csv.Annotations[substitutesForAnnotationKey]
	if !ok {
		return ""
	}
	return substitutesFor
}
