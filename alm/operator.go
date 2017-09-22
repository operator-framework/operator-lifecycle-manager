package alm

import (
	"fmt"
	"time"

	opClient "github.com/coreos-inc/operator-client/pkg/client"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/install"
)

type Operator struct {
	queue       workqueue.RateLimitingInterface
	informer    cache.SharedIndexInformer
	opClient    opClient.Interface
	opVerClient *rest.RESTClient
}

// New creates a new Operator configured to manage the cluster defined in kubeconfig.
func New(kubeconfig string) (*Operator, error) {
	opClient := opClient.NewClient(kubeconfig)
	opVerClient, err := client.NewOperatorVersionClient(kubeconfig)
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

	resolver := install.NewStrategyResolver(o.opClient, operatorVersion.ObjectMeta)
	err = resolver.ApplyStrategy(&operatorVersion.Spec.InstallStrategy)
	if err != nil {
		return err
	}

	log.Infof("%s install strategy successful for key: %s", operatorVersion.Spec.InstallStrategy.StrategyName, key)
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
