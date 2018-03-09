package servicebroker

import (
	"errors"
	"fmt"
	"time"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	ipv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

// Options passed in from cmd
type Options struct {
	Namespace string // restrict to resources within a namespace, default all namespaces
}

// ALMBroker contains the clients and logic for fetching the catalog and creating instances
type ALMBroker struct {
	client versioned.Interface

	namespace    string
	dashboardURL *string // URL of a web-based management UI for services
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
		client:    versionedClient,
		namespace: options.Namespace,
	}
	return br, nil
}

// ensure *almBroker implements osb-broker-lib interface
var _ broker.Interface = &ALMBroker{}

// ValidateBrokerAPIVersion ensures version compatibility
func (a *ALMBroker) ValidateBrokerAPIVersion(version string) error {
	ok, supported := supportedOSBVersions[version]
	log.Debugf("Component=ServiceBroker Endpoint=ValidateBrokerAPIVersion Version=%s Supported=%s",
		version, ok)
	if !ok {
		return fmt.Errorf("unknown OpenServiceBroker API Version: %s", version)
	}
	if !supported {
		return fmt.Errorf("unsupported OpenServiceBroker API Version: %s", version)
	}
	return nil
}

// GetCatalog returns the CSVs in the catalog
func (a *ALMBroker) GetCatalog(b *broker.RequestContext) (*osb.CatalogResponse, error) {
	log.Debugf("Component=ServiceBroker Endpoint=GetCatalog Context=%#v", b)
	// find all CatalogSources
	csList, err := a.client.CatalogsourceV1alpha1().CatalogSources(a.namespace).List(metav1.ListOptions{})
	if err != nil {
		log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", err)
		return nil, err
	}
	if csList == nil {
		log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", "<nil> catalog source")
		return nil, errors.New("unexpected response fetching catalogsources - <nil>")
	}

	// load service definitions from configmaps into temp in memory service registry
	loader := registry.ConfigMapCatalogResourceLoader{registry.NewInMem(), a.namespace, a.opClient}
	for _, cs := range csList.Items {
		loader.Namespace = cs.GetNamespace()
		if err := loader.LoadCatalogResources(cs.Spec.ConfigMap); err != nil {
			log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", err)
			return nil, err
		}
	}
	csvs, err := loader.Catalog.ListServices()
	if err != nil {
		log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", err)
		return nil, err
	}

	// convert ClusterServiceVersions into OpenServiceBroker API `Service` object
	services := make([]osb.Service, len(csvs))
	for i, csv := range csvs {
		s, err := csvToService(csv)
		if err != nil {
			log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", err)
			return nil, err
		}
		services[i] = s
	}
	log.Debugf("Component=ServiceBroker Endpoint=GetCatalog Services=%#v", services)
	return &osb.CatalogResponse{services}, nil
}

func ensureNamespace(ns string, client opClient.Interface) error {
	_, err := client.KubernetesInterface().CoreV1().Namespaces().Get(ns, metav1.GetOptions{})
	if err == nil {
		return err
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	obj := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	ip, err := client.KubernetesInterface().CoreV1().Namespaces().Create(&obj)
	if err != nil {
		return err
	}
	if ip == nil {
		return errors.New("unexpected installplan returned by k8s api on create: <nil>")
	}

	return err
}
func ensureCSV(namespace string, csvName string, client versioned.Interface) error {
	// check that desired CSV has been installed
	csv, err := client.ClusterserviceversionV1alpha1().ClusterServiceVersions(namespace).Get(csvName, metav1.GetOptions{})
	if err == nil && csv != nil {
		return nil
	}
	// install CSV if doesn't exist
	obj := &ipv1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    namespace,
			GenerateName: fmt.Sprintf("servicebroker-install-%s", csvName),
		},
		Spec: ipv1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{csvName},
			Approval:                   ipv1alpha1.ApprovalAutomatic,
		},
	}
	ip, err := client.InstallplanV1alpha1().InstallPlans(namespace).Create(obj)
	if err != nil {
		return err
	}
	if ip == nil {
		return errors.New("unexpected response installing service plan")
	}
	// wait for installplan to finish
	pollInterval := 1 * time.Second
	pollDuration := 5 * time.Minute
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		pollIp, pollErr := client.InstallplanV1alpha1().InstallPlans(namespace).Get(ip.Name, metav1.GetOptions{})

		if pollErr != nil {
			return false, err
		}

		if pollIp.Status.Phase != ipv1alpha1.InstallPlanPhaseComplete {
			return false, nil
		}

		return true, nil
	})
	return err
}

func (a *ALMBroker) Provision(request *osb.ProvisionRequest, c *broker.RequestContext) (*osb.ProvisionResponse, error) {
	log.Debugf("Component=ServiceBroker Endpoint=Provision Request=%#v", request)
	namespace := a.namespace
	if n, ok := request.Context[namespaceKey]; ok {
		namespace = n.(string)
	}
	if namespace == "" {
		return nil, NamespaceRequiredError
	}
	if err := ensureNamespace(namespace, a.opClient); err != nil {
		return nil, err
	}
	csvName := request.ServiceID
	if err := ensureCSV(namespace, csvName, a.client); err != nil {
		return nil, err
	}

	catalog, err := a.GetCatalog(nil)
	if err != nil {
		return nil, err
	}
	var plan osb.Plan
	found := false
	for _, s := range catalog.Services {
		if s.ID == request.ServiceID {
			for _, p := range s.Plans {
				if p.ID == request.PlanID {
					plan = p
					found = true
					break
				}
			}
		}
	}
	if !found {
		return nil, errors.New("unknown plan")
	}
	cr, err := planToCustomResourceObject(plan, request.InstanceID, request.Parameters)
	if err != nil {
		return nil, err
	}

	// cr.Namespace = namespace
	if err := a.opClient.CreateCustomResource(&cr); err != nil {
		return nil, err
	}
	opkey := osb.OperationKey(cr.GetObjectKind().GroupVersionKind().String())
	response := osb.ProvisionResponse{
		Async:        true,
		OperationKey: &opkey,
		DashboardURL: a.dashboardURL, // TODO make specific to created resource
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
