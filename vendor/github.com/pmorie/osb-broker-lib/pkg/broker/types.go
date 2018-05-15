package broker

import osb "github.com/pmorie/go-open-service-broker-client/v2"

// CatalogResponse is sent as the response to a catalog requests.
type CatalogResponse struct {
	osb.CatalogResponse
}

// ProvisionResponse is sent as the response to a provision call.
type ProvisionResponse struct {
	osb.ProvisionResponse

	// Exists - is set if the request was already completed
	// and the requested parameters are identical to the existing
	// Service Instance.
	Exists bool `json:"-"`
}

// UpdateInstanceResponse is sent as the response to a update call.
type UpdateInstanceResponse struct {
	osb.UpdateInstanceResponse
}

// DeprovisionResponse is sent as the response to a deprovision call.
type DeprovisionResponse struct {
	osb.DeprovisionResponse
}

// LastOperationResponse is sent as the response to a last operation call.
type LastOperationResponse struct {
	osb.LastOperationResponse
}

// BindResponse is sent as the response to a bind call.
type BindResponse struct {
	osb.BindResponse

	// Exists - is set if the request was already completed
	// and the requested parameters are identical to the existing
	// Service Binding.
	Exists bool `json:"-"`
}

// UnbindResponse is sent as the response to a bind call.
type UnbindResponse struct {
	osb.UnbindResponse
}
