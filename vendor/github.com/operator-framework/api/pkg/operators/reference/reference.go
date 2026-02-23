package reference

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	ref "k8s.io/client-go/tools/reference"

	"github.com/operator-framework/api/pkg/operators/install"
)

var scheme = runtime.NewScheme()

func init() {
	// Register all OLM types with the scheme
	install.Install(scheme)
	utilruntime.Must(k8sscheme.AddToScheme(scheme))
}

// GetReference returns an ObjectReference for the given object.
// The objects dynamic type must be an OLM type.
func GetReference(obj runtime.Object) (*corev1.ObjectReference, error) {
	return ref.GetReference(scheme, obj)
}
