package provider

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/packagemanifest/v1alpha1"
)

type PackageChan <-chan v1alpha1.PackageManifest

type PackageManifestProvider interface {
	Get(namespace, name string) (*v1alpha1.PackageManifest, error)
	List(namespace string) (*v1alpha1.PackageManifestList, error)
	Subscribe(stopCh <-chan struct{}) (add, modify, delete PackageChan, err error)
}
