package v1

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

const (
	Group                   = "packages." + operators.GroupName
	Version                 = "v1"
	PackageManifestKind     = "PackageManifest"
	PackageManifestListKind = "PackageManifestList"
)

// SchemeGroupVersion is the group version used to register these objects.
var SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}

// Resource takes an unqualified resource and returns a Group-qualified GroupResource.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

// addKnownTypes adds the set of types defined in this package to the supplied scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypeWithName(
		SchemeGroupVersion.WithKind(PackageManifestKind),
		&PackageManifest{},
	)
	scheme.AddKnownTypeWithName(
		SchemeGroupVersion.WithKind(PackageManifestListKind),
		&PackageManifestList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)

	return nil
}
