package manifests

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

// Dependencies is a list of dependencies for a given bundle
type Dependency struct {
	// The type of dependency. It can be `olm.package` for operator-version based
	// dependency or `olm.gvk` for gvk based dependency. This field is required.
	Type string `json:"type" yaml:"type"`

	// The value of the dependency (either GVKDependency or PackageDependency)
	Value string `json:"value" yaml:"value"`
}
