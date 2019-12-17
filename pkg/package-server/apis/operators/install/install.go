package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"
	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators/v1"
)

// Install registers API groups and adds types to a scheme.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(operators.AddToScheme(scheme))
	utilruntime.Must(operatorsv1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(operatorsv1.SchemeGroupVersion))
}
