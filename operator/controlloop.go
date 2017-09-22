package operator

import (
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// SyncHandler is the function that reconciles the controlled object when seen
type SyncHandler func(obj interface{}) error

// ControlLoop ties an informer to a queue in order to process events from the informer
// the informer watches objects of interest and adds objects to the queue for processing
// the syncHandler is called for all objects on the queue
type ControlLoop struct {
	queue                     workqueue.RateLimitingInterface
	informer                  cache.SharedIndexInformer
	syncHandler               SyncHandler
	resourceEventHandlerFuncs *cache.ResourceEventHandlerFuncs
}

// enqueue adds a key to the queue. If obj is a key already it gets added directly.
// Otherwise, the key is extracted via keyFunc.
func (q *ControlLoop) enqueue(obj interface{}) {
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

// keyFunc turns an object into a key for the queue. In the future will use a (name, namespace) struct as key
func (q *ControlLoop) keyFunc(obj interface{}) (string, bool) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Infof("creating key failed: %s", err)
		return k, false
	}

	return k, true
}

// defaultResourceEventhandlerFuncs provides the default implementation for responding to events
// these simply log the event and add the object's key to the queue for later processing
func (q *ControlLoop) defaultResourceEventHandlerFuncs() *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, ok := q.keyFunc(obj)
			if !ok {
				return
			}

			log.Infof("%s added", key)
			q.enqueue(key)
		},
		DeleteFunc: func(obj interface{}) {
			key, ok := q.keyFunc(obj)
			if !ok {
				return
			}

			log.Infof("%s deleted", key)
			q.enqueue(key)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, ok := q.keyFunc(newObj)
			if !ok {
				return
			}

			log.Infof("%s updated", key)
			q.enqueue(key)
		},
	}
}

// NewControlLoop creates a new control loop given a name, an informer, and a sync handler to handle the objects
// that the operator is managing. Optionally, custom event handler funcs can be passed in (defaults will be provided)
func NewControlLoop(queuename string, informer cache.SharedIndexInformer, handler SyncHandler, funcs *cache.ResourceEventHandlerFuncs) *ControlLoop {
	controlLoop := &ControlLoop{
		queue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), queuename),
		informer:    informer,
		syncHandler: handler,
	}
	if funcs == nil {
		controlLoop.resourceEventHandlerFuncs = controlLoop.defaultResourceEventHandlerFuncs()
	} else {
		controlLoop.resourceEventHandlerFuncs = funcs
	}
	controlLoop.informer.AddEventHandler(controlLoop.resourceEventHandlerFuncs)
	return controlLoop
}
