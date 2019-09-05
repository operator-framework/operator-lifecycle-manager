# Operator Package Logos

Status: In Progress

Version: Alpha

Implementation Owner: [@alecmerdler](github.com/alecmerdler)

# Motivation

Having logo icons for Operator packages is important. Currently, we include the base64-encoded representation of the image data in the `PackageManifest` API object itself. This data is not small. Even for a trivial number of Operator packages, the API response is quite large, which is a poor experience for clients. For example, a `PackageManifestList` with 65 Operators is ~1.9MB _with logos_; removing the logos reduces the size to ~450 KB.

# Proposed Changes

Add a new [API subresource](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#types-kinds) to `PackageManifest` which serves the associated logo image file.

## Implementation

Add subresource following `pods/log` example in [core Kubernetes apiserver code](https://sourcegraph.com/github.com/kubernetes/kubernetes@master/-/blob/pkg/registry/core/rest/storage_core.go#L230).


**Add subresource to both API groups:**

`pkg/package-server/apiserver/generic/storage.go`
```go
func BuildStorage(providers *ProviderConfig) []generic.APIGroupInfo {
	// Build storage for packages.operators.coreos.com
	operatorInfo := generic.NewDefaultAPIGroupInfo(v1.Group, Scheme, metav1.ParameterCodec, Codecs)
	operatorStorage := storage.NewStorage(v1.Resource("packagemanifests"), providers.Provider, Scheme)
	logoStorage := storage.NewLogoStorage(providers.Provider)
	operatorResources := map[string]rest.Storage{
		"packagemanifests":      operatorStorage,
		"packagemanifests/logo": logoStorage,
	}
	operatorInfo.VersionedResourcesStorageMap[v1.Version] = operatorResources

	// Build storage for packages.apps.redhat.com
	appInfo := generic.NewDefaultAPIGroupInfo(v1alpha1.Group, Scheme, metav1.ParameterCodec, Codecs)

	// Use storage for package.operators.coreos.com since types are identical
	appResources := map[string]rest.Storage{
		"packagemanifests":      operatorStorage,
		"packagemanifests/logo": logoStorage,
	}
	appInfo.VersionedResourcesStorageMap[v1alpha1.Version] = appResources

	return []generic.APIGroupInfo{
		operatorInfo,
		appInfo,
	}
}
```

**Implement logo storage struct**

`pkg/package-server/storage/subresources.go`
```go
type LogoStorage struct {
	prov provider.PackageManifestProvider
}

var _ rest.Connecter = &LogoStorage{}
var _ rest.StorageMetadata = &LogoStorage{}

// Implement the necessary methods below...
```

# Next Stage

Once the logos are being served from the subresource, console will be updated to fetch the images from those endpoints. Then we can remove the `icon` field completely from the `PackageManifest` object and benefit from the size reduction.

