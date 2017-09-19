package alm

import (
	"time"

	"fmt"

	"github.com/coreos-inc/alm/operators"
	"github.com/coreos-inc/operator-client/pkg/client"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	crv1 "k8s.io/apiextensions-apiserver/examples/client-go/apis/cr/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Operator struct {
	queue       workqueue.RateLimitingInterface
	informer    cache.SharedIndexInformer
	opClient    client.Interface
	opVerClient *rest.RESTClient
}

// NewOperatorVersionClient creates a client that can interact with the OperatorVersion resource in k8s api
func NewOperatorVersionClient(kubeconfig string) (*rest.RESTClient, error) {
	var config *rest.Config
	var err error

	if kubeconfig != "" {
		log.Infof("Loading kube client config from path %q", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		log.Infof("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := crv1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	config.GroupVersion = &crv1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: serializer.NewCodecFactory(scheme)}

	return rest.RESTClientFor(config)
}

// New creates a new Operator configured to manage the cluster defined in kubeconfig
func New(kubeconfig string) (*Operator, error) {
	operator := &Operator{
		opClient: client.NewClient(kubeconfig),
	}

	opVerClient, err := NewOperatorVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	operator.opVerClient = opVerClient

	operator.queue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "alm")
	operatorVersionWatcher := cache.NewListWatchFromClient(
		operator.opVerClient,
		"operatorversion-v1s",
		metav1.NamespaceAll,
		fields.Everything(),
	)
	operator.informer = cache.NewSharedIndexInformer(
		operatorVersionWatcher,
		&OperatorVersion{},
		15*time.Minute,
		cache.Indexers{},
	)
	operator.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: operator.handleAddOperatorVersion,
	})
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

	err := o.sync(key.(string))
	if err == nil {
		o.queue.Forget(key)
		return true
	}

	utilruntime.HandleError(errors.Wrap(err, fmt.Sprintf("Sync %q failed", key)))
	o.queue.AddRateLimited(key)

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

	operatorVersion, ok := obj.(*OperatorVersion)
	if !ok {
		return fmt.Errorf("casting OperatorVersionSpec failed")
	}

	log.Info("msg", "sync OperatorVersionSpec", "key", key)
	install := operatorVersion.Spec.InstallStrategy.UnstructuredContent()
	strategy := install["strategy"]
	strategyString, ok := strategy.(string)
	if !ok {
		return fmt.Errorf("casting strategy failed")
	}
	if strategyString == "deployment" {
		kubeDeployment := alm.NewKubeDeployment(o.opClient)
		kubeDeployment.Install(operatorVersion.ObjectMeta.Namespace, install["deployments"])
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
