package broker

import (
	"errors"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"time"

	"github.com/coreos-inc/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
	installplan "github.com/coreos-inc/alm/pkg/api/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/api/client"
	"github.com/coreos-inc/alm/pkg/api/client/clientset/versioned"
	"github.com/coreos-inc/alm/pkg/controller/registry"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
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

func (b *ALMBroker) getCatalog() (registry.Source, error) {
	csList, err := b.client.CatalogsourceV1alpha1().CatalogSources(b.namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	log.Debugf("found %d catalog sources", len(csList.Items))
	if csList == nil || len(csList.Items) == 0 {
		return nil, nil
	}

	loader := registry.ConfigMapCatalogResourceLoader{registry.NewInMem(), b.namespace, b.opClient}
	for _, cs := range csList.Items {
		loader.Namespace = cs.GetNamespace()
		if err := loader.LoadCatalogResources(cs.Spec.ConfigMap); err != nil {
			return nil, err
		}
	}
	return loader.Catalog, nil
}

func csvToService(csv *v1alpha1.ClusterServiceVersion) osb.Service {
	service := osb.Service{
		ID:                  csv.GetName(),
		Name:                csv.Spec.DisplayName,
		Description:         csv.Spec.Description,
		Tags:                csv.Spec.Keywords,
		Requires:            []string{},   // TODO add permissions
		Bindable:            false,        // TODO replace when binding implemented
		BindingsRetrievable: false,        // TODO replace when binding implemented
		Plans:               []osb.Plan{}, // TODO complete
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
	catalog, err := a.getCatalog()
	if err != nil {
		return nil, err
	}
	csvs, err := catalog.ListServices()
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
	namespace := request.OrganizationGUID
	cr := request.PlanID
	csv := request.ServiceID

	if _, err := a.opClient.KubernetesInterface().CoreV1().Namespaces().Get(namespace, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			_, createErr := a.opClient.KubernetesInterface().CoreV1().Namespaces().Create(&v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
			if createErr != nil {
				return nil, createErr
			}
		} else {
			return nil, err
		}
	}

	if _, err := a.client.ClusterserviceversionV1alpha1().ClusterServiceVersions(namespace).Get(csv, metav1.GetOptions{}); err != nil {
		// TODO: might want to create subscriptions with a target version here so that multiple versions can't be installed
		if apierrors.IsNotFound(err) {
			ip, createErr := a.client.InstallplanV1alpha1().InstallPlans(namespace).Create(&installplan.InstallPlan{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: csv,
					Namespace:    namespace,
				},
				Spec: installplan.InstallPlanSpec{
					ClusterServiceVersionNames: []string{csv},
				},
			})
			if createErr != nil {
				return nil, createErr
			}

			// wait for installplan to finish
			pollInterval := 1 * time.Second
			pollDuration := 5 * time.Minute
			err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
				pollIp, pollErr := a.client.InstallplanV1alpha1().InstallPlans(namespace).Get(ip.Name, metav1.GetOptions{})

				if pollErr != nil {
					return false, err
				}

				if pollIp.Status.Phase != installplan.InstallPlanPhaseComplete {
					return false, nil
				}

				return true, nil
			})
		} else {
			return nil, err
		}
	}

	catalog, err := a.getCatalog()
	if err != nil {
		return nil, err
	}
	csvDef, err := catalog.FindCSVByName(csv)
	if err != nil {
		return nil, err
	}

	type TypeObjectMeta struct {
		metav1.TypeMeta
		metav1.ObjectMeta
		Spec map[string]interface{}
	}

	var crdDesc v1alpha1.CRDDescription
	for _, desc := range csvDef.Spec.CustomResourceDefinitions.Owned {
		if desc.Kind == cr {
			crdDesc = desc
			break
		}
	}
	crInstance := TypeObjectMeta{
		TypeMeta: metav1.TypeMeta{
			Kind:       crdDesc.Kind,
			APIVersion: crdDesc.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: crdDesc.Kind,
			Namespace:    namespace,
		},
		Spec: request.Parameters,
	}
	unstructuredCR, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&crInstance)

	if err != nil {
		return nil, err
	}

	if err = a.opClient.CreateCustomResource(&unstructured.Unstructured{
		Object: unstructuredCR,
	}); err != nil {
		return nil, err
	}

	return &osb.ProvisionResponse{
		Async: false,
	}, nil
}

func (a *ALMBroker) Deprovision(request *osb.DeprovisionRequest, c *broker.RequestContext) (*osb.DeprovisionResponse, error) {
	// TODO implement
	return nil, errors.New("not implemented")
}

func (a *ALMBroker) LastOperation(request *osb.LastOperationRequest, c *broker.RequestContext) (*osb.LastOperationResponse, error) {
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
