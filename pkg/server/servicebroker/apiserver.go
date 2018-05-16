package servicebroker

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"

	ipv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

var (
	// default poll times for waiting on resources
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute

	asyncOnlyErrorMessage     = "AsyncOnlySupported"
	asyncOnlyErrorDescription = "Only asynchronous operations supported"

	AsyncOnlyError = osb.HTTPStatusCodeError{
		StatusCode:   http.StatusUnprocessableEntity,
		ErrorMessage: &asyncOnlyErrorMessage,
		Description:  &asyncOnlyErrorDescription,
	}
)

type Options struct {
	Namespace string // restrict to resources within a namespace, default all namespaces
}

// ALMBroker contains the clients and logic for fetching the catalog and creating instances
type ALMBroker struct {
	opClient opClient.Interface
	client   versioned.Interface
	catalog  catalogLoader

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
	almOpClient := opClient.NewClient(kubeconfigPath)
	// Allocate the new instance of an ALMBroker
	br := &ALMBroker{
		client:   versionedClient,
		opClient: almOpClient,
		catalog: &inClusterCatalog{
			client:   versionedClient,
			opClient: almOpClient,
		},
		namespace: options.Namespace,
	}
	return br, nil
}

// ensure *almBroker implements osb-broker-lib interface
var _ broker.Interface = &ALMBroker{}

type catalogLoader interface {
	Load(namespace string) (registry.Source, error)
}
type inClusterCatalog struct {
	client   versioned.Interface
	opClient opClient.Interface
}

func (c *inClusterCatalog) Load(namespace string) (registry.Source, error) {
	// find all CatalogSources
	csList, err := c.client.CatalogsourceV1alpha1().CatalogSources(namespace).List(metav1.ListOptions{})
	if err != nil {
		log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", err)
		return nil, err
	}
	if csList == nil {
		log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", "<nil> catalog source")
		return nil, errors.New("unexpected response fetching catalogsources - <nil>")
	}

	// load service definitions from configmaps into temp in memory service registry
	loader := registry.ConfigMapCatalogResourceLoader{registry.NewInMem(), namespace, c.opClient}
	for _, cs := range csList.Items {
		loader.Namespace = cs.GetNamespace()
		if err := loader.LoadCatalogResources(cs.Spec.ConfigMap); err != nil {
			log.Errorf("Component=ServiceBroker Endpoint=GetCatalog Error=%s", err)
			return nil, err
		}
	}
	return loader.Catalog, nil
}

func (a *ALMBroker) ValidateBrokerAPIVersion(version string) error {
	supported, ok := supportedOSBVersions[version]
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
func (a *ALMBroker) GetCatalog(b *broker.RequestContext) (*broker.CatalogResponse, error) {
	// Load Catalog
	catalog, err := a.catalog.Load(a.namespace)
	if err != nil {
		return nil, err
	}
	// List Packages in catalog and convert ClusterServiceVersions from default channel for each
	// package into OpenServiceBroker API `Service` object
	pkgs := catalog.AllPackages()
	services := []osb.Service{}
	for _, pkg := range pkgs {
		channel := pkg.GetDefaultChannel()
		if channel == "" {
			log.Warnf("Skipping Package %s - no default channel found", pkg.PackageName)
			continue
		}
		csv, err := catalog.FindCSVForPackageNameUnderChannel(pkg.PackageName, channel)
		if err != nil {
			log.Warnf("Skipping Package %s - error finding CSV in %s channel: %s", pkg.PackageName, channel, err)
			continue
		}
		if csv == nil {
			log.Warnf("Skipping Package %s - no CSV found in %s channel", pkg.PackageName, channel)
			continue
		}
		service, err := csvToService(*csv, catalog)
		if err != nil {
			log.Warnf("Skipping Package %s - error converting CSV %s to serviceclass: %s", pkg.PackageName, csv.GetName(), err)
			continue
		}
		log.Debugf("Loaded Service for package %s from channel - CSV name: %s", pkg.PackageName, channel, csv.GetName())
		services = append(services, service)
	}
	log.Debugf("Component=ServiceBroker Endpoint=GetCatalog Services=%#v", len(services))
	return &broker.CatalogResponse{osb.CatalogResponse{services}}, nil
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

func (a *ALMBroker) Provision(request *osb.ProvisionRequest, c *broker.RequestContext) (*broker.ProvisionResponse, error) {
	log.Debugf("Component=ServiceBroker Endpoint=Provision Request=%#v", request)
	namespace := a.namespace
	if n, ok := request.Context[namespaceKey]; ok {
		namespace = n.(string)
	}
	if namespace == "" {
		return nil, NamespaceRequiredError
	}
	if err := ensureNamespace(namespace, a.opClient); err != nil {
		return nil, fmt.Errorf("failed to ensure namespace '%s' for plan '%s': %v", namespace, request.PlanID, err)
	}
	resp, err := a.GetCatalog(nil)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch catalog to provision plan '%s': %v", request.PlanID, err)
	}

	var plan osb.Plan
	var csvName string
	found := false
	for _, s := range resp.CatalogResponse.Services {
		if s.ID == request.ServiceID {
			csvName = s.Metadata[csvNameLabel].(string)
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
		return nil, fmt.Errorf("unknown plan '%s'", request.PlanID)
	}
	if err := ensureCSV(namespace, csvName, a.client); err != nil {
		return nil, fmt.Errorf("failed to ensure CSV '%s' exists in namspace '%s' for plan '%s': %v", csvName, namespace, request.PlanID, err)
	}
	cr, err := planToCustomResourceObject(plan, request.InstanceID, request.Parameters)
	if err != nil {
		return nil, fmt.Errorf("could not convert plan '%s' to a custom resource: %v", request.PlanID, err)
	}
	cr.SetNamespace(namespace)
	exists := false
	if err := a.opClient.CreateCustomResource(cr); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create custom resource for provisioning plan '%s': %v", request.PlanID, err)
		}
		exists = true
	}
	gvk := cr.GroupVersionKind()
	obj, err := a.opClient.GetCustomResource(gvk.Group, gvk.Version, namespace, gvk.Kind, cr.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to find newly provisioned custom resource %s: %v", gvk.String(), err)
	}
	opkey := osb.OperationKey(obj.GetSelfLink())
	response := &broker.ProvisionResponse{
		ProvisionResponse: osb.ProvisionResponse{
			Async:        true,
			OperationKey: &opkey,
			DashboardURL: a.dashboardURL, // TODO make specific to created resource
		},
		Exists: exists,
	}
	return response, nil

}

// TEMP
// alm-service-broker-clusterserviceplan-id: couchbase-operator-v0-8-0-couchbasecluster
// \---

func (a *ALMBroker) Deprovision(request *osb.DeprovisionRequest, c *broker.RequestContext) (*broker.DeprovisionResponse, error) {
	var (
		object unstructured.Unstructured

		plan osb.Plan

		serviceID  string
		planID     string
		instanceID string
	)

	//
	// Validate request
	//
	if request == nil || c.Request == nil {
		return nil, errors.New("invalid request: <nil>")
	}
	values := c.Request.URL.Query()

	serviceID = values.Get("service_id")
	planID = values.Get("plan_id")
	instanceID = request.InstanceID

	if serviceID == "" {
		return nil, errors.New("invalid request: missing required `service_id` query parameter")
	}
	if planID == "" {
		return nil, errors.New("invalid request: missing required `plan_id` query parameter")
	}
	if instanceID == "" {
		return nil, errors.New("invalid request: missing required url paramter for instance id")
	}
	if values.Get("accepts_incomplete") == "" {
		// Only accept requests with `accepts_incomplete` since deprovisioning is async.
		// ALM deletes the CustomResource and the operator is responsible for removing the instance.
		// See OpenServiveBroker API spec:
		//   https://github.com/openservicebrokerapi/servicebroker/blob/v2.13/spec.md#parameters-4
		return nil, AsyncOnlyError
	}

	//
	// Fetch plan definition from catalog
	//
	catalog, err := a.GetCatalog(nil)
	if err != nil {
		return nil, err
	}
FindPlan:
	for _, s := range catalog.Services {
		if s.ID != serviceID {
			continue FindPlan
		}
		for _, p := range s.Plans {
			if p.ID == planID {
				plan = p
				goto Deprovisioning
			}
		}
		return nil, errors.New("unknown plan")
	}
	return nil, errors.New("unknown service")
Deprovisioning:
	cr, err := planToCustomResourceObject(plan, instanceID, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	gvk := cr.GroupVersionKind()
	uri := strings.ToLower(fmt.Sprintf("/apis/%s/%s/%ss", gvk.Group, gvk.Version, gvk.Kind))
	opkey := osb.OperationKey(uri)

	err = a.opClient.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().RESTClient().
		Get().RequestURI(uri).
		Do().Into(&object)

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		return &broker.DeprovisionResponse{
			DeprovisionResponse: osb.DeprovisionResponse{
				Async:        false,
				OperationKey: &opkey,
			},
		}, nil
	}
	namespace := ""
	if object.IsList() {
		field, ok := object.Object["items"]
		if !ok {
			return nil, errors.New("no resources found")
		}
		items, ok := field.([]interface{})
		if !ok || len(items) < 1 {
			return nil, errors.New("no resources found")
		}
		namespace = items[0].(map[string]interface{})["metadata"].(map[string]interface{})["namespace"].(string)
	} else {
		namespace = object.GetNamespace()
	}
	err = a.opClient.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().RESTClient().
		Delete().Namespace(namespace).RequestURI(uri).Do().Error()

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, err
		}
		return &broker.DeprovisionResponse{
			DeprovisionResponse: osb.DeprovisionResponse{
				Async:        false,
				OperationKey: &opkey,
			},
		}, nil
	}
	return &broker.DeprovisionResponse{
		DeprovisionResponse: osb.DeprovisionResponse{
			Async:        true,
			OperationKey: &opkey,
		},
	}, nil
}

func (a *ALMBroker) LastOperation(request *osb.LastOperationRequest, c *broker.RequestContext) (*broker.LastOperationResponse, error) {
	var object unstructured.Unstructured
	var description string
	if request == nil {
		return nil, errors.New("invalid request: <nil>")
	}

	values := c.Request.URL.Query()
	serviceID := values.Get("service_id")
	planID := values.Get("plan_id")
	instanceID := request.InstanceID
	catalog, err := a.GetCatalog(nil)
	if err != nil {
		return nil, err
	}
	var plan osb.Plan
	found := false
	for _, s := range catalog.Services {
		if s.ID == serviceID {
			for _, p := range s.Plans {
				if p.ID == planID {
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
	cr, err := planToCustomResourceObject(plan, instanceID, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	gvk := cr.GroupVersionKind()
	uri := fmt.Sprintf("/apis/%s/%s/%ss",
		strings.ToLower(gvk.Group),
		strings.ToLower(gvk.Version),
		strings.ToLower(gvk.Kind))
	err = a.opClient.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().RESTClient().
		Get().RequestURI(uri).
		Do().Into(&object)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &broker.LastOperationResponse{
				LastOperationResponse: osb.LastOperationResponse{
					State: osb.StateInProgress,
				},
			}, nil
		}
		msg := err.Error()
		return &broker.LastOperationResponse{
			LastOperationResponse: osb.LastOperationResponse{
				State:       osb.StateFailed,
				Description: &msg,
			},
		}, nil
	}

	resp := &broker.LastOperationResponse{
		LastOperationResponse: osb.LastOperationResponse{
			State:       osb.StateSucceeded, // TODO
			Description: &description,
		},
	}
	return resp, nil
}

func (a *ALMBroker) Bind(request *osb.BindRequest, c *broker.RequestContext) (*broker.BindResponse, error) {
	// TODO implement
	return nil, errors.New("not supported")
}

func (a *ALMBroker) Unbind(request *osb.UnbindRequest, c *broker.RequestContext) (*broker.UnbindResponse, error) {
	// TODO implement
	return nil, errors.New("not supported")
}

func (a *ALMBroker) Update(request *osb.UpdateInstanceRequest, c *broker.RequestContext) (*broker.UpdateInstanceResponse, error) {
	// TODO implement
	return nil, errors.New("not supported")
}
