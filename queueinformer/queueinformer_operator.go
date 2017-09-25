package queueinformer

import (
	"fmt"

	opClient "github.com/coreos-inc/operator-client/pkg/client"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

// An Operator is a collection of QueueInformers
// OpClient is used to establish the connection to kubernetes
type Operator struct {
	queueInformers []*QueueInformer
	OpClient       opClient.Interface
}

// NewOperator creates a new Operator configured to manage the cluster defined in kubeconfig.
func NewOperator(kubeconfig string, queueInformers ...*QueueInformer) (*Operator, error) {
	opClient := opClient.NewClient(kubeconfig)
	if queueInformers == nil {
		queueInformers = []*QueueInformer{}
	}
	operator := &Operator{
		OpClient:       opClient,
		queueInformers: queueInformers,
	}
	return operator, nil
}

// RegisterQueueInformer adds a QueueInformer to this operator
func (o *Operator) RegisterQueueInformer(queueInformer *QueueInformer) {
	o.queueInformers = append(o.queueInformers, queueInformer)
}

// Run starts the operator's control loops
func (o *Operator) Run(stopc <-chan struct{}) error {
	for _, queueInformer := range o.queueInformers {
		defer queueInformer.queue.ShutDown()
	}

	errChan := make(chan error)
	go func() {
		v, err := o.OpClient.KubernetesInterface().Discovery().ServerVersion()
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

	for _, queueInformer := range o.queueInformers {
		go o.worker(queueInformer)
		go queueInformer.informer.Run(stopc)
	}

	<-stopc
	return nil
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (o *Operator) worker(loop *QueueInformer) {
	for o.processNextWorkItem(loop) {
	}
}

func (o *Operator) processNextWorkItem(loop *QueueInformer) bool {
	queue := loop.queue
	key, quit := queue.Get()
	if quit {
		return false
	}
	defer queue.Done(key)

	if err := o.sync(loop, key.(string)); err != nil {
		utilruntime.HandleError(errors.Wrap(err, fmt.Sprintf("Sync %q failed", key)))
		queue.AddRateLimited(key)
		return true
	}
	queue.Forget(key)
	return true
}

func (o *Operator) sync(loop *QueueInformer, key string) error {
	obj, exists, err := loop.informer.GetIndexer().GetByKey(key)
	if err != nil {
		return err
	}

	if !exists {
		// For now, we ignore the case where an object used to exist but no longer does
		return nil
	}
	return loop.syncHandler(obj)
}
