package operators

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

// GroupName is the API group name.
const GroupName = "operators.coreos.com"

var (
	scheme             = runtime.NewScheme()
	localSchemeBuilder = runtime.SchemeBuilder{
		v1alpha1.AddToScheme,
		v1.AddToScheme,
	}

	// AddToScheme adds all types in the operators.coreos.com group to the given scheme.
	AddToScheme = localSchemeBuilder.AddToScheme
)

func init() {
	utilruntime.Must(AddToScheme(scheme))
}
