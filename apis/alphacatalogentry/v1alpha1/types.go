package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	CSVv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

const (
	AlphaCatalogEntryCRDName       = "alphacatalogentry-v1s"
	AlphaCatalogEntryCRDAPIVersion = "app.coreos.com/v1alpha1" // API version w/ CRD support
	AlphaCatalogEntryKind          = "AlphaCatalogEntry-v1"
	AlphaCatalogEntryListKind      = "AlphaCatalogEntryList-v1"
	GroupVersion                   = "v1alpha1"
)

// AlphaCatalogEntrySpec defines an Application that can be installed
type AlphaCatalogEntrySpec struct {
	CSVv1alpha1.ClusterServiceVersionSpec
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
	return &AlphaCatalogEntry{Spec: app}
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type AlphaCatalogEntryList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*AlphaCatalogEntrySpec `json:"items"`
}
