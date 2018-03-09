package broker

import (
	"errors"
	"fmt"
	"strings"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos-inc/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
	ipv1alpha1 "github.com/coreos-inc/alm/pkg/api/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/api/client"
	"github.com/coreos-inc/alm/pkg/api/client/clientset/versioned"
	"github.com/coreos-inc/alm/pkg/controller/registry"
)

type Options struct {
	Namespace string // restrict to resources within a namespace, default all namespaces
}

// ALMBroker contains the clients and logic for fetching the catalog and creating instances
type ALMBroker struct {
	opClient opClient.Interface
	client   versioned.Interface

	namespace string
}

// NewBrokerSource creates a new BrokerSource client
func NewALMBroker(kubeconfigPath string, options Options) (*ALMBroker, error) {
	// Create a new client for ALM types (CRs)
	versionedClient, err := client.NewClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	// Allocate the new instance of an ALMBroker
	br := &ALMBroker{
		opClient:  opClient.NewClient(kubeconfigPath),
		client:    versionedClient,
		namespace: options.Namespace,
	}
	return br, nil
}

func (b *ALMBroker) getCatalog() ([]v1alpha1.ClusterServiceVersion, error) {
	csList, err := b.client.CatalogsourceV1alpha1().CatalogSources(b.namespace).List(metav1.ListOptions{})
	if err != nil {
		return []v1alpha1.ClusterServiceVersion{}, err
	}
	log.Debugf("found %d catalog sources", len(csList.Items))
	if csList == nil || len(csList.Items) == 0 {
		return []v1alpha1.ClusterServiceVersion{}, nil
	}

	loader := registry.ConfigMapCatalogResourceLoader{registry.NewInMem(), b.namespace, b.opClient}
	for _, cs := range csList.Items {
		loader.Namespace = cs.GetNamespace()
		if err := loader.LoadCatalogResources(cs.Spec.ConfigMap); err != nil {
			return []v1alpha1.ClusterServiceVersion{}, err
		}
	}
	return loader.Catalog.ListServices()
}

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

// ensure *almBroker implements osb-broker-lib interface
var _ broker.Interface = &ALMBroker{}

func (a *ALMBroker) ValidateBrokerAPIVersion(version string) error {
	// TODO implement
	return nil
}

// GetCatalog returns the CSVs in the catalog
func (a *ALMBroker) GetCatalog(*broker.RequestContext) (*osb.CatalogResponse, error) {
	csvs, err := a.getCatalog()
	if err != nil {
		return nil, err
	}
	services := make([]osb.Service, len(csvs))
	for i, csv := range csvs {
		services[i] = csvToService(&csv)
	}
	return &osb.CatalogResponse{services}, nil
}

func (a *ALMBroker) Provision(request *osb.ProvisionRequest, c *broker.RequestContext) (*osb.ProvisionResponse, error) {
	// install CSV if doesn't exist
	ip := &ipv1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{},
		Spec: ipv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{request.ServiceID},
			Approval:                   ipv1alpha1.ApprovalAutomatic,
		},
	}
	ip.SetGenerateName(fmt.Sprintf("install-%s", request.ServiceID))
	ip.SetNamespace(request.SpaceGUID)
	res, err := a.client.InstallplanV1alpha1().InstallPlans(request.SpaceGUID).Create(ip)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("unexpected installplan returned by k8s api on create: <nil>")
	}
	opkey := osb.OperationKey(res.GetName())
	response := osb.ProvisionResponse{
		Async:        true,
		OperationKey: &opkey,
	}
	return &response, nil

}

func (a *ALMBroker) Deprovision(request *osb.DeprovisionRequest, c *broker.RequestContext) (*osb.DeprovisionResponse, error) {
	// TODO implement
	return nil, errors.New("not implemented")
}

func (a *ALMBroker) LastOperation(request *osb.LastOperationRequest, c *broker.RequestContext) (*osb.LastOperationResponse, error) {
	ip, err := a.client.InstallplanV1alpha1().InstallPlans(a.namespace).Get(string(*request.OperationKey), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if ip == nil {
		return nil, nil
	}

	// TODO implement
	return nil, errors.New("not implemented")
}

func (a *ALMBroker) Bind(request *osb.BindRequest, c *broker.RequestContext) (*osb.BindResponse, error) {
	// TODO implement
	return nil, errors.New("not implemented")
}

func (a *ALMBroker) Unbind(request *osb.UnbindRequest, c *broker.RequestContext) (*osb.UnbindResponse, error) {
	// TODO implement
	return nil, errors.New("not implemented")
}

func (a *ALMBroker) Update(request *osb.UpdateInstanceRequest, c *broker.RequestContext) (*osb.UpdateInstanceResponse, error) {
	// TODO implement
	return nil, errors.New("not implemented")
}
