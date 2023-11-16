package operators

import (
	"github.com/operator-framework/api/pkg/lib/version"
	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PackageManifestList is a list of PackageManifest objects.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PackageManifestList struct {
	metav1.TypeMeta
	metav1.ListMeta
	// +listType=set
	Items []PackageManifest
}

// PackageManifest holds information about a package, which is a reference to one (or more)
// channels under a single package.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type PackageManifest struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   PackageManifestSpec
	Status PackageManifestStatus
}

// PackageManifestSpec defines the desired state of PackageManifest
type PackageManifestSpec struct{}

// PackageManifestStatus represents the current status of the PackageManifest
type PackageManifestStatus struct {
	// CatalogSource is the name of the CatalogSource this package belongs to
	CatalogSource            string
	CatalogSourceDisplayName string
	CatalogSourcePublisher   string

	//  CatalogSourceNamespace is the namespace of the owning CatalogSource
	CatalogSourceNamespace string

	// Provider is the provider of the PackageManifest's default CSV
	Provider AppLink

	// PackageName is the name of the overall package, ala .
	PackageName string

	// Deprecation is an optional field which contains information if the package is deprecated.
	Deprecation *Deprecation

	// Channels are the declared channels for the package, ala .
	// +listType=set
	Channels []PackageChannel

	// DefaultChannel is, if specified, the name of the default channel for the package. The
	// default channel will be installed if no other channel is explicitly given. If the package
	// has a single channel, then that channel is implicitly the default.
	DefaultChannel string
}

// GetDefaultChannel gets the default channel or returns the only one if there's only one. returns empty string if it
// can't determine the default
func (m PackageManifest) GetDefaultChannel() string {
	if m.Status.DefaultChannel != "" {
		return m.Status.DefaultChannel
	}
	if len(m.Status.Channels) == 1 {
		return m.Status.Channels[0].Name
	}
	return ""
}

// PackageChannel defines a single channel under a package, pointing to a version of that
// package.
type PackageChannel struct {
	// Name is the name of the channel, e.g.
	Name string

	// CurrentCSV defines a reference to the CSV holding the version of this package currently
	// for the channel.
	CurrentCSV string

	// CurrentCSVSpec holds the spec of the current CSV
	CurrentCSVDesc CSVDescription

	// Deprecation is an optional field which contains information if the channel is deprecated.
	Deprecation *Deprecation

	// Entries lists all CSVs in the channel.
	Entries []ChannelEntry
}

// ChannelEntry defines a member of a package channel.
type ChannelEntry struct {
	// Name is the name of the bundle for this entry.
	Name string

	// Version is the version of the bundle for this entry.
	Version string

	// Deprecation is an optional field which contains information if the channel entry is deprecated.
	Deprecation *Deprecation
}

// Deprecation conveys information regarding a deprecated resource.
type Deprecation struct {
	// Message is a human readable message describing the deprecation.
	Message string
}

// CSVDescription defines a description of a CSV
type CSVDescription struct {
	// DisplayName is the CSV's display name
	DisplayName string

	// Icon is the CSV's base64 encoded icon
	// +listType=set
	Icon []Icon

	// Version is the CSV's semantic version
	Version version.OperatorVersion

	// Provider is the CSV's provider
	Provider    AppLink
	Annotations map[string]string
	Keywords    []string
	Links       []AppLink
	Maintainers []Maintainer
	Maturity    string

	// LongDescription is the CSV's description
	LongDescription string

	// InstallModes specify supported installation types
	InstallModes []operatorv1alpha1.InstallMode

	CustomResourceDefinitions operatorv1alpha1.CustomResourceDefinitions
	APIServiceDefinitions     operatorv1alpha1.APIServiceDefinitions
	NativeAPIs                []metav1.GroupVersionKind `json:"nativeApis,omitempty"`

	// Minimum Kubernetes version for operator installation
	MinKubeVersion string `json:"minKubeVersion,omitempty"`

	// List of related images
	RelatedImages []string `json:"relatedImages,omitempty"`
}

// AppLink defines a link to an application
type AppLink struct {
	Name string
	URL  string
}

// Maintainer defines a project maintainer
type Maintainer struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Icon defines a base64 encoded icon and media type
type Icon struct {
	Base64Data string
	Mediatype  string
}

// IsDefaultChannel returns true if the PackageChannel is the default for the PackageManifest
func (pc PackageChannel) IsDefaultChannel(pm PackageManifest) bool {
	return pc.Name == pm.Status.DefaultChannel || len(pm.Status.Channels) == 1
}
