package servicebroker

import (
	"fmt"
	"net/http"
	"strings"

	osb "github.com/pmorie/go-open-service-broker-client/v2"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/api/apis/uicatalogentry/v1alpha1"
	"github.com/coreos-inc/alm/pkg/controller/registry"
)

const (
	NamespaceRequiredErrorMessage     = "NamespaceRequired"
	NamespaceRequiredErrorDescription = "Namespace must be specified via the `SpaceGUID` parameter"
)

var (
	True  = true
	False = false

	NamespaceRequiredError = osb.HTTPStatusCodeError{
		StatusCode:   http.StatusBadRequest,
		ErrorMessage: *NamespaceRequiredErrorMessage,
		Description:  *NamespaceRequiredErrorDescription,
	}
)

type ServicePlan struct {
	CSVName string
}

func csvToService(csv *csvv1alpha1.ClusterServiceVersion) osb.Service {
	serviceID := fmt.Sprintf("%s.clusterserviceversion", strings.ToLower(csv.GetName()))
	service := osb.Service{
		ID:                  serviceID,
		Name:                csv.Spec.DisplayName,
		Description:         csv.Spec.Description,
		Tags:                csv.Spec.Keywords,
		Requires:            []string{}, // TODO add permissions
		Bindable:            false,      // TODO replace when binding implemented
		BindingsRetrievable: false,      // TODO replace when binding implemented
		Plans: []osb.Plan{
			{
				ID:               serviceID,
				Name:             fmt.Sprintf("%sv%s-default", csv.Spec.DisplayName, csv.Spec.Version.String()),
				Description:      fmt.Sprintf("Default service plan for %s version %s", csv.Spec.DisplayName, csv.Spec.Version.String()),
				Free:             &free,
				Bindable:         &bindable,
				Metadata:         map[string]interface{}{},
				ParameterSchemas: nil,
			},
		}, // TODO complete
		Metadata: map[string]interface{}{
			"Spec":   csv.Spec,
			"Status": csv.Status,
		},
	}
	return service
}

func findOSBServiceByPackageName(reg registry.Source, pkg string) *osb.Service {
	plan := osb.Plan{
		ID:          "",
		Name:        "",
		Description: "",
		Free:        &True,
		Bindable:    &False,
		Metadata:    map[string]interface{}{},
		ParameterSchemas: &osb.ParameterSchemas{
			ServiceInstances: &osb.ServiceInstanceSchema{
				Create: *InputParameters{
					Parameters: map[string]interface{}{},
				},
				Update: *InputParameters{
					Parameters: map[string]interface{}{},
				},
			},
			ServiceBindings: &osb.ServiceBindingSchema{
				Create: *InputParameters{
					Parameters: map[string]interface{}{},
				},
			},
		},
	}
	return &plan
}
