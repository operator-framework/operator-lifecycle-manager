package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv1alpha2 "github.com/operator-framework/api/pkg/operators/v1alpha2"
)

// Install registers the API group and adds all of its types to the given scheme.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(operatorsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(operatorsv1alpha2.AddToScheme(scheme))
	utilruntime.Must(operatorsv1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(operatorsv1.GroupVersion, operatorsv1alpha2.GroupVersion, operatorsv1alpha1.SchemeGroupVersion))
}
