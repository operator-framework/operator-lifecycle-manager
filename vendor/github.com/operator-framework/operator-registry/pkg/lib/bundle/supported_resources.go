package bundle

const (
	CSVKind                = "ClusterServiceVersion"
	CRDKind                = "CustomResourceDefinition"
	SecretKind             = "Secret"
	ClusterRoleKind        = "ClusterRole"
	ClusterRoleBindingKind = "ClusterRoleBinding"
	ServiceAccountKind     = "ServiceAccount"
	ServiceKind            = "Service"
	RoleKind               = "Role"
	RoleBindingKind        = "RoleBinding"
	PrometheusRuleKind     = "PrometheusRule"
	ServiceMonitorKind     = "ServiceMonitor"
)

var supportedResources map[string]bool

// Add a list of supported resources to the map
// Key: Kind name
// Value: If namaspaced kind, true. Otherwise, false
func init() {
	supportedResources = make(map[string]bool, 11)

	supportedResources[CSVKind] = true
	supportedResources[CRDKind] = false
	supportedResources[ClusterRoleKind] = false
	supportedResources[ClusterRoleBindingKind] = false
	supportedResources[ServiceKind] = true
	supportedResources[ServiceAccountKind] = true
	supportedResources[RoleKind] = true
	supportedResources[RoleBindingKind] = true
	supportedResources[PrometheusRuleKind] = true
	supportedResources[ServiceMonitorKind] = true
}

// IsSupported checks if the object kind is OLM-supported and if it is namespaced
func IsSupported(kind string) (bool, bool) {
	namespaced, ok := supportedResources[kind]
	return ok, namespaced
}
