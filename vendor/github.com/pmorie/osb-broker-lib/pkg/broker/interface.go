package broker

import (
	"net/http"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
)

// Interface contains the business logic for the broker's operations.
// Interface is the interface broker authors should implement and is
// embedded in an APISurface.
type Interface interface {
	// ValidateBrokerAPIVersion encapsulates the business logic of validating
	// the OSB API version sent to the broker with every request and returns
	// an error.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#api-version-header
	ValidateBrokerAPIVersion(version string) error
	// GetCatalog encapsulates the business logic for returning the broker's
	// catalog of services. Brokers must tell platforms they're integrating with
	// which services they provide. GetCatalog is called when a platform makes
	// initial contact with the broker to find out about that broker's services.
	//
	// The parameters are:
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#catalog-management
	GetCatalog(c *RequestContext) (*CatalogResponse, error)
	// Provision encapsulates the business logic for a provision operation and
	// returns a osb.ProvisionResponse or an error. Provisioning creates a new
	// instance of a particular service.
	//
	// The parameters are:
	// - a osb.ProvisionRequest created from the original http request
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// Implementers should return a ProvisionResponse for a successful operation
	// or an error. The APISurface handles translating ProvisionResponses or
	// errors into the correct form in the http response.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#provisioning
	Provision(request *osb.ProvisionRequest, c *RequestContext) (*ProvisionResponse, error)
	// Deprovision encapsulates the business logic for a deprovision operation
	// and returns a osb.DeprovisionResponse or an error. Deprovisioning deletes
	// an instance of a service and releases the resources associated with it.
	//
	// The parameters are:
	// - a osb.DeprovisionRequest created from the original http request
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// Implementers should return a DeprovisionResponse for a successful
	// operation or an error. The APISurface handles translating
	// DeprovisionResponses or errors into the correct form in the http
	// response.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#deprovisioning
	Deprovision(request *osb.DeprovisionRequest, c *RequestContext) (*DeprovisionResponse, error)
	// LastOperation encapsulates the business logic for a last operation
	// request and returns a osb.LastOperationResponse or an error.
	// LastOperation is called when a platform checks the status of an ongoing
	// asynchronous operation on an instance of a service.
	//
	// The parameters are:
	// - a osb.LastOperationRequest created from the original http request
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// Implementers should return a LastOperationResponse for a successful
	// operation or an error. The APISurface handles translating
	// LastOperationResponses or errors into the correct form in the http
	// response.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#polling-last-operation
	LastOperation(request *osb.LastOperationRequest, c *RequestContext) (*LastOperationResponse, error)
	// Bind encapsulates the business logic for a bind operation and returns a
	// osb.BindResponse or an error. Binding creates a new set of credentials for
	// a consumer to use an instance of a service. Not all services are
	// bindable; in order for a service to be bindable, either the service or
	// the current plan associated with the instance must declare itself to be
	// bindable.
	//
	// The parameters are:
	// - a osb.BindRequest created from the original http request
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// Implementers should return a BindResponse for a successful operation or
	// an error. The APISurface handles translating BindResponses or errors into
	// the correct form in the http response.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#binding
	Bind(request *osb.BindRequest, c *RequestContext) (*BindResponse, error)
	// Unbind encapsulates the business logic for an unbind operation and
	// returns a osb.UnbindResponse or an error. Unbind deletes a binding and the
	// resources associated with it.
	//
	// The parameters are:
	// - a osb.UnbindRequest created from the original http request
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// Implementers should return a UnbindResponse for a successful operation or
	// an error. The APISurface handles translating UnbindResponses or errors
	// into the correct form in the http response.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#unbinding
	Unbind(request *osb.UnbindRequest, c *RequestContext) (*UnbindResponse, error)
	// Update encapsulates the business logic for an update operation and
	// returns a osb.UpdateInstanceResponse or an error. Update updates the
	// instance.
	//
	// The parameters are:
	// - a osb.UpdateInstanceRequest created from the original http request
	// - a RequestContext object which encapsulates:
	//    - a response writer, in case fine-grained control over the response is
	//      required
	//    - the original http request, in case access is required (to get special
	//      request headers, for example)
	//
	// Implementers should return a UpdateInstanceResponse for a successful operation or
	// an error. The APISurface handles translating UpdateInstanceResponses or errors
	// into the correct form in the http response.
	//
	// For more information, see:
	//
	// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#updating-a-service-instance
	Update(request *osb.UpdateInstanceRequest, c *RequestContext) (*UpdateInstanceResponse, error)
}

// RequestContext encapsulates the following parameters:
// - a response writer, in case fine-grained control over the response is required
// - the original http request, in case access is required (to get special
//   request headers, for example)
type RequestContext struct {
	Writer  http.ResponseWriter
	Request *http.Request
}
