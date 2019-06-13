package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain/v1alpha1"
)

// Install registers the API group and adds types to a scheme.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(porcelain.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(v1alpha1.SchemeGroupVersion))
}
