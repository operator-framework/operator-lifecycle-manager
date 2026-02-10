/*
Copyright Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
