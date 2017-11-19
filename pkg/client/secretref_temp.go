package client

// SecretReference represents a Secret Reference. It has enough information to retrieve secret
// in any namespace
// TODO: Remove this once we can update the corev1 library that defines this struct:
// https://github.com/kubernetes/api/blob/218912509d74a117d05a718bb926d0948e531c20/core/v1/types.go#L934
type SecretReference struct {
	// Name is unique within a namespace to reference a secret resource.
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	// Namespace defines the space within which the secret name must be unique.
	// +optional
	Namespace string `json:"namespace,omitempty" protobuf:"bytes,2,opt,name=namespace"`
}
