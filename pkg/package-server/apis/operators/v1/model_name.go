package v1

// OpenAPIModelName returns the OpenAPI model name for this type.
// This is used by openapi-gen to register the GVK extension.
// The format matches what k8s.io/apimachinery/pkg/runtime.Scheme.ToOpenAPIDefinitionName would generate.
func (PackageManifest) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.PackageManifest"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
// This is used by openapi-gen to register the GVK extension.
// The format matches what k8s.io/apimachinery/pkg/runtime.Scheme.ToOpenAPIDefinitionName would generate.
func (PackageManifestList) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.PackageManifestList"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (PackageManifestSpec) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.PackageManifestSpec"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (PackageManifestStatus) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.PackageManifestStatus"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (PackageChannel) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.PackageChannel"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (ChannelEntry) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.ChannelEntry"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (CSVDescription) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.CSVDescription"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (AppLink) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.AppLink"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (Maintainer) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.Maintainer"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (Icon) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.Icon"
}

// OpenAPIModelName returns the OpenAPI model name for this type.
func (Deprecation) OpenAPIModelName() string {
	return "com.github.operator-framework.operator-lifecycle-manager.pkg.package-server.apis.operators.v1.Deprecation"
}
