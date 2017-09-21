package alm

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
)

var (
	OperatorVersionSchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	OperatorVersionAddToScheme   = OperatorVersionSchemeBuilder.AddToScheme
)

// SchemeGroupVersion is the group version used to register these objects.
var OperatorVersionSchemeGroupVersion = schema.GroupVersion{Group: ALMGroup, Version: v1alpha1.GroupVersion}

// Resource takes an unqualified resource and returns a Group-qualified GroupResource.
func Resource(resource string) schema.GroupResource {
	return OperatorVersionSchemeGroupVersion.WithResource(resource).GroupResource()
}

// addKnownTypes adds the set of types defined in this package to the supplied scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypeWithName(
		OperatorVersionSchemeGroupVersion.WithKind("OperatorVersion-v1"),
		&v1alpha1.OperatorVersion{},
	)
	scheme.AddKnownTypeWithName(
		OperatorVersionSchemeGroupVersion.WithKind("OperatorVersionList-v1"),
		&v1alpha1.OperatorVersionList{},
	)
	metav1.AddToGroupVersion(scheme, OperatorVersionSchemeGroupVersion)
	return nil
}
