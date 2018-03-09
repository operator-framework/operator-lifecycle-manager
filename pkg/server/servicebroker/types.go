package servicebroker

import (
	"fmt"
	"net/http"
	"strings"

	osb "github.com/pmorie/go-open-service-broker-client/v2"

	"github.com/coreos-inc/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
)

const (
	NamespaceRequiredErrorMessage     = "NamespaceRequired"
	NamespaceRequiredErrorDescription = "Namespace must be specified via the `SpaceGUID` parameter"
)

var (
	NamespaceRequiredError = osb.HTTPStatusCodeError{
		StatusCode:   http.StatusBadRequest,
		ErrorMessage: *NamespaceRequiredErrorMessage,
		Description:  *NamespaceRequiredErrorDescription,
	}
)

func csvToService(csv *v1alpha1.ClusterServiceVersion) osb.Service {
	free := true
	bindable := false
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
