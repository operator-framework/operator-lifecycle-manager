package servicebroker

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
	//log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
)

const (
	versionKey = "Version"
	kindKey    = "Kind"
	crdNameKey = "Name"

	namespaceKey        = "namespace"
	csvNameLabel        = "clusterserviceversion-name"
	serviceClassIDLabel = "alm-service-broker-clusterserviceclass-id"
	servicePlanIDLabel  = "alm-service-broker-clusterserviceplan-id"
)

var (
	NamespaceRequiredErrorMessage     = "NamespaceRequired"
	NamespaceRequiredErrorDescription = "Namespace must be specified via the `SpaceGUID` parameter"

	ValidServiceNameDescription = "MUST only contain alphanumeric characters, periods, and hyphens (no spaces)."

	supportedOSBVersions = map[string]bool{
		osb.Version2_11().HeaderValue(): true,
		osb.Version2_12().HeaderValue(): true,
		osb.Version2_13().HeaderValue(): true,
	}
	validServiceName        = regexp.MustCompile(`[a-zA-Z0-9-]+`)
	invalidServiceNameChars = regexp.MustCompile(`[^a-zA-Z0-9-]+`)

	defaultPlanFree = true

	NamespaceRequiredError = osb.HTTPStatusCodeError{
		StatusCode:   http.StatusBadRequest,
		ErrorMessage: &NamespaceRequiredErrorMessage,
		Description:  &NamespaceRequiredErrorDescription,
	}
)

func serviceClassName(csv csvv1alpha1.ClusterServiceVersion) string {
	return invalidServiceNameChars.ReplaceAllString(strings.ToLower(csv.GetName()), "-")
}
func serviceClassID(csv csvv1alpha1.ClusterServiceVersion) string {
	return invalidServiceNameChars.ReplaceAllString(strings.ToLower(csv.GetName()), "-")
}

func planID(service string, plan csvv1alpha1.CRDDescription) string {
	return strings.ToLower(invalidServiceNameChars.ReplaceAllString(service+"-"+plan.Kind, "-"))
}

func planName(service string, plan csvv1alpha1.CRDDescription) string {
	return strings.ToLower(invalidServiceNameChars.ReplaceAllString(service+"-"+plan.Kind, "-"))
}

func csvToService(csv csvv1alpha1.ClusterServiceVersion) (osb.Service, error) {
	// validate CSV can be converted into a valid OpenServiceBroker ServiceInstance
	name := csv.GetName()
	if ok := validServiceName.MatchString(name); !ok {
		return osb.Service{}, fmt.Errorf("invalid service name '%s': %s", name, ValidServiceNameDescription)
	}
	plans := make([]osb.Plan, len(csv.Spec.CustomResourceDefinitions.Owned))
	for i, crdDef := range csv.Spec.CustomResourceDefinitions.Owned {
		plans[i] = crdToServicePlan(name, crdDef)
	}
	description := csv.Spec.Description
	if description == "" {
		description = fmt.Sprintf("OpenCloudService for %s", name) // TODO better default msg
	}

	service := osb.Service{
		Name:            serviceClassName(csv),
		ID:              serviceClassID(csv),
		Description:     description,
		Tags:            csv.Spec.Keywords,
		Requires:        []string{}, // not relevant to k8s
		Bindable:        false,      // overwritten by plan if CRD has specDescriptors defined
		Plans:           plans,
		DashboardClient: nil, // TODO
		Metadata: map[string]interface{}{
			csvNameLabel: csv.GetName(),
			"Spec":       csv.Spec,
			"Status":     csv.Status,
		},
	}
	return service, nil
}

func specDescriptorsToInputParameters(specs []csvv1alpha1.SpecDescriptor) *osb.InputParameters {
	parameters := map[string]csvv1alpha1.SpecDescriptor{}
	for _, s := range specs {
		parameters[s.Path] = s
	}
	return &osb.InputParameters{parameters}
}

type CustomResourceObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec map[string]interface{}
}

func planToCustomResourceObject(plan osb.Plan, name string, spec map[string]interface{}) (*unstructured.Unstructured, error) {
	kind, ok := plan.Metadata[kindKey]
	if !ok {
		return nil, errors.New("missing required field: `Metadata[\"Kind\"]`")
	}
	version, ok := plan.Metadata[versionKey]
	if !ok {
		return nil, errors.New("missing required field: `Metadata[\"Version\"]`")
	}
	crdName, ok := plan.Metadata[crdNameKey]
	if !ok {
		return nil, errors.New("missing required field: `Metadata[\"Name\"]`")
	}
	apiVersion := schema.ParseGroupResource(crdName.(string)).WithVersion(version.(string)).GroupVersion().String()
	obj := CustomResourceObject{
		Spec: spec,
	}
	//log.Debugf("planToCustomResourceObject: plan=%+v cr=%+v", plan, obj)
	unstructuredCR, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&obj)
	if err != nil {
		return nil, err
	}
	cr := &unstructured.Unstructured{Object: unstructuredCR}
	cr.SetAPIVersion(apiVersion)
	cr.SetKind(kind.(string))
	cr.SetName(name)
	cr.SetLabels(map[string]string{
		servicePlanIDLabel: plan.ID,
	})
	return cr, nil
}

//'[{"apiVersion":"vault.security.coreos.com/v1alpha1","kind":"VaultService","metadata":{"name":"example"},"spec":{  "nodes":2,"version":"0.9.1-0"}}]'
func crdToServicePlan(service string, crd csvv1alpha1.CRDDescription) osb.Plan {
	bindable := len(crd.StatusDescriptors) > 0
	plan := osb.Plan{
		ID:          planID(service, crd),
		Name:        planName(service, crd),
		Description: crd.Description,
		Free:        &defaultPlanFree,
		Bindable:    &bindable,
		Metadata: map[string]interface{}{
			crdNameKey: crd.Name,
			versionKey: crd.Version,
			kindKey:    crd.Kind,
		},
		ParameterSchemas: &osb.ParameterSchemas{
			ServiceInstances: &osb.ServiceInstanceSchema{
				Create: specDescriptorsToInputParameters(crd.SpecDescriptors),
				Update: specDescriptorsToInputParameters(crd.SpecDescriptors),
			},
			ServiceBindings: nil,
		},
	}
	//log.Debugf("crdToServicePlan: crd=%+v plan=%+v", crd, plan)
	return plan
}
