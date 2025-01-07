package queueinformer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/cache"
)

const (
	defaultServerVersionInterval = 1 * time.Minute
)

// ExtensibleOperator describes a Reconciler that can be extended with additional informers and queue informers
type ExtensibleOperator interface {
	// RegisterQueueInformer registers the given QueueInformer with the Operator.
	// This method returns an error if the Operator has already been started.
	RegisterQueueInformer(queueInformer *QueueInformer) error

	// RegisterInformer registers an informer with the Operator.
	// This method returns an error if the Operator has already been started.
	RegisterInformer(cache.SharedIndexInformer) error
}

// ObservableOperator describes a Reconciler whose state can be queried
type ObservableOperator interface {
	// Ready returns a channel that is closed when the Operator is ready to run.
	Ready() <-chan struct{}

	// Done returns a channel that is closed when the Operator is done running.
	Done() <-chan struct{}

	// AtLevel returns a channel that emits errors when the Operator is not at level.
	AtLevel() <-chan error

	// Started returns true if RunInformers() has been called, false otherwise.
	Started() bool

	// HasSynced returns true if the Operator's Informers have synced, false otherwise.
	HasSynced() bool
}

// Operator describes a Reconciler that manages a set of QueueInformers.
type Operator interface {
	ObservableOperator
	ExtensibleOperator
	// RunInformers starts the Operator's underlying Informers.
	RunInformers(ctx context.Context)

	// Run starts the Operator and its underlying Informers.
	Run(ctx context.Context)
}

type operator struct {
	serverVersion    discovery.ServerVersionInterface
	queueInformers   []*QueueInformer
	informers        []cache.SharedIndexInformer
	hasSynced        cache.InformerSynced
	mu               sync.RWMutex
	numWorkers       int
	runInformersOnce sync.Once
	reconcileOnce    sync.Once
	logger           *logrus.Logger
	ready            chan struct{}
	done             chan struct{}
	atLevel          chan error
	syncCh           chan error
	started          bool
}

func (o *operator) Ready() <-chan struct{} {
	return o.ready
}

func (o *operator) Done() <-chan struct{} {
	return o.done
}

func (o *operator) AtLevel() <-chan error {
	return o.atLevel
}

func (o *operator) HasSynced() bool {
	return o.hasSynced()
}

func (o *operator) Started() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return o.started
}

func (o *operator) RegisterQueueInformer(queueInformer *QueueInformer) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	err := errors.New("failed to register queue informer")
	if queueInformer == nil {
		return errors.Wrap(err, "nil queue informer")
	}

	if o.started {
		return errors.Wrap(err, "operator already started")
	}

	o.queueInformers = append(o.queueInformers, queueInformer)

	// Some QueueInformers do not have informers associated with them.
	// Only add to the list of informers when one exists.
	if informer := queueInformer.informer; informer != nil {
		o.registerInformer(informer)
	}

	return nil
}

func (o *operator) RegisterInformer(informer cache.SharedIndexInformer) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	err := errors.New("failed to register informer")
	if informer == nil {
		return errors.Wrap(err, "nil informer")
	}

	if o.started {
		return errors.Wrap(err, "operator already started")
	}

	o.registerInformer(informer)

	return nil
}

func (o *operator) registerInformer(informer cache.SharedIndexInformer) {
	// never double-register an informer
	for i := range o.informers {
		if o.informers[i] == informer {
			return
		}
	}
	o.informers = append(o.informers, informer)
	o.addHasSynced(informer.HasSynced)
}

func (o *operator) addHasSynced(hasSynced cache.InformerSynced) {
	if o.hasSynced == nil {
		o.hasSynced = hasSynced
		return
	}

	prev := o.hasSynced
	o.hasSynced = func() bool {
		return prev() && hasSynced()
	}
}

// TODO: generalize over func(ctx) so this can start informers + source watcher
func (o *operator) RunInformers(ctx context.Context) {
	o.runInformersOnce.Do(func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		for _, informer := range o.informers {
			go informer.Run(ctx.Done())
		}

		o.started = true
		o.logger.Infof("informers started")
	})
}

// Run starts the operator's control loops.
func (o *operator) Run(ctx context.Context) {
	o.reconcileOnce.Do(func() {
		go func() {
			defer func() {
				for _, queueInformer := range o.queueInformers {
					queueInformer.queue.ShutDown()
				}
				close(o.atLevel)
				close(o.done)
			}()
			if err := o.start(ctx); err != nil {
				o.logger.WithError(err).Error("error encountered during startup")
				return
			}
			<-ctx.Done()
		}()
	})
}

func (o *operator) start(ctx context.Context) error {
	defer close(o.ready)

	// goroutine will be unnecessary after https://github.com/kubernetes/enhancements/pull/1503
	errs := make(chan error)
	go func() {
		defer close(errs)
		v, err := o.serverVersion.ServerVersion()
		if err == nil {
			o.logger.Infof("connection established. cluster-version: %v", v)
			return
		}
		select {
		case <-time.After(defaultServerVersionInterval):
		case <-ctx.Done():
			return
		}
		v, err = o.serverVersion.ServerVersion()
		if err != nil {
			select {
			case errs <- errors.Wrap(err, "communicating with server failed"):
			case <-ctx.Done():
				// don't block send forever on cancellation
			}
			return
		}
		o.logger.Infof("connection established. cluster-version: %v", v)
	}()

	select {
	case err := <-errs:
		if err != nil {
			return fmt.Errorf("operator not ready: %s", err.Error())
		}
		o.logger.Info("operator ready")
	case <-ctx.Done():
		return nil
	}

	o.logger.Info("starting informers...")
	o.RunInformers(ctx)

	o.logger.Info("waiting for caches to sync...")
	if ok := cache.WaitForCacheSync(ctx.Done(), o.hasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	o.logger.Info("starting workers...")
	for _, queueInformer := range o.queueInformers {
		for w := 0; w < o.numWorkers; w++ {
			go o.worker(ctx, queueInformer)
		}
	}

	return nil
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (o *operator) worker(ctx context.Context, loop *QueueInformer) {
	for o.processNextWorkItem(ctx, loop) {
	}
}

func (o *operator) processNextWorkItem(ctx context.Context, loop *QueueInformer) bool {
	// **************************** WARNING ****************************
	// The QueueInformer listens to resource events raised by its
	// (client-go) informer. For Add/Update event, it extracts the key
	// for the resource and adds to its queue (that we Get() from below)
	// a ResourceEvent carrying the key.
	// **Except** if it is a deletion event. In that case,
	// ResourceEvent carries the resource object (or tombstone).
	// The sync'er expects a ResourceEvent carrying the resource.
	// So, in the case of an Add/Update event coming from the queue,
	// the resource is acquired from the index (through the key), and then
	// a ResourceEvent carrying the resource is handed to the syncer.
	// It should also be noted that throughout the code, items are added to
	// queueinformers out of band of informer notifications.
	// The fact that the queueinformers queue processes ResourceEvents, which
	// themselves encapsulate an interface{} "Resource" make it tricky for the
	// queue to dedup. Previous to the writing of this comment, the queue was
	// processing strings and ResourceEvents, which led to concurrent processing
	// of the same resource. To address this, we enforce (with panic) that the resource
	// in the ResourceEvent must either be a cache.ExplicitKey or a client.Object.
	// We then make sure that the ResourceEvent's String() returns the key for the
	// encapsulated resource. Thus, independent of the resource type, the queue always
	// processes it by key and dedups appropriately.
	// Furthermore, we also enforce here that Add/Update events always contain
	// cache.ExplicitKey as their Resource
	queue := loop.queue
	item, quit := queue.Get()

	if quit {
		return false
	}
	defer queue.Done(item)

	logger := o.logger.WithField("item", item)
	logger.WithField("queue-length", queue.Len()).Info("popped queue")

	var event = item
	if item.Type() != kubestate.ResourceDeleted {
		key, keyable := loop.key(item)
		if !keyable {
			logger.WithField("item", item).Warn("could not form key")
			queue.Forget(item)
			return true
		}

		logger = logger.WithField("cache-key", key)

		// Get the current cached version of the resource
		var exists bool
		var err error
		resource, exists, err := loop.indexer.GetByKey(string(key))
		if err != nil {
			logger.WithError(err).Error("cache get failed")
			queue.Forget(item)
			return true
		}
		if !exists {
			logger.WithField("existing-cache-keys", loop.indexer.ListKeys()).Debug("cache get failed, key not in cache")
			queue.Forget(item)
			return true
		}
		event = kubestate.NewResourceEvent(item.Type(), resource)
	}

	// Sync and requeue on error (throw out failed deletion syncs)
	err := loop.Sync(ctx, event)
	if requeues := queue.NumRequeues(item); err != nil && requeues < 8 && item.Type() != kubestate.ResourceDeleted {
		logger.WithField("requeues", requeues).Trace("requeuing with rate limiting")
		utilruntime.HandleError(errors.Wrap(err, fmt.Sprintf("sync %q failed", item)))
		queue.AddRateLimited(item)
		return true
	}
	queue.Forget(item)

	select {
	case o.syncCh <- err:
	default:
	}

	return true
}

// NewOperator returns a new Operator configured to manage the cluster with the given server version client.
func NewOperator(sv discovery.ServerVersionInterface, options ...OperatorOption) (Operator, error) {
	config := defaultOperatorConfig()
	config.serverVersion = sv
	config.apply(options)
	if err := config.validate(); err != nil {
		return nil, err
	}

	return newOperatorFromConfig(config)

}

func newOperatorFromConfig(config *operatorConfig) (*operator, error) {
	op := &operator{
		serverVersion: config.serverVersion,
		numWorkers:    config.numWorkers,
		logger:        config.logger,
		ready:         make(chan struct{}),
		done:          make(chan struct{}),
		atLevel:       make(chan error, 25),
	}
	op.syncCh = op.atLevel

	// Register QueueInformers and Informers
	for _, queueInformer := range op.queueInformers {
		if err := op.RegisterQueueInformer(queueInformer); err != nil {
			return nil, err
		}
	}
	for _, informer := range op.informers {
		if err := op.RegisterInformer(informer); err != nil {
			return nil, err
		}
	}

	return op, nil
}
