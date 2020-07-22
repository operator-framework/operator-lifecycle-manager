package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blang/semver"
)

var (
	// ErrPackageNotInDatabase is an error that describes a package not found error when querying the registry
	ErrPackageNotInDatabase = errors.New("Package not in database")
)

// BundleImageAlreadyAddedErr is an error that describes a bundle is already added
type BundleImageAlreadyAddedErr struct {
	ErrorString string
}

func (e BundleImageAlreadyAddedErr) Error() string {
	return e.ErrorString
}

// PackageVersionAlreadyAddedErr is an error that describes that a bundle that is already in the databse that provides this package and version
type PackageVersionAlreadyAddedErr struct {
	ErrorString string
}

func (e PackageVersionAlreadyAddedErr) Error() string {
	return e.ErrorString
}

const (
	GVKType     = "olm.gvk"
	PackageType = "olm.package"
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

// ChannelEntryAnnotated is a denormalized node in a channel graph annotated with additional entry level info
type ChannelEntryAnnotated struct {
	PackageName        string
	ChannelName        string
	BundleName         string
	BundlePath         string
	Version            string
	Replaces           string
	ReplacesVersion    string
	ReplacesBundlePath string
}

// AnnotationsFile holds annotation information about a bundle
type AnnotationsFile struct {
	// annotations is a list of annotations for a given bundle
	Annotations Annotations `json:"annotations" yaml:"annotations"`
}

// Annotations is a list of annotations for a given bundle
type Annotations struct {
	// PackageName is the name of the overall package, ala `etcd`.
	PackageName string `json:"operators.operatorframework.io.bundle.package.v1" yaml:"operators.operatorframework.io.bundle.package.v1"`

	// Channels are a comma separated list of the declared channels for the bundle, ala `stable` or `alpha`.
	Channels string `json:"operators.operatorframework.io.bundle.channels.v1" yaml:"operators.operatorframework.io.bundle.channels.v1"`

	// DefaultChannelName is, if specified, the name of the default channel for the package. The
	// default channel will be installed if no other channel is explicitly given. If the package
	// has a single channel, then that channel is implicitly the default.
	DefaultChannelName string `json:"operators.operatorframework.io.bundle.channel.default.v1" yaml:"operators.operatorframework.io.bundle.channel.default.v1"`
}

// DependenciesFile holds dependency information about a bundle
type DependenciesFile struct {
	// Dependencies is a list of dependencies for a given bundle
	Dependencies []Dependency `json:"dependencies" yaml:"dependencies"`
}

// Dependency specifies a single constraint that can be satisfied by a property on another bundle..
type Dependency struct {
	// The type of dependency. This field is required.
	Type string `json:"type" yaml:"type"`

	// The serialized value of the dependency
	Value json.RawMessage `json:"value" yaml:"value"`
}

// Property defines a single piece of the public interface for a bundle. Dependencies are specified over properties.
// The Type of the property determines how to interpret the Value, but the value is treated opaquely for
// for non-first-party types.
type Property struct {
	// The type of property. This field is required.
	Type string `json:"type" yaml:"type"`

	// The serialized value of the propertuy
	Value string `json:"value" yaml:"value"`
}

type GVKDependency struct {
	// The group of GVK based dependency
	Group string `json:"group" yaml:"group"`

	// The kind of GVK based dependency
	Kind string `json:"kind" yaml:"kind"`

	// The version of GVK based dependency
	Version string `json:"version" yaml:"version"`
}

type PackageDependency struct {
	// The name of dependency such as 'etcd'
	PackageName string `json:"packageName" yaml:"packageName"`

	// The version range of dependency in semver range format
	Version string `json:"version" yaml:"version"`
}

type GVKProperty struct {
	// The group of GVK based property
	Group string `json:"group" yaml:"group"`

	// The kind of GVK based property
	Kind string `json:"kind" yaml:"kind"`

	// The version of the API
	Version string `json:"version" yaml:"version"`
}

type PackageProperty struct {
	// The name of package such as 'etcd'
	PackageName string `json:"packageName" yaml:"packageName"`

	// The version of package in semver format
	Version string `json:"version" yaml:"version"`
}

// Validate will validate GVK dependency type and return error(s)
func (gd *GVKDependency) Validate() []error {
	errs := []error{}
	if gd.Group == "" {
		errs = append(errs, fmt.Errorf("API Group is empty"))
	}
	if gd.Version == "" {
		errs = append(errs, fmt.Errorf("API Version is empty"))
	}
	if gd.Kind == "" {
		errs = append(errs, fmt.Errorf("API Kind is empty"))
	}
	return errs
}

// Validate will validate package dependency type and return error(s)
func (pd *PackageDependency) Validate() []error {
	errs := []error{}
	if pd.PackageName == "" {
		errs = append(errs, fmt.Errorf("Package name is empty"))
	}
	if pd.Version == "" {
		errs = append(errs, fmt.Errorf("Package version is empty"))
	} else {
		_, err := semver.ParseRange(pd.Version)
		if err != nil {
			errs = append(errs, fmt.Errorf("Invalid semver format version"))
		}
	}
	return errs
}

// GetDependencies returns the list of dependency
func (d *DependenciesFile) GetDependencies() []*Dependency {
	var dependencies []*Dependency
	for _, item := range d.Dependencies {
		dep := item
		dependencies = append(dependencies, &dep)
	}
	return dependencies
}

// GetType returns the type of dependency
func (e *Dependency) GetType() string {
	return e.Type
}

// GetTypeValue returns the dependency object that is converted
// from value string
func (e *Dependency) GetTypeValue() interface{} {
	switch e.GetType() {
	case GVKType:
		dep := GVKDependency{}
		err := json.Unmarshal([]byte(e.GetValue()), &dep)
		if err != nil {
			return nil
		}
		return dep
	case PackageType:
		dep := PackageDependency{}
		err := json.Unmarshal([]byte(e.GetValue()), &dep)
		if err != nil {
			return nil
		}
		return dep
	}
	return nil
}

// GetValue returns the value content of dependency
func (e *Dependency) GetValue() string {
	return string(e.Value)
}

// GetName returns the package name of the bundle
func (a *AnnotationsFile) GetName() string {
	return a.Annotations.PackageName
}

// GetChannels returns the channels that this bundle should be added to
func (a *AnnotationsFile) GetChannels() []string {
	if a.Annotations.Channels != "" {
		return strings.Split(a.Annotations.Channels, ",")
	}
	return []string{}
}

// GetDefaultChannelName returns the name of the default channel
func (a *AnnotationsFile) GetDefaultChannelName() string {
	if a.Annotations.DefaultChannelName != "" {
		return a.Annotations.DefaultChannelName
	}
	channels := a.GetChannels()
	if len(channels) == 1 {
		return channels[0]
	}
	return ""
}
