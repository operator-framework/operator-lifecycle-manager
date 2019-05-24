package catalog

import (
	"time"

	mlw "github.com/coreos/prometheus-operator/pkg/listwatch"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	listersrbacv1 "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/listwatch"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

type Builder interface {
	operators.Builder

	WithNamespace(namespace string) Builder
	WithConfigMapRegistryImage(image string) Builder
	WithResolver(res resolver.Resolver) Builder
	WithRegistryReconciler(rec reconciler.RegistryReconcilerFactory) Builder
	BuildCatalogOperator() (*Operator, error)
}

type operatorBuilder struct {
	operators.Builder

	configMapRegistryImage string
	namespace              string
	resolver               resolver.Resolver
	reconciler             reconciler.RegistryReconcilerFactory
}

func NewBuilder() Builder {
	return &operatorBuilder{
		Builder: operators.NewBuilder(),
	}
}

func (o *operatorBuilder) BuildCatalogOperator() (*Operator, error) {
	base, err := o.Builder.Build()
	if err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:   base,
		namespace:  o.namespace,
		resolver:   o.resolver,
		reconciler: o.reconciler,
		sources:    make(map[resolver.CatalogKey]resolver.SourceRef),
	}

	if op.resolver == nil {
		op.resolver = resolver.NewOperatorsV1alpha1Resolver(op.Lister)
	}

	if op.reconciler == nil {
		op.reconciler = reconciler.NewRegistryReconcilerFactory(op.Lister, op.OpClient, o.configMapRegistryImage)
	}

	// Wire CatalogSource informer
	defaultIndexers := cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}
	catsrcInformer := cache.NewSharedIndexInformer(
		listwatch.CatalogSourceListerWatcher(op.Client, op.Namespaces()...),
		&v1alpha1.CatalogSource{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	catsrcInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{DeleteFunc: op.handleCatSrcDeletion})
	catsrcQueueName := "catalogsources"
	op.catsrcQueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), catsrcQueueName)
	catsrcQueueInformer := queueinformer.NewQueueInformer(
		catsrcQueueName,
		op.catsrcQueue,
		catsrcInformer,
		queueinformer.WithMetricsProvider(metrics.NewMetricsCatalogSource(op.Client)),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncCatalogSources),
	)
	op.RegisterQueueInformer(catsrcQueueInformer)
	op.Lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, listers.NewCatalogSourceLister(catsrcInformer.GetIndexer()))

	// Wire Subscription informer
	subInformer := cache.NewSharedIndexInformer(
		listwatch.SubscriptionListerWatcher(op.Client, op.Namespaces()...),
		&v1alpha1.Subscription{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	subQueueName := "subscriptions"
	op.subQueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), subQueueName)
	subQueueInformer := queueinformer.NewQueueInformer(
		subQueueName,
		op.subQueue,
		subInformer,
		queueinformer.WithMetricsProvider(metrics.NewMetricsSubscription(op.Client)),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncSubscriptions),
	)
	op.RegisterQueueInformer(subQueueInformer)
	op.Lister.OperatorsV1alpha1().RegisterSubscriptionLister(metav1.NamespaceAll, listers.NewSubscriptionLister(subInformer.GetIndexer()))

	// Wire InstallPlan informer
	ipInformer := cache.NewSharedIndexInformer(
		listwatch.InstallPlanListerWatcher(op.Client, op.Namespaces()...),
		&v1alpha1.InstallPlan{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	ipQueueName := "installplans"
	ipQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), ipQueueName)
	ipQueueInformer := queueinformer.NewQueueInformer(
		ipQueueName,
		ipQueue,
		ipInformer,
		queueinformer.WithMetricsProvider(metrics.NewMetricsInstallPlan(op.Client)),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncInstallPlans),
	)
	op.RegisterQueueInformer(ipQueueInformer)
	op.Lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, listers.NewInstallPlanLister(ipInformer.GetIndexer()))

	// Wire CSV informer
	handleDeletion := &cache.ResourceEventHandlerFuncs{DeleteFunc: op.handleDeletion}
	csvInformer := cache.NewSharedIndexInformer(
		listwatch.ClusterServiceVersionListerWatcher(op.Client, op.Namespaces()...),
		&v1alpha1.ClusterServiceVersion{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	csvInformer.AddEventHandler(handleDeletion)
	csvQueueName := "clusterserviceversions"
	csvQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), csvQueueName)
	csvQueueInformer := queueinformer.NewQueueInformer(
		csvQueueName,
		csvQueue,
		csvInformer,
		// queueinformer.WithMetricsProvider(metrics.NewMetricsInstallPlan(op.Client)),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(csvQueueInformer)
	op.Lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(metav1.NamespaceAll, listers.NewClusterServiceVersionLister(csvInformer.GetIndexer()))

	// Wire Role informer
	k8sClient := op.OpClient.KubernetesInterface()
	rInformer := cache.NewSharedIndexInformer(
		listwatch.RoleListerWatcher(k8sClient, op.Namespaces()...),
		&rbacv1.Role{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	rInformer.AddEventHandler(handleDeletion)
	rQueueName := "roles"
	rQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), rQueueName)
	rQueueInformer := queueinformer.NewQueueInformer(
		rQueueName,
		rQueue,
		rInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(rQueueInformer)
	op.Lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, listersrbacv1.NewRoleLister(rInformer.GetIndexer()))

	// Wire RoleBinding informer
	rbInformer := cache.NewSharedIndexInformer(
		listwatch.RoleBindingListerWatcher(k8sClient, op.Namespaces()...),
		&rbacv1.RoleBinding{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	rbInformer.AddEventHandler(handleDeletion)
	rbQueueName := "rolebindings"
	rbQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), rbQueueName)
	rbQueueInformer := queueinformer.NewQueueInformer(
		rbQueueName,
		rbQueue,
		rbInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(rbQueueInformer)
	op.Lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, listersrbacv1.NewRoleBindingLister(rbInformer.GetIndexer()))

	// Wire Service informer
	sInformer := cache.NewSharedIndexInformer(
		listwatch.ServiceListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.Service{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	sInformer.AddEventHandler(handleDeletion)
	sQueueName := "services"
	sQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), sQueueName)
	sQueueInformer := queueinformer.NewQueueInformer(
		sQueueName,
		sQueue,
		sInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(sQueueInformer)
	op.Lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, listerscorev1.NewServiceLister(sInformer.GetIndexer()))

	// Wire ServiceAccount informer
	saInformer := cache.NewSharedIndexInformer(
		listwatch.ServiceAccountListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.ServiceAccount{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	saInformer.AddEventHandler(handleDeletion)
	saQueueName := "serviceaccounts"
	saQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), saQueueName)
	saQueueInformer := queueinformer.NewQueueInformer(
		saQueueName,
		saQueue,
		saInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(saQueueInformer)
	op.Lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, listerscorev1.NewServiceAccountLister(saInformer.GetIndexer()))

	// Wire Pod informer
	pInformer := cache.NewSharedIndexInformer(
		listwatch.PodListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.Pod{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	pInformer.AddEventHandler(handleDeletion)
	pQueueName := "pods"
	pQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), pQueueName)
	pQueueInformer := queueinformer.NewQueueInformer(
		pQueueName,
		pQueue,
		pInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(pQueueInformer)
	op.Lister.CoreV1().RegisterPodLister(metav1.NamespaceAll, listerscorev1.NewPodLister(pInformer.GetIndexer()))

	// Wire ConfigMap informer
	cmInformer := cache.NewSharedIndexInformer(
		listwatch.ConfigMapListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.ConfigMap{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	cmInformer.AddEventHandler(handleDeletion)
	cmQueueName := "configmaps"
	cmQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), cmQueueName)
	cmQueueInformer := queueinformer.NewQueueInformer(
		cmQueueName,
		cmQueue,
		cmInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(cmQueueInformer)
	op.Lister.CoreV1().RegisterConfigMapLister(metav1.NamespaceAll, listerscorev1.NewConfigMapLister(cmInformer.GetIndexer()))

	// Wire Namespace informer
	// nsResyncPeriod is used to control how often the namespace informer
	// should resync. If the unprivileged ListerWatcher is used, then the
	// informer must resync more often because it cannot watch for
	// namespace changes.
	nsResyncPeriod := 15 * time.Second
	// If the only namespace is v1.NamespaceAll, then the client must be
	// privileged and a regular cache.ListWatch will be used. In this case
	// watching works and we do not need to resync so frequently.
	if mlw.IsAllNamespaces(op.Namespaces()) {
		nsResyncPeriod = op.ResyncPeriod()
	}
	// TODO: Issues with fake client prevent unit testing with this, use typed factory instead (see: https://github.com/kubernetes/client-go/issues/352)
	// nsInformer := cache.NewSharedIndexInformer(
	// 	mlw.NewUnprivilegedNamespaceListWatchFromClient(k8sClient.CoreV1().RESTClient(), op.Namespaces(), fields.Everything()),
	// 	&corev1.Namespace{},
	// 	nsResyncPeriod,
	// 	cache.Indexers{},
	// )
	nsInformer := informers.NewSharedInformerFactory(op.OpClient.KubernetesInterface(), nsResyncPeriod).Core().V1().Namespaces().Informer()
	resolveQueueName := "resolver"
	resolveQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), resolveQueueName)
	nsQueueInformer := queueinformer.NewQueueInformer(
		resolveQueueName,
		resolveQueue,
		nsInformer,
		queueinformer.WithLogger(op.Log),
		// Namespace sync for resolving subscriptions
		queueinformer.WithSyncHandlers(op.syncResolvingNamespace),
	)
	op.RegisterQueueInformer(nsQueueInformer)
	op.Lister.CoreV1().RegisterNamespaceLister(listerscorev1.NewNamespaceLister(nsInformer.GetIndexer()))
	op.resolveQueue = resolveQueue

	return op, nil
}

func (o *operatorBuilder) WithNamespace(namespace string) Builder {
	o.namespace = namespace
	return o
}

func (o *operatorBuilder) WithConfigMapRegistryImage(image string) Builder {
	o.configMapRegistryImage = image
	return o
}

func (o *operatorBuilder) WithResolver(res resolver.Resolver) Builder {
	o.resolver = res
	return o
}

func (o *operatorBuilder) WithRegistryReconciler(rec reconciler.RegistryReconcilerFactory) Builder {
	o.reconciler = rec
	return o
}
