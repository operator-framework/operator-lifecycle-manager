package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	AlphaCatalogEntryCRDName       = "alphacatalogentry-v1s.app.coreos.com"
	AlphaCatalogEntryCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// AlphaCatalogEntrySpec defines an Application that can be installed
type AlphaCatalogEntrySpec struct {
	Name    string `json:"name"` // must match ClusterServiceVersion for now
	Version string `json:"version"`

	DisplayName string       `json:"displayName"`
	Description string       `json:"description"`
	Keywords    []string     `json:"keywords"`
	Maintainers []Maintainer `json:"maintainers"`
	Links       []AppLink    `json:"links"`
	Icon        Icon         `json:"iconURL"`
	Provider    string       `json:"provider"`
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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "AlphaCatalogEntrySpec" (AlphaCatalogEntrySpec CRD created by ALM)
type AlphaCatalogEntry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *AlphaCatalogEntrySpec `json:"spec"`
	Status metav1.Status          `json:"status"`
}

func NewAlphaCatalogEntryResource(app *AlphaCatalogEntrySpec) *AlphaCatalogEntry {
	resource := AlphaCatalogEntry{}
	resource.Kind = AlphaCatalogEntryCRDName
	resource.APIVersion = AlphaCatalogEntryCRDAPIVersion
	resource.Spec = app
	return &resource
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type AlphaCatalogEntryList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*AlphaCatalogEntrySpec `json:"items"`
}
