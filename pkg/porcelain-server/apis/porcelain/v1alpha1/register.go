package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the name of the API group used to register v1alpha1 types with a scheme.
const GroupName = "porcelain.operators.coreos.com"

var (
	// SchemeGroupVersion is the group version used to register v1alpha1 types with a scheme.
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}
	// SchemeBuilder lets clients add generated v1alpha1 types to a scheme without explictly referencing them.
	SchemeBuilder      runtime.SchemeBuilder
	localSchemeBuilder = &SchemeBuilder

	// AddToScheme adds generated v1alpha1 types to a given scheme.
	AddToScheme = localSchemeBuilder.AddToScheme
)

func init() {
	// We only register manually written functions here. The registration of the
	// generated functions takes place in the generated files. The separation
	// makes the code compile even when the generated files are missing.
	localSchemeBuilder.Register(addKnownTypes, addConversionFuncs, addDefaultingFuncs)
}

// Adds the list of known types to the given scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&InstalledOperator{},
		&InstalledOperatorList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}
