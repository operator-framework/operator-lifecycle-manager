package queueinformer

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

type queueInformerConfig struct {
	provider metrics.MetricsProvider
	logger   *logrus.Logger
	queue    workqueue.TypedRateLimitingInterface[types.NamespacedName]
	informer cache.SharedIndexInformer
	indexer  cache.Indexer
	syncer   kubestate.Syncer
	onDelete func(interface{})
}

// Option applies an option to the given queue informer config.
type Option func(config *queueInformerConfig)

// apply sequentially applies the given options to the config.
func (c *queueInformerConfig) apply(options []Option) {
	for _, option := range options {
		option(c)
	}
}

func newInvalidConfigError(msg string) error {
	return errors.Errorf("invalid queue informer config: %s", msg)
}

func (c *queueInformerConfig) complete() {
	if c.indexer == nil && c.informer != nil {
		// Extract indexer from informer if
		c.indexer = c.informer.GetIndexer()
	}
}

// validate returns an error if the config isn't valid.
func (c *queueInformerConfig) validateQueueInformer() (err error) {
	switch config := c; {
	case config.provider == nil:
		err = newInvalidConfigError("nil metrics provider")
	case config.logger == nil:
		err = newInvalidConfigError("nil logger")
	case config.queue == nil:
		err = newInvalidConfigError("nil queue")
	case config.indexer == nil:
		err = newInvalidConfigError("nil indexer")
	case config.syncer == nil:
		err = newInvalidConfigError("nil syncer")
	}

	return
}

func defaultConfig() *queueInformerConfig {
	return &queueInformerConfig{
		provider: metrics.NewMetricsNil(),
		onDelete: func(obj interface{}) {},
		queue: workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
			workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
				Name: "default",
			}),
		logger: logrus.New(),
	}
}

// WithMetricsProvider configures the QueueInformer's MetricsProvider as provider.
func WithMetricsProvider(provider metrics.MetricsProvider) Option {
	return func(config *queueInformerConfig) {
		config.provider = provider
	}
}

// WithLogger configures logger as the QueueInformer's Logger.
func WithLogger(logger *logrus.Logger) Option {
	return func(config *queueInformerConfig) {
		config.logger = logger
	}
}

// WithQueue sets the queue used by a QueueInformer.
func WithQueue(queue workqueue.TypedRateLimitingInterface[types.NamespacedName]) Option {
	return func(config *queueInformerConfig) {
		config.queue = queue
	}
}

// WithInformer sets the informer used by a QueueInformer.
func WithInformer(informer cache.SharedIndexInformer) Option {
	return func(config *queueInformerConfig) {
		config.informer = informer
	}
}

// WithIndexer sets the indexer used by a QueueInformer.
func WithIndexer(indexer cache.Indexer) Option {
	return func(config *queueInformerConfig) {
		config.indexer = indexer
	}
}

// WithSyncer sets the syncer invoked by a QueueInformer.
func WithSyncer(syncer kubestate.Syncer) Option {
	return func(config *queueInformerConfig) {
		config.syncer = syncer
	}
}

func WithDeletionHandler(onDelete func(obj interface{})) Option {
	return func(config *queueInformerConfig) {
		config.onDelete = onDelete
	}
}

type operatorConfig struct {
	serverVersion  discovery.ServerVersionInterface
	queueInformers []*QueueInformer
	informers      []cache.SharedIndexInformer
	logger         *logrus.Logger
	numWorkers     int
}

type OperatorOption func(*operatorConfig)

// apply sequentially applies the given options to the config.
func (c *operatorConfig) apply(options []OperatorOption) {
	for _, option := range options {
		option(c)
	}
}

func newInvalidOperatorConfigError(msg string) error {
	return errors.Errorf("invalid queue informer operator config: %s", msg)
}

// WithOperatorLogger sets the logger used by an Operator.
func WithOperatorLogger(logger *logrus.Logger) OperatorOption {
	return func(config *operatorConfig) {
		config.logger = logger
	}
}

// WithQueueInformers registers a set of initial QueueInformers with an Operator.
// If the QueueInformer is configured with a SharedIndexInformer, that SharedIndexInformer
// is registered with the Operator automatically.
func WithQueueInformers(queueInformers ...*QueueInformer) OperatorOption {
	return func(config *operatorConfig) {
		config.queueInformers = queueInformers
	}
}

// WithInformers registers a set of initial Informers with an Operator.
func WithInformers(informers ...cache.SharedIndexInformer) OperatorOption {
	return func(config *operatorConfig) {
		config.informers = informers
	}
}

// WithNumWorkers sets the number of workers an Operator uses to process each queue.
// It translates directly to the number of queue items processed in parallel for a given queue.
// Specifying zero or less workers is an invariant and will cause an error upon configuration.
// Specifying one worker indicates that each queue will only have one item processed at a time.
func WithNumWorkers(numWorkers int) OperatorOption {
	return func(config *operatorConfig) {
		config.numWorkers = numWorkers
	}
}

// validate returns an error if the config isn't valid.
func (c *operatorConfig) validate() (err error) {
	switch config := c; {
	case config.serverVersion == nil:
		err = newInvalidOperatorConfigError("discovery client nil")
	case config.numWorkers < 1:
		err = newInvalidOperatorConfigError("must specify at least one worker per queue")
	}

	return
}

func defaultOperatorConfig() *operatorConfig {
	return &operatorConfig{
		logger:     logrus.New(),
		numWorkers: 2,
	}
}
