package bundle

const (
	CSVKind                = "ClusterServiceVersion"
	CRDKind                = "CustomResourceDefinition"
	SecretKind             = "Secret"
	ClusterRoleKind        = "ClusterRole"
	ClusterRoleBindingKind = "ClusterRoleBinding"
	ConfigMapKind          = "ConfigMap"
	ServiceAccountKind     = "ServiceAccount"
	ServiceKind            = "Service"
	RoleKind               = "Role"
	RoleBindingKind        = "RoleBinding"
	PrometheusRuleKind     = "PrometheusRule"
	ServiceMonitorKind     = "ServiceMonitor"
)

// Namespaced indicates whether the resource is namespace scoped (true) or cluster-scoped (false).
type Namespaced bool

// Key: Kind name
// Value: If namespaced kind, true. Otherwise, false
var supportedResources = map[string]Namespaced{
	CSVKind:                true,
	CRDKind:                false,
	ClusterRoleKind:        false,
	ClusterRoleBindingKind: false,
	ServiceKind:            true,
	ServiceAccountKind:     true,
	RoleKind:               true,
	RoleBindingKind:        true,
	PrometheusRuleKind:     true,
	ServiceMonitorKind:     true,
	SecretKind:             true,
	ConfigMapKind:          true,
}

// IsSupported checks if the object kind is OLM-supported and if it is namespaced
func IsSupported(kind string) (bool, Namespaced) {
	namespaced, ok := supportedResources[kind]
	return ok, namespaced
}
