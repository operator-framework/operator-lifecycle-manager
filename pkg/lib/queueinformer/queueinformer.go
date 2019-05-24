package queueinformer

import (
	"sync"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

// SyncHandler is a function that reconciles the controlled object and returns its updated representation.
type SyncHandler func(obj interface{}) error

// SyncHandlerChain is a sequence of SyncHandlers.
type SyncHandlerChain []SyncHandler

// Sync invokes each handler in the chain in order, skipping any nil handlers.
// It feeds obj to each handler and returns any error immediately.
func (s SyncHandlerChain) Sync(obj interface{}) error {
	for _, sync := range s {
		if sync != nil {
			if err := sync(obj); err != nil {
				return err
			}
		}
	}

	return nil
}

// QueueInformer ties an informer to a queue in order to process events from the informer
// the informer watches objects of interest and adds objects to the queue for processing
// the syncHandler is called for all objects on the queue
type QueueInformer struct {
	metrics.MetricsProvider
	cache.SharedIndexInformer

	mu               sync.RWMutex
	name             string
	queue            workqueue.RateLimitingInterface
	syncHandler      SyncHandler
	resourceHandlers *cache.ResourceEventHandlerFuncs
	metricHandlers   *cache.ResourceEventHandlerFuncs
	logger           *logrus.Logger
}

// Sync invokes all registered sync handlers in the QueueInformer's chain
func (q *QueueInformer) Sync(obj interface{}) error {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.syncHandler == nil {
		return nil
	}

	return q.syncHandler(obj)
}

// Enqueue adds a key to the queue. If obj is a key already it gets added directly.
// Otherwise, the key is extracted via keyFunc.
func (q *QueueInformer) Enqueue(obj interface{}) {
	if obj == nil {
		return
	}

	key, ok := obj.(string)
	if !ok {
		key, ok = q.keyFunc(obj)
		if !ok {
			return
		}
	}

	q.queue.Add(key)
}

// AddSyncHandlers adds the given handler and additional handlers to the QueueInformer's handler chain.
func (q *QueueInformer) AddSyncHandlers(handler SyncHandler, additional ...SyncHandler) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.syncHandler == nil {
		q.syncHandler = handler
		return
	}

	chain := append(SyncHandlerChain{q.syncHandler, handler}, additional...)
	q.syncHandler = chain.Sync
}

// keyFunc turns an object into a key for the queue. In the future will use a (name, namespace) struct as key
func (q *QueueInformer) keyFunc(obj interface{}) (string, bool) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		q.logger.Infof("creating key failed: %s", err)
		return k, false
	}

	return k, true
}

// defaultResourceHandlers provides the default implementation for responding to events
// these simply Log the event and add the object's key to the queue for later processing.
func (q *QueueInformer) defaultResourceHandlers() *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, ok := q.keyFunc(obj)
			if !ok {
				q.logger.Warnf("couldn't add %v, couldn't create key", obj)
				return
			}

			q.Enqueue(key)
		},
		DeleteFunc: func(obj interface{}) {
			key, ok := q.keyFunc(obj)
			if !ok {
				q.logger.Warnf("couldn't delete %v, couldn't create key", obj)
				return
			}

			q.queue.Forget(key)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, ok := q.keyFunc(newObj)
			if !ok {
				q.logger.Warnf("couldn't update %v, couldn't create key", newObj)
				return
			}

			q.Enqueue(key)
		},
	}
}

// defaultMetricHandlers provides the default implementation for handling metrics in response to events.
func (q *QueueInformer) defaultMetricHandlers() *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if err := q.HandleMetrics(); err != nil {
				q.logger.WithError(err).WithField("key", obj).Warn("error handling metrics on add event")
			}
		},
		DeleteFunc: func(obj interface{}) {
			if err := q.HandleMetrics(); err != nil {
				q.logger.WithError(err).WithField("key", obj).Warn("error handling metrics on delete event")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if err := q.HandleMetrics(); err != nil {
				q.logger.WithError(err).WithField("key", newObj).Warn("error handling metrics on update event")
			}
		},
	}
}

// NewQueueInformer returns a new QueueInformer configured with options.
func NewQueueInformer(name string, queue workqueue.RateLimitingInterface, informer cache.SharedIndexInformer, options ...Option) *QueueInformer {
	// Set default QueueInformer configuration
	queueInformer := &QueueInformer{
		MetricsProvider:     metrics.NewMetricsNil(),
		SharedIndexInformer: informer,

		name:   name,
		queue:  queue,
		logger: logrus.New(),
	}
	queueInformer.resourceHandlers = queueInformer.defaultResourceHandlers()
	queueInformer.metricHandlers = queueInformer.defaultMetricHandlers()
	queueInformer.AddSyncHandlers(queueInformer.metaLoggingSyncHandler)

	// Apply configuration options
	queueInformer.apply(options...)

	queueInformer.AddEventHandler(queueInformer.resourceHandlers)
	queueInformer.AddEventHandler(queueInformer.metricHandlers)

	return queueInformer
}

// NewQueueInformers creates a set of new queueinformers given a name, a set of informers, and a sync handler to handle the objects
// that the operator is managing. Optionally, custom event handler funcs can be passed in (defaults will be provided)
func NewQueueInformers(name string, queue workqueue.RateLimitingInterface, informers []cache.SharedIndexInformer, options ...Option) []*QueueInformer {
	queueInformers := []*QueueInformer{}
	for _, informer := range informers {
		queueInformers = append(queueInformers, NewQueueInformer(name, queue, informer, options...))
	}
	return queueInformers
}
