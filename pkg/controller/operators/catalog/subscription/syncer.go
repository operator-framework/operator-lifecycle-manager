package subscription

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	utilclock "k8s.io/utils/clock"

	"github.com/operator-framework/api/pkg/operators/install"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
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
	installPlanLister      listers.InstallPlanLister
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
func (s *subscriptionSyncer) Sync(ctx context.Context, obj client.Object) error {
	res := &v1alpha1.Subscription{}
	if err := scheme.Convert(obj, res, nil); err != nil {
		return err
	}

	metrics.EmitSubMetric(res)

	logger := s.logger.WithFields(logrus.Fields{
		"reconciling": fmt.Sprintf("%T", res),
		"selflink":    res.GetSelfLink(),
	})
	logger.Info("syncing")

	// Enter initial state based on subscription and event type
	// TODO: Consider generalizing initial generic add, update, delete transitions in the kubestate package.
	// 		 Possibly make a resource event aware bridge between Sync and reconciler.
	initial := NewSubscriptionState(res.DeepCopy()).Update()

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

func (s *subscriptionSyncer) Notify(event types.NamespacedName) {
	s.notify(event)
}

// catalogSubscriptionKeys returns the set of explicit subscription keys, cluster-wide, that are possibly affected by catalogs in the given namespace.
func (s *subscriptionSyncer) catalogSubscriptionKeys(namespace string) ([]types.NamespacedName, error) {
	var cacheKeys []string
	var err error
	if namespace == s.globalCatalogNamespace {
		cacheKeys = s.subscriptionCache.ListKeys()
	} else {
		cacheKeys, err = s.subscriptionCache.IndexKeys(cache.NamespaceIndex, namespace)
	}

	keys := make([]types.NamespacedName, 0, len(cacheKeys))
	for _, k := range cacheKeys {
		ns, name, err := cache.SplitMetaNamespaceKey(k)
		if err != nil {
			s.logger.Warnf("could not split meta key %q", k)
			continue
		}
		keys = append(keys, types.NamespacedName{Namespace: ns, Name: name})
	}

	return keys, err
}

// notifyOnCatalog notifies dependent subscriptions of the change with the given object.
// The given object is assumed to be a CatalogSource, CatalogSource tombstone, or a cache.ExplicitKey.
func (s *subscriptionSyncer) notifyOnCatalog(ctx context.Context, obj interface{}) {
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
		s.Notify(subKey)
	}
	logger.Trace("dependent subscriptions notified")
}

// notifyOnInstallPlan notifies dependent subscriptions of the change with the given object.
// The given object is assumed to be an InstallPlan, InstallPlan tombstone, or a cache.ExplicitKey.
func (s *subscriptionSyncer) notifyOnInstallPlan(ctx context.Context, obj interface{}) {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		s.logger.WithField("obj", fmt.Sprintf("%v", obj)).Trace("could not cast as installplan directly while notifying subscription syncer")
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			if plan, ok = tombstone.Obj.(*v1alpha1.InstallPlan); !ok {
				s.logger.WithField("tombstone", tombstone).Warn("could not cast as installplan")
				return
			}
		} else {
			k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
			if err != nil {
				s.logger.WithField("resource", obj).Warn("could not unpack key")
				return
			}
			logger := s.logger.WithField("key", k)

			ns, name, err := cache.SplitMetaNamespaceKey(k)
			if err != nil {
				logger.Warn("could not split meta key")
				return
			}

			if plan, err = s.installPlanLister.InstallPlans(ns).Get(name); err != nil {
				logger.WithError(err).Warn("could not get installplan")
				return
			}
		}
	}

	logger := s.logger.WithFields(logrus.Fields{
		"namespace":   plan.GetNamespace(),
		"installplan": plan.GetName(),
	})

	// Notify dependent owner Subscriptions
	owners := ownerutil.GetOwnersByKind(plan, v1alpha1.SubscriptionKind)
	for _, owner := range owners {
		subKey := types.NamespacedName{Namespace: plan.GetNamespace(), Name: owner.Name}
		logger.Tracef("notifying subscription %s", subKey)
		s.Notify(subKey)
	}
}

func eventHandlers(ctx context.Context, notify func(ctx context.Context, obj interface{})) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			notify(ctx, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			notify(ctx, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			notify(ctx, obj)
		},
	}
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
		installPlanLister: config.lister.OperatorsV1alpha1().InstallPlanLister(),
		notify: func(event types.NamespacedName) {
			// Notify Subscriptions by enqueuing to the Subscription queue.
			config.subscriptionQueue.Add(event)
		},
	}

	// Add metrics handler to subscription informer
	// NOTE: This is different from how metrics are handled for other resources (install plan, catalog source, etc.)
	// which use metrics provider and through the QueueInformer
	config.subscriptionInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if sub, ok := newObj.(*v1alpha1.Subscription); ok {
				metrics.UpdateSubsSyncCounterStorage(sub)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if sub, ok := obj.(*v1alpha1.Subscription); ok {
				metrics.DeleteSubsMetric(sub)
			}
		},
	})

	// Build a reconciler chain from the default and configured reconcilers
	// Default reconcilers should always come first in the chain
	defaultReconcilers := kubestate.ReconcilerChain{
		&installPlanReconciler{
			now:               s.now,
			client:            config.client,
			installPlanLister: config.lister.OperatorsV1alpha1().InstallPlanLister(),
		},
		&catalogHealthReconciler{
			now:                       s.now,
			client:                    config.client,
			catalogLister:             config.lister.OperatorsV1alpha1().CatalogSourceLister(),
			registryReconcilerFactory: config.registryReconcilerFactory,
			globalCatalogNamespace:    config.globalCatalogNamespace,
			operatorCacheProvider:     config.operatorCacheProvider,
			logger:                    config.logger,
		},
	}
	s.reconcilers = append(defaultReconcilers, s.reconcilers...)

	// Add dependency notifications
	config.installPlanInformer.AddEventHandler(eventHandlers(ctx, s.notifyOnInstallPlan))
	config.catalogInformer.AddEventHandler(eventHandlers(ctx, s.notifyOnCatalog))

	return s, nil
}
