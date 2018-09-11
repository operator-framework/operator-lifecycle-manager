package provider

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

type PackageManifestProvider interface {
	GetPackageManifest(namespace, name string) (*v1alpha1.PackageManifest, error)
	ListPackageManifests(namespace string) (*v1alpha1.PackageManifestList, error)
	WatchPackageManifests(namespace string, out chan v1alpha1.PackageManifest, stop <-chan struct{})
}
