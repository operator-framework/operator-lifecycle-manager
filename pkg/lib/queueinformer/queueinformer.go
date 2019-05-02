package queueinformer

import (
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	opcache "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

// SyncHandler is the function that reconciles the controlled object when seen
type SyncHandler func(obj interface{}) error

// QueueInformer ties an informer to a queue in order to process events from the informer
// the informer watches objects of interest and adds objects to the queue for processing
// the syncHandler is called for all objects on the queue
type QueueInformer struct {
	queue                     workqueue.RateLimitingInterface
	informer                  cache.SharedIndexInformer
	syncHandler               SyncHandler
	resourceEventHandlerFuncs *cache.ResourceEventHandlerFuncs
	name                      string
	metrics.MetricsProvider
	log *logrus.Logger
}

// enqueue adds a key to the queue. If obj is a key already it gets added directly.
// Otherwise, the key is extracted via keyFunc.
func enqueue(obj interface{}, key keyFunc, queue workqueue.RateLimitingInterface) {
	if obj == nil {
		return
	}

	k, ok := obj.(string)
	if !ok {
		k, ok = key(obj)
		if !ok {
			return
		}
	}

	queue.Add(k)
}

// keyFunc turns an object into a key for the queue. In the future will use a (name, namespace) struct as key
type keyFunc func(obj interface{}) (string, bool)

// key turns an object into a key for the queue. In the future will use a (name, namespace) struct as key
func (q *QueueInformer) key(obj interface{}) (string, bool) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		q.log.WithError(err).Debugf("creating key failed for: %v", obj)
		return k, false
	}

	return k, true
}

func (q *QueueInformer) defaultAddFunc(queue workqueue.RateLimitingInterface) func(obj interface{}) {
	return func(obj interface{}) {
		if _, ok := q.key(obj); !ok {
			q.log.Warnf("couldn't add %v, couldn't create key", obj)
			return
		}

		enqueue(obj, q.key, queue)
	}
}

func (q *QueueInformer) defaultDeleteFunc(queue workqueue.RateLimitingInterface) func(obj interface{}) {
	return func(obj interface{}) {
		k, ok := q.key(obj)
		if !ok {
			q.log.Warnf("couldn't delete %v, couldn't create key", obj)
			return
		}

		queue.Forget(k)
	}
}

func (q *QueueInformer) defaultUpdateFunc(queue workqueue.RateLimitingInterface) func(oldObj, newObj interface{}) {
	return func(oldObj, newObj interface{}) {
		if _, ok := q.key(newObj); !ok {
			q.log.Warnf("couldn't update %v, couldn't create key", newObj)
			return
		}

		enqueue(newObj, q.key, queue)
	}
}

// defaultResourceEventhandlerFuncs provides the default implementation for responding to events
// these simply Log the event and add the object's key to the queue for later processing
func (q *QueueInformer) defaultResourceEventHandlerFuncs(queue workqueue.RateLimitingInterface) *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		AddFunc:    q.defaultAddFunc(queue),
		DeleteFunc: q.defaultDeleteFunc(queue),
		UpdateFunc: q.defaultUpdateFunc(queue),
	}
}

// New creates a set of new queueinformers given a name, a set of informers, and a sync handler to handle the objects
// that the operator is managing. Optionally, custom event handler funcs can be passed in (defaults will be provided)
func New(queue workqueue.RateLimitingInterface, informers []cache.SharedIndexInformer, handler SyncHandler, funcs *cache.ResourceEventHandlerFuncs, name string, metrics metrics.MetricsProvider, logger *logrus.Logger) []*QueueInformer {
	queueInformers := []*QueueInformer{}
	for _, informer := range informers {
		queueInformers = append(queueInformers, NewInformer(queue, informer, handler, funcs, name, metrics, logger))
	}
	return queueInformers
}

// NewInformer creates a new queueinformer given a name, an informer, and a sync handler to handle the objects
// that the operator is managing. Optionally, custom event handler funcs can be passed in (defaults will be provided)
func NewInformer(queue workqueue.RateLimitingInterface, informer cache.SharedIndexInformer, handler SyncHandler, funcs *cache.ResourceEventHandlerFuncs, name string, metrics metrics.MetricsProvider, logger *logrus.Logger) *QueueInformer {
	queueInformer := &QueueInformer{
		queue:           queue,
		informer:        informer,
		syncHandler:     handler,
		name:            name,
		MetricsProvider: metrics,
		log:             logger,
	}
	queueInformer.resourceEventHandlerFuncs = queueInformer.defaultResourceEventHandlerFuncs(queue)
	if funcs != nil {
		if funcs.AddFunc != nil {
			queueInformer.resourceEventHandlerFuncs.AddFunc = funcs.AddFunc
		}
		if funcs.DeleteFunc != nil {
			queueInformer.resourceEventHandlerFuncs.DeleteFunc = funcs.DeleteFunc
		}
		if funcs.UpdateFunc != nil {
			queueInformer.resourceEventHandlerFuncs.UpdateFunc = funcs.UpdateFunc
		}
	}
	queueInformer.informer.AddEventHandler(queueInformer.resourceEventHandlerFuncs)
	return queueInformer
}

// WithViewIndexer adds EventHandler funcs that inform the given ViewIndexer of changes.
// Returns the queueinformer to support chaining.
// TODO: Make this a method of a type that embeds QueueInformer instead.
func (q *QueueInformer) WithViewIndexer(viewIndexer opcache.ViewIndexer) *QueueInformer {
	logger := q.log.WithField("eventhandler", "viewindexer")
	handler := &cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			// TODO: Check for resource existing and update instead if it exists
			logger := logger.WithField("operation", "add")
			logger.Debugf("%v", obj)
			if err := viewIndexer.Add(obj); err != nil {
				logger.WithError(err).Warnf("could not add object of type %T to ViewIndexer", obj)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			// TODO: Check for resource not existing before update and add instead if it doesn't exist
			logger := logger.WithField("operation", "update")
			logger.Debugf("%v", newObj)
			if err := viewIndexer.Update(newObj); err != nil {
				logger.WithError(err).Warnf("could not update object of type %T in ViewIndexer", newObj)
			}
		},
		DeleteFunc: func(obj interface{}) {
			// TODO: Check for resource existence before deleting
			logger := logger.WithField("operation", "delete")
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
				logger.Debug("unpacked tombstone")
			}

			logger.Debugf("%v", obj)
			if err := viewIndexer.Delete(obj); err != nil {
				logger.WithError(err).Warnf("could not delete object of type %T in ViewIndexer", obj)
			}
		},
	}

	q.informer.AddEventHandler(handler)

	return q
}

// AdditionallyDistribute adds EventHandler funcs that distribute events to each queue in the given list.
// Returns the queueinformer to support chaining.
// TODO: No ordering is guarenteed between event handlers.
func (q *QueueInformer) AdditionallyDistribute(queues ...workqueue.RateLimitingInterface) *QueueInformer {
	// Build handler func for each queue
	numQueues := len(queues)
	add := make([]func(obj interface{}), numQueues)
	update := make([]func(oldObj, newObj interface{}), numQueues)
	del := make([]func(obj interface{}), numQueues)
	for i, queue := range queues {
		add[i] = q.defaultAddFunc(queue)
		update[i] = q.defaultUpdateFunc(queue)
		del[i] = q.defaultDeleteFunc(queue)
	}

	handler := &cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			for _, f := range add {
				f(obj)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			for _, f := range update {
				f(oldObj, newObj)
			}
		},
		DeleteFunc: func(obj interface{}) {
			for _, f := range del {
				f(obj)
			}
		},
	}

	q.informer.AddEventHandler(handler)

	return q
}
