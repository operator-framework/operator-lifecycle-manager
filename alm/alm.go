package alm

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/coreos-inc/operator-client/pkg/client"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
	"github.com/coreos-inc/alm/installstrategies"
)

type Operator struct {
	queue       workqueue.RateLimitingInterface
	informer    cache.SharedIndexInformer
	opClient    client.Interface
	opVerClient *rest.RESTClient
}

// NewOperatorVersionClient creates a client that can interact with the OperatorVersion resource in k8s api
func NewOperatorVersionClient(kubeconfig string) (client *rest.RESTClient, err error) {
	var config *rest.Config

	if len(kubeconfig) == 0 {
		// Work around https://github.com/kubernetes/kubernetes/issues/40973
		// See https://github.com/coreos/etcd-operator/issues/731#issuecomment-283804819
		if len(os.Getenv("KUBERNETES_SERVICE_HOST")) == 0 {
			addrs, err := net.LookupHost("kubernetes.default.svc")
			if err != nil {
				panic(err)
			}

			os.Setenv("KUBERNETES_SERVICE_HOST", addrs[0])
		}

		if len(os.Getenv("KUBERNETES_SERVICE_PORT")) == 0 {
			os.Setenv("KUBERNETES_SERVICE_PORT", "443")
		}

		log.Infof("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	} else {
		log.Infof("Loading kube client config from path %q", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return
	}

	scheme := runtime.NewScheme()
	if err := OperatorVersionAddToScheme(scheme); err != nil {
		return nil, err
	}

	config.GroupVersion = &OperatorVersionSchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}
	return rest.RESTClientFor(config)
}

// New creates a new Operator configured to manage the cluster defined in kubeconfig.
func New(kubeconfig string) (*Operator, error) {
	opClient := client.NewClient(kubeconfig)
	opVerClient, err := NewOperatorVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "alm")
	operatorVersionWatcher := cache.NewListWatchFromClient(
		opVerClient,
		"operatorversion-v1s",
		metav1.NamespaceAll,
		fields.Everything(),
	)
	operator := &Operator{
		opClient:    opClient,
		opVerClient: opVerClient,
		queue:       queue,
	}
	informer := cache.NewSharedIndexInformer(
		operatorVersionWatcher,
		&v1alpha1.OperatorVersion{},
		15*time.Minute,
		cache.Indexers{},
	)
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: operator.handleAddOperatorVersion,
	})
	operator.informer = informer
	return operator, nil
}

func (o *Operator) Run(stopc <-chan struct{}) error {
	defer o.queue.ShutDown()

	errChan := make(chan error)
	go func() {
		v, err := o.opClient.KubernetesInterface().Discovery().ServerVersion()
		if err != nil {
			errChan <- errors.Wrap(err, "communicating with server failed")
			return
		}
		log.Infof("connection established. cluster-version: %v", v)
		errChan <- nil
	}()

	select {
	case err := <-errChan:
		if err != nil {
			return err
		}
		log.Info("Operator ready")
	case <-stopc:
		return nil
	}

	go o.worker()
	go o.informer.Run(stopc)

	<-stopc
	return nil
}

func (o *Operator) keyFunc(obj interface{}) (string, bool) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Infof("creating key failed: %s", err)
		return k, false
	}

	return k, true
}

// enqueue adds a key to the queue. If obj is a key already it gets added directly.
// Otherwise, the key is extracted via keyFunc.
func (o *Operator) enqueue(obj interface{}) {
	if obj == nil {
		return
	}

	key, ok := obj.(string)
	if !ok {
		key, ok = o.keyFunc(obj)
		if !ok {
			return
		}
	}

	o.queue.Add(key)
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (c *Operator) worker() {
	for c.processNextWorkItem() {
	}
}

func (o *Operator) processNextWorkItem() bool {
	key, quit := o.queue.Get()
	if quit {
		return false
	}
	defer o.queue.Done(key)

	if err := o.sync(key.(string)); err != nil {
		utilruntime.HandleError(errors.Wrap(err, fmt.Sprintf("Sync %q failed", key)))
		o.queue.AddRateLimited(key)
		return true
	}
	o.queue.Forget(key)
	return true
}

func (o *Operator) sync(key string) error {
	obj, exists, err := o.informer.GetIndexer().GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		// For now, we ignore the case where an OperatorVersionSpec used to exist but no longer does
		return nil
	}

	operatorVersion, ok := obj.(*v1alpha1.OperatorVersion)
	if !ok {
		return fmt.Errorf("casting OperatorVersionSpec failed")
	}

	log.Infof("sync OperatorVersionSpec. key: %s", key)

	install := operatorVersion.Spec.InstallStrategy
	strategyDetails, err := v1alpha1.StrategyMapper.GetStrategySpec(&install)
	if err != nil {
		return err
	}
	if install.StrategyName == "deployment" {
		deployStrategy, ok := strategyDetails.(*v1alpha1.StrategyDetailsDeployment)
		if !ok {
			return fmt.Errorf("couldn't cast to deploy strategy: %v", strategyDetails)
		}

		existingDeployments, err := o.opClient.ListDeploymentsWithLabels(
			operatorVersion.Namespace,
			map[string]string{
				"alm-owned":           "true",
				"alm-owner-name":      operatorVersion.Name,
				"alm-owner-namespace": operatorVersion.Namespace})
		if err != nil {
			return fmt.Errorf("couldn't query for existing deployments: %s", err)
		}
		if len(existingDeployments.Items) != 0 {
			log.Infof("deployments found for %s, skipping install: %v", operatorVersion.Name, existingDeployments)
			return nil
		}
		kubeDeployment := installstrategies.NewKubeDeployment(o.opClient)
		if err := kubeDeployment.Install(operatorVersion.ObjectMeta, deployStrategy.Deployments); err != nil {
			return err
		} else {
			log.Infof("%s install strategy successful", install.StrategyName)
		}
	}

	return nil
}

func (o *Operator) handleAddOperatorVersion(obj interface{}) {
	key, ok := o.keyFunc(obj)
	if !ok {
		return
	}
	log.Info("msg", "OperatorVersionSpec added", "key", key)
	o.enqueue(key)
}
