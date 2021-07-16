package catalog

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"testing"
)

func TestDeprecatedAPIs(t *testing.T) {
	type apis struct {
		gv         schema.GroupVersion
		deprecated bool
	}

	var tt = []apis{
		{
			gv:         schema.GroupVersion{Group: "admissionregistration.k8s.io", Version: "v1beta1"},
			deprecated: true,
		},
		{
			gv:         schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1"},
			deprecated: false,
		},
		{
			gv:         schema.GroupVersion{Group: "apiextensions.k8s.io", Version: "v1beta1"},
			deprecated: true,
		},
		{
			gv:         schema.GroupVersion{Group: "verticalpodautoscalers.autoscaling.k8s.io", Version: "v1beta1"},
			deprecated: false,
		},
	}

	for _, api := range tt {
		dep := deprecated(api.gv)
		if dep != api.deprecated {
			t.Fatalf("expected deprecation of %s api to be %t, got %t", api.gv, api.deprecated, dep)
		}
	}
}
