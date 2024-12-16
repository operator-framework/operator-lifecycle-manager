package subscription

import (
	"context"
	"fmt"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/client-go/util/workqueue"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	utilclock "k8s.io/utils/clock"

	"github.com/operator-framework/api/pkg/operators/install"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	resolverCache "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
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
	client                 versioned.Interface
	clock                  utilclock.Clock
	reconcilers            ReconcilerChain
	subscriptionCache      cache.Indexer
	subscriptionLister     listers.SubscriptionLister
	installPlanLister      listers.InstallPlanLister
	globalCatalogNamespace string
	notify                 kubestate.NotifyFunc
	sourceProvider         resolverCache.SourceProvider
	nsResolveQueue         workqueue.TypedRateLimitingInterface[any]
}

// now returns the Syncer's current time.
func (s *subscriptionSyncer) now() *metav1.Time {
	now := metav1.NewTime(s.clock.Now().UTC())
	return &now
}

// Sync reconciles Subscription events by invoking a sequence of reconcilers, passing the result of each
// successful reconciliation as an argument to its successor.
func (s *subscriptionSyncer) Sync(ctx context.Context, event kubestate.ResourceEvent) error {
	sub, ok := event.Resource().(*v1alpha1.Subscription)
	if !ok {
		tombstone, ok := event.Resource().(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", event.Resource()))
			return nil
		}

		sub, ok = tombstone.Obj.(*v1alpha1.Subscription)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a metav1 object %#v", event.Resource()))
			return nil
		}
	}

	logger := s.logger.WithFields(logrus.Fields{
		"reconciling": fmt.Sprintf("%T", event),
		"selflink":    sub.GetSelfLink(),
		"event":       event.Type(),
	})
	logger.Info("syncing")

	// Enter initial state based on subscription and event type
	// TODO: Consider generalizing initial generic add, update, delete transitions in the kubestate package.
	// 		 Possibly make a resource event aware bridge between Sync and reconciler.
	if event.Type() == kubestate.ResourceDeleted {
		metrics.DeleteSubsMetric(sub)
		return nil
	}

	res, err := s.subscriptionLister.Subscriptions(sub.GetNamespace()).Get(sub.GetName())
	if err != nil {
		return err
	}

	metrics.EmitSubMetric(res)
	metrics.UpdateSubsSyncCounterStorage(res)

	reconciled, err := s.reconcilers.Reconcile(ctx, res.DeepCopy())
	if err != nil {
		logger.WithError(err).Warn("an error was encountered during reconciliation")
		return err
	}

	if !equality.Semantic.DeepEqual(res.Status, reconciled.Status) {
		if _, err := s.client.OperatorsV1alpha1().Subscriptions(reconciled.GetNamespace()).UpdateStatus(ctx, reconciled, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}

	s.nsResolveQueue.Add(res.GetNamespace())

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
		s.Notify(kubestate.NewResourceEvent(kubestate.ResourceUpdated, subKey))
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
		subKey := fmt.Sprintf("%s/%s", plan.GetNamespace(), owner.Name)
		logger.Tracef("notifying subscription %s", subKey)
		s.Notify(kubestate.NewResourceEvent(kubestate.ResourceUpdated, cache.ExplicitKey(subKey)))
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
		logger:             config.logger,
		client:             config.client,
		clock:              config.clock,
		reconcilers:        config.reconcilers,
		subscriptionCache:  config.subscriptionInformer.GetIndexer(),
		installPlanLister:  config.lister.OperatorsV1alpha1().InstallPlanLister(),
		subscriptionLister: config.lister.OperatorsV1alpha1().SubscriptionLister(),
		sourceProvider:     config.sourceProvider,
		nsResolveQueue:     config.nsResolveQueue,
		notify: func(event kubestate.ResourceEvent) {
			// Notify Subscriptions by enqueuing to the Subscription queue.
			config.subscriptionQueue.Add(event)
		},
	}

	// Build a reconciler chain from the default and configured reconcilers
	// Default reconcilers should always come first in the chain
	defaultReconcilers := ReconcilerChain{
		&installPlanReconciler{
			now:               s.now,
			installPlanLister: config.lister.OperatorsV1alpha1().InstallPlanLister(),
		},
		&catalogHealthReconciler{
			now:                       s.now,
			catalogLister:             config.lister.OperatorsV1alpha1().CatalogSourceLister(),
			registryReconcilerFactory: config.registryReconcilerFactory,
			globalCatalogNamespace:    config.globalCatalogNamespace,
			sourceProvider:            config.sourceProvider,
		},
	}
	s.reconcilers = append(defaultReconcilers, s.reconcilers...)

	// Add dependency notifications
	config.installPlanInformer.AddEventHandler(eventHandlers(ctx, s.notifyOnInstallPlan))
	config.catalogInformer.AddEventHandler(eventHandlers(ctx, s.notifyOnCatalog))

	return s, nil
}
