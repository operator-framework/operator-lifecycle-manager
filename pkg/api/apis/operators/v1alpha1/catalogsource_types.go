package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	CatalogSourceCRDAPIVersion = GroupName + "/" + GroupVersion
	CatalogSourceKind          = "CatalogSource"
)

// SourceType indicates the type of backing store for a CatalogSource
type SourceType string

const (
	// SourceTypeInternal (deprecated) specifies a CatalogSource of type SourceTypeConfigmap
	SourceTypeInternal SourceType = "internal"

	// SourceTypeConfigmap specifies a CatalogSource that generates a configmap-server registry
	SourceTypeConfigmap SourceType = "configmap"

	// SourceTypeGrpc specifies a CatalogSource that can use an operator registry image to generate a
	// registry-server or connect to a pre-existing registry at an address.
	SourceTypeGrpc SourceType = "grpc"
)

const (
	CatalogSourceConfigMapError      ConditionReason = "ConfigMapError"
	CatalogSourceRegistryServerError ConditionReason = "RegistryServerError"
)

type CatalogSourceSpec struct {
	// SourceType is the type of source
	SourceType SourceType `json:"sourceType"`

	// ConfigMap is the name of the ConfigMap to be used to back a configmap-server registry.
	// Only used when SourceType = SourceTypeConfigmap or SourceTypeInternal.
	// +Optional
	ConfigMap string `json:"configMap,omitempty"`

	// Address is a host that OLM can use to connect to a pre-existing registry.
	// Format: <registry-host or ip>:<port>
	// Only used when SourceType = SourceTypeGrpc.
	// Ignored when the Image field is set.
	// +Optional
	Address string `json:"address,omitempty"`

	// Image is an operator-registry container image to instantiate a registry-server with.
	// Only used when SourceType = SourceTypeGrpc.
	// If present, the address field is ignored.
	// +Optional
	Image string `json:"image,omitempty"`

	// Secrets represent set of secrets that can be used to access the contents of the catalog.
	// It is best to keep this list small, since each will need to be tried for every catalog entry.
	// +Optional
	Secrets []string `json:"secrets,omitempty"`

	// Metadata
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Icon        Icon   `json:"icon,omitempty"`
}

type RegistryServiceStatus struct {
	Protocol         string      `json:"protocol,omitempty"`
	ServiceName      string      `json:"serviceName,omitempty"`
	ServiceNamespace string      `json:"serviceNamespace,omitempty"`
	Port             string      `json:"port,omitempty"`
	CreatedAt        metav1.Time `json:"createdAt,omitempty"`
}

func (s *RegistryServiceStatus) Address() string {
	return fmt.Sprintf("%s.%s.svc:%s", s.ServiceName, s.ServiceNamespace, s.Port)
}

type GRPCConnectionState struct {
	Address           string      `json:"address,omitempty"`
	LastObservedState string      `json:"lastObservedState"`
	LastConnectTime   metav1.Time `json:"lastConnect,omitempty"`
}

type CatalogSourceStatus struct {
	// A human readable message indicating details about why the ClusterServiceVersion is in this condition.
	// +optional
	Message string `json:"message,omitempty"`
	// Reason is the reason the Subscription was transitioned to its current state.
	// +optional
	Reason ConditionReason `json:"reason,omitempty"`

	ConfigMapResource     *ConfigMapResourceReference `json:"configMapReference,omitempty"`
	RegistryServiceStatus *RegistryServiceStatus      `json:"registryService,omitempty"`
	GRPCConnectionState   *GRPCConnectionState        `json:"connectionState,omitempty"`
}

type ConfigMapResourceReference struct {
	Name            string      `json:"name"`
	Namespace       string      `json:"namespace"`
	UID             types.UID   `json:"uid,omitempty"`
	ResourceVersion string      `json:"resourceVersion,omitempty"`
	LastUpdateTime  metav1.Time `json:"lastUpdateTime,omitempty"`
}

func (r *ConfigMapResourceReference) IsAMatch(object *metav1.ObjectMeta) bool {
	return r.UID == object.GetUID() && r.ResourceVersion == object.GetResourceVersion()
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient

// CatalogSource is a repository of CSVs, CRDs, and operator packages.
type CatalogSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   CatalogSourceSpec   `json:"spec"`
	Status CatalogSourceStatus `json:"status"`
}

func (c *CatalogSource) Address() string {
	if c.Spec.Address != "" {
		return c.Spec.Address
	}
	return c.Status.RegistryServiceStatus.Address()
}

func (c *CatalogSource) SetError(reason ConditionReason, err error) {
	c.Status.Reason = reason
	c.Status.Message = ""
	if err != nil {
		c.Status.Message = err.Error()
	}
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CatalogSourceList is a repository of CSVs, CRDs, and operator packages.
type CatalogSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []CatalogSource `json:"items"`
}
