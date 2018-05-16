package servicebroker

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
	stripmd "github.com/writeas/go-strip-markdown"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
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
func serviceClassDescription(csv csvv1alpha1.ClusterServiceVersion) string {
	// TODO better short description
	return fmt.Sprintf("%s %s (%s) by %s", csv.Spec.DisplayName, csv.Spec.Version.String(),
		csv.Spec.Maturity, csv.Spec.Provider.Name)
}
func serviceClassLongDescription(csv csvv1alpha1.ClusterServiceVersion) string {
	description := stripmd.Strip(csv.Spec.Description)
	if description == "" {
		description = fmt.Sprintf("Cloud Service for %s", csv.GetName())
	}
	return description
}
func planID(service string, plan csvv1alpha1.CRDDescription) string {
	return strings.ToLower(invalidServiceNameChars.ReplaceAllString(service+"-"+plan.Kind, "-"))
}
func planName(service string, plan csvv1alpha1.CRDDescription) string {
	return strings.ToLower(invalidServiceNameChars.ReplaceAllString(service+"-"+plan.Kind, "-"))
}

func csvToService(csv csvv1alpha1.ClusterServiceVersion, catalog registry.Source) (osb.Service, error) {
	// validate CSV can be converted into a valid OpenServiceBroker ServiceInstance
	name := csv.GetName()
	if ok := validServiceName.MatchString(name); !ok {
		return osb.Service{}, fmt.Errorf("invalid service name '%s': %s", name, ValidServiceNameDescription)
	}
	plans := make([]osb.Plan, len(csv.Spec.CustomResourceDefinitions.Owned))
	for i, crdDef := range csv.Spec.CustomResourceDefinitions.Owned {
		key := registry.CRDKey{
			Kind:    crdDef.Kind,
			Name:    crdDef.Name,
			Version: crdDef.Version,
		}
		crd, err := catalog.FindCRDByKey(key)
		if err != nil {
			return osb.Service{}, fmt.Errorf("missing CRD '%s' for service %s", key.String(), name)
		}
		plans[i] = crdToServicePlan(name, crdDef, crd)
	}

	service := osb.Service{
		Name:            serviceClassName(csv),
		ID:              serviceClassID(csv),
		Description:     serviceClassDescription(csv),
		Tags:            csv.Spec.Keywords,
		Requires:        []string{}, // not relevant to k8s
		Bindable:        false,      // overwritten by plan if CRD has specDescriptors defined
		Plans:           plans,
		DashboardClient: nil, // TODO
		Metadata: map[string]interface{}{
			"displayName":         csv.Spec.DisplayName + " " + csv.Spec.Version.String(),
			"longDescription":     serviceClassLongDescription(csv),
			"providerDisplayName": csv.Spec.Provider.Name,
			csvNameLabel:          csv.GetName(),
			"Spec":                csv.Spec,
			"Status":              csv.Status,
		},
	}
	if len(csv.Spec.Icon) > 0 {
		service.Metadata["imageUrl"] = fmt.Sprintf("data:%s;base64,%s", csv.Spec.Icon[0].MediaType, csv.Spec.Icon[0].Data)
	}
	if len(csv.Spec.Links) > 0 {
		service.Metadata["supportURL"] = csv.Spec.Links[0].URL
	}
	return service, nil
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

type openshiftFormDefinition struct {
	serviceInstance struct {
		create struct {
			params []string `json:"openshift_form_definition,omitempty"`
		} `json:"create,omitempty"`
	} `json:"service_instance,omitempty"`
}

//'[{"apiVersion":"vault.security.coreos.com/v1alpha1","kind":"VaultService","metadata":{"name":"example"},"spec":{  "nodes":2,"version":"0.9.1-0"}}]'
func crdToServicePlan(service string, crdDesc csvv1alpha1.CRDDescription, crd *v1beta1.CustomResourceDefinition) osb.Plan {
	bindable := false // when binding implemented, change to `len(crd.StatusDescriptors) > 0`
	plan := osb.Plan{
		ID:          planID(service, crdDesc),
		Name:        planName(service, crdDesc),
		Description: crdDesc.Description,
		Free:        &defaultPlanFree,
		Bindable:    &bindable,
		Metadata: map[string]interface{}{
			"displayName": crdDesc.DisplayName,
			// !!! REQUIRED by olm-service-broker for proper provisioning !!!
			crdNameKey: crdDesc.Name,
			versionKey: crdDesc.Version,
			kindKey:    crdDesc.Kind,
		},
		Schemas: &osb.Schemas{
			ServiceInstance: &osb.ServiceInstanceSchema{},
		},
	}
	if crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil {
		plan.Schemas.ServiceInstance.Create = &osb.InputParametersSchema{
			Parameters: crd.Spec.Validation.OpenAPIV3Schema,
		}
		osSchema := openshiftFormDefinition{}
		osSchema.serviceInstance.create.params = crd.Spec.Validation.OpenAPIV3Schema.Required
		plan.Metadata["schemas"] = osSchema

	}
	return plan
}

//func getServiceClassForPackage(catalog registry.Source, pkg registry.PackageManifest) (osb.Service, error) {
