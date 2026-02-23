package provider

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"
	"k8s.io/apimachinery/pkg/labels"
)

type PackageManifestProvider interface {
	Get(namespace, name string) (*operators.PackageManifest, error)
	List(namespace string, selector labels.Selector) (*operators.PackageManifestList, error)
}
