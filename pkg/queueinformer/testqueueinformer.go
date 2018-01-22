package queueinformer

import (
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

// TestQueueInformer wraps a normal queueinformer with knobs for injecting data for testing
type TestQueueInformer struct {
	QueueInformer
}

func (q *TestQueueInformer) Enqueue(obj interface{}) {
	q.QueueInformer.enqueue(obj)
}

func NewTestQueueInformer(queue workqueue.RateLimitingInterface, informer cache.SharedIndexInformer, handler SyncHandler, funcs *cache.ResourceEventHandlerFuncs) *TestQueueInformer {
	queueInformer := &TestQueueInformer{
		QueueInformer{
			queue:       queue,
			informer:    informer,
			syncHandler: handler,
		},
	}
	if funcs == nil {
		queueInformer.resourceEventHandlerFuncs = queueInformer.defaultResourceEventHandlerFuncs()
	} else {
		queueInformer.resourceEventHandlerFuncs = funcs
	}
	queueInformer.informer.AddEventHandler(queueInformer.resourceEventHandlerFuncs)

	return queueInformer
}
