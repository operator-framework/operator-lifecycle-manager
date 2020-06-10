package manifests

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

// IsEmpty returns true if the PackageManifest instance is equal to the zero value
func (p *PackageManifest) IsEmpty() bool {
	return p.PackageName == "" && len(p.Channels) == 0 && p.DefaultChannelName == ""
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
