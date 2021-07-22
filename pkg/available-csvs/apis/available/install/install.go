package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available/v1alpha1"
)

// Install registers API groups and adds types to a scheme.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(available.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(operatorsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(v1alpha1.SchemeGroupVersion))
}
