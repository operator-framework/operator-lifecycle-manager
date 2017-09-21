package alm

import (
	"encoding/json"

	"fmt"
	"reflect"

	"github.com/coreos/go-semver/semver"
	"k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/openapi"
	"k8s.io/apimachinery/pkg/runtime"
)

/////////////////
//  App Types  //
/////////////////

const (
	ALMGroup             = "app.coreos.com"
	AppTypeCRDName       = "apptype-v1s.app.coreos.com"
	AppTypeCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// AppType defines an Application that can be installed
type AppType struct {
	DisplayName string       `json:"displayName"`
	Description string       `json:"description"`
	Keywords    []string     `json:"keywords"`
	Maintainers []Maintainer `json:"maintainers"`
	Links       []AppLink    `json:"links"`
	Icon        Icon         `json:"iconURL"`
}

type Maintainer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type AppLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Icon struct {
	Data      string `json:"base64data"`
	MediaType string `json:"mediatype"`
}

// Custom Resource of type "AppType" (AppType CRD created by ALM)
type AppTypeResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *AppType      `json:"spec"`
	Status metav1.Status `json:"status"`
}

func NewAppTypeResource(app *AppType) *AppTypeResource {
	resource := AppTypeResource{}
	resource.Kind = AppTypeCRDName
	resource.APIVersion = AppTypeCRDAPIVersion
	resource.Spec = app
	return &resource
}

type AppTypeList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*AppType `json:"items"`
}

/////////////////////////////
//  Application Instances  //
/////////////////////////////

// CRD's representing the Apps that will be controlled by their OperatorVersionSpec-installed operator
type AppCRD struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   AppCRDSpec    `json:"spec"`
	Status metav1.Status `json:"status"`
}

// CRD's must correspond to this schema to be recognized by the ALM
type AppCRDSpec struct {
	metav1.GroupVersionForDiscovery `json:",inline"`

	Scope      string                    `json:"scope"`
	Validation openapi.OpenAPIDefinition `json:"validation"`
	Outputs    []AppOutput               `json:"outputs"`
	Names      ResourceNames             `json:"names"`
}

type AppOutput struct {
	Name         string   `json:"string"`
	Capabilities []string `json:"x-alm-capabilities,omitempty"`
	Description  string   `json:"description"`
}

type ResourceNames struct {
	Plural   string `json:"plural"`
	Singular string `json:"singular"`
	Kind     string `json:"kind"`
}

type InstallStrategy struct {
	StrategyName   string          `json:"strategy"`
	StategySpecRaw json.RawMessage `json:"spec"`
}

type StrategyDetailsDeployment struct {
	Deployments []v1beta1.DeploymentSpec `json:"deployments"`
}

type TypeMapper map[string]reflect.Type

func (m TypeMapper) GetStrategySpec(s *InstallStrategy) (interface{}, error) {
	t, found := m[s.StrategyName]
	if !found {
		return nil, fmt.Errorf("No stategy registered for name: %s", s.StrategyName)
	}

	v := reflect.New(t).Interface()
	err := json.Unmarshal(s.StategySpecRaw, v)
	if err != nil {
		return nil, err
	}
	return v, nil
}

var StrategyMapper = TypeMapper{
	"deployment": reflect.TypeOf(StrategyDetailsDeployment{}),
}

////////////////////////
//  Operator Version  //
////////////////////////

// OperatorVersionSpec declarations tell the ALM how to install an operator that can manage apps for
// given version and AppType
type OperatorVersionSpec struct {
	InstallStrategy InstallStrategy              `json:"install"`
	Version         semver.Version               `json:"version"`
	Maturity        string                       `json:"maturity"`
	Requirements    []*unstructured.Unstructured `json:"requirements"`
	Permissions     []string                     `json:"permissions"`
}

// Interface for these install strategies
type Installer interface {
	Method() string
	Install(namespace string, spec *unstructured.Unstructured) error
}

// CustomResource of type `OperatorVersionSpec`
type OperatorVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   OperatorVersionSpec `json:"spec"`
	Status metav1.Status       `json:"status"`
}

func (in *OperatorVersion) DeepCopyInto(out *OperatorVersion) {
	*out = *in
	return
}

func (in *OperatorVersion) DeepCopy() *OperatorVersion {
	if in == nil {
		return nil
	}
	out := new(OperatorVersion)
	in.DeepCopyInto(out)
	return out
}

func (in *OperatorVersion) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}

type OperatorVersionList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []OperatorVersion `json:"items"`
}

func (in *OperatorVersionList) DeepCopyInto(out *OperatorVersionList) {
	*out = *in
	return
}

func (in *OperatorVersionList) DeepCopy() *OperatorVersionList {
	if in == nil {
		return nil
	}
	out := new(OperatorVersionList)
	in.DeepCopyInto(out)
	return out
}

func (in *OperatorVersionList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}

const (
	OperatorVersionGroupVersion = "v1alpha1"
)

type OperatorVersionCRDSpec struct {
	metav1.GroupVersionForDiscovery `json:",inline"`

	Scope      string                    `json:"scope"`
	Validation openapi.OpenAPIDefinition `json:"validation"`
	Names      ResourceNames             `json:"names"`
}
