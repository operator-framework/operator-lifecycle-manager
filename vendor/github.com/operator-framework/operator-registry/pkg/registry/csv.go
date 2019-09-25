package registry

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
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

	// The yaml attribute that specifies the version of the ClusterServiceVersion
	// expected to be semver and parseable by blang/semver
	version = "version"
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
		return
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
