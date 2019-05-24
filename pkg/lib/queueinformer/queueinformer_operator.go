package queueinformer

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// An Operator is a collection of QueueInformers and QueueIndexers.
// OpClient is used to establish the connection to kubernetes.
type Operator struct {
	queueInformers []*QueueInformer
	queueIndexers  []*QueueIndexer
	informers      []cache.SharedIndexInformer
	hasSynced      cache.InformerSynced
	mu             sync.RWMutex
	OpClient       operatorclient.ClientInterface
	Log            *logrus.Logger
	syncCh         chan error
}

// NewOperator creates a new Operator configured to manage the cluster defined in kubeconfig.
func NewOperator(kubeconfig string, logger *logrus.Logger, queueInformers ...*QueueInformer) (*Operator, error) {
	opClient := operatorclient.NewClientFromConfig(kubeconfig, logger)
	if queueInformers == nil {
		queueInformers = []*QueueInformer{}
	}
	operator := &Operator{
		OpClient:       opClient,
		queueInformers: queueInformers,
		Log:            logger,
	}
	return operator, nil
}

func NewOperatorFromClient(opClient operatorclient.ClientInterface, logger *logrus.Logger, queueInformers ...*QueueInformer) (*Operator, error) {
	operator := &Operator{
		OpClient:       opClient,
		queueInformers: queueInformers,
		Log:            logger,
	}
	return operator, nil
}

func (o *Operator) HasSynced() bool {
	return o.hasSynced()
}

func (o *Operator) addHasSynced(hasSynced cache.InformerSynced) {
	if o.hasSynced == nil {
		o.hasSynced = hasSynced
		return
	}

	prev := o.hasSynced
	o.hasSynced = func() bool {
		return prev() && hasSynced()
	}
}

// RegisterQueueInformer adds a QueueInformer to the operator.
func (o *Operator) RegisterQueueInformer(queueInformer *QueueInformer) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.queueInformers = append(o.queueInformers, queueInformer)
	o.addHasSynced(queueInformer.HasSynced)
}

// RegisterQueueIndexer adds a QueueIndexer to the operator.
func (o *Operator) RegisterQueueIndexer(indexer *QueueIndexer) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.queueIndexers = append(o.queueIndexers, indexer)
}

func (o *Operator) RunInformers(ctx context.Context) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, informer := range o.queueInformers {
		go informer.Run(ctx.Done())
	}
}

// Run starts the operator's control loops.
func (o *Operator) Run(ctx context.Context) (ready, done chan struct{}, atLevel chan error) {
	ready = make(chan struct{})
	atLevel = make(chan error, 25)
	done = make(chan struct{})

	o.syncCh = atLevel

	go func() {
		// Prevent any informers from being added while running
		o.mu.RLock()
		defer o.mu.RUnlock()

		defer func() {
			close(ready)
			close(atLevel)
			close(done)
		}()

		for _, queueInformer := range o.queueInformers {
			defer queueInformer.queue.ShutDown()
		}

		errs := make(chan error)
		go func() {
			defer close(errs)
			v, err := o.OpClient.KubernetesInterface().Discovery().ServerVersion()
			if err != nil {
				errs <- errors.Wrap(err, "communicating with server failed")
				return
			}
			o.Log.Infof("connection established. cluster-version: %v", v)

		}()

		select {
		case err := <-errs:
			if err != nil {
				o.Log.Infof("operator not ready: %s", err.Error())
				return
			}
			o.Log.Info("operator ready")
		case <-ctx.Done():
			return
		}

		o.Log.Info("starting informers...")
		o.RunInformers(ctx)

		o.Log.Info("waiting for caches to sync...")
		if ok := cache.WaitForCacheSync(ctx.Done(), o.hasSynced); !ok {
			o.Log.Info("failed to wait for caches to sync")
			return
		}

		o.Log.Info("starting workers...")
		for _, queueInformer := range o.queueInformers {
			go o.worker(queueInformer)
			go o.worker(queueInformer)
		}

		for _, queueIndexer := range o.queueIndexers {
			go o.indexerWorker(queueIndexer)
			go o.indexerWorker(queueIndexer)
		}
		ready <- struct{}{}
		<-ctx.Done()
	}()

	return
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

	// requeue five times on error
	err := o.sync(loop, key.(string))
	if err != nil && queue.NumRequeues(key.(string)) < 5 {
		o.Log.Infof("retrying %s", key)
		utilruntime.HandleError(errors.Wrap(err, fmt.Sprintf("Sync %q failed", key)))
		queue.AddRateLimited(key)
		return true
	}
	queue.Forget(key)

	select {
	case o.syncCh <- err:
	default:
	}

	return true
}

func (o *Operator) sync(loop *QueueInformer, key string) error {
	logger := o.Log.WithField("queue", loop.name).WithField("key", key)
	obj, exists, err := loop.GetIndexer().GetByKey(key)
	if err != nil {
		return err
	}
	if !exists {
		// For now, we ignore the case where an object used to exist but no longer does
		logger.Info("couldn't get from queue")
		logger.Debugf("have keys: %v", loop.GetIndexer().ListKeys())
		return nil
	}

	return loop.Sync(obj)
}

// This provides the same function as above, but for queues that are not auto-fed by informers.
// indexerWorker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (o *Operator) indexerWorker(loop *QueueIndexer) {
	for o.processNextIndexerWorkItem(loop) {
	}
}

func (o *Operator) processNextIndexerWorkItem(loop *QueueIndexer) bool {
	queue := loop.queue
	key, quit := queue.Get()

	if quit {
		return false
	}
	defer queue.Done(key)

	// requeue five times on error
	if err := o.syncIndexer(loop, key.(string)); err != nil && queue.NumRequeues(key.(string)) < 5 {
		o.Log.Infof("retrying %s", key)
		utilruntime.HandleError(errors.Wrap(err, fmt.Sprintf("Sync %q failed", key)))
		queue.AddRateLimited(key)
		return true
	}
	queue.Forget(key)

	return true
}

func (o *Operator) syncIndexer(loop *QueueIndexer, key string) error {
	logger := o.Log.WithField("queue", loop.name).WithField("key", key)

	obj, exists, err := loop.indexer.GetByKey(key)
	if err != nil {
		return err
	}
	if !exists {
		// For now, we ignore the case where an object used to exist but no longer does
		logger.Info("couldn't get from queue")
		logger.Debugf("have keys: %v", loop.indexer.ListKeys())
		return nil
	}

	return loop.Sync(obj)
}
