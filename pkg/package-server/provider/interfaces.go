package provider

import "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"

type PackageManifestProvider interface {
	Get(namespace, name string) (*operators.PackageManifest, error)
	List(namespace string) (*operators.PackageManifestList, error)
}
