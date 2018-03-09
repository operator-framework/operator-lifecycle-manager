package servicebroker

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

// ensure *almBroker implements osb-broker-lib interface
var _ broker.Interface = &ALMBroker{}

func (a *ALMBroker) ValidateBrokerAPIVersion(version string) error {
	// TODO implement
	return nil
}

// GetCatalog returns the CSVs in the catalog
func (a *ALMBroker) GetCatalog(*broker.RequestContext) (*osb.CatalogResponse, error) {
	// find all CatalogSources
	csList, err := a.client.CatalogsourceV1alpha1().CatalogSources(a.namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	if csList == nil {
		return nil, errors.New("unexpected response fetching catalogsources - <nil>")
	}

	// load service definitions from configmaps into temp in memory service registry
	loader := registry.ConfigMapCatalogResourceLoader{registry.NewInMem(), a.namespace, a.opClient}
	for _, cs := range csList.Items {
		loader.Namespace = cs.GetNamespace()
		if err := loader.LoadCatalogResources(cs.Spec.ConfigMap); err != nil {
			return nil, err
		}
	}
	csvs, err := loader.ListServices()
	if err != nil {
		return nil, err
	}

	// convert ClusterServiceVersions into OpenServiceBroker API `Service` object
	services := make([]osb.Service, len(csvs))
	for i, csv := range csvs {
		services[i] = csvToService(&csv)
	}
	return &osb.CatalogResponse{services}, nil
}

func (a *ALMBroker) Provision(request *osb.ProvisionRequest, c *broker.RequestContext) (*osb.ProvisionResponse, error) {
	// install CSV if doesn't exist
	ip := &ipv1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    a.namespace,
			GenerateName: fmt.Sprintf("servicebroker-install-%s", request.ServiceID),
		},
		Spec: ipv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{request.ServiceID},
			Approval:                   ipv1alpha1.ApprovalAutomatic,
		},
	}
	// use namespace from request if specified
	if request.SpaceGUID != "" {
		ip.SetNamespace(request.SpaceGUID)
	}
	if ip.GetNamespace() == "" {
		return nil, NamespaceRequiredError
	}
	res, err := a.client.InstallplanV1alpha1().InstallPlans(request.SpaceGUID).Create(ip)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("unexpected response installing service plan")
	}
	opkey := osb.OperationKey(res.GetUUID())
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
