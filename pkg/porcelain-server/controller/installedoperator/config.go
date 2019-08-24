package installedoperator

import (
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	operatorsv1informers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions/operators/v1"
	operatorsv1alpha1informers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions/operators/v1alpha1"
	registry "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/registry/porcelain/installedoperator"
)

type controllerConfig struct {
	kubeclientset kubernetes.Interface
	registry      *registry.REST
	workqueue     workqueue.RateLimitingInterface
	csvInformer   operatorsv1alpha1informers.ClusterServiceVersionInformer
	subInformer   operatorsv1alpha1informers.SubscriptionInformer
	ogInformer    operatorsv1informers.OperatorGroupInformer
}

func (c *controllerConfig) apply(options []ControllerOption) {
	for _, option := range options {
		option(c)
	}
}

func newControllerConfig() *controllerConfig {
	return &controllerConfig{}
}

type configValidationError string

func (c configValidationError) Error() string {
	return fmt.Sprintf("controller config validation error: %s", c)
}

func (c *controllerConfig) validate() error {
	if c.kubeclientset == nil {
		return configValidationError("nil kube clientset")
	}
	if c.csvInformer == nil {
		return configValidationError("nil csv informer")
	}
	if c.subInformer == nil {
		return configValidationError("nil sub informer")
	}
	if c.ogInformer == nil {
		return configValidationError("nil og informer")
	}

	return nil
}

func (c *controllerConfig) complete() {
	if c.workqueue == nil {
		c.workqueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "installedoperators")
	}

	// Add CSV index so we can quickly look up related Subscriptions.
	c.subInformer.Informer().AddIndexers(cache.Indexers{
		CSVSubscriptionIndexFuncKey: CSVSubscriptionIndexFunc,
	})
}

// ControllerOption is a configuration option for a controller.
type ControllerOption func(*controllerConfig)

// WithKubeclientset sets a controller's kubernetes client.
func WithKubeclientset(clientset kubernetes.Interface) ControllerOption {
	return func(config *controllerConfig) {
		config.kubeclientset = clientset
	}
}

// WithRegistry sets a controller's registry.
func WithRegistry(reg *registry.REST) ControllerOption {
	return func(config *controllerConfig) {
		config.registry = reg
	}
}

// WithWorkqueue sets a controller's workqueue for processing events concerning InstalledOperators.
func WithWorkqueue(queue workqueue.RateLimitingInterface) ControllerOption {
	return func(config *controllerConfig) {
		config.workqueue = queue
	}
}

// WithCSVInformer sets a controller's CSV informer for tracking changes to CSVs.
func WithCSVInformer(informer operatorsv1alpha1informers.ClusterServiceVersionInformer) ControllerOption {
	return func(config *controllerConfig) {
		config.csvInformer = informer
	}
}

// WithSubInformer sets a controller's Subscription informer for tracking changes to subscriptions.
func WithSubInformer(informer operatorsv1alpha1informers.SubscriptionInformer) ControllerOption {
	return func(config *controllerConfig) {
		config.subInformer = informer
	}
}

// WithOGInformer sets a controller's OperatorGroup informer for tracking changes to OperatorGroups.
func WithOGInformer(informer operatorsv1informers.OperatorGroupInformer) ControllerOption {
	return func(config *controllerConfig) {
		config.ogInformer = informer
	}
}
