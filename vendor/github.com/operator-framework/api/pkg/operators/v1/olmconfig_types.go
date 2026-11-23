package v1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	DisabledCopiedCSVsConditionType = "DisabledCopiedCSVs"
)

// OLMConfigSpec is the spec for an OLMConfig resource.
type OLMConfigSpec struct {
	Features *Features `json:"features,omitempty"`
}

// Features contains the list of configurable OLM features.
type Features struct {

	// DisableCopiedCSVs is used to disable OLM's "Copied CSV" feature
	// for operators installed at the cluster scope, where a cluster
	// scoped operator is one that has been installed in an
	// OperatorGroup that targets all namespaces.
	// When reenabled, OLM will recreate the "Copied CSVs" for each
	// cluster scoped operator.
	DisableCopiedCSVs *bool `json:"disableCopiedCSVs,omitempty"`
	// RoleAggregation configures OLM's automatic aggregation of access
	// to provided APIs into the default user-facing admin, edit and
	// view ClusterRoles. When unset, aggregation is enabled for every
	// provided API (the legacy behavior).
	// +optional
	RoleAggregation OLMConfigRoleAggregation `json:"roleAggregation,omitzero"`
	// PackageServerSyncInterval is used to define the sync interval for
	// packagerserver pods. Packageserver pods periodically check the
	// status of CatalogSources; this specifies the period using duration
	// format (e.g. "60m"). For this parameter, only hours ("h"), minutes
	// ("m"), and seconds ("s") may be specified. When not specified, the
	// period defaults to the value specified within the packageserver.
	// +optional
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(s|m|h))+$"
	PackageServerSyncInterval *metav1.Duration `json:"packageServerSyncInterval,omitempty"`
}

// OLMConfigRoleAggregation configures OLM's automatic aggregation of access
// to provided APIs into the default user-facing admin, edit and view
// ClusterRoles.
// +kubebuilder:validation:XValidation:rule="!has(self.enableAPIs) || self.enableAPIs.size() == 0 || (has(self.disable) && self.disable)",message="enableAPIs may only be set when disable is true"
type OLMConfigRoleAggregation struct {
	// Disable is used to disable OLM's automatic role aggregation
	// of access to provided APIs into the default
	// user-facing admin, edit and view ClusterRoles, except for the
	// APIs listed in enableAPIs. When true, the generated
	// ClusterRoles for provided APIs are maintained without
	// the aggregation labels, so the aggregated roles drop their rules;
	// when unset or false, the labels are restored.
	// +optional
	Disable bool `json:"disable,omitzero"`

	// EnableAPIs lists the provided APIs that remain aggregated into
	// the default user-facing admin, edit and view ClusterRoles while
	// role aggregation is otherwise disabled by `disable: true`. Aggregation is
	// performed only for provided APIs matching one of the entries
	// (group and kind must both match). Entries identify an API
	// independently of its served versions: the aggregated permission
	// applies to the resource as a whole, so a version bump of an
	// exempted API does not require updating the list. Provided APIs
	// that do not match are still granted their generated
	// ClusterRoles, but without the aggregation labels. May only be
	// set when disable is true.
	// +optional
	// +kubebuilder:validation:MaxItems=1024
	// +listType=map
	// +listMapKey=group
	// +listMapKey=kind
	EnableAPIs []GroupKind `json:"enableAPIs,omitempty,omitzero"`
}

// GroupKind identifies a kind of API within a group, independently of any
// served versions of that API.
// +structType=atomic
type GroupKind struct {
	// group is the name of the Kubernetes API group, (e.g. "apps", "networking.k8s.io").
	// Must be a DNS subdomain, as required of CustomResourceDefinition spec.group,
	// and must not be empty: provided APIs are never part of the core ("") group.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Group string `json:"group"`
	// kind is the Kubernetes resourcekind of the target object, (e.g. "Pod", "Deployment", "Service").
	// Must be a DNS (1035) label, which may have mixed case, as required of
	// CustomResourceDefinition spec.names.kind.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Za-z]([-A-Za-z0-9]*[A-Za-z0-9])?$`
	Kind string `json:"kind"`
}

// OLMConfigStatus is the status for an OLMConfig resource.
type OLMConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=olm,scope=Cluster
// +kubebuilder:subresource:status

// OLMConfig is a resource responsible for configuring OLM.
type OLMConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   OLMConfigSpec   `json:"spec,omitempty"`
	Status OLMConfigStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// OLMConfigList is a list of OLMConfig resources.
type OLMConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	// +listType=set
	Items []OLMConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OLMConfig{}, &OLMConfigList{})
}

// CopiedCSVsAreEnabled returns true if and only if the olmConfigs DisableCopiedCSVs is set and true,
// otherwise false is returned
func (config *OLMConfig) CopiedCSVsAreEnabled() bool {
	if config == nil || config.Spec.Features == nil || config.Spec.Features.DisableCopiedCSVs == nil {
		return true
	}

	return !*config.Spec.Features.DisableCopiedCSVs
}

func (config *OLMConfig) PackageServerSyncInterval() *time.Duration {
	if config == nil || config.Spec.Features == nil || config.Spec.Features.PackageServerSyncInterval == nil {
		return nil
	}
	return &config.Spec.Features.PackageServerSyncInterval.Duration
}
