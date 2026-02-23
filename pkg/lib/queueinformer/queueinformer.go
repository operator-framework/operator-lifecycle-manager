package queueinformer

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

// QueueInformer ties an informer to a queue in order to process events from the informer
// the informer watches objects of interest and adds objects to the queue for processing
// the syncHandler is called for all objects on the queue
type QueueInformer struct {
	metrics.MetricsProvider

	logger   *logrus.Logger
	queue    workqueue.TypedRateLimitingInterface[types.NamespacedName]
	informer cache.SharedIndexInformer
	indexer  cache.Indexer
	syncer   kubestate.Syncer
	onDelete func(interface{})
}

// Sync invokes all registered sync handlers in the QueueInformer's chain
func (q *QueueInformer) Sync(ctx context.Context, obj client.Object) error {
	return q.syncer.Sync(ctx, obj)
}

// Enqueue adds a key to the queue. If obj is a key already it gets added directly.
func (q *QueueInformer) Enqueue(item types.NamespacedName) {
	q.logger.WithField("item", item).Trace("enqueuing item")
	q.queue.Add(item)
}

// resourceHandlers provides the default implementation for responding to events
// these simply Log the event and add the object's key to the queue for later processing.
func (q *QueueInformer) resourceHandlers(_ context.Context) *cache.ResourceEventHandlerFuncs {
	return &cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			metaObj, ok := obj.(metav1.Object)
			if !ok {
				panic(fmt.Errorf("unexpected object type in add event: %T", obj))
			}
			q.Enqueue(types.NamespacedName{
				Namespace: metaObj.GetNamespace(),
				Name:      metaObj.GetName(),
			})
		},
		UpdateFunc: func(_, newObj interface{}) {
			metaObj, ok := newObj.(metav1.Object)
			if !ok {
				panic(fmt.Errorf("unexpected object type in update event: %T", newObj))
			}
			q.Enqueue(types.NamespacedName{
				Namespace: metaObj.GetNamespace(),
				Name:      metaObj.GetName(),
			})
		},
		DeleteFunc: func(obj interface{}) {
			q.onDelete(obj)
		},
	}
}

// metricHandlers provides the default implementation for handling metrics in response to events.
func (q *QueueInformer) metricHandlers() *cache.ResourceEventHandlerFuncs {
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
func NewQueueInformer(ctx context.Context, options ...Option) (*QueueInformer, error) {
	// Get default config and apply given options
	config := defaultConfig()
	config.apply(options)
	config.complete()

	return newQueueInformerFromConfig(ctx, config)
}

func newQueueInformerFromConfig(ctx context.Context, config *queueInformerConfig) (*QueueInformer, error) {
	if err := config.validateQueueInformer(); err != nil {
		return nil, err
	}

	// Extract config
	queueInformer := &QueueInformer{
		MetricsProvider: config.provider,
		logger:          config.logger,
		queue:           config.queue,
		indexer:         config.indexer,
		informer:        config.informer,
		syncer:          config.syncer,
		onDelete:        config.onDelete,
	}

	// Register event handlers for resource and metrics
	if queueInformer.informer != nil {
		queueInformer.informer.AddEventHandler(queueInformer.resourceHandlers(ctx))
		queueInformer.informer.AddEventHandler(queueInformer.metricHandlers())
	}

	return queueInformer, nil
}

// LegacySyncHandler is a deprecated signature for syncing resources.
type LegacySyncHandler func(obj interface{}) error

// ToSyncer returns the Syncer equivalent of the sync handler.
func (l LegacySyncHandler) ToSyncer() kubestate.Syncer {
	return kubestate.SyncFunc(func(ctx context.Context, obj client.Object) error {
		return l(obj)
	})
}
