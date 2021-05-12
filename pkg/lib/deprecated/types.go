package deprecated

import "k8s.io/apimachinery/pkg/runtime/schema"

// deprecatedAPIs is a map of deprecated API Group/Versions to Kinds as of Kube 1.22 for the types that OLM supports.
// This map should not be modified at runtime but is instead a "constant" map for reference use only.
var deprecatedAPIs = map[string][]string{
	"apiextensions.k8s.io/v1beta1":      {"CustomResourceDefinition"},
	"rbac.authorization.k8s.io/v1beta1": {"ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding"},
	"scheduling.k8s.io/v1beta1":         {"PriorityClass"},
}

// Is uses the deprecatedAPIs to determine whether or not a particular GVK is deprecated.
func Is(gvk schema.GroupVersionKind) bool {
	if gvk.Empty() {
		return false
	}

	kinds, ok := deprecatedAPIs[gvk.GroupVersion().String()]
	if !ok {
		return false
	}
	for _, kind := range kinds {
		if gvk.Kind == kind {
			return true
		}
	}
	return false
}
