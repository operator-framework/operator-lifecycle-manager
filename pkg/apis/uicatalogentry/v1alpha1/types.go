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
	Manifest PackageManifest                       `json:"manifest"`
	CSVSpec  CSVv1alpha1.ClusterServiceVersionSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
type UICatalogEntry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *UICatalogEntrySpec `json:"spec"`
	Status metav1.Status       `json:"status"`
}

func NewUICatalogEntryResource(spec *UICatalogEntrySpec) *UICatalogEntry {
	return &UICatalogEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       UICatalogEntryKind,
			APIVersion: UICatalogEntryCRDAPIVersion,
		},
		Spec: spec,
	}
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type UICatalogEntryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []*UICatalogEntry `json:"items"`
}

// PackageManifest holds information about a package, which is a reference to one (or more)
// channels under a single package.
type PackageManifest struct {
	// PackageName is the name of the overall package, ala `etcd`.
	PackageName string `json:"packageName"`

	// Channels are the declared channels for the package, ala `stable` or `alpha`.
	Channels []PackageChannel `json:"channels"`

	// DefaultChannelName is, if specified, the name of the default channel for the package. The
	// default channel will be installed if no other channel is explicitly given. If the package
	// has a single channel, then that channel is implicitly the default.
	DefaultChannelName string `json:"defaultChannel"`
}

// GetDefaultChannel gets the default channel or returns the only one if there's only one. returns empty string if it
// can't determine the default
func (m PackageManifest) GetDefaultChannel() string {
	if m.DefaultChannelName != "" {
		return m.DefaultChannelName
	}
	if len(m.Channels) == 1 {
		return m.Channels[0].Name
	}
	return ""
}

// PackageChannel defines a single channel under a package, pointing to a version of that
// package.
type PackageChannel struct {
	// Name is the name of the channel, e.g. `alpha` or `stable`
	Name string `json:"name"`

	// CurrentCSVName defines a reference to the CSV holding the version of this package currently
	// for the channel.
	CurrentCSVName string `json:"currentCSV"`
}

// IsDefaultChannel returns true if the PackageChennel is the default for the PackageManifest
func (pc PackageChannel) IsDefaultChannel(pm PackageManifest) bool {
	return pc.Name == pm.DefaultChannelName || len(pm.Channels) == 1
}
