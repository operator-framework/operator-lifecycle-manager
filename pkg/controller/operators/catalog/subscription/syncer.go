package subscription

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
)

var scheme = runtime.NewScheme()

func init() {
	// Register internal types and conversion funcs
	install.Install(scheme)
}

// subscriptionSyncer syncs Subscriptions by invoking its reconciler chain for each Subscription event it receives.
type subscriptionSyncer struct {
	logger                 *logrus.Logger
	clock                  utilclock.Clock
	reconcilers            kubestate.ReconcilerChain
	subscriptionCache      cache.Indexer
	globalCatalogNamespace string
	notify                 kubestate.NotifyFunc
}

// now returns the Syncer's current time.
func (s *subscriptionSyncer) now() *metav1.Time {
	now := metav1.NewTime(s.clock.Now().UTC())
	return &now
}

// Sync reconciles Subscription events by invoking a sequence of reconcilers, passing the result of each
// successful reconciliation as an argument to its successor.
func (s *subscriptionSyncer) Sync(ctx context.Context, event kubestate.ResourceEvent) error {
	res := &v1alpha1.Subscription{}
	if err := scheme.Convert(event.Resource(), res, nil); err != nil {
		return err
	}

	logger := s.logger.WithFields(logrus.Fields{
		"reconciling": fmt.Sprintf("%T", res),
		"selflink":    res.GetSelfLink(),
		"event":       event.Type(),
	})
	logger.Info("syncing")

	// Enter initial state based on subscription and event type
	// TODO: Consider generalizing initial generic add, update, delete transitions in the kubestate package.
	// 		 Possibly make a resource event aware bridge between Sync and reconciler.
	initial := NewSubscriptionState(res.DeepCopy())
	switch event.Type() {
	case kubestate.ResourceAdded:
		initial = initial.Add()
	case kubestate.ResourceUpdated:
		initial = initial.Update()
	case kubestate.ResourceDeleted:
		initial = initial.Delete()
	}

	reconciled, err := s.reconcilers.Reconcile(ctx, initial)
	if err != nil {
		logger.WithError(err).Warn("an error was encountered during reconciliation")
		return err
	}

	logger.WithFields(logrus.Fields{
		"state": fmt.Sprintf("%T", reconciled),
	}).Debug("reconciliation successful")

	return nil
}

func (s *subscriptionSyncer) Notify(event kubestate.ResourceEvent) {
	s.notify(event)
}

// catalogSubscriptionKeys returns the set of explicit subscription keys, cluster-wide, that are possibly affected by catalogs in the given namespace.
func (s *subscriptionSyncer) catalogSubscriptionKeys(namespace string) ([]string, error) {
	var keys []string
	var err error
	if namespace == s.globalCatalogNamespace {
		keys = s.subscriptionCache.ListKeys()
	} else {
		keys, err = s.subscriptionCache.IndexKeys(cache.NamespaceIndex, namespace)
	}

	return keys, err
}

// catalogNotification notifies dependent subscriptions of the change with the given object.
// The given object is assumed to be a Subscription, Subscription tombstone, or a cache.ExplicitKey.
func (s *subscriptionSyncer) catalogNotification(ctx context.Context, obj interface{}) {
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		s.logger.WithField("resource", obj).Warn("could not unpack key")
		return
	}

	logger := s.logger.WithField("key", k)
	ns, _, err := cache.SplitMetaNamespaceKey(k)
	if err != nil {
		logger.Warn("could not split meta key")
		return
	}

	dependentKeys, err := s.catalogSubscriptionKeys(ns)
	if err != nil {
		logger.Warn("could not retrieve dependent subscriptions")
		return
	}

	logger = logger.WithField("dependents", len(dependentKeys))
	logger.Trace("notifing dependent subscriptions")
	for _, subKey := range dependentKeys {
		logger.Tracef("notifying subscription %s", subKey)
		s.Notify(kubestate.NewResourceEvent(kubestate.ResourceUpdated, subKey))
	}
	logger.Trace("dependent subscriptions notified")
}

// NewSyncer returns a syncer that syncs Subscription resources.
func NewSyncer(ctx context.Context, options ...SyncerOption) (kubestate.Syncer, error) {
	config := defaultSyncerConfig()
	config.apply(options)

	return newSyncerWithConfig(ctx, config)
}

func newSyncerWithConfig(ctx context.Context, config *syncerConfig) (kubestate.Syncer, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}

	s := &subscriptionSyncer{
		logger:            config.logger,
		clock:             config.clock,
		reconcilers:       config.reconcilers,
		subscriptionCache: config.subscriptionInformer.GetIndexer(),
		notify: func(event kubestate.ResourceEvent) {
			// Notify Subscriptions by enqueuing to the Subscription queue.
			config.subscriptionQueue.Add(event)
		},
	}

	// Build a reconciler chain from the default and configured reconcilers
	// Default reconcilers should always come first in the chain
	defaultReconcilers := kubestate.ReconcilerChain{
		&catalogHealthReconciler{
			now:                       s.now,
			client:                    config.client,
			catalogLister:             config.lister.OperatorsV1alpha1().CatalogSourceLister(),
			registryReconcilerFactory: config.registryReconcilerFactory,
			globalCatalogNamespace:    config.globalCatalogNamespace,
		},
	}
	s.reconcilers = append(defaultReconcilers, s.reconcilers...)

	// Add dependency notifications
	config.catalogInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			s.catalogNotification(ctx, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			s.catalogNotification(ctx, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			s.catalogNotification(ctx, obj)
		},
	})

	return s, nil
}
