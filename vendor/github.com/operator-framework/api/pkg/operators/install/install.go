package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/operator-framework/api/pkg/operators"
	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/operators/v1alpha2"
	"github.com/operator-framework/api/pkg/operators/v2alpha1"
)

// Install registers the API group and adds all of its types to the given scheme.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(operators.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(v1alpha2.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
	utilruntime.Must(v2alpha1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(v2alpha1.GroupVersion, v1.SchemeGroupVersion, v1alpha2.GroupVersion, v1alpha1.SchemeGroupVersion))
}
