package rest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang/glog"
	"github.com/gorilla/mux"

	osb "github.com/pmorie/go-open-service-broker-client/v2"

	"github.com/pmorie/osb-broker-lib/pkg/broker"
	"github.com/pmorie/osb-broker-lib/pkg/metrics"
)

// APISurface is a type that describes a OSB REST API surface. APISurface is
// responsible for decoding HTTP requests and transforming them into the request
// object for each operation and transforming responses and errors returned from
// the broker's internal business logic into the correct places in the HTTP
// response.
type APISurface struct {
	// Broker contains the business logic that provides the
	// implementation for the different OSB API operations.
	Broker     broker.Interface
	Metrics    *metrics.OSBMetricsCollector
	EnableCORS bool
}

// NewAPISurface returns a new, ready-to-go APISurface.
func NewAPISurface(brokerInterface broker.Interface, m *metrics.OSBMetricsCollector) (*APISurface, error) {
	api := &APISurface{
		Broker:  brokerInterface,
		Metrics: m,
	}

	return api, nil
}

// OptionsHandler deals with the OPTIONS type request allowing the client to gather the headers.
func (s *APISurface) OptionsHandler(w http.ResponseWriter, r *http.Request) {
	s.writeResponse(w, http.StatusOK, nil)
}

// GetCatalogHandler is the mux handler that dispatches requests to get the
// broker's catalog to the broker's Interface.
func (s *APISurface) GetCatalogHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("get_catalog").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.GetCatalog(c)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	s.writeResponse(w, http.StatusOK, response)
}

// ProvisionHandler is the mux handler that dispatches ProvisionRequests to the
// broker's Interface.
func (s *APISurface) ProvisionHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("provision").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	request, err := unpackProvisionRequest(r)
	if err != nil {
		s.writeError(w, err, http.StatusBadRequest)
		return
	}

	glog.V(4).Infof("Received ProvisionRequest for instanceID %q", request.InstanceID)

	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.Provision(request, c)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	// MUST be returned if the Service Instance was provisioned
	// as a result of this request and Not async
	status := http.StatusCreated

	// MUST be returned if the Service Instance provisioning is in progress.
	if response.Async {
		status = http.StatusAccepted
	}

	if response.Exists {
		// MUST be returned if the Service Instance already exists,
		// is fully provisioned, and the requested parameters
		// are identical to the existing Service Instance
		status = http.StatusOK
	}

	s.writeResponse(w, status, response)
}

// unpackProvisionRequest unpacks an osb request from the given HTTP request.
func unpackProvisionRequest(r *http.Request) (*osb.ProvisionRequest, error) {
	// unpacking an osb request from an http request involves:
	// - unmarshaling the request body
	// - getting IDs out of mux vars
	// - getting query parameters from request URL
	// - retrieve originating origin identity
	osbRequest := &osb.ProvisionRequest{}
	if err := unmarshalRequestBody(r, osbRequest); err != nil {
		return nil, err
	}

	vars := mux.Vars(r)
	osbRequest.InstanceID = vars[osb.VarKeyInstanceID]

	asyncQueryParamVal := r.URL.Query().Get(osb.AcceptsIncomplete)
	if strings.ToLower(asyncQueryParamVal) == "true" {
		osbRequest.AcceptsIncomplete = true
	}
	identity, err := retrieveOriginatingIdentity(r)
	// This could be not found because platforms may support the feature
	// but are not guaranteed to.
	if err != nil {
		glog.Infof("Unable to retrieve originating identity - %v", err)
	}

	osbRequest.OriginatingIdentity = identity

	return osbRequest, nil
}

// DeprovisionHandler is the mux handler that dispatches deprovision requests to
// the broker's Interface.
func (s *APISurface) DeprovisionHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("deprovision").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	request, err := unpackDeprovisionRequest(r)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	glog.V(4).Infof("Received DeprovisionRequest for instanceID %q", request.InstanceID)

	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.Deprovision(request, c)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	status := http.StatusOK
	if response.Async {
		status = http.StatusAccepted
	}

	s.writeResponse(w, status, response)
}

// unpackDeprovisionRequest unpacks an osb request from the given HTTP request.
func unpackDeprovisionRequest(r *http.Request) (*osb.DeprovisionRequest, error) {
	osbRequest := &osb.DeprovisionRequest{}

	vars := mux.Vars(r)
	osbRequest.InstanceID = vars[osb.VarKeyInstanceID]
	osbRequest.ServiceID = r.FormValue(osb.VarKeyServiceID)
	osbRequest.PlanID = r.FormValue(osb.VarKeyPlanID)

	asyncQueryParamVal := r.FormValue(osb.AcceptsIncomplete)
	if strings.ToLower(asyncQueryParamVal) == "true" {
		osbRequest.AcceptsIncomplete = true
	}
	identity, err := retrieveOriginatingIdentity(r)
	// This could be not found because platforms may support the feature
	// but are not guaranteed to.
	if err != nil {
		glog.Infof("Unable to retrieve originating identity - %v", err)
	}
	osbRequest.OriginatingIdentity = identity

	return osbRequest, nil
}

// LastOperationHandler is the mux handler that dispatches last-operation
// requests to the broker's Interface.
func (s *APISurface) LastOperationHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("last_operation").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	request, err := unpackLastOperationRequest(r)
	if err != nil {
		// TODO: This should return a 400 in this case as it is either
		// malformed or missing mandatory data, as per the OSB spec.
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	glog.V(4).Infof("Received LastOperationRequest for instanceID %q", request.InstanceID)

	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.LastOperation(request, c)
	if err != nil {
		// TODO: This should return a 400 in this case as it is either
		// malformed or missing mandatory data, as per the OSB spec.
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	s.writeResponse(w, http.StatusOK, response)
}

// unpackLastOperationRequest unpacks an osb request from the given HTTP request.
func unpackLastOperationRequest(r *http.Request) (*osb.LastOperationRequest, error) {
	osbRequest := &osb.LastOperationRequest{}

	vars := mux.Vars(r)
	osbRequest.InstanceID = vars[osb.VarKeyInstanceID]
	serviceID := vars[osb.VarKeyServiceID]
	if serviceID != "" {
		osbRequest.ServiceID = &serviceID
	}
	planID := vars[osb.VarKeyPlanID]
	if planID != "" {
		osbRequest.PlanID = &planID
	}
	operation := vars[osb.VarKeyOperation]
	if operation != "" {
		typedOperation := osb.OperationKey(operation)
		osbRequest.OperationKey = &typedOperation
	}
	return osbRequest, nil
}

// BindHandler is the mux handler that dispatches bind requests to the broker's
// Interface.
func (s *APISurface) BindHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("bind").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	request, err := unpackBindRequest(r)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	glog.V(4).Infof("Received BindRequest for instanceID %q, bindingID %q", request.InstanceID, request.BindingID)

	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.Bind(request, c)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}
	status := http.StatusCreated
	if response.Exists {
		status = http.StatusOK
	}

	s.writeResponse(w, status, response)
}

// unpackBindRequest unpacks an osb request from the given HTTP request.
func unpackBindRequest(r *http.Request) (*osb.BindRequest, error) {
	osbRequest := &osb.BindRequest{}
	if err := unmarshalRequestBody(r, osbRequest); err != nil {
		return nil, err
	}

	vars := mux.Vars(r)
	osbRequest.InstanceID = vars[osb.VarKeyInstanceID]
	osbRequest.BindingID = vars[osb.VarKeyBindingID]
	identity, err := retrieveOriginatingIdentity(r)
	// This could be not found because platforms may support the feature
	// but are not guaranteed to.
	if err != nil {
		glog.Infof("Unable to retrieve originating identity - %v", err)
	}

	osbRequest.OriginatingIdentity = identity

	return osbRequest, nil
}

// UnbindHandler is the mux handler that dispatches unbind requests to the
// broker's Interface.
func (s *APISurface) UnbindHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("unbind").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	v := mux.Vars(r)
	request, err := unpackUnbindRequest(r, v)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	glog.V(4).Infof("Received UnbindRequest for instanceID %q, bindingID %q", request.InstanceID, request.BindingID)
	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.Unbind(request, c)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	s.writeResponse(w, http.StatusOK, response)
}

// unpackUnbindRequest unpacks an osb request from the given HTTP request.
func unpackUnbindRequest(r *http.Request, vars map[string]string) (*osb.UnbindRequest, error) {
	osbRequest := &osb.UnbindRequest{}

	osbRequest.InstanceID = vars[osb.VarKeyInstanceID]
	osbRequest.BindingID = vars[osb.VarKeyBindingID]

	// plan_id and service_id are set in the query string parameters and thus need to
	// be obtained differently than instance_id and binding_id.
	osbRequest.PlanID = r.FormValue(osb.VarKeyPlanID)
	osbRequest.ServiceID = r.FormValue(osb.VarKeyServiceID)

	identity, err := retrieveOriginatingIdentity(r)
	// This could be not found because platforms may support the feature
	// but are not guaranteed to.
	if err != nil {
		glog.Infof("Unable to retrieve originating identity - %v", err)
	}
	osbRequest.OriginatingIdentity = identity

	return osbRequest, nil
}

// UpdateHandler is the mux handler that dispatches Update requests to the
// broker's Interface.
func (s *APISurface) UpdateHandler(w http.ResponseWriter, r *http.Request) {
	s.Metrics.Actions.WithLabelValues("update").Inc()

	version := getBrokerAPIVersionFromRequest(r)
	if err := s.Broker.ValidateBrokerAPIVersion(version); err != nil {
		s.writeError(w, err, http.StatusPreconditionFailed)
		return
	}

	v := mux.Vars(r)
	request, err := unpackUpdateRequest(r, v)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	glog.V(4).Infof("Received Update Request for instanceID %q", request.InstanceID)

	c := &broker.RequestContext{
		Writer:  w,
		Request: r,
	}

	response, err := s.Broker.Update(request, c)
	if err != nil {
		s.writeError(w, err, http.StatusInternalServerError)
		return
	}

	status := http.StatusOK
	if response.Async {
		status = http.StatusAccepted
	}

	s.writeResponse(w, status, response)
}

func unpackUpdateRequest(r *http.Request, vars map[string]string) (*osb.UpdateInstanceRequest, error) {
	osbRequest := &osb.UpdateInstanceRequest{}
	if err := unmarshalRequestBody(r, osbRequest); err != nil {
		return nil, err
	}

	osbRequest.InstanceID = vars[osb.VarKeyInstanceID]

	asyncQueryParamVal := r.FormValue(osb.AcceptsIncomplete)
	if strings.ToLower(asyncQueryParamVal) == "true" {
		osbRequest.AcceptsIncomplete = true
	}
	identity, err := retrieveOriginatingIdentity(r)
	// This could be not found because platforms may support the feature
	// but are not guaranteed to.
	if err != nil {
		glog.Infof("Unable to retrieve originating identity - %v", err)
	}
	osbRequest.OriginatingIdentity = identity

	return osbRequest, nil
}

// retrieveOriginatingIdentity retrieves the originating identity from
// the request header.
func retrieveOriginatingIdentity(r *http.Request) (*osb.OriginatingIdentity, error) {
	identityHeader := r.Header.Get(osb.OriginatingIdentityHeader)
	if identityHeader != "" {
		identitySlice := strings.Split(identityHeader, " ")
		if len(identitySlice) != 2 {
			glog.Infof("invalid header for originating origin - %v", identityHeader)
			return nil, fmt.Errorf("invalid originating identity header")
		}
		// Base64 decode the value string so the value is passed as valid JSON.
		val, err := base64.StdEncoding.DecodeString(identitySlice[1])
		if err != nil {
			glog.Infof("invalid header for originating origin - %v", identityHeader)
			return nil, fmt.Errorf("invalid encoding for value of originating identity header")
		}
		return &osb.OriginatingIdentity{
			Platform: identitySlice[0],
			Value:    string(val),
		}, nil
	}
	return nil, fmt.Errorf("unable to find originating identity")
}

// writeResponse will serialize 'object' to the HTTP ResponseWriter
// using the 'code' as the HTTP status code
func (s *APISurface) writeResponse(w http.ResponseWriter, code int, object interface{}) {
	data, err := json.Marshal(object)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if s.EnableCORS {
		//Allow CORS here By * or specific origin
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, PATCH, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, X-Broker-API-Version, X-Broker-API-Originating-Identity, Content-Type, Authorization, Accept")
	}

	w.WriteHeader(code)
	w.Write(data)
}

// writeError accepts any error and writes it to the given ResponseWriter along
// with a status code.
//
// If the error is an osb.HTTPStatusCodeError, the error's StatusCode field will
// be used and the response body will contain the error's Description and
// ErrorMessage fields (if set).
//
// Otherwise, the given defaultStatusCode will be used, and the response body
// will have the result of calling the error's Error method set in the
// 'description' field.
//
// For more information about OSB errors, see:
//
// https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md#service-broker-errors
func (s *APISurface) writeError(w http.ResponseWriter, err error, defaultStatusCode int) {
	if httpErr, ok := osb.IsHTTPError(err); ok {
		s.writeOSBStatusCodeErrorResponse(w, httpErr)
		return
	}

	s.writeErrorResponse(w, defaultStatusCode, err)
}

// writeOSBStatusCodeErrorResponse writes the given HTTPStatusCodeError to the
// given ResponseWriter. The HTTP response's status code is the error's
// StatusCode field and the body contains the ErrorMessage and Description
// fields, if set.
func (s *APISurface) writeOSBStatusCodeErrorResponse(w http.ResponseWriter, err *osb.HTTPStatusCodeError) {
	type e struct {
		ErrorMessage *string `json:"error,omitempty"`
		Description  *string `json:"description,omitempty"`
	}

	body := &e{}
	if err.Description != nil {
		body.Description = err.Description
	}

	if err.ErrorMessage != nil {
		body.ErrorMessage = err.ErrorMessage
	}

	s.writeResponse(w, err.StatusCode, body)
}

// writeErrorResponse writes the given status code and error to the given
// ResponseWriter. The response body will be a json object with the field
// 'description' set from calling Error() on the passed-in error.
func (s *APISurface) writeErrorResponse(w http.ResponseWriter, code int, err error) {
	type e struct {
		Description string `json:"description"`
	}
	s.writeResponse(w, code, &e{
		Description: err.Error(),
	})
}
