package provider

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1"
)

type PackageManifestProvider interface {
	Get(name, namespace string) (*v1.PackageManifest, error)
	List(namespace string) (*v1.PackageManifestList, error)
}
