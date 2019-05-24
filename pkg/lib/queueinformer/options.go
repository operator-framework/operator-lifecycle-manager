package queueinformer

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
)

// metaLoggingSyncHandler logs some metadata about obj.
func (q *QueueInformer) metaLoggingSyncHandler(obj interface{}) error {
	m, err := meta.Accessor(obj)
	if err != nil {
		return err
	}

	q.logger.WithFields(logrus.Fields{
		"uid":             m.GetUID(),
		"namespace":       m.GetNamespace(),
		"name":            m.GetName(),
		"resourceversion": m.GetResourceVersion(),
	}).Debug("syncing")

	return nil
}

func (q *QueueInformer) apply(options ...Option) {
	for _, option := range options {
		option(q)
	}
}

// Option applies a configuration to informer.
type Option func(informer *QueueInformer)

// WithSyncHandlers adds handler and additional to the chain of SyncHandlers invoked by the QueueInformer.
func WithSyncHandlers(handler SyncHandler, additional ...SyncHandler) Option {
	return func(q *QueueInformer) {
		q.AddSyncHandlers(handler, additional...)
	}
}

// WithMetricsProvider configures the QueueInformer's MetricsProvider as provider.
func WithMetricsProvider(provider metrics.MetricsProvider) Option {
	return func(informer *QueueInformer) {
		informer.MetricsProvider = provider
	}
}

// WithLogger configures logger as the QueueInformer's Logger.
func WithLogger(logger *logrus.Logger) Option {
	return func(informer *QueueInformer) {
		informer.logger = logger
	}
}
