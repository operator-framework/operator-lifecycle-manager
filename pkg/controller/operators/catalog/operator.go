package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/operator-framework/operator-registry/pkg/api/grpc_health_v1"
	registryclient "github.com/operator-framework/operator-registry/pkg/client"
	errorwrap "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/connectivity"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	opcache "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

const (
	crdKind                = "CustomResourceDefinition"
	secretKind             = "Secret"
	clusterRoleKind        = "ClusterRole"
	clusterRoleBindingKind = "ClusterRoleBinding"
	serviceAccountKind     = "ServiceAccount"
	serviceKind            = "Service"
	roleKind               = "Role"
	roleBindingKind        = "RoleBinding"
	generatedByKey         = "olm.generated-by"
)

// for test stubbing and for ensuring standardization of timezones to UTC
var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

// Operator represents a Kubernetes operator that executes InstallPlans by
// resolving dependencies in a catalog.
type Operator struct {
	*queueinformer.Operator
	client                versioned.Interface
	lister                operatorlister.OperatorLister
	namespace             string
	watchedNamespaces     []string
	sources               map[resolver.CatalogKey]resolver.SourceRef
	sourcesLock           sync.RWMutex
	sourcesLastUpdate     metav1.Time
	resolver              resolver.Resolver
	catsrcQueueSet        *queueinformer.ResourceQueueSet
	subQueueSet           *queueinformer.ResourceQueueSet
	namespaceResolveQueue workqueue.RateLimitingInterface
	reconciler            reconciler.RegistryReconcilerFactory
	viewIndexerSet        *opcache.ViewIndexerSet
	subIndexerSet         *opcache.IndexerSet
}

// NewOperator returns a configured, unstarted Operator.
func NewOperator(kubeconfigPath string, logger *logrus.Logger, wakeupInterval time.Duration, configmapRegistryImage, operatorNamespace string, watchedNamespaces ...string) (*Operator, error) {
	// Default to watching all namespaces.
	if watchedNamespaces == nil {
		watchedNamespaces = []string{metav1.NamespaceAll}
	}

	// Create a new client for OLM types (CRs)
	crClient, err := client.NewClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create a new queueinformer-based operator.
	queueOperator, err := queueinformer.NewOperator(kubeconfigPath, logger)
	if err != nil {
		return nil, err
	}

	lister := operatorlister.NewLister()
	op := &Operator{
		Operator:          queueOperator,
		catsrcQueueSet:    queueinformer.NewEmptyResourceQueueSet(),
		subQueueSet:       queueinformer.NewEmptyResourceQueueSet(),
		client:            crClient,
		lister:            lister,
		namespace:         operatorNamespace,
		watchedNamespaces: watchedNamespaces,
		sources:           make(map[resolver.CatalogKey]resolver.SourceRef),
		resolver:          resolver.NewOperatorsV1alpha1Resolver(lister),
		reconciler:        reconciler.NewRegistryReconcilerFactory(lister, queueOperator.OpClient, configmapRegistryImage),
		viewIndexerSet:    opcache.NewViewIndexerSet(),
		subIndexerSet:     opcache.NewIndexerSet(),
	}

	// Setup global components
	namespaceInformer := informers.NewSharedInformerFactory(op.OpClient.KubernetesInterface(), wakeupInterval).Core().V1().Namespaces()
	resolvingNamespaceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resolver")
	namespaceQueueInformer := queueinformer.NewInformer(
		resolvingNamespaceQueue,
		namespaceInformer.Informer(),
		// Namespace sync for resolving subscriptions
		op.syncResolvingNamespace,
		nil,
		"resolver",
		metrics.NewMetricsNil(),
		logger,
	)

	op.RegisterQueueInformer(namespaceQueueInformer)
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())
	op.namespaceResolveQueue = resolvingNamespaceQueue

	// Setup namespaced components
	for _, namespace := range watchedNamespaces {
		viewIndexer := opcache.NewViewIndexer()
		op.viewIndexerSet.Set(namespace, viewIndexer)

		// Create CR informers
		crInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		catsrcInformer := crInformerFactory.Operators().V1alpha1().CatalogSources()
		subInformer := crInformerFactory.Operators().V1alpha1().Subscriptions()
		ipInformer := crInformerFactory.Operators().V1alpha1().InstallPlans()
		csvInformer := crInformerFactory.Operators().V1alpha1().ClusterServiceVersions()

		// Create k8s informers
		k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(op.OpClient.KubernetesInterface(), wakeupInterval, informers.WithNamespace(namespace))
		roleInformer := k8sInformerFactory.Rbac().V1().Roles()
		roleBindingInformer := k8sInformerFactory.Rbac().V1().RoleBindings()
		serviceAccountInformer := k8sInformerFactory.Core().V1().ServiceAccounts()
		serviceInformer := k8sInformerFactory.Core().V1().Services()
		podInformer := k8sInformerFactory.Core().V1().Pods()
		configMapInformer := k8sInformerFactory.Core().V1().ConfigMaps()

		// Register CR listers
		lister.OperatorsV1alpha1().RegisterCatalogSourceLister(namespace, catsrcInformer.Lister())
		lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, subInformer.Lister())
		lister.OperatorsV1alpha1().RegisterInstallPlanLister(namespace, ipInformer.Lister())
		lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, csvInformer.Lister())

		// Register k8s listers
		op.lister.RbacV1().RegisterRoleLister(namespace, roleInformer.Lister())
		op.lister.RbacV1().RegisterRoleBindingLister(namespace, roleBindingInformer.Lister())
		op.lister.CoreV1().RegisterServiceAccountLister(namespace, serviceAccountInformer.Lister())
		op.lister.CoreV1().RegisterServiceLister(namespace, serviceInformer.Lister())
		op.lister.CoreV1().RegisterPodLister(namespace, podInformer.Lister())
		op.lister.CoreV1().RegisterConfigMapLister(namespace, configMapInformer.Lister())

		// Create all queues
		catsrcQueueName := "catalogsources"
		subQueueName := "subscriptions"
		subCatQueueName := "subcat"
		ipQueueName := "installplans"
		catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), catsrcQueueName)
		subQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), subQueueName)
		subCatQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), subCatQueueName)
		ipQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), ipQueueName)
		roleQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "role")
		roleBindingQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "rolebinding")
		serviceAccountQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "serviceaccount")
		serviceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "service")
		podQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "pod")
		configmapQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "configmap")

		// Create queue sets
		op.catsrcQueueSet.Set(namespace, catsrcQueue)
		op.subQueueSet.Set(namespace, subQueue)

		// Set up viewers
		mv := newMetaViewer(op, withNamespace(namespace))
		scv := newSubCatViewer(mv)
		scvKey := opcache.NewViewerKey(subCatIndexKey, new(v1alpha1.CatalogSource), new(v1alpha1.SubscriptionCatalogStatus))
		viewIndexer.AddViewer(scvKey, scv)

		// Set up indexing and index enqueuing
		if err := viewIndexer.AddIndex(subCatIndexKey, opcache.BuildEnqueuingIndex(scv.subCatViewIndex, subCatQueue)); err != nil {
			logrus.WithError(err).Warn("failed to setup subscription catalog status view indexer")
			return nil, err
		}

		// TODO: Create and register queue indexers
		subIndexers := map[string]cache.Indexer{namespace: subInformer.Informer().GetIndexer()}
		op.subIndexerSet.Set(namespace, subInformer.Informer().GetIndexer())
		subCatQueueIndexerName := "subcat-" + namespace
		subCatQueueIndexer := queueinformer.NewQueueIndexer(subCatQueue, subIndexers, scv.setSubCatStatus, subCatQueueIndexerName, logger, metrics.NewMetricsNil())
		op.RegisterQueueIndexer(subCatQueueIndexer)

		// Define extra event handlers
		handleCatsrcDelete := &cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleCatSrcDeletion,
		}
		handleDelete := &cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleDeletion,
		}

		// Create and register queue informers
		queueInformers := []*queueinformer.QueueInformer{
			// CR informers
			queueinformer.NewInformer(
				catsrcQueue,
				catsrcInformer.Informer(),
				op.syncCatalogSources,
				handleCatsrcDelete,
				catsrcQueueName,
				metrics.NewMetricsCatalogSource(op.client),
				logger,
			).WithViewIndexer(viewIndexer),
			queueinformer.NewInformer(
				ipQueue,
				ipInformer.Informer(),
				op.syncInstallPlans,
				nil,
				ipQueueName,
				metrics.NewMetricsInstallPlan(op.client),
				logger,
			),
			queueinformer.NewInformer(
				subQueue,
				subInformer.Informer(),
				op.syncSubscriptions,
				nil,
				subQueueName,
				metrics.NewMetricsSubscription(op.client),
				logger,
			).AdditionallyDistribute(subCatQueue),

			// k8s informers
			queueinformer.NewInformer(roleQueue, roleInformer.Informer(), op.syncObject, handleDelete, "role", metrics.NewMetricsNil(), logger),
			queueinformer.NewInformer(roleBindingQueue, roleBindingInformer.Informer(), op.syncObject, handleDelete, "rolebinding", metrics.NewMetricsNil(), logger),
			queueinformer.NewInformer(serviceAccountQueue, serviceAccountInformer.Informer(), op.syncObject, handleDelete, "serviceaccount", metrics.NewMetricsNil(), logger),
			queueinformer.NewInformer(serviceQueue, serviceInformer.Informer(), op.syncObject, handleDelete, "service", metrics.NewMetricsNil(), logger),
			queueinformer.NewInformer(podQueue, podInformer.Informer(), op.syncObject, handleDelete, "pod", metrics.NewMetricsNil(), logger),
			queueinformer.NewInformer(configmapQueue, configMapInformer.Informer(), op.syncObject, handleDelete, "configmap", metrics.NewMetricsNil(), logger),
		}
		for _, q := range queueInformers {
			op.RegisterQueueInformer(q)
		}

	}

	// // Register the QueueInformer for SubscriptionStatus.CatalogStatus
	// subCatsrcStatusQueueName := fmt.Sprintf("%s/subscription/status", catsrcQueueName)
	// subCatsrcStatusQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), subCatsrcStatusQueueName)
	// catsrcQueueInformers = append(catsrcQueueInformers, queueinformer.New(
	// 	subCatsrcStatusQueue,
	// 	catsrcSharedIndexInformers,
	// 	op.syncSubscriptionCatalogStatus,
	// 	nil,
	// 	subCatsrcStatusQueueName,
	// 	metrics.NewMetricsCatalogSource(op.client),
	// 	logger,
	// )...)

	// TODO: add subscription queueindexer for updating subscription status

	// Register the QueueInformer for SubscriptionStatus.CatalogStatus
	// subCleanupQueueName := fmt.Sprintf("%s/catalogstatus/cleanup", subQueueName)
	// subCleanupQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), subCleanupQueueName)
	// subQueueInformers = append(subQueueInformers, queueinformer.New(
	// 	subCleanupQueue,
	// 	subSharedIndexInformers,
	// 	op.cleanupSubscriptionCatalogStatus,
	// 	nil,
	// 	subCleanupQueueName,
	// 	metrics.NewMetricsSubscription(op.client),
	// 	logger,
	// )...)

	return op, nil
}

func (o *Operator) syncObject(obj interface{}) (syncError error) {
	// Assert as runtime.Object
	runtimeObj, ok := obj.(runtime.Object)
	if !ok {
		syncError = errors.New("object sync: casting to runtime.Object failed")
		o.Log.Warn(syncError.Error())
		return
	}

	gvk := runtimeObj.GetObjectKind().GroupVersionKind()
	logger := o.Log.WithFields(logrus.Fields{
		"group":   gvk.Group,
		"version": gvk.Version,
		"kind":    gvk.Kind,
	})

	// Assert as metav1.Object
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		syncError = errors.New("object sync: casting to metav1.Object failed")
		logger.Warn(syncError.Error())
		return
	}
	logger = logger.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
	})

	if owner := ownerutil.GetOwnerByKind(metaObj, v1alpha1.CatalogSourceKind); owner != nil {
		sourceKey := resolver.CatalogKey{Name: owner.Name, Namespace: metaObj.GetNamespace()}
		func() {
			o.sourcesLock.RLock()
			defer o.sourcesLock.RUnlock()
			if _, ok := o.sources[sourceKey]; ok {
				logger.Debug("requeueing owner CatalogSource")
				if err := o.catsrcQueueSet.Requeue(owner.Name, metaObj.GetNamespace()); err != nil {
					logger.Warn(err.Error())
				}
			}
		}()
	}

	return nil
}

func (o *Operator) handleDeletion(obj interface{}) {
	ownee, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}

		ownee, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a metav1 object %#v", obj))
			return
		}
	}

	logger := o.Log.WithFields(logrus.Fields{
		"sync":      "resourcedeletion",
		"name":      ownee.GetName(),
		"namespace": ownee.GetNamespace(),
	})

	owner := ownerutil.GetOwnerByKind(ownee, v1alpha1.CatalogSourceKind)
	if owner == nil {
		logger.Debug("no owner catalogsource found")
		return
	}

	logger = logger.WithFields(logrus.Fields{
		"owner":     owner.Name,
		"ownerkind": owner.Kind,
	})

	// Get the owner CatalogSource
	catsrc, err := o.lister.OperatorsV1alpha1().CatalogSourceLister().CatalogSources(ownee.GetNamespace()).Get(owner.Name)
	if err != nil {
		logger.WithError(err).Warn("could not get owner catalogsource from cache")
		return
	}

	// Requeue CatalogSource
	if err := o.catsrcQueueSet.Requeue(catsrc.GetName(), catsrc.GetNamespace()); err != nil {
		logger.WithError(err).Warn("error requeuing owner catalogsource")
	}
}

func (o *Operator) handleCatSrcDeletion(obj interface{}) {
	catsrc, ok := obj.(metav1.Object)
	if !ok {
		if !ok {
			tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
			if !ok {
				utilruntime.HandleError(fmt.Errorf("Couldn't get object from tombstone %#v", obj))
				return
			}

			catsrc, ok = tombstone.Obj.(metav1.Object)
			if !ok {
				utilruntime.HandleError(fmt.Errorf("Tombstone contained object that is not a Namespace %#v", obj))
				return
			}
		}
	}
	sourceKey := resolver.CatalogKey{Name: catsrc.GetName(), Namespace: catsrc.GetNamespace()}
	func() {
		o.sourcesLock.Lock()
		defer o.sourcesLock.Unlock()
		delete(o.sources, sourceKey)
	}()
	o.Log.WithField("source", sourceKey).Info("removed client for deleted catalogsource")

	if err := o.catsrcQueueSet.Remove(sourceKey.Name, sourceKey.Namespace); err != nil {
		o.Log.WithError(err)
	}
}

func (o *Operator) syncCatalogSources(obj interface{}) (syncError error) {
	catsrc, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting CatalogSource failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"source": catsrc.GetName(),
		"id":     queueinformer.NewLoopID(),
	})
	logger.Debug("syncing catsrc")
	out := catsrc.DeepCopy()
	sourceKey := resolver.CatalogKey{Name: catsrc.GetName(), Namespace: catsrc.GetNamespace()}

	if catsrc.Spec.SourceType == v1alpha1.SourceTypeInternal || catsrc.Spec.SourceType == v1alpha1.SourceTypeConfigmap {
		logger.Debug("checking catsrc configmap state")

		// Get the catalog source's config map
		configMap, err := o.lister.CoreV1().ConfigMapLister().ConfigMaps(catsrc.GetNamespace()).Get(catsrc.Spec.ConfigMap)
		if err != nil {
			return fmt.Errorf("failed to get catalog config map %s: %s", catsrc.Spec.ConfigMap, err)
		}

		if wasOwned := ownerutil.EnsureOwner(configMap, catsrc); !wasOwned {
			configMap, err = o.OpClient.KubernetesInterface().CoreV1().ConfigMaps(configMap.GetNamespace()).Update(configMap)
			if err != nil {
				return fmt.Errorf("unable to write owner onto catalog source configmap")
			}
			logger.Debug("adopted configmap")
		}

		if catsrc.Status.ConfigMapResource == nil || catsrc.Status.ConfigMapResource.UID != configMap.GetUID() || catsrc.Status.ConfigMapResource.ResourceVersion != configMap.GetResourceVersion() {
			logger.Debug("updating catsrc configmap state")
			// configmap ref nonexistent or updated, write out the new configmap ref to status and exit
			out.Status.ConfigMapResource = &v1alpha1.ConfigMapResourceReference{
				Name:            configMap.GetName(),
				Namespace:       configMap.GetNamespace(),
				UID:             configMap.GetUID(),
				ResourceVersion: configMap.GetResourceVersion(),
			}
			out.Status.LastSync = timeNow()
			if _, err = o.client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(out); err != nil {
				return err
			}

			o.sourcesLastUpdate = timeNow()

			return nil
		}

		logger.Debug("catsrc configmap state good, checking registry pod")
	}

	srcReconciler := o.reconciler.ReconcilerForSource(catsrc)
	if srcReconciler == nil {
		// TODO: Add failure status on catalogsource and remove from sources
		return fmt.Errorf("no reconciler for source type %s", catsrc.Spec.SourceType)
	}

	healthy, err := srcReconciler.CheckRegistryServer(catsrc)
	if err != nil {
		return err
	}

	// If registry pod hasn't been created or hasn't been updated since the last configmap update, recreate it
	if !healthy || catsrc.Status.RegistryServiceStatus == nil || catsrc.Status.RegistryServiceStatus.CreatedAt.Before(&catsrc.Status.LastSync) {
		logger.Debug("ensuring registry server")

		if err := srcReconciler.EnsureRegistryServer(out); err != nil {
			logger.WithError(err).Warn("couldn't ensure registry server")
			return err
		}
		logger.Debug("ensured registry server")

		out.Status.RegistryServiceStatus.CreatedAt = timeNow()
		out.Status.LastSync = out.Status.RegistryServiceStatus.CreatedAt

		logger.Debug("updating catsrc status")
		// update status
		if _, err := o.client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(out); err != nil {
			return err
		}

		o.sourcesLastUpdate = timeNow()
		logger.Debug("registry server recreated")

		func() {
			o.sourcesLock.Lock()
			defer o.sourcesLock.Unlock()
			delete(o.sources, sourceKey)
		}()

		return nil
	}
	logger.Debug("registry state good")

	// update operator's view of sources
	sourcesUpdated := false
	func() {
		o.sourcesLock.Lock()
		defer o.sourcesLock.Unlock()
		address := catsrc.Address()
		currentSource, ok := o.sources[sourceKey]
		logger = logger.WithField("currentSource", sourceKey)
		if !ok || currentSource.Address != address || catsrc.Status.LastSync.After(currentSource.LastConnect.Time) {
			logger.Info("building connection to registry")
			client, err := registryclient.NewClient(address)
			if err != nil {
				logger.WithError(err).Warn("couldn't connect to registry")
			}
			sourceRef := resolver.SourceRef{
				Address:     address,
				Client:      client,
				LastConnect: timeNow(),
				LastHealthy: metav1.Time{}, // haven't detected healthy yet
			}
			o.sources[sourceKey] = sourceRef
			currentSource = sourceRef
			sourcesUpdated = true
		}
		if currentSource.LastHealthy.IsZero() {
			logger.Info("client hasn't yet become healthy, attempt a health check")
			client, ok := currentSource.Client.(*registryclient.Client)
			if !ok {
				logger.WithField("client", currentSource.Client).Warn("unexpected client")
				return
			}
			res, err := client.Health.Check(context.TODO(), &grpc_health_v1.HealthCheckRequest{Service: "Registry"})
			if err != nil {
				logger.WithError(err).Debug("error checking health")
				if client.Conn.GetState() == connectivity.TransientFailure {
					logger.Debug("wait for state to change")
					ctx, _ := context.WithTimeout(context.TODO(), 1*time.Second)
					if !client.Conn.WaitForStateChange(ctx, connectivity.TransientFailure) {
						logger.Debug("state didn't change, trigger reconnect. this may happen when cached dns is wrong.")
						delete(o.sources, sourceKey)
						if err := o.catsrcQueueSet.Requeue(sourceKey.Name, sourceKey.Namespace); err != nil {
							logger.WithError(err).Debug("error requeueing")
						}
						return
					}
				}
				return
			}
			if res.Status != grpc_health_v1.HealthCheckResponse_SERVING {
				logger.WithField("status", res.Status.String()).Debug("source not healthy")
				return
			}
			currentSource.LastHealthy = timeNow()
			o.sources[sourceKey] = currentSource
			sourcesUpdated = true
		}
	}()

	if !sourcesUpdated {
		return nil
	}

	// record that we've done work here onto the status
	out.Status.LastSync = timeNow()
	if _, err := o.client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(out); err != nil {
		return err
	}

	// Trigger a resolve, will pick up any subscriptions that depend on the catalog
	o.resolveNamespace(out.GetNamespace())

	return nil
}

func (o *Operator) syncDependentSubscriptions(logger *logrus.Entry, catalogSource, catalogSourceNamespace string) {
	subs, err := o.lister.OperatorsV1alpha1().SubscriptionLister().List(labels.Everything())
	if err != nil {
		logger.Warnf("could not list Subscriptions")
		return
	}

	for _, sub := range subs {
		logger = logger.WithFields(logrus.Fields{
			"subscriptionCatalogSource":    sub.Spec.CatalogSource,
			"subscriptionCatalogNamespace": sub.Spec.CatalogSourceNamespace,
			"subscription":                 sub.GetName(),
		})
		catalogNamespace := sub.Spec.CatalogSourceNamespace
		if catalogNamespace == "" {
			catalogNamespace = o.namespace
		}
		if sub.Spec.CatalogSource == catalogSource && catalogNamespace == catalogSourceNamespace {
			logger.Debug("requeueing subscription because catalog changed")
			if err := o.subQueueSet.RequeueRateLimited(sub.GetName(), sub.GetNamespace()); err != nil {
				logger.WithError(err).Warn("failed to requeue subscription")
			}
		}
	}
}

func (o *Operator) syncResolvingNamespace(obj interface{}) error {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Namespace failed")
	}
	namespace := ns.GetName()

	logger := o.Log.WithFields(logrus.Fields{
		"namespace": namespace,
		"id":        queueinformer.NewLoopID(),
	})

	// get the set of sources that should be used for resolution and best-effort get their connections working
	logger.Debug("resolving sources")
	resolverSources := o.ensureResolverSources(logger, namespace)
	querier := resolver.NewNamespaceSourceQuerier(resolverSources)

	logger.Debug("checking if subscriptions need update")

	subs, err := o.lister.OperatorsV1alpha1().SubscriptionLister().Subscriptions(namespace).List(labels.Everything())
	if err != nil {
		logger.WithError(err).Debug("couldn't list subscriptions")
		return err
	}

	// TODO: parallel
	subscriptionUpdated := false
	for _, sub := range subs {
		logger := logger.WithFields(logrus.Fields{
			"sub":     sub.GetName(),
			"source":  sub.Spec.CatalogSource,
			"pkg":     sub.Spec.Package,
			"channel": sub.Spec.Channel,
		})

		// ensure the installplan reference is correct
		sub, changedIP, err := o.ensureSubscriptionInstallPlanState(logger, sub)
		if err != nil {
			return err
		}
		subscriptionUpdated = subscriptionUpdated || changedIP

		// record the current state of the desired corresponding CSV in the status. no-op if we don't know the csv yet.
		sub, changedCSV, err := o.ensureSubscriptionCSVState(logger, sub, querier)
		if err != nil {
			return err
		}

		subscriptionUpdated = subscriptionUpdated || changedCSV
	}
	if subscriptionUpdated {
		logger.Debug("subscriptions were updated, wait for a new resolution")
		return nil
	}

	shouldUpdate := false
	for _, sub := range subs {
		shouldUpdate = shouldUpdate || !o.nothingToUpdate(logger, sub)
	}
	if !shouldUpdate {
		logger.Debug("all subscriptions up to date")
		return nil
	}

	logger.Debug("resolving subscriptions in namespace")

	// resolve a set of steps to apply to a cluster, a set of subscriptions to create/update, and any errors
	steps, updatedSubs, err := o.resolver.ResolveSteps(namespace, querier)
	if err != nil {
		return err
	}

	// create installplan if anything updated
	if len(updatedSubs) > 0 {
		logger.Debug("resolution caused subscription changes, creating installplan")
		// any subscription in the namespace with manual approval will force generated installplans to be manual
		// TODO: this is an odd artifact of the older resolver, and will probably confuse users. approval mode could be on the operatorgroup?
		installPlanApproval := v1alpha1.ApprovalAutomatic
		for _, sub := range subs {
			if sub.Spec.InstallPlanApproval == v1alpha1.ApprovalManual {
				installPlanApproval = v1alpha1.ApprovalManual
				break
			}
		}

		installPlanReference, err := o.ensureInstallPlan(logger, namespace, subs, installPlanApproval, steps)
		if err != nil {
			logger.WithError(err).Debug("error ensuring installplan")
			return err
		}
		if err := o.updateSubscriptionStatus(namespace, updatedSubs, installPlanReference); err != nil {
			logger.WithError(err).Debug("error ensuring subscription installplan state")
			return err
		}
		return nil
	}

	return nil
}

func (o *Operator) syncSubscriptions(obj interface{}) error {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Subscription failed")
	}

	o.resolveNamespace(sub.GetNamespace())

	return nil
}

func (o *Operator) resolveNamespace(namespace string) {
	o.namespaceResolveQueue.AddRateLimited(namespace)
}

func (o *Operator) ensureResolverSources(logger *logrus.Entry, namespace string) map[resolver.CatalogKey]registryclient.Interface {
	// TODO: record connection status onto an object
	resolverSources := make(map[resolver.CatalogKey]registryclient.Interface, 0)
	func() {
		o.sourcesLock.RLock()
		defer o.sourcesLock.RUnlock()
		for k, ref := range o.sources {
			if ref.LastHealthy.IsZero() {
				logger = logger.WithField("source", k)
				logger.Debug("omitting source, hasn't yet become healthy")
				if err := o.catsrcQueueSet.Requeue(k.Name, k.Namespace); err != nil {
					logger.Warn("error requeueing")
				}
				continue
			}
			// only resolve in namespace local + global catalogs
			if k.Namespace == namespace || k.Namespace == o.namespace {
				resolverSources[k] = ref.Client
			}
		}
	}()

	for k, s := range resolverSources {
		client, ok := s.(*registryclient.Client)
		if !ok {
			logger.Warn("unexpected client")
			continue
		}

		logger = logger.WithField("resolverSource", k)
		logger.WithField("clientState", client.Conn.GetState()).Debug("source")
		if client.Conn.GetState() == connectivity.TransientFailure {
			logger.WithField("clientState", client.Conn.GetState()).Debug("waiting for connection")
			ctx, _ := context.WithTimeout(context.TODO(), 2*time.Second)
			changed := client.Conn.WaitForStateChange(ctx, connectivity.TransientFailure)
			if !changed {
				logger.WithField("clientState", client.Conn.GetState()).Debug("source in transient failure and didn't recover")
				delete(resolverSources, k)
			} else {
				logger.WithField("clientState", client.Conn.GetState()).Debug("connection re-established")
			}
		}
	}
	return resolverSources
}

func (o *Operator) nothingToUpdate(logger *logrus.Entry, sub *v1alpha1.Subscription) bool {
	// Only sync if catalog has been updated since last sync time
	if o.sourcesLastUpdate.Before(&sub.Status.LastUpdated) && sub.Status.State != v1alpha1.SubscriptionStateUpgradeAvailable {
		logger.Debugf("skipping update: no new updates to catalog since last sync at %s", sub.Status.LastUpdated.String())
		return true
	}
	if sub.Status.InstallPlanRef != nil && sub.Status.State == v1alpha1.SubscriptionStateUpgradePending {
		logger.Debugf("skipping update: installplan already created")
		return true
	}
	return false
}

func (o *Operator) ensureSubscriptionInstallPlanState(logger *logrus.Entry, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, bool, error) {
	if sub.Status.InstallPlanRef != nil {
		return sub, false, nil
	}

	logger.Debug("checking for existing installplan")

	// check if there's an installplan that created this subscription (only if it doesn't have a reference yet)
	// this indicates it was newly resolved by another operator, and we should reference that installplan in the status
	ipName, ok := sub.GetAnnotations()[generatedByKey]
	if !ok {
		return sub, false, nil
	}

	ip, err := o.lister.OperatorsV1alpha1().InstallPlanLister().InstallPlans(sub.GetNamespace()).Get(ipName)
	if err != nil {
		logger.WithField("installplan", ipName).Warn("unable to get installplan from cache")
		return nil, false, err
	}
	logger.WithField("installplan", ipName).Debug("found installplan that generated subscription")

	out := sub.DeepCopy()
	ref, err := operators.GetReference(ip)
	if err != nil {
		logger.WithError(err).Warn("unable to generate installplan reference")
		return nil, false, err
	}
	out.Status.InstallPlanRef = ref
	out.Status.Install = v1alpha1.NewInstallPlanReference(ref)
	out.Status.State = v1alpha1.SubscriptionStateUpgradePending
	out.Status.CurrentCSV = out.Spec.StartingCSV
	out.Status.LastUpdated = timeNow()

	updated, err := o.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).UpdateStatus(out)
	if err != nil {
		return nil, false, err
	}

	return updated, true, nil
}

func (o *Operator) ensureSubscriptionCSVState(logger *logrus.Entry, sub *v1alpha1.Subscription, querier resolver.SourceQuerier) (*v1alpha1.Subscription, bool, error) {
	if sub.Status.CurrentCSV == "" {
		return sub, false, nil
	}

	csv, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(sub.GetNamespace()).Get(sub.Status.CurrentCSV, metav1.GetOptions{})
	out := sub.DeepCopy()
	if err != nil {
		logger.WithError(err).WithField("currentCSV", sub.Status.CurrentCSV).Debug("error fetching csv listed in subscription status")
		out.Status.State = v1alpha1.SubscriptionStateUpgradePending
	} else {
		// Check if an update is available for the current csv
		if err := querier.Queryable(); err != nil {
			return nil, false, err
		}
		bundle, _, _ := querier.FindReplacement(&csv.Spec.Version.Version, sub.Status.CurrentCSV, sub.Spec.Package, sub.Spec.Channel, resolver.CatalogKey{sub.Spec.CatalogSource, sub.Spec.CatalogSourceNamespace})
		if bundle != nil {
			out.Status.State = v1alpha1.SubscriptionStateUpgradeAvailable
		} else {
			out.Status.State = v1alpha1.SubscriptionStateAtLatest
		}

		out.Status.InstalledCSV = sub.Status.CurrentCSV
	}

	if sub.Status.State == out.Status.State {
		// The subscription status represents the cluster state
		return sub, false, nil
	}
	out.Status.LastUpdated = timeNow()

	// Update Subscription with status of transition. Log errors if we can't write them to the status.
	updatedSub, err := o.client.OperatorsV1alpha1().Subscriptions(out.GetNamespace()).UpdateStatus(out)
	if err != nil {
		logger.WithError(err).Info("error updating subscription status")
		return nil, false, fmt.Errorf("error updating Subscription status: " + err.Error())
	}

	// subscription status represents cluster state
	return updatedSub, true, nil
}

func (o *Operator) updateSubscriptionStatus(namespace string, subs []*v1alpha1.Subscription, installPlanRef *corev1.ObjectReference) error {
	// TODO: parallel, sync waitgroup
	var err error
	for _, sub := range subs {
		sub.Status.LastUpdated = timeNow()
		if installPlanRef != nil {
			sub.Status.InstallPlanRef = installPlanRef
			sub.Status.Install = v1alpha1.NewInstallPlanReference(installPlanRef)
			sub.Status.State = v1alpha1.SubscriptionStateUpgradePending
		}
		if _, subErr := o.client.OperatorsV1alpha1().Subscriptions(namespace).UpdateStatus(sub); subErr != nil {
			err = subErr
		}
	}
	return err
}

func (o *Operator) ensureInstallPlan(logger *logrus.Entry, namespace string, subs []*v1alpha1.Subscription, installPlanApproval v1alpha1.Approval, steps []*v1alpha1.Step) (*corev1.ObjectReference, error) {
	if len(steps) == 0 {
		return nil, nil
	}

	// Check if any existing installplans are creating the same resources
	installPlans, err := o.lister.OperatorsV1alpha1().InstallPlanLister().InstallPlans(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	for _, installPlan := range installPlans {
		if installPlan.Status.CSVManifestsMatch(steps) {
			logger.Infof("found InstallPlan with matching manifests: %s", installPlan.GetName())
			return operators.GetReference(installPlan)
		}
	}
	logger.Warn("no installplan found with matching manifests, creating new one")

	return o.createInstallPlan(namespace, subs, installPlanApproval, steps)
}

func (o *Operator) createInstallPlan(namespace string, subs []*v1alpha1.Subscription, installPlanApproval v1alpha1.Approval, steps []*v1alpha1.Step) (*corev1.ObjectReference, error) {
	if len(steps) == 0 {
		return nil, nil
	}

	csvNames := []string{}
	catalogSourceMap := map[string]struct{}{}
	for _, s := range steps {
		if s.Resource.Kind == "ClusterServiceVersion" {
			csvNames = append(csvNames, s.Resource.Name)
		}
		catalogSourceMap[s.Resource.CatalogSource] = struct{}{}
	}
	catalogSources := []string{}
	for s := range catalogSourceMap {
		catalogSources = append(catalogSources, s)
	}

	phase := v1alpha1.InstallPlanPhaseInstalling
	if installPlanApproval == v1alpha1.ApprovalManual {
		phase = v1alpha1.InstallPlanPhaseRequiresApproval
	}
	ip := &v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "install-",
			Namespace:    namespace,
		},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: csvNames,
			Approval:                   installPlanApproval,
			Approved:                   installPlanApproval == v1alpha1.ApprovalAutomatic,
		},
	}
	for _, sub := range subs {
		ownerutil.AddNonBlockingOwner(ip, sub)
	}

	res, err := o.client.OperatorsV1alpha1().InstallPlans(namespace).Create(ip)
	if err != nil {
		return nil, err
	}

	res.Status = v1alpha1.InstallPlanStatus{
		Phase:          phase,
		Plan:           steps,
		CatalogSources: catalogSources,
	}
	res, err = o.client.OperatorsV1alpha1().InstallPlans(namespace).UpdateStatus(res)
	if err != nil {
		return nil, err
	}

	return operators.GetReference(res)
}

func (o *Operator) syncInstallPlans(obj interface{}) (syncError error) {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting InstallPlan failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"ip":        plan.GetName(),
		"namespace": plan.GetNamespace(),
		"phase":     plan.Status.Phase,
	})

	logger.Info("syncing")

	if len(plan.Status.Plan) == 0 {
		logger.Info("skip processing installplan without status - subscription sync responsible for initial status")
		return
	}

	outInstallPlan, syncError := transitionInstallPlanState(logger.Logger, o, *plan)

	if syncError != nil {
		logger = logger.WithField("syncError", syncError)
	}

	// no changes in status, don't update
	if outInstallPlan.Status.Phase == plan.Status.Phase {
		return
	}

	// notify subscription loop of installplan changes
	if ownerutil.IsOwnedByKind(outInstallPlan, v1alpha1.SubscriptionKind) {
		oref := ownerutil.GetOwnerByKind(outInstallPlan, v1alpha1.SubscriptionKind)
		logger.WithField("owner", oref).Debug("requeueing installplan owner")
		if err := o.subQueueSet.RequeueRateLimited(oref.Name, outInstallPlan.GetNamespace()); err != nil {
			logger.WithError(err).Warn("failed to requeue installplan owner subscription")
		}
	}

	// Update InstallPlan with status of transition. Log errors if we can't write them to the status.
	if _, err := o.client.OperatorsV1alpha1().InstallPlans(plan.GetNamespace()).UpdateStatus(outInstallPlan); err != nil {
		logger = logger.WithField("updateError", err.Error())
		updateErr := errors.New("error updating InstallPlan status: " + err.Error())
		if syncError == nil {
			logger.Info("error updating InstallPlan status")
			return updateErr
		}
		logger.Info("error transitioning InstallPlan")
		syncError = fmt.Errorf("error transitioning InstallPlan: %s and error updating InstallPlan status: %s", syncError, updateErr)
	}
	return
}

type installPlanTransitioner interface {
	ResolvePlan(*v1alpha1.InstallPlan) error
	ExecutePlan(*v1alpha1.InstallPlan) error
}

var _ installPlanTransitioner = &Operator{}

func transitionInstallPlanState(log *logrus.Logger, transitioner installPlanTransitioner, in v1alpha1.InstallPlan) (*v1alpha1.InstallPlan, error) {
	out := in.DeepCopy()

	switch in.Status.Phase {
	case v1alpha1.InstallPlanPhaseRequiresApproval:
		if out.Spec.Approved {
			log.Debugf("approved, setting to %s", v1alpha1.InstallPlanPhasePlanning)
			out.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		} else {
			log.Debug("not approved, skipping sync")
		}
		return out, nil

	case v1alpha1.InstallPlanPhaseInstalling:
		log.Debug("attempting to install")
		if err := transitioner.ExecutePlan(out); err != nil {
			out.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanInstalled,
				v1alpha1.InstallPlanReasonComponentFailed, err))
			out.Status.Phase = v1alpha1.InstallPlanPhaseFailed
			return out, err
		}
		out.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanInstalled))
		out.Status.Phase = v1alpha1.InstallPlanPhaseComplete
		return out, nil
	default:
		return out, nil
	}
}

// ResolvePlan modifies an InstallPlan to contain a Plan in its Status field.
func (o *Operator) ResolvePlan(plan *v1alpha1.InstallPlan) error {
	return nil
}

// ExecutePlan applies a planned InstallPlan to a namespace.
func (o *Operator) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	if plan.Status.Phase != v1alpha1.InstallPlanPhaseInstalling {
		panic("attempted to install a plan that wasn't in the installing phase")
	}

	namespace := plan.GetNamespace()

	// Get the set of initial installplan csv names
	initialCSVNames := getCSVNameSet(plan)
	// Get pre-existing CRD owners to make decisions about applying resolved CSVs
	existingCRDOwners, err := o.getExistingApiOwners(plan.GetNamespace())
	if err != nil {
		return err
	}

	for i, step := range plan.Status.Plan {
		switch step.Status {
		case v1alpha1.StepStatusPresent, v1alpha1.StepStatusCreated:
			continue

		case v1alpha1.StepStatusUnknown, v1alpha1.StepStatusNotPresent:
			o.Log.WithFields(logrus.Fields{"kind": step.Resource.Kind, "name": step.Resource.Name}).Debug("execute resource")
			switch step.Resource.Kind {
			case crdKind:
				// Marshal the manifest into a CRD instance.
				var crd v1beta1ext.CustomResourceDefinition
				err := json.Unmarshal([]byte(step.Resource.Manifest), &crd)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// TODO: check that names are accepted
				// Attempt to create the CRD.
				_, err = o.OpClient.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(&crd)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
					continue
				} else if err != nil {
					return err
				} else {
					// If no error occured, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
					continue
				}

			case v1alpha1.ClusterServiceVersionKind:
				// Marshal the manifest into a CSV instance.
				var csv v1alpha1.ClusterServiceVersion
				err := json.Unmarshal([]byte(step.Resource.Manifest), &csv)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Check if the resolved CSV is in the initial set
				if _, ok := initialCSVNames[csv.GetName()]; !ok {
					// Check for pre-existing CSVs that own the same CRDs
					competingOwners, err := competingCRDOwnersExist(plan.GetNamespace(), &csv, existingCRDOwners)
					if err != nil {
						return errorwrap.Wrapf(err, "error checking crd owners for: %s", csv.GetName())
					}

					// TODO: decide on fail/continue logic for pre-existing dependent CSVs that own the same CRD(s)
					if competingOwners {
						// For now, error out
						return fmt.Errorf("pre-existing CRD owners found for owned CRD(s) of dependent CSV %s", csv.GetName())
					}
				}

				// Attempt to create the CSV.
				csv.SetNamespace(namespace)
				_, err = o.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Create(&csv)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating csv %s", csv.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}
			case v1alpha1.SubscriptionKind:
				// Marshal the manifest into a subscription instance.
				var sub v1alpha1.Subscription
				err := json.Unmarshal([]byte(step.Resource.Manifest), &sub)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Add the InstallPlan's name as an annotation
				if annotations := sub.GetAnnotations(); annotations != nil {
					annotations[generatedByKey] = plan.GetName()
				} else {
					sub.SetAnnotations(map[string]string{generatedByKey: plan.GetName()})
				}

				// Attempt to create the Subscription
				sub.SetNamespace(namespace)
				_, err = o.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).Create(&sub)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating subscription %s", sub.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}
			case secretKind:
				// TODO: this will confuse bundle users that include secrets in their bundles - this only handles pull secrets
				// Get the pre-existing secret.
				secret, err := o.OpClient.KubernetesInterface().CoreV1().Secrets(o.namespace).Get(step.Resource.Name, metav1.GetOptions{})
				if k8serrors.IsNotFound(err) {
					return fmt.Errorf("secret %s does not exist", step.Resource.Name)
				} else if err != nil {
					return errorwrap.Wrapf(err, "error getting pull secret from olm namespace %s", secret.GetName())
				}

				// Set the namespace to the InstallPlan's namespace and attempt to
				// create a new secret.
				secret.SetNamespace(namespace)
				_, err = o.OpClient.KubernetesInterface().CoreV1().Secrets(plan.Namespace).Create(&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.Name,
						Namespace: plan.Namespace,
					},
					Data: secret.Data,
					Type: secret.Type,
				})
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occured, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case clusterRoleKind:
				// Marshal the manifest into a ClusterRole instance.
				var cr rbacv1.ClusterRole
				err := json.Unmarshal([]byte(step.Resource.Manifest), &cr)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(cr.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for clusterrole %s", cr.GetName())
				}
				cr.OwnerReferences = updated

				// Attempt to create the ClusterRole.
				_, err = o.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(&cr)
				if k8serrors.IsAlreadyExists(err) {
					_, err = o.OpClient.UpdateClusterRole(&cr)
					if err != nil {
						return errorwrap.Wrapf(err, "error updating clusterrole %s", cr.GetName())
					}
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating clusterrole %s", cr.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}
			case clusterRoleBindingKind:
				// Marshal the manifest into a RoleBinding instance.
				var rb rbacv1.ClusterRoleBinding
				err := json.Unmarshal([]byte(step.Resource.Manifest), &rb)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(rb.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for clusterrolebinding %s", rb.GetName())
				}
				rb.OwnerReferences = updated

				// Attempt to create the ClusterRoleBinding.
				_, err = o.OpClient.KubernetesInterface().RbacV1().ClusterRoleBindings().Create(&rb)
				if k8serrors.IsAlreadyExists(err) {
					rb.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateClusterRoleBinding(&rb)
					if err != nil {
						return errorwrap.Wrapf(err, "error updating clusterrolebinding %s", rb.GetName())
					}

					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating clusterrolebinding %s", rb.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case roleKind:
				// Marshal the manifest into a Role instance.
				var r rbacv1.Role
				err := json.Unmarshal([]byte(step.Resource.Manifest), &r)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(r.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for role %s", r.GetName())
				}
				r.SetOwnerReferences(updated)
				r.SetNamespace(namespace)

				// Attempt to create the Role.
				_, err = o.OpClient.KubernetesInterface().RbacV1().Roles(plan.Namespace).Create(&r)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					r.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateRole(&r)
					if err != nil {
						return errorwrap.Wrapf(err, "error updating role %s", r.GetName())
					}

					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating role %s", r.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case roleBindingKind:
				// Marshal the manifest into a RoleBinding instance.
				var rb rbacv1.RoleBinding
				err := json.Unmarshal([]byte(step.Resource.Manifest), &rb)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(rb.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for rolebinding %s", rb.GetName())
				}
				rb.SetOwnerReferences(updated)
				rb.SetNamespace(namespace)

				// Attempt to create the RoleBinding.
				_, err = o.OpClient.KubernetesInterface().RbacV1().RoleBindings(plan.Namespace).Create(&rb)
				if k8serrors.IsAlreadyExists(err) {
					rb.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateRoleBinding(&rb)
					if err != nil {
						return errorwrap.Wrapf(err, "error updating rolebinding %s", rb.GetName())
					}

					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating rolebinding %s", rb.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case serviceAccountKind:
				// Marshal the manifest into a ServiceAccount instance.
				var sa corev1.ServiceAccount
				err := json.Unmarshal([]byte(step.Resource.Manifest), &sa)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(sa.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for service account: %s", sa.GetName())
				}
				sa.SetOwnerReferences(updated)
				sa.SetNamespace(namespace)

				// Attempt to create the ServiceAccount.
				_, err = o.OpClient.KubernetesInterface().CoreV1().ServiceAccounts(plan.Namespace).Create(&sa)
				if k8serrors.IsAlreadyExists(err) {
					// If it already exists we need to patch the existing SA with the new OwnerReferences
					sa.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateServiceAccount(&sa)
					if err != nil {
						return errorwrap.Wrapf(err, "error updating service account: %s", sa.GetName())
					}

					// Mark as present
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating service account: %s", sa.GetName())
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case serviceKind:
				// Marshal the manifest into a Service instance
				var s corev1.Service
				err := json.Unmarshal([]byte(step.Resource.Manifest), &s)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(s.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for service: %s", s.GetName())
				}
				s.SetOwnerReferences(updated)
				s.SetNamespace(namespace)

				// Attempt to create the Service
				_, err = o.OpClient.KubernetesInterface().CoreV1().Services(plan.Namespace).Create(&s)
				if k8serrors.IsAlreadyExists(err) {
					// If it already exists we need to patch the existing SA with the new OwnerReferences
					s.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateService(&s)
					if err != nil {
						return errorwrap.Wrapf(err, "error updating service: %s", s.GetName())
					}

					// Mark as present
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return errorwrap.Wrapf(err, "error creating service: %s", s.GetName())
				} else {
					// If no error occurred, mark the step as Created
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			default:
				return v1alpha1.ErrInvalidInstallPlan
			}

		default:
			return v1alpha1.ErrInvalidInstallPlan
		}
	}

	// Loop over one final time to check and see if everything is good.
	for _, step := range plan.Status.Plan {
		switch step.Status {
		case v1alpha1.StepStatusCreated, v1alpha1.StepStatusPresent:
		default:
			return nil
		}
	}

	return nil
}

// getExistingApiOwners creates a map of CRD names to existing owner CSVs in the given namespace
func (o *Operator) getExistingApiOwners(namespace string) (map[string][]string, error) {
	// Get a list of CSVs in the namespace
	csvList, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(metav1.ListOptions{})

	if err != nil {
		return nil, err
	}

	// Map CRD names to existing owner CSV CRs in the namespace
	owners := make(map[string][]string)
	for _, csv := range csvList.Items {
		for _, crd := range csv.Spec.CustomResourceDefinitions.Owned {
			owners[crd.Name] = append(owners[crd.Name], csv.GetName())
		}
		for _, api := range csv.Spec.APIServiceDefinitions.Owned {
			owners[api.Group] = append(owners[api.Group], csv.GetName())
		}
	}

	return owners, nil
}

func (o *Operator) getUpdatedOwnerReferences(refs []metav1.OwnerReference, namespace string) ([]metav1.OwnerReference, error) {
	updated := append([]metav1.OwnerReference(nil), refs...)

	for i, owner := range refs {
		if owner.Kind == v1alpha1.ClusterServiceVersionKind {
			csv, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(owner.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			owner.UID = csv.GetUID()
			updated[i] = owner
		}
	}
	return updated, nil
}

// competingCRDOwnersExist returns true if there exists a CSV that owns at least one of the given CSVs owned CRDs (that's not the given CSV)
func competingCRDOwnersExist(namespace string, csv *v1alpha1.ClusterServiceVersion, existingOwners map[string][]string) (bool, error) {
	// Attempt to find a pre-existing owner in the namespace for any owned crd
	for _, crdDesc := range csv.Spec.CustomResourceDefinitions.Owned {
		crdOwners := existingOwners[crdDesc.Name]
		l := len(crdOwners)
		switch {
		case l == 1:
			// One competing owner found
			if crdOwners[0] != csv.GetName() {
				return true, nil
			}
		case l > 1:
			return true, olmerrors.NewMultipleExistingCRDOwnersError(crdOwners, crdDesc.Name, namespace)
		}
	}

	return false, nil
}

// getCSVNameSet returns a set of the given installplan's csv names
func getCSVNameSet(plan *v1alpha1.InstallPlan) map[string]struct{} {
	csvNameSet := make(map[string]struct{})
	for _, name := range plan.Spec.ClusterServiceVersionNames {
		csvNameSet[name] = struct{}{}
	}

	return csvNameSet
}
