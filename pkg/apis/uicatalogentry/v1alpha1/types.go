package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	CSVv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
)

const (
	UICatalogEntryCRDName       = "uicatalogentry-v1s"
	UICatalogEntryCRDAPIVersion = "app.coreos.com/v1alpha1" // API version w/ CRD support
	UICatalogEntryKind          = "UICatalogEntry-v1"
	UICatalogEntryListKind      = "UICatalogEntryList-v1"
	GroupVersion                = "v1alpha1"
)

// UICatalogEntrySpec defines an Application that can be installed
type UICatalogEntrySpec struct {
	CSVv1alpha1.ClusterServiceVersionSpec
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "UICatalogEntrySpec" (UICatalogEntrySpec CRD created by ALM)
type UICatalogEntry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *UICatalogEntrySpec `json:"spec"`
	Status metav1.Status       `json:"status"`
}

func NewUICatalogEntryResource(app *UICatalogEntrySpec) *UICatalogEntry {
	return &UICatalogEntry{Spec: app}
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type UICatalogEntryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []*UICatalogEntrySpec `json:"items"`
}
