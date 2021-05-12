package deprecated

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIs(t *testing.T) {
	var table = []struct {
		GVK        schema.GroupVersionKind
		deprecated bool
	}{
		{
			GVK: schema.GroupVersionKind{
				Kind:    "pod",
				Group:   "",
				Version: "v1",
			},
			deprecated: false,
		},
		{
			GVK: schema.GroupVersionKind{
				Kind:    "CustomResourceDefinition",
				Group:   "apiextensions.k8s.io",
				Version: "v1beta1",
			},
			deprecated: true,
		},
		{
			GVK: schema.GroupVersionKind{
				Kind:    "ClusterRole",
				Group:   "rbac.authorization.k8s.io",
				Version: "v1beta1",
			},
			deprecated: true,
		},
		{
			GVK: schema.GroupVersionKind{
				Kind:    "ClusterRole",
				Group:   "rbac.authorization.k8s.io",
				Version: "v1",
			},
			deprecated: false,
		},
		{
			GVK: schema.GroupVersionKind{
				Kind:    "Descheduler",
				Group:   "scheduling.k8s.io",
				Version: "v1beta1",
			},
			deprecated: false,
		},
	}

	for _, tt := range table {
		dep := Is(tt.GVK)
		if dep != tt.deprecated {
			t.Fatalf("deprecation warning: expected %t, received %t for %#v", tt.deprecated, dep, tt.GVK)
		}
	}
}
