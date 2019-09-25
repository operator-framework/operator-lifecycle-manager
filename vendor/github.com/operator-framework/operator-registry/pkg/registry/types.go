package registry

import (
	"fmt"
)

// APIKey stores GroupVersionKind for use as map keys
type APIKey struct {
	Group   string
	Version string
	Kind    string
	Plural  string
}

func (k APIKey) String() string {
	return fmt.Sprintf("%s/%s/%s (%s)", k.Group, k.Version, k.Kind, k.Plural)
}

// DefinitionKey represents the metadata for either an APIservice or a CRD from a CSV spec
type DefinitionKey struct {
	Group   string `json:"group"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// PackageManifest holds information about a package, which is a reference to one (or more)
// channels under a single package.
type PackageManifest struct {
	// PackageName is the name of the overall package, ala `etcd`.
	PackageName string `json:"packageName" yaml:"packageName"`

	// Channels are the declared channels for the package, ala `stable` or `alpha`.
	Channels []PackageChannel `json:"channels" yaml:"channels"`

	// DefaultChannelName is, if specified, the name of the default channel for the package. The
	// default channel will be installed if no other channel is explicitly given. If the package
	// has a single channel, then that channel is implicitly the default.
	DefaultChannelName string `json:"defaultChannel" yaml:"defaultChannel"`
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
	Name string `json:"name" yaml:"name"`

	// CurrentCSVName defines a reference to the CSV holding the version of this package currently
	// for the channel.
	CurrentCSVName string `json:"currentCSV" yaml:"currentCSV"`
}

// IsDefaultChannel returns true if the PackageChennel is the default for the PackageManifest
func (pc PackageChannel) IsDefaultChannel(pm PackageManifest) bool {
	return pc.Name == pm.DefaultChannelName || len(pm.Channels) == 1
}

// ChannelEntry is a denormalized node in a channel graph
type ChannelEntry struct {
	PackageName string
	ChannelName string
	BundleName  string
	Replaces    string
}
