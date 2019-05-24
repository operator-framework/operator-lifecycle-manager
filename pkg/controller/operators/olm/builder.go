package olm

import (
	"time"

	mlw "github.com/coreos/prometheus-operator/pkg/listwatch"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extinf "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	listersappsv1 "k8s.io/client-go/listers/apps/v1"
	listerscorev1 "k8s.io/client-go/listers/core/v1"
	listersrbacv1 "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"

	opsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	listersv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	csvutility "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/csv"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	index "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/index"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/labeler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/listwatch"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

type Builder interface {
	operators.Builder

	WithEventRecorder(rec record.EventRecorder) Builder
	WithStrategyResolver(res install.StrategyResolverInterface) Builder
	WithAPIReconciler(rec resolver.APIIntersectionReconciler) Builder
	WithLabeler(labeler labeler.Labeler) Builder
	BuildOLMOperator() (*Operator, error)
}

type operatorBuilder struct {
	operators.Builder

	recorder      record.EventRecorder
	resolver      install.StrategyResolverInterface
	apiReconciler resolver.APIIntersectionReconciler
	apiLabeler    labeler.Labeler
}

func NewBuilder() Builder {
	return &operatorBuilder{
		Builder: operators.NewBuilder(),
	}
}

func (o *operatorBuilder) BuildOLMOperator() (*Operator, error) {
	base, err := o.Builder.Build()
	if err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:         base,
		recorder:         o.recorder,
		resolver:         o.resolver,
		apiReconciler:    o.apiReconciler,
		apiLabeler:       o.apiLabeler,
		csvSetGenerator:  csvutility.NewSetGenerator(base.Log, base.Lister),
		csvReplaceFinder: csvutility.NewReplaceFinder(base.Log, base.Client),
	}

	if op.recorder == nil {
		eventRecorder, err := event.NewRecorder(op.OpClient.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
		if err != nil {
			return nil, err
		}

		op.recorder = eventRecorder
	}

	if op.resolver == nil {
		op.resolver = &install.StrategyResolver{}
	}

	if op.apiReconciler == nil {
		op.apiReconciler = resolver.APIIntersectionReconcileFunc(resolver.ReconcileAPIIntersection)
	}

	if op.apiLabeler == nil {
		op.apiLabeler = labeler.Func(resolver.LabelSetsFor)
	}

	// Wire CSV informer
	csvInformer := cache.NewSharedIndexInformer(
		listwatch.ClusterServiceVersionListerWatcher(op.Client, op.Namespaces()...),
		&v1alpha1.ClusterServiceVersion{},
		op.ResyncPeriod(),
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc, index.MetaLabelIndexFuncKey: index.MetaLabelIndexFunc},
	)
	csvInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		DeleteFunc: op.handleClusterServiceVersionDeletion,
	})
	op.csvIndexer = csvInformer.GetIndexer()
	op.Lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(metav1.NamespaceAll, listersv1alpha1.NewClusterServiceVersionLister(op.csvIndexer))
	csvQueueName := "clusterserviceversions"
	op.csvQueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), csvQueueName)
	csvQueueInformer := queueinformer.NewQueueInformer(
		csvQueueName,
		op.csvQueue,
		csvInformer,
		queueinformer.WithMetricsProvider(metrics.NewMetricsCSV(op.Lister.OperatorsV1alpha1().ClusterServiceVersionLister())),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncClusterServiceVersion),
	)
	op.RegisterQueueInformer(csvQueueInformer)

	// Register separate queue for copying CSVs
	csvCopyQueueName := csvQueueName + "-copy"
	csvCopyQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), csvCopyQueueName)
	op.copyQueueIndexer = queueinformer.NewQueueIndexer(
		csvCopyQueueName,
		csvCopyQueue,
		op.csvIndexer,
		op.syncCopyCSV,
		op.Log,
	)
	op.RegisterQueueIndexer(op.copyQueueIndexer)

	// Register separate queue for gcing CSVs
	csvGCQueueName := csvQueueName + "-gc"
	csvGCQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), csvGCQueueName)
	op.gcQueueIndexer = queueinformer.NewQueueIndexer(
		csvGCQueueName,
		csvGCQueue,
		op.csvIndexer,
		op.gcCSV,
		op.Log,
	)
	op.RegisterQueueIndexer(op.gcQueueIndexer)

	// Wire OperatorGroup informer
	defaultIndexers := cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}
	ogInformer := cache.NewSharedIndexInformer(
		listwatch.OperatorGroupListerWatcher(op.Client, op.Namespaces()...),
		&opsv1.OperatorGroup{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	ogInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		DeleteFunc: op.operatorGroupDeleted,
	})
	op.Lister.OperatorsV1().RegisterOperatorGroupLister(metav1.NamespaceAll, listersv1.NewOperatorGroupLister(ogInformer.GetIndexer()))
	ogQueueName := "operatorgroups"
	op.ogQueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), ogQueueName)
	ogQueueInformer := queueinformer.NewQueueInformer(
		ogQueueName,
		op.ogQueue,
		ogInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncOperatorGroups),
	)
	op.RegisterQueueInformer(ogQueueInformer)

	// Wire Role informer
	k8sClient := op.OpClient.KubernetesInterface()
	rQueueName := "roles"
	rInformer := cache.NewSharedIndexInformer(
		listwatch.RoleListerWatcher(k8sClient, op.Namespaces()...),
		&rbacv1.Role{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	handleDeletion := &cache.ResourceEventHandlerFuncs{
		DeleteFunc: op.handleDeletion,
	}
	rInformer.AddEventHandler(handleDeletion)
	op.Lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, listersrbacv1.NewRoleLister(rInformer.GetIndexer()))
	rQueueInformer := queueinformer.NewQueueInformer(
		rQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), rQueueName),
		rInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(rQueueInformer)

	// Wire RoleBinding informer
	rbQueueName := "rolebindings"
	rbInformer := cache.NewSharedIndexInformer(
		listwatch.RoleBindingListerWatcher(k8sClient, op.Namespaces()...),
		&rbacv1.RoleBinding{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	rbInformer.AddEventHandler(handleDeletion)
	op.Lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, listersrbacv1.NewRoleBindingLister(rbInformer.GetIndexer()))
	rbQueueInformer := queueinformer.NewQueueInformer(
		rbQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), rbQueueName),
		rbInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(rbQueueInformer)

	// Wire ClusterRole informer
	informerFactory := informers.NewSharedInformerFactory(op.OpClient.KubernetesInterface(), op.ResyncPeriod())
	crQueueName := "clusterroles"
	crInformer := informerFactory.Rbac().V1().ClusterRoles()
	crInformer.Informer().AddEventHandler(handleDeletion)
	op.Lister.RbacV1().RegisterClusterRoleLister(crInformer.Lister())
	crQueueInformer := queueinformer.NewQueueInformer(
		crQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), crQueueName),
		crInformer.Informer(),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(crQueueInformer)

	// Wire ClusterRoleBinding informer
	crbQueueName := "clusterolebinding"
	crbInformer := informerFactory.Rbac().V1().ClusterRoleBindings()
	crbInformer.Informer().AddEventHandler(handleDeletion)
	op.Lister.RbacV1().RegisterClusterRoleBindingLister(crbInformer.Lister())
	crbQueueInformer := queueinformer.NewQueueInformer(
		crbQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), crbQueueName),
		crbInformer.Informer(),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(crbQueueInformer)

	// Wire Secret informer
	scQueueName := "secrets"
	scInformer := cache.NewSharedIndexInformer(
		listwatch.SecretListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.Secret{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	scInformer.AddEventHandler(handleDeletion)
	op.Lister.CoreV1().RegisterSecretLister(metav1.NamespaceAll, listerscorev1.NewSecretLister(scInformer.GetIndexer()))
	scQueueInformer := queueinformer.NewQueueInformer(
		scQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), scQueueName),
		scInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(scQueueInformer)

	// Wire Service informer
	srQueueName := "services"
	srInformer := cache.NewSharedIndexInformer(
		listwatch.ServiceListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.Service{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	srInformer.AddEventHandler(handleDeletion)
	op.Lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, listerscorev1.NewServiceLister(srInformer.GetIndexer()))
	srQueueInformer := queueinformer.NewQueueInformer(
		srQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), srQueueName),
		srInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(srQueueInformer)

	// Wire Deployment informer
	dQueueName := "deployments"
	dInformer := cache.NewSharedIndexInformer(
		listwatch.DeploymentListerWatcher(k8sClient, op.Namespaces()...),
		&appsv1.Deployment{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	dInformer.AddEventHandler(handleDeletion)
	op.Lister.AppsV1().RegisterDeploymentLister(metav1.NamespaceAll, listersappsv1.NewDeploymentLister(dInformer.GetIndexer()))
	dQueueInformer := queueinformer.NewQueueInformer(
		dQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), dQueueName),
		dInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(dQueueInformer)

	// Wire Namespaces informer
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
	nsQueueName := "namespaces"
	nsInformer := informers.NewSharedInformerFactory(op.OpClient.KubernetesInterface(), nsResyncPeriod).Core().V1().Namespaces().Informer()
	nsInformer.AddEventHandler(&cache.ResourceEventHandlerFuncs{
		DeleteFunc: op.namespaceAddedOrRemoved,
		AddFunc:    op.namespaceAddedOrRemoved,
	})
	op.Lister.CoreV1().RegisterNamespaceLister(listerscorev1.NewNamespaceLister(nsInformer.GetIndexer()))
	nsQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), nsQueueName)
	nsQueueInformer := queueinformer.NewQueueInformer(
		nsQueueName,
		nsQueue,
		nsInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(nsQueueInformer)

	// Wire Deployment informer
	saQueueName := "serviceaccounts"
	saInformer := cache.NewSharedIndexInformer(
		listwatch.ServiceAccountListerWatcher(k8sClient, op.Namespaces()...),
		&corev1.ServiceAccount{},
		op.ResyncPeriod(),
		defaultIndexers,
	)
	saInformer.AddEventHandler(handleDeletion)
	op.Lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, listerscorev1.NewServiceAccountLister(saInformer.GetIndexer()))
	saQueueInformer := queueinformer.NewQueueInformer(
		saQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), saQueueName),
		saInformer,
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(saQueueInformer)

	// Wire APIService informer
	asQueueName := "apiservices"
	asInformer := kagg.NewSharedInformerFactory(op.OpClient.ApiregistrationV1Interface(), op.ResyncPeriod()).Apiregistration().V1().APIServices()
	asInformer.Informer().AddEventHandler(handleDeletion)
	op.Lister.APIRegistrationV1().RegisterAPIServiceLister(asInformer.Lister())
	asQueueInformer := queueinformer.NewQueueInformer(
		asQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), asQueueName),
		asInformer.Informer(),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncAPIService),
	)
	op.RegisterQueueInformer(asQueueInformer)

	// Wire CustomResourceDefinition informer
	crdQueueName := "customresourcedefinitions"
	crdInformer := extinf.NewSharedInformerFactory(op.OpClient.ApiextensionsV1beta1Interface(), op.ResyncPeriod()).Apiextensions().V1beta1().CustomResourceDefinitions()
	crdInformer.Informer().AddEventHandler(handleDeletion)
	op.Lister.APIExtensionsV1beta1().RegisterCustomResourceDefinitionLister(crdInformer.Lister())
	crdQueueInformer := queueinformer.NewQueueInformer(
		crdQueueName,
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), crdQueueName),
		crdInformer.Informer(),
		queueinformer.WithLogger(op.Log),
		queueinformer.WithSyncHandlers(op.syncObject),
	)
	op.RegisterQueueInformer(crdQueueInformer)

	return op, nil
}

func (o *operatorBuilder) WithEventRecorder(rec record.EventRecorder) Builder {
	o.recorder = rec
	return o
}

func (o *operatorBuilder) WithStrategyResolver(res install.StrategyResolverInterface) Builder {
	o.resolver = res
	return o
}

func (o *operatorBuilder) WithAPIReconciler(rec resolver.APIIntersectionReconciler) Builder {
	o.apiReconciler = rec
	return o
}

func (o *operatorBuilder) WithLabeler(labeler labeler.Labeler) Builder {
	o.apiLabeler = labeler
	return o
}
