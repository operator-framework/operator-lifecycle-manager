package olm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extinf "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/overrides"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clients"
	csvutility "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/csv"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	index "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/index"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/labeler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/proxy"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

var (
	ErrRequirementsNotMet      = errors.New("requirements were not met")
	ErrCRDOwnerConflict        = errors.New("conflicting CRD owner in namespace")
	ErrAPIServiceOwnerConflict = errors.New("unable to adopt APIService")
)

type Operator struct {
	queueinformer.Operator

	clock                 utilclock.Clock
	logger                *logrus.Logger
	opClient              operatorclient.ClientInterface
	client                versioned.Interface
	lister                operatorlister.OperatorLister
	ogQueueSet            *queueinformer.ResourceQueueSet
	csvQueueSet           *queueinformer.ResourceQueueSet
	csvCopyQueueSet       *queueinformer.ResourceQueueSet
	csvGCQueueSet         *queueinformer.ResourceQueueSet
	objGCQueueSet         *queueinformer.ResourceQueueSet
	nsQueueSet            workqueue.RateLimitingInterface
	apiServiceQueue       workqueue.RateLimitingInterface
	csvIndexers           map[string]cache.Indexer
	recorder              record.EventRecorder
	resolver              install.StrategyResolverInterface
	apiReconciler         resolver.APIIntersectionReconciler
	apiLabeler            labeler.Labeler
	csvSetGenerator       csvutility.SetGenerator
	csvReplaceFinder      csvutility.ReplaceFinder
	csvNotification       csvutility.WatchNotification
	serviceAccountSyncer  *scoped.UserDefinedServiceAccountSyncer
	clientAttenuator      *scoped.ClientAttenuator
	serviceAccountQuerier *scoped.UserDefinedServiceAccountQuerier
	clientFactory         clients.Factory
}

func NewOperator(ctx context.Context, options ...OperatorOption) (*Operator, error) {
	config := defaultOperatorConfig()
	config.apply(options)

	return newOperatorWithConfig(ctx, config)
}

func newOperatorWithConfig(ctx context.Context, config *operatorConfig) (*Operator, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}

	queueOperator, err := queueinformer.NewOperator(config.operatorClient.KubernetesInterface().Discovery(), queueinformer.WithOperatorLogger(config.logger))
	if err != nil {
		return nil, err
	}

	eventRecorder, err := event.NewRecorder(config.operatorClient.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
	if err != nil {
		return nil, err
	}

	lister := operatorlister.NewLister()

	scheme := runtime.NewScheme()
	if err := k8sscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:              queueOperator,
		clock:                 config.clock,
		logger:                config.logger,
		opClient:              config.operatorClient,
		client:                config.externalClient,
		ogQueueSet:            queueinformer.NewEmptyResourceQueueSet(),
		csvQueueSet:           queueinformer.NewEmptyResourceQueueSet(),
		csvCopyQueueSet:       queueinformer.NewEmptyResourceQueueSet(),
		csvGCQueueSet:         queueinformer.NewEmptyResourceQueueSet(),
		objGCQueueSet:         queueinformer.NewEmptyResourceQueueSet(),
		apiServiceQueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "apiservice"),
		resolver:              config.strategyResolver,
		apiReconciler:         config.apiReconciler,
		lister:                lister,
		recorder:              eventRecorder,
		apiLabeler:            config.apiLabeler,
		csvIndexers:           map[string]cache.Indexer{},
		csvSetGenerator:       csvutility.NewSetGenerator(config.logger, lister),
		csvReplaceFinder:      csvutility.NewReplaceFinder(config.logger, config.externalClient),
		serviceAccountSyncer:  scoped.NewUserDefinedServiceAccountSyncer(config.logger, scheme, config.operatorClient, config.externalClient),
		clientAttenuator:      scoped.NewClientAttenuator(config.logger, config.restConfig, config.operatorClient),
		serviceAccountQuerier: scoped.NewUserDefinedServiceAccountQuerier(config.logger, config.externalClient),
		clientFactory:         clients.NewFactory(config.restConfig),
	}

	// Set up syncing for namespace-scoped resources
	k8sSyncer := queueinformer.LegacySyncHandler(op.syncObject).ToSyncerWithDelete(op.handleDeletion)
	for _, namespace := range config.watchedNamespaces {
		// Wire CSVs
		extInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(op.client, config.resyncPeriod(), externalversions.WithNamespace(namespace))
		csvInformer := extInformerFactory.Operators().V1alpha1().ClusterServiceVersions()
		op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, csvInformer.Lister())
		csvQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/csv", namespace))
		op.csvQueueSet.Set(namespace, csvQueue)
		csvQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithMetricsProvider(metrics.NewMetricsCSV(csvInformer.Lister())),
			queueinformer.WithLogger(op.logger),
			queueinformer.WithQueue(csvQueue),
			queueinformer.WithInformer(csvInformer.Informer()),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncClusterServiceVersion).ToSyncerWithDelete(op.handleClusterServiceVersionDeletion)),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(csvQueueInformer); err != nil {
			return nil, err
		}
		if err := csvInformer.Informer().AddIndexers(cache.Indexers{index.MetaLabelIndexFuncKey: index.MetaLabelIndexFunc}); err != nil {
			return nil, err
		}
		csvIndexer := csvInformer.Informer().GetIndexer()
		op.csvIndexers[namespace] = csvIndexer

		// Register separate queue for copying csvs
		csvCopyQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/csv-copy", namespace))
		op.csvCopyQueueSet.Set(namespace, csvCopyQueue)
		csvCopyQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithQueue(csvCopyQueue),
			queueinformer.WithIndexer(csvIndexer),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncCopyCSV).ToSyncer()),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(csvCopyQueueInformer); err != nil {
			return nil, err
		}

		// Register separate queue for gcing csvs
		csvGCQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/csv-gc", namespace))
		op.csvGCQueueSet.Set(namespace, csvGCQueue)
		csvGCQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithQueue(csvGCQueue),
			queueinformer.WithIndexer(csvIndexer),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncGcCsv).ToSyncer()),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(csvGCQueueInformer); err != nil {
			return nil, err
		}

		// Wire OperatorGroup reconciliation
		operatorGroupInformer := extInformerFactory.Operators().V1().OperatorGroups()
		op.lister.OperatorsV1().RegisterOperatorGroupLister(namespace, operatorGroupInformer.Lister())
		ogQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/og", namespace))
		op.ogQueueSet.Set(namespace, ogQueue)
		operatorGroupQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithQueue(ogQueue),
			queueinformer.WithInformer(operatorGroupInformer.Informer()),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncOperatorGroups).ToSyncerWithDelete(op.operatorGroupDeleted)),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(operatorGroupQueueInformer); err != nil {
			return nil, err
		}

		// Register OperatorCondition QueueInformer
		opConditionInformer := extInformerFactory.Operators().V2().OperatorConditions()
		op.lister.OperatorsV2().RegisterOperatorConditionLister(namespace, opConditionInformer.Lister())
		opConditionQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(opConditionInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(opConditionQueueInformer); err != nil {
			return nil, err
		}

		subInformer := extInformerFactory.Operators().V1alpha1().Subscriptions()
		op.lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, subInformer.Lister())
		subQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(subInformer.Informer()),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncSubscription).ToSyncerWithDelete(op.syncSubscriptionDeleted)),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(subQueueInformer); err != nil {
			return nil, err
		}

		// Wire Deployments
		k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), config.resyncPeriod(), informers.WithNamespace(namespace))
		depInformer := k8sInformerFactory.Apps().V1().Deployments()
		op.lister.AppsV1().RegisterDeploymentLister(namespace, depInformer.Lister())
		depQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(depInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(depQueueInformer); err != nil {
			return nil, err
		}

		// Set up RBAC informers
		roleInformer := k8sInformerFactory.Rbac().V1().Roles()
		op.lister.RbacV1().RegisterRoleLister(namespace, roleInformer.Lister())
		roleQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(roleInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(roleQueueInformer); err != nil {
			return nil, err
		}

		roleBindingInformer := k8sInformerFactory.Rbac().V1().RoleBindings()
		op.lister.RbacV1().RegisterRoleBindingLister(namespace, roleBindingInformer.Lister())
		roleBindingQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(roleBindingInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(roleBindingQueueInformer); err != nil {
			return nil, err
		}

		// Register Secret QueueInformer
		secretInformer := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), config.resyncPeriod(), informers.WithNamespace(namespace), informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}).String()
		})).Core().V1().Secrets()
		op.lister.CoreV1().RegisterSecretLister(namespace, secretInformer.Lister())
		secretQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(secretInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(secretQueueInformer); err != nil {
			return nil, err
		}

		// Register Service QueueInformer
		serviceInformer := k8sInformerFactory.Core().V1().Services()
		op.lister.CoreV1().RegisterServiceLister(namespace, serviceInformer.Lister())
		serviceQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(serviceInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(serviceQueueInformer); err != nil {
			return nil, err
		}

		// Register ServiceAccount QueueInformer
		serviceAccountInformer := k8sInformerFactory.Core().V1().ServiceAccounts()
		op.lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, serviceAccountInformer.Lister())
		serviceAccountQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(serviceAccountInformer.Informer()),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(serviceAccountQueueInformer); err != nil {
			return nil, err
		}

		objGCQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/obj-gc", namespace))
		op.objGCQueueSet.Set(namespace, objGCQueue)
		objGCQueueInformer, err := queueinformer.NewQueue(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithQueue(objGCQueue),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncGCObject).ToSyncer()),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(objGCQueueInformer); err != nil {
			return nil, err
		}

	}

	// add queue for all namespaces as well
	objGCQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("%s/obj-gc", ""))
	op.objGCQueueSet.Set("", objGCQueue)
	objGCQueueInformer, err := queueinformer.NewQueue(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(objGCQueue),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncGCObject).ToSyncer()),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(objGCQueueInformer); err != nil {
		return nil, err
	}

	k8sInformerFactory := informers.NewSharedInformerFactory(op.opClient.KubernetesInterface(), config.resyncPeriod())
	clusterRoleInformer := k8sInformerFactory.Rbac().V1().ClusterRoles()
	op.lister.RbacV1().RegisterClusterRoleLister(clusterRoleInformer.Lister())
	clusterRoleQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithInformer(clusterRoleInformer.Informer()),
		queueinformer.WithSyncer(k8sSyncer),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(clusterRoleQueueInformer); err != nil {
		return nil, err
	}

	clusterRoleBindingInformer := k8sInformerFactory.Rbac().V1().ClusterRoleBindings()
	op.lister.RbacV1().RegisterClusterRoleBindingLister(clusterRoleBindingInformer.Lister())
	clusterRoleBindingQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithInformer(clusterRoleBindingInformer.Informer()),
		queueinformer.WithSyncer(k8sSyncer),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(clusterRoleBindingQueueInformer); err != nil {
		return nil, err
	}

	// register namespace queueinformer
	namespaceInformer := k8sInformerFactory.Core().V1().Namespaces()
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())
	op.nsQueueSet = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resolver")
	namespaceInformer.Informer().AddEventHandler(
		&cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.namespaceAddedOrRemoved,
			AddFunc:    op.namespaceAddedOrRemoved,
		},
	)
	namespaceQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(op.nsQueueSet),
		queueinformer.WithInformer(namespaceInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncNamespace).ToSyncer()),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(namespaceQueueInformer); err != nil {
		return nil, err
	}

	// Register APIService QueueInformer
	apiServiceInformer := kagg.NewSharedInformerFactory(op.opClient.ApiregistrationV1Interface(), config.resyncPeriod()).Apiregistration().V1().APIServices()
	op.lister.APIRegistrationV1().RegisterAPIServiceLister(apiServiceInformer.Lister())
	apiServiceQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(op.apiServiceQueue),
		queueinformer.WithInformer(apiServiceInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncAPIService).ToSyncerWithDelete(op.handleDeletion)),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(apiServiceQueueInformer); err != nil {
		return nil, err
	}

	// Register CustomResourceDefinition QueueInformer
	crdInformer := extinf.NewSharedInformerFactory(op.opClient.ApiextensionsInterface(), config.resyncPeriod()).Apiextensions().V1().CustomResourceDefinitions()
	op.lister.APIExtensionsV1().RegisterCustomResourceDefinitionLister(crdInformer.Lister())
	crdQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithInformer(crdInformer.Informer()),
		queueinformer.WithSyncer(k8sSyncer),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(crdQueueInformer); err != nil {
		return nil, err
	}

	// setup proxy env var injection policies
	discovery := config.operatorClient.KubernetesInterface().Discovery()
	proxyAPIExists, err := proxy.IsAPIAvailable(discovery)
	if err != nil {
		op.logger.Errorf("error happened while probing for Proxy API support - %v", err)
		return nil, err
	}

	proxyQuerierInUse := proxy.NoopQuerier()
	if proxyAPIExists {
		op.logger.Info("OpenShift Proxy API  available - setting up watch for Proxy type")

		proxyInformer, proxySyncer, proxyQuerier, err := proxy.NewSyncer(op.logger, config.configClient, discovery)
		if err != nil {
			err = fmt.Errorf("failed to initialize syncer for Proxy type - %v", err)
			return nil, err
		}

		op.logger.Info("OpenShift Proxy query will be used to fetch cluster proxy configuration")
		proxyQuerierInUse = proxyQuerier

		informer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(proxyInformer.Informer()),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(proxySyncer.SyncProxy).ToSyncerWithDelete(proxySyncer.HandleProxyDelete)),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(informer); err != nil {
			return nil, err
		}
	}

	overridesBuilderFunc := overrides.NewDeploymentInitializer(op.logger, proxyQuerierInUse, op.lister)
	op.resolver = &install.StrategyResolver{
		OverridesBuilderFunc: overridesBuilderFunc.GetDeploymentInitializer,
	}

	return op, nil
}

func (a *Operator) now() *metav1.Time {
	now := metav1.NewTime(a.clock.Now().UTC())
	return &now
}

func (a *Operator) syncSubscription(obj interface{}) error {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		a.logger.Debugf("wrong type: %#v\n", obj)
		return fmt.Errorf("casting Subscription failed")
	}

	installedCSV := sub.Status.InstalledCSV
	if installedCSV != "" {
		a.logger.WithField("csv", installedCSV).Debug("subscription has changed, requeuing installed csv")
		if err := a.csvQueueSet.Requeue(sub.GetNamespace(), installedCSV); err != nil {
			a.logger.WithField("csv", installedCSV).Debug("failed to requeue installed csv")
			return err
		}
	}

	return nil
}

func (a *Operator) syncSubscriptionDeleted(obj interface{}) {
	_, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		a.logger.Debugf("casting Subscription failed, wrong type: %#v\n", obj)
	}

	return
}

func (a *Operator) syncAPIService(obj interface{}) (syncError error) {
	apiService, ok := obj.(*apiregistrationv1.APIService)
	if !ok {
		a.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting APIService failed")
	}

	logger := a.logger.WithFields(logrus.Fields{
		"id":         queueinformer.NewLoopID(),
		"apiService": apiService.GetName(),
	})
	logger.Debug("syncing APIService")

	if name, ns, ok := ownerutil.GetOwnerByKindLabel(apiService, v1alpha1.ClusterServiceVersionKind); ok {
		_, err := a.lister.CoreV1().NamespaceLister().Get(ns)
		if k8serrors.IsNotFound(err) {
			logger.Debug("Deleting api service since owning namespace is not found")
			syncError = a.opClient.DeleteAPIService(apiService.GetName(), &metav1.DeleteOptions{})
			return
		}

		_, err = a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
		if k8serrors.IsNotFound(err) {
			logger.Debug("Deleting api service since owning CSV is not found")
			syncError = a.opClient.DeleteAPIService(apiService.GetName(), &metav1.DeleteOptions{})
			return
		} else if err != nil {
			syncError = err
			return
		} else {
			if ownerutil.IsOwnedByKindLabel(apiService, v1alpha1.ClusterServiceVersionKind) {
				logger.Debug("requeueing owner CSVs")
				a.requeueOwnerCSVs(apiService)
			}
		}
	}

	return nil
}

func (a *Operator) GetCSVSetGenerator() csvutility.SetGenerator {
	return a.csvSetGenerator
}

func (a *Operator) GetReplaceFinder() csvutility.ReplaceFinder {
	return a.csvReplaceFinder
}

func (a *Operator) RegisterCSVWatchNotification(csvNotification csvutility.WatchNotification) {
	if csvNotification == nil {
		return
	}

	a.csvNotification = csvNotification
}

func (a *Operator) syncGCObject(obj interface{}) (syncError error) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		a.logger.Warn("object sync: casting to metav1.Object failed")
		return
	}
	logger := a.logger.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
		"self":      metaObj.GetSelfLink(),
	})

	switch metaObj.(type) {
	case *rbacv1.ClusterRole:
		if name, ns, ok := ownerutil.GetOwnerByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind); ok {
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
			if err == nil {
				logger.Debugf("CSV still present, must wait until it is deleted (owners=%v/%v)", ns, name)
				syncError = fmt.Errorf("cleanup must wait")
				return
			} else if !k8serrors.IsNotFound(err) {
				syncError = err
				return
			}
		}

		if err := a.opClient.DeleteClusterRole(metaObj.GetName(), &metav1.DeleteOptions{}); err != nil {
			logger.WithError(err).Warn("cannot delete cluster role")
			break
		}
		logger.Debugf("Deleted cluster role %v due to no owning CSV", metaObj.GetName())
	case *rbacv1.ClusterRoleBinding:
		if name, ns, ok := ownerutil.GetOwnerByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind); ok {
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
			if err == nil {
				logger.Debugf("CSV still present, must wait until it is deleted (owners=%v)", name)
				syncError = fmt.Errorf("cleanup must wait")
				return
			} else if !k8serrors.IsNotFound(err) {
				syncError = err
				return
			}
		}

		if err := a.opClient.DeleteClusterRoleBinding(metaObj.GetName(), &metav1.DeleteOptions{}); err != nil {
			logger.WithError(err).Warn("cannot delete cluster role binding")
			break
		}
		logger.Debugf("Deleted cluster role binding %v due to no owning CSV", metaObj.GetName())
	case *admissionregistrationv1.MutatingWebhookConfiguration:
		if name, ns, ok := ownerutil.GetOwnerByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind); ok {
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
			if err == nil {
				logger.Debugf("CSV still present, must wait until it is deleted (owners=%v)", name)
				syncError = fmt.Errorf("cleanup must wait")
				return
			} else if !k8serrors.IsNotFound(err) {
				logger.Infof("error CSV retrieval error")
				syncError = err
				return
			}
		}

		if err := a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(context.TODO(), metaObj.GetName(), metav1.DeleteOptions{}); err != nil {
			logger.WithError(err).Warn("cannot delete MutatingWebhookConfiguration")
			break
		}
		logger.Debugf("Deleted MutatingWebhookConfiguration %v due to no owning CSV", metaObj.GetName())
	case *admissionregistrationv1.ValidatingWebhookConfiguration:
		if name, ns, ok := ownerutil.GetOwnerByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind); ok {
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
			if err == nil {
				logger.Debugf("CSV still present, must wait until it is deleted (owners=%v)", name)
				syncError = fmt.Errorf("cleanup must wait")
				return
			} else if !k8serrors.IsNotFound(err) {
				logger.Infof("Error CSV retrieval error")
				syncError = err
				return
			}
		}

		if err := a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(context.TODO(), metaObj.GetName(), metav1.DeleteOptions{}); err != nil {
			logger.WithError(err).Warn("cannot delete ValidatingWebhookConfiguration")
			break
		}
		logger.Debugf("Deleted ValidatingWebhookConfiguration %v due to no owning CSV", metaObj.GetName())
	}

	return
}

func (a *Operator) syncObject(obj interface{}) (syncError error) {
	// Assert as metav1.Object
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		syncError = errors.New("object sync: casting to metav1.Object failed")
		a.logger.Warn(syncError.Error())
		return
	}
	logger := a.logger.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
		"self":      metaObj.GetSelfLink(),
	})

	// Requeues objects that can't have ownerrefs (cluster -> namespace, cross-namespace)
	if ownerutil.IsOwnedByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind) {
		name, ns, ok := ownerutil.GetOwnerByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind)
		if !ok {
			logger.Error("unexpected owner label retrieval failure")
		}
		_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
		if !k8serrors.IsNotFound(err) {
			logger.Debug("requeueing owner csvs from owner label")
			a.requeueOwnerCSVs(metaObj)
		} else {
			switch metaObj.(type) {
			case *rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding, *admissionregistrationv1.MutatingWebhookConfiguration, *admissionregistrationv1.ValidatingWebhookConfiguration:
				resourceEvent := kubestate.NewResourceEvent(
					kubestate.ResourceUpdated,
					metaObj,
				)
				if syncError = a.objGCQueueSet.RequeueEvent("", resourceEvent); syncError != nil {
					logger.WithError(syncError).Warnf("failed to requeue gc event: %v", resourceEvent)
				}
				return
			}
		}

	}

	// Requeue all owner CSVs
	if ownerutil.IsOwnedByKind(metaObj, v1alpha1.ClusterServiceVersionKind) {
		logger.Debug("requeueing owner csvs")
		a.requeueOwnerCSVs(metaObj)
	}

	// Requeue CSVs with provided and required labels (for CRDs)
	if labelSets, err := a.apiLabeler.LabelSetsFor(metaObj); err != nil {
		logger.WithError(err).Warn("couldn't create label set")
	} else if len(labelSets) > 0 {
		logger.Debug("requeueing providing/requiring csvs")
		a.requeueCSVsByLabelSet(logger, labelSets...)
	}

	// Requeue CSVs that have the reason of `CSVReasonComponentFailedNoRetry` in the case of an RBAC change
	var errs []error
	related, _ := scoped.IsObjectRBACRelated(metaObj)
	if related {
		csvList := a.csvSet(metaObj.GetNamespace(), v1alpha1.CSVPhaseFailed)
		for _, csv := range csvList {
			if csv.Status.Reason != v1alpha1.CSVReasonComponentFailedNoRetry {
				continue
			}
			csv.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonDetectedClusterChange, "Cluster resources changed state", a.now())
			_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).UpdateStatus(context.TODO(), csv, metav1.UpdateOptions{})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
				errs = append(errs, err)
			}
			logger.Debug("Requeuing CSV due to detected RBAC change")
		}
	}

	syncError = utilerrors.NewAggregate(errs)
	return nil
}

func (a *Operator) namespaceAddedOrRemoved(obj interface{}) {
	// Check to see if any operator groups are associated with this namespace
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		return
	}

	logger := a.logger.WithFields(logrus.Fields{
		"name": namespace.GetName(),
	})

	operatorGroupList, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		logger.WithError(err).Warn("lister failed")
		return
	}

	for _, group := range operatorGroupList {
		if resolver.NewNamespaceSet(group.Status.Namespaces).Contains(namespace.GetName()) {
			if err := a.ogQueueSet.Requeue(group.Namespace, group.Name); err != nil {
				logger.WithError(err).Warn("error requeuing operatorgroup")
			}
		}
	}
	return
}

func (a *Operator) syncNamespace(obj interface{}) error {
	// Check to see if any operator groups are associated with this namespace
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		a.logger.Debugf("wrong type: %#v\n", obj)
		return fmt.Errorf("casting Namespace failed")
	}

	logger := a.logger.WithFields(logrus.Fields{
		"name": namespace.GetName(),
	})

	// Remove existing OperatorGroup labels
	for label := range namespace.GetLabels() {
		if v1.IsOperatorGroupLabel(label) {
			delete(namespace.Labels, label)
		}
	}

	operatorGroupList, err := a.lister.OperatorsV1().OperatorGroupLister().List(labels.Everything())
	if err != nil {
		logger.WithError(err).Warn("lister failed")
		return err
	}

	// Query OG in this namespace
	groups, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(namespace.GetName()).List(labels.Everything())
	if err != nil {
		logger.WithError(err).Warn("failed to list OperatorGroups in the namespace")
		return err
	}

	// Check if there is a stale multiple OG condition and clear it if existed.
	if len(groups) == 1 {
		og := groups[0]
		if c := meta.FindStatusCondition(og.Status.Conditions, v1.MutlipleOperatorGroupCondition); c != nil {
			meta.RemoveStatusCondition(&og.Status.Conditions, v1.MutlipleOperatorGroupCondition)
			_, err = a.client.OperatorsV1().OperatorGroups(namespace.GetName()).UpdateStatus(context.TODO(), og, metav1.UpdateOptions{})
			if err != nil {
				logger.Warnf("fail to upgrade operator group status og=%s with condition %+v: %s", og.GetName(), c, err.Error())
			}
		}
	} else if len(groups) > 1 {
		// Add to all OG's status conditions to indicate they're multiple OGs in the
		// same namespace which is not allowed.
		cond := metav1.Condition{
			Type:    v1.MutlipleOperatorGroupCondition,
			Status:  metav1.ConditionTrue,
			Reason:  v1.MultipleOperatorGroupsReason,
			Message: "Multiple OperatorGroup found in the same namespace",
		}
		for _, og := range groups {
			meta.SetStatusCondition(&og.Status.Conditions, cond)
			_, err = a.client.OperatorsV1().OperatorGroups(namespace.GetName()).UpdateStatus(context.TODO(), og, metav1.UpdateOptions{})
			if err != nil {
				logger.Warnf("fail to upgrade operator group status og=%s with condition %+v: %s", og.GetName(), cond, err.Error())
			}
		}
	}

	for _, group := range operatorGroupList {
		namespaceSet := resolver.NewNamespaceSet(group.Status.Namespaces)

		// Apply the label if not an All Namespaces OperatorGroup.
		if namespaceSet.Contains(namespace.GetName()) && !namespaceSet.IsAllNamespaces() {
			if namespace.Labels == nil {
				namespace.Labels = make(map[string]string, 1)
			}
			ogLabelKey, ogLabelValue, err := group.OGLabelKeyAndValue()
			if err != nil {
				return err
			}
			namespace.Labels[ogLabelKey] = ogLabelValue
		}
	}

	// Update the Namespace
	_, err = a.opClient.KubernetesInterface().CoreV1().Namespaces().Update(context.TODO(), namespace, metav1.UpdateOptions{})

	return err
}

func (a *Operator) handleClusterServiceVersionDeletion(obj interface{}) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}

		clusterServiceVersion, ok = tombstone.Obj.(*v1alpha1.ClusterServiceVersion)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a ClusterServiceVersion %#v", obj))
			return
		}
	}

	if a.csvNotification != nil {
		a.csvNotification.OnDelete(clusterServiceVersion)
	}

	logger := a.logger.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})

	metrics.DeleteCSVMetric(clusterServiceVersion)

	defer func(csv v1alpha1.ClusterServiceVersion) {
		if clusterServiceVersion.IsCopied() {
			logger.Debug("deleted csv is copied. skipping operatorgroup requeue")
			return
		}

		// Requeue all OperatorGroups in the namespace
		logger.Debug("requeueing operatorgroups in namespace")
		operatorGroups, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).List(labels.Everything())
		if err != nil {
			logger.WithError(err).Warnf("an error occurred while listing operatorgroups to requeue after csv deletion")
			return
		}

		for _, operatorGroup := range operatorGroups {
			logger := logger.WithField("operatorgroup", operatorGroup.GetName())
			logger.Debug("requeueing")
			if err := a.ogQueueSet.Requeue(operatorGroup.GetNamespace(), operatorGroup.GetName()); err != nil {
				logger.WithError(err).Debug("error requeueing operatorgroup")
			}
		}
	}(*clusterServiceVersion)

	targetNamespaces, ok := clusterServiceVersion.Annotations[v1.OperatorGroupTargetsAnnotationKey]
	if !ok {
		logger.Debug("missing target namespaces annotation on csv")
		return
	}

	operatorNamespace, ok := clusterServiceVersion.Annotations[v1.OperatorGroupNamespaceAnnotationKey]
	if !ok {
		logger.Debug("missing operator namespace annotation on csv")
		return
	}

	if _, ok = clusterServiceVersion.Annotations[v1.OperatorGroupAnnotationKey]; !ok {
		logger.Debug("missing operatorgroup name annotation on csv")
		return
	}

	if clusterServiceVersion.IsCopied() {
		logger.Debug("deleted csv is copied. skipping additional cleanup steps")
		return
	}

	logger.Info("gcing children")
	namespaces := make([]string, 0)
	if targetNamespaces == "" {
		namespaceList, err := a.opClient.KubernetesInterface().CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			logger.WithError(err).Warn("cannot list all namespaces to requeue child csvs for deletion")
			return
		}
		for _, namespace := range namespaceList.Items {
			namespaces = append(namespaces, namespace.GetName())
		}
	} else {
		namespaces = strings.Split(targetNamespaces, ",")
	}
	for _, namespace := range namespaces {
		if namespace != operatorNamespace {
			logger.WithField("targetNamespace", namespace).Debug("requeueing child csv for deletion")
			if err := a.csvGCQueueSet.Requeue(namespace, clusterServiceVersion.GetName()); err != nil {
				logger.WithError(err).Warn("unable to requeue")
			}
		}
	}

	for _, desc := range clusterServiceVersion.Spec.APIServiceDefinitions.Owned {
		apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
		fetched, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
		if k8serrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			logger.WithError(err).Warn("api service get failure")
			continue
		}
		apiServiceLabels := fetched.GetLabels()
		if clusterServiceVersion.GetName() == apiServiceLabels[ownerutil.OwnerKey] && clusterServiceVersion.GetNamespace() == apiServiceLabels[ownerutil.OwnerNamespaceKey] {
			logger.Infof("gcing api service %v", apiServiceName)
			err := a.opClient.DeleteAPIService(apiServiceName, &metav1.DeleteOptions{})
			if err != nil {
				logger.WithError(err).Warn("cannot delete orphaned api service")
			}
		}
	}

	ownerSelector := ownerutil.CSVOwnerSelector(clusterServiceVersion)
	crbs, err := a.lister.RbacV1().ClusterRoleBindingLister().List(ownerSelector)
	if err != nil {
		logger.WithError(err).Warn("cannot list cluster role bindings")
	}
	for _, crb := range crbs {
		if err := a.objGCQueueSet.RequeueEvent("", kubestate.NewResourceEvent(kubestate.ResourceUpdated, crb)); err != nil {
			logger.WithError(err).Warnf("failed to requeue gc event: %v", crb)
		}
	}

	crs, err := a.lister.RbacV1().ClusterRoleLister().List(ownerSelector)
	if err != nil {
		logger.WithError(err).Warn("cannot list cluster roles")
	}
	for _, cr := range crs {
		if err := a.objGCQueueSet.RequeueEvent("", kubestate.NewResourceEvent(kubestate.ResourceUpdated, cr)); err != nil {
			logger.WithError(err).Warnf("failed to requeue gc event: %v", cr)
		}
	}

	webhookSelector := labels.SelectorFromSet(ownerutil.OwnerLabel(clusterServiceVersion, v1alpha1.ClusterServiceVersionKind)).String()
	mWebhooks, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		logger.WithError(err).Warn("cannot list MutatingWebhookConfigurations")
	}
	for _, webhook := range mWebhooks.Items {
		w := webhook
		if err := a.objGCQueueSet.RequeueEvent("", kubestate.NewResourceEvent(kubestate.ResourceUpdated, &w)); err != nil {
			logger.WithError(err).Warnf("failed to requeue gc event: %v", webhook)
		}
	}

	vWebhooks, err := a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().List(context.TODO(), metav1.ListOptions{LabelSelector: webhookSelector})
	if err != nil {
		logger.WithError(err).Warn("cannot list ValidatingWebhookConfigurations")
	}
	for _, webhook := range vWebhooks.Items {
		w := webhook
		if err := a.objGCQueueSet.RequeueEvent("", kubestate.NewResourceEvent(kubestate.ResourceUpdated, &w)); err != nil {
			logger.WithError(err).Warnf("failed to requeue gc event: %v", webhook)
		}
	}
}

func (a *Operator) removeDanglingChildCSVs(csv *v1alpha1.ClusterServiceVersion) error {
	logger := a.logger.WithFields(logrus.Fields{
		"id":          queueinformer.NewLoopID(),
		"csv":         csv.GetName(),
		"namespace":   csv.GetNamespace(),
		"phase":       csv.Status.Phase,
		"labels":      csv.GetLabels(),
		"annotations": csv.GetAnnotations(),
	})

	if !csv.IsCopied() {
		logger.Debug("removeDanglingChild called on a parent. this is a no-op but should be avoided.")
		return nil
	}

	operatorNamespace, ok := csv.Annotations[v1.OperatorGroupNamespaceAnnotationKey]
	if !ok {
		logger.Debug("missing operator namespace annotation on copied CSV")
		return a.deleteChild(csv, logger)
	}

	logger = logger.WithField("parentNamespace", operatorNamespace)
	parent, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(operatorNamespace).Get(csv.GetName())
	if k8serrors.IsNotFound(err) || k8serrors.IsGone(err) || parent == nil {
		logger.Debug("deleting copied CSV since parent is missing")
		return a.deleteChild(csv, logger)
	}

	if parent.Status.Phase == v1alpha1.CSVPhaseFailed && parent.Status.Reason == v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
		logger.Debug("deleting copied CSV since parent has intersecting operatorgroup conflict")
		return a.deleteChild(csv, logger)
	}

	if annotations := parent.GetAnnotations(); annotations != nil {
		if !resolver.NewNamespaceSetFromString(annotations[v1.OperatorGroupTargetsAnnotationKey]).Contains(csv.GetNamespace()) {
			logger.WithField("parentTargets", annotations[v1.OperatorGroupTargetsAnnotationKey]).
				Debug("deleting copied CSV since parent no longer lists this as a target namespace")
			return a.deleteChild(csv, logger)
		}
	}

	if parent.GetNamespace() == csv.GetNamespace() {
		logger.Debug("deleting copied CSV since it has incorrect parent annotations")
		return a.deleteChild(csv, logger)
	}

	return nil
}

func (a *Operator) deleteChild(csv *v1alpha1.ClusterServiceVersion, logger *logrus.Entry) error {
	logger.Debug("gcing csv")
	return a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Delete(context.TODO(), csv.GetName(), *metav1.NewDeleteOptions(0))
}

// syncClusterServiceVersion is the method that gets called when we see a CSV event in the cluster
func (a *Operator) syncClusterServiceVersion(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		a.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	logger := a.logger.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})
	logger.Debug("syncing CSV")

	if a.csvNotification != nil {
		a.csvNotification.OnAddOrUpdate(clusterServiceVersion)
	}

	if clusterServiceVersion.IsCopied() {
		logger.Debug("skipping copied csv transition, schedule for gc check")
		if err := a.csvGCQueueSet.Requeue(clusterServiceVersion.GetNamespace(), clusterServiceVersion.GetName()); err != nil {
			logger.WithError(err).Warn("unable to requeue")
		}
		return
	}

	outCSV, syncError := a.transitionCSVState(*clusterServiceVersion)

	if outCSV == nil {
		return
	}

	// status changed, update CSV
	if !(outCSV.Status.LastUpdateTime.Equal(clusterServiceVersion.Status.LastUpdateTime) &&
		outCSV.Status.Phase == clusterServiceVersion.Status.Phase &&
		outCSV.Status.Reason == clusterServiceVersion.Status.Reason &&
		outCSV.Status.Message == clusterServiceVersion.Status.Message) {

		// Update CSV with status of transition. Log errors if we can't write them to the status.
		_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(outCSV.GetNamespace()).UpdateStatus(context.TODO(), outCSV, metav1.UpdateOptions{})
		if err != nil {
			updateErr := errors.New("error updating ClusterServiceVersion status: " + err.Error())
			if syncError == nil {
				logger.Info(updateErr)
				syncError = updateErr
			} else {
				syncError = fmt.Errorf("error transitioning ClusterServiceVersion: %s and error updating CSV status: %s", syncError, updateErr)
			}
		} else {
			metrics.EmitCSVMetric(clusterServiceVersion, outCSV)
		}
	}

	operatorGroup := a.operatorGroupFromAnnotations(logger, clusterServiceVersion)
	if operatorGroup == nil {
		logger.WithField("reason", "no operatorgroup found for active CSV").Debug("skipping potential RBAC creation in target namespaces")
		return
	}

	if len(operatorGroup.Status.Namespaces) == 1 && operatorGroup.Status.Namespaces[0] == operatorGroup.GetNamespace() {
		logger.Debug("skipping copy for OwnNamespace operatorgroup")
		return
	}
	// Ensure operator has access to targetnamespaces with cluster RBAC
	// (roles/rolebindings are checked for each target namespace in syncCopyCSV)
	if err := a.ensureRBACInTargetNamespace(clusterServiceVersion, operatorGroup); err != nil {
		logger.WithError(err).Info("couldn't ensure RBAC in target namespaces")
		syncError = err
	}

	if !outCSV.IsUncopiable() {
		if err := a.csvCopyQueueSet.Requeue(outCSV.GetNamespace(), outCSV.GetName()); err != nil {
			logger.WithError(err).Warn("unable to requeue")
		}
	}

	logger.Debug("done syncing CSV")
	return
}

func (a *Operator) syncCopyCSV(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		a.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	logger := a.logger.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})

	logger.Debug("copying CSV")

	operatorGroup := a.operatorGroupFromAnnotations(logger, clusterServiceVersion)
	if operatorGroup == nil {
		// since syncClusterServiceVersion is the only enqueuer, annotations should be present
		logger.WithField("reason", "no operatorgroup found for active CSV").Error("operatorgroup should have annotations")
		syncError = fmt.Errorf("operatorGroup for csv '%v' should have annotations", clusterServiceVersion.GetName())
		return
	}

	logger.WithFields(logrus.Fields{
		"targetNamespaces": strings.Join(operatorGroup.Status.Namespaces, ","),
	}).Debug("copying csv to targets")

	// Check if we need to do any copying / annotation for the operatorgroup
	if err := a.ensureCSVsInNamespaces(clusterServiceVersion, operatorGroup, resolver.NewNamespaceSet(operatorGroup.Status.Namespaces)); err != nil {
		logger.WithError(err).Info("couldn't copy CSV to target namespaces")
		syncError = err
	}

	return
}

func (a *Operator) syncGcCsv(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		a.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}
	if clusterServiceVersion.IsCopied() {
		syncError = a.removeDanglingChildCSVs(clusterServiceVersion)
		return
	}
	return
}

// operatorGroupFromAnnotations returns the OperatorGroup for the CSV only if the CSV is active one in the group
func (a *Operator) operatorGroupFromAnnotations(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) *v1.OperatorGroup {
	annotations := csv.GetAnnotations()

	// Not part of a group yet
	if annotations == nil {
		logger.Info("not part of any operatorgroup, no annotations")
		return nil
	}

	// Not in the OperatorGroup namespace
	if annotations[v1.OperatorGroupNamespaceAnnotationKey] != csv.GetNamespace() {
		logger.Info("not in operatorgroup namespace")
		return nil
	}

	operatorGroupName, ok := annotations[v1.OperatorGroupAnnotationKey]

	// No OperatorGroup annotation
	if !ok {
		logger.Info("no olm.operatorGroup annotation")
		return nil
	}

	logger = logger.WithField("operatorgroup", operatorGroupName)

	operatorGroup, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).Get(operatorGroupName)
	// OperatorGroup not found
	if err != nil {
		logger.Info("operatorgroup not found")
		return nil
	}

	targets, ok := annotations[v1.OperatorGroupTargetsAnnotationKey]

	// No target annotation
	if !ok {
		logger.Info("no olm.targetNamespaces annotation")
		return nil
	}

	// Target namespaces don't match
	if targets != operatorGroup.BuildTargetNamespaces() {
		logger.Info("olm.targetNamespaces annotation doesn't match operatorgroup status")
		return nil
	}

	return operatorGroup
}

func (a *Operator) operatorGroupForCSV(csv *v1alpha1.ClusterServiceVersion, logger *logrus.Entry) (*v1.OperatorGroup, error) {
	now := a.now()

	// Attempt to associate an OperatorGroup with the CSV.
	operatorGroups, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).List(labels.Everything())
	if err != nil {
		logger.Errorf("error occurred while attempting to associate csv with operatorgroup")
		return nil, err
	}
	var operatorGroup *v1.OperatorGroup

	switch len(operatorGroups) {
	case 0:
		err = fmt.Errorf("csv in namespace with no operatorgroups")
		logger.Warn(err)
		csv.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoOperatorGroup, err.Error(), now, a.recorder)
		return nil, err
	case 1:
		operatorGroup = operatorGroups[0]
		logger = logger.WithField("opgroup", operatorGroup.GetName())
		if a.operatorGroupAnnotationsDiffer(&csv.ObjectMeta, operatorGroup) {
			a.setOperatorGroupAnnotations(&csv.ObjectMeta, operatorGroup, true)
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(context.TODO(), csv, metav1.UpdateOptions{}); err != nil {
				logger.WithError(err).Warn("error adding operatorgroup annotations")
				return nil, err
			}
			if targetNamespaceList, err := a.getOperatorGroupTargets(operatorGroup); err == nil && len(targetNamespaceList) == 0 {
				csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoTargetNamespaces, "no targetNamespaces are matched operatorgroups namespace selection", now, a.recorder)
			}
			logger.Debug("CSV not in operatorgroup, requeuing operator group")
			// this requeue helps when an operator group has not annotated a CSV due to a permissions error
			// but the permissions issue has now been resolved
			if err := a.ogQueueSet.Requeue(operatorGroup.GetNamespace(), operatorGroup.GetName()); err != nil {
				return nil, err
			}
			return nil, nil
		}
		logger.Debug("csv in operatorgroup")
		return operatorGroup, nil
	default:
		err = fmt.Errorf("csv created in namespace with multiple operatorgroups, can't pick one automatically")
		logger.WithError(err).Warn("csv failed to become an operatorgroup member")
		if csv.Status.Reason != v1alpha1.CSVReasonTooManyOperatorGroups {
			csv.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonTooManyOperatorGroups, err.Error(), now, a.recorder)
		}
		return nil, err
	}
}

// transitionCSVState moves the CSV status state machine along based on the current value and the current cluster state.
// SyncError should be returned when an additional reconcile of the CSV might fix the issue.
func (a *Operator) transitionCSVState(in v1alpha1.ClusterServiceVersion) (out *v1alpha1.ClusterServiceVersion, syncError error) {
	logger := a.logger.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       in.GetName(),
		"namespace": in.GetNamespace(),
		"phase":     in.Status.Phase,
	})

	if in.Status.Reason == v1alpha1.CSVReasonComponentFailedNoRetry {
		// will change phase out of failed in the event of an intentional requeue
		logger.Debugf("skipping sync for CSV in failed-no-retry state")
		return
	}

	out = in.DeepCopy()
	now := a.now()

	operatorSurface, err := resolver.NewOperatorFromV1Alpha1CSV(out)
	if err != nil {
		// If the resolver is unable to retrieve the operator info from the CSV the CSV requires changes, a syncError should not be returned.
		logger.WithError(err).Warn("Unable to retrieve operator information from CSV")
		return
	}

	// Ensure required and provided API labels
	if labelSets, err := a.apiLabeler.LabelSetsFor(operatorSurface); err != nil {
		logger.WithError(err).Warn("couldn't create label set")
	} else if len(labelSets) > 0 {
		updated, err := a.ensureLabels(out, labelSets...)
		if err != nil {
			logger.WithError(err).Warn("issue ensuring csv api labels")
			syncError = err
			return
		}
		// Update the underlying value of out to preserve changes
		*out = *updated
	}

	// Verify CSV operatorgroup (and update annotations if needed)
	operatorGroup, err := a.operatorGroupForCSV(out, logger)
	if operatorGroup == nil {
		// when err is nil, we still want to exit, but we don't want to re-add the csv ratelimited to the queue
		syncError = err
		logger.WithError(err).Info("operatorgroup incorrect")
		return
	}

	modeSet, err := v1alpha1.NewInstallModeSet(out.Spec.InstallModes)
	if err != nil {
		logger.WithError(err).Warn("csv has invalid installmodes")
		out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidInstallModes, err.Error(), now, a.recorder)
		return
	}

	// Check if the CSV supports its operatorgroup's selected namespaces
	targets, ok := out.GetAnnotations()[v1.OperatorGroupTargetsAnnotationKey]
	if ok {
		namespaces := strings.Split(targets, ",")

		if err := modeSet.Supports(out.GetNamespace(), namespaces); err != nil {
			logger.WithField("reason", err.Error()).Info("installmodeset does not support operatorgroups namespace selection")
			out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonUnsupportedOperatorGroup, err.Error(), now, a.recorder)
			return
		}
	} else {
		logger.Info("csv missing olm.targetNamespaces annotation")
		out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoTargetNamespaces, "csv missing olm.targetNamespaces annotation", now, a.recorder)
		return
	}

	// Check for intersecting provided APIs in intersecting OperatorGroups
	allGroups, err := a.lister.OperatorsV1().OperatorGroupLister().List(labels.Everything())
	otherGroups := make([]v1.OperatorGroup, 0, len(allGroups))
	for _, g := range allGroups {
		if g.GetName() != operatorGroup.GetName() || g.GetNamespace() != operatorGroup.GetNamespace() {
			otherGroups = append(otherGroups, *g)
		}
	}

	groupSurface := resolver.NewOperatorGroup(operatorGroup)
	otherGroupSurfaces := resolver.NewOperatorGroupSurfaces(otherGroups...)
	providedAPIs := operatorSurface.ProvidedAPIs().StripPlural()

	switch result := a.apiReconciler.Reconcile(providedAPIs, groupSurface, otherGroupSurfaces...); {
	case operatorGroup.Spec.StaticProvidedAPIs && (result == resolver.AddAPIs || result == resolver.RemoveAPIs):
		// Transition the CSV to FAILED with status reason "CannotModifyStaticOperatorGroupProvidedAPIs"
		if out.Status.Reason != v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.WithField("apis", providedAPIs).Warn("cannot modify provided apis of static provided api operatorgroup")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs, "static provided api operatorgroup cannot be modified by these apis", now, a.recorder)
			a.cleanupCSVDeployments(logger, out)
		}
		return
	case result == resolver.APIConflict:
		// Transition the CSV to FAILED with status reason "InterOperatorGroupOwnerConflict"
		if out.Status.Reason != v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.WithField("apis", providedAPIs).Warn("intersecting operatorgroups provide the same apis")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, "intersecting operatorgroups provide the same apis", now, a.recorder)
			a.cleanupCSVDeployments(logger, out)
		}
		return
	case result == resolver.AddAPIs:
		// Add the CSV's provided APIs to its OperatorGroup's annotation
		logger.WithField("apis", providedAPIs).Debug("adding csv provided apis to operatorgroup")
		union := groupSurface.ProvidedAPIs().Union(providedAPIs)
		unionedAnnotations := operatorGroup.GetAnnotations()
		if unionedAnnotations == nil {
			unionedAnnotations = make(map[string]string)
		}
		if unionedAnnotations[v1.OperatorGroupProvidedAPIsAnnotationKey] == union.String() {
			// resolver may think apis need adding with invalid input, so continue when there's no work
			// to be done so that the CSV can progress far enough to get requirements checked
			a.logger.Debug("operator group annotations up to date, continuing")
			break
		}
		unionedAnnotations[v1.OperatorGroupProvidedAPIsAnnotationKey] = union.String()
		operatorGroup.SetAnnotations(unionedAnnotations)
		if _, err := a.client.OperatorsV1().OperatorGroups(operatorGroup.GetNamespace()).Update(context.TODO(), operatorGroup, metav1.UpdateOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", v1.OperatorGroupProvidedAPIsAnnotationKey, err)
		}
		if err := a.csvQueueSet.Requeue(out.GetNamespace(), out.GetName()); err != nil {
			a.logger.WithError(err).Warn("unable to requeue")
		}
		return
	case result == resolver.RemoveAPIs:
		// Remove the CSV's provided APIs from its OperatorGroup's annotation
		logger.WithField("apis", providedAPIs).Debug("removing csv provided apis from operatorgroup")
		difference := groupSurface.ProvidedAPIs().Difference(providedAPIs)
		if diffedAnnotations := operatorGroup.GetAnnotations(); diffedAnnotations != nil {
			diffedAnnotations[v1.OperatorGroupProvidedAPIsAnnotationKey] = difference.String()
			operatorGroup.SetAnnotations(diffedAnnotations)
			if _, err := a.client.OperatorsV1().OperatorGroups(operatorGroup.GetNamespace()).Update(context.TODO(), operatorGroup, metav1.UpdateOptions{}); err != nil && !k8serrors.IsNotFound(err) {
				syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", v1.OperatorGroupProvidedAPIsAnnotationKey, err)
			}
		}
		if err := a.csvQueueSet.Requeue(out.GetNamespace(), out.GetName()); err != nil {
			a.logger.WithError(err).Warn("unable to requeue")
		}
		return
	default:
		logger.WithField("apis", providedAPIs).Debug("no intersecting operatorgroups provide the same apis")
	}

	switch out.Status.Phase {
	case v1alpha1.CSVPhaseNone:
		logger.Info("scheduling ClusterServiceVersion for requirement verification")
		out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "requirements not yet checked", now, a.recorder)
	case v1alpha1.CSVPhasePending:
		// Check previous version's Upgradeable condition
		replacedCSV := a.isReplacing(out)
		if replacedCSV != nil {
			operatorUpgradeable, condErr := a.isOperatorUpgradeable(replacedCSV)
			if !operatorUpgradeable {
				out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonOperatorConditionNotUpgradeable, fmt.Sprintf("operator is not upgradeable: %s", condErr), now, a.recorder)
				return
			}
		}
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil {
			// TODO: account for Bad Rule as well
			logger.Info("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, a.recorder)
			return
		}
		out.SetRequirementStatus(statuses)

		// Check if we need to requeue the previous
		if prev := a.isReplacing(out); prev != nil {
			if prev.Status.Phase == v1alpha1.CSVPhaseSucceeded {
				if err := a.csvQueueSet.Requeue(prev.GetNamespace(), prev.GetName()); err != nil {
					a.logger.WithError(err).Warn("error requeueing previous")
				}
			}
		}

		if !met {
			logger.Info("requirements were not met")
			out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, "one or more requirements couldn't be found", now, a.recorder)
			syncError = ErrRequirementsNotMet
			return
		}

		// Create a map to track unique names
		webhookNames := map[string]struct{}{}
		// Check if Webhooks have valid rules and unique names
		// TODO: Move this to validating library
		for _, desc := range out.Spec.WebhookDefinitions {
			_, present := webhookNames[desc.GenerateName]
			if present {
				logger.WithError(fmt.Errorf("Repeated WebhookDescription name %s", desc.GenerateName)).Warn("CSV is invalid")
				out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidWebhookDescription, "CSV contains repeated WebhookDescription name", now, a.recorder)
				return
			}
			webhookNames[desc.GenerateName] = struct{}{}
			if err = install.ValidWebhookRules(desc.Rules); err != nil {
				logger.WithError(err).Warnf("WebhookDescription %s includes invalid rules", desc.GenerateName)
				out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidWebhookDescription, err.Error(), now, a.recorder)
				return
			}
		}

		// Check for CRD ownership conflicts
		if syncError = a.crdOwnerConflicts(out, a.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny)); syncError != nil {
			if syncError == ErrCRDOwnerConflict {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, syncError.Error(), now, a.recorder)
			}
			return
		}

		// Check for APIServices ownership conflicts
		if syncError = a.apiServiceOwnerConflicts(out); syncError != nil {
			if syncError == ErrAPIServiceOwnerConflict {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, syncError.Error(), now, a.recorder)
			}
			return
		}

		// Check if we're not ready to install part of the replacement chain yet
		if prev := a.isReplacing(out); prev != nil {
			if prev.Status.Phase != v1alpha1.CSVPhaseReplacing {
				logger.WithError(fmt.Errorf("CSV being replaced is in phase %s instead of %s", prev.Status.Phase, v1alpha1.CSVPhaseReplacing)).Warn("Unable to replace previous CSV")
				return
			}
		}

		logger.Info("scheduling ClusterServiceVersion for install")
		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstallReady, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting install", now, a.recorder)
	case v1alpha1.CSVPhaseInstallReady:
		installer, strategy := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		if syncError = installer.Install(strategy); syncError != nil {
			if install.IsErrorUnrecoverable(syncError) {
				logger.Infof("Setting CSV reason to failed without retry: %v", syncError)
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailedNoRetry, fmt.Sprintf("install strategy failed: %s", syncError), now, a.recorder)
				return
			}
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", syncError), now, a.recorder)
			return
		}

		if out.HasCAResources() {
			now := metav1.Now()
			expiration := now.Add(install.DefaultCertValidFor)
			rotateAt := expiration.Add(-1 * install.DefaultCertMinFresh)
			rotateTime := metav1.NewTime(rotateAt)
			out.Status.CertsLastUpdated = &now
			out.Status.CertsRotateAt = &rotateTime
		}

		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonInstallSuccessful, "waiting for install components to report healthy", now, a.recorder)
		err := a.csvQueueSet.Requeue(out.GetNamespace(), out.GetName())
		if err != nil {
			a.logger.Warn(err.Error())
		}
		return

	case v1alpha1.CSVPhaseInstalling:
		installer, strategy := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		strategy, err := a.updateDeploymentSpecsWithApiServiceData(out, strategy)
		if err != nil {
			logger.WithError(err).Debug("Unable to calculate expected deployment")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall, "calculated deployment install is bad", now, a.recorder)
			return
		}
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonWaiting); installErr != nil {
			// Re-sync if kube-apiserver was unavailable
			if k8serrors.IsServiceUnavailable(installErr) {
				logger.WithError(installErr).Info("could not update install status")
				syncError = installErr
				return
			}
			// Set phase to failed if it's been a long time since the last transition (5 minutes)
			if out.Status.LastTransitionTime != nil && a.now().Sub(out.Status.LastTransitionTime.Time) >= 5*time.Minute {
				logger.Warn("install timed out")
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install timeout"), now, a.recorder)
				return
			}
		}
		logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Infof("install strategy successful")

	case v1alpha1.CSVPhaseSucceeded:
		// Check if the current CSV is being replaced, return with replacing status if so
		if err := a.checkReplacementsAndUpdateStatus(out); err != nil {
			logger.WithError(err).Info("replacement check")
			return
		}

		installer, strategy := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		// Check if any generated resources are missing
		if err := a.checkAPIServiceResources(out, certs.PEMSHA256); err != nil {
			logger.WithError(err).Debug("API Resources are unavailable")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonAPIServiceResourceIssue, err.Error(), now, a.recorder)
			return
		}

		// Check if it's time to refresh owned APIService certs
		if install.ShouldRotateCerts(out) {
			logger.Debug("CSV owns resources that require a cert refresh")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "CSV owns resources that require a cert refresh", now, a.recorder)
			return
		}

		// Ensure requirements are still present
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil {
			logger.Info("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, a.recorder)
			return
		} else if !met {
			logger.Debug("CSV Requirements are no longer met")
			out.SetRequirementStatus(statuses)
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonRequirementsNotMet, fmt.Sprintf("requirements no longer met"), now, a.recorder)
			return
		}

		// Check install status
		strategy, err = a.updateDeploymentSpecsWithApiServiceData(out, strategy)
		if err != nil {
			logger.WithError(err).Debug("Unable to calculate expected deployment")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall, "calculated deployment install is bad", now, a.recorder)
			return
		}
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentUnhealthy); installErr != nil {
			// Re-sync if kube-apiserver was unavailable
			if k8serrors.IsServiceUnavailable(installErr) {
				logger.WithError(installErr).Info("could not update install status")
				syncError = installErr
				return
			}
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Warnf("unhealthy component: %s", installErr)
			return
		}

		// Ensure cluster roles exist for using provided apis
		if err := a.ensureClusterRolesForCSV(out); err != nil {
			logger.WithError(err).Info("couldn't ensure clusterroles for provided api types")
			syncError = err
			return
		}

	case v1alpha1.CSVPhaseFailed:
		installer, strategy := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		// Check if failed due to unsupported InstallModes
		if out.Status.Reason == v1alpha1.CSVReasonNoTargetNamespaces ||
			out.Status.Reason == v1alpha1.CSVReasonNoOperatorGroup ||
			out.Status.Reason == v1alpha1.CSVReasonTooManyOperatorGroups ||
			out.Status.Reason == v1alpha1.CSVReasonUnsupportedOperatorGroup {
			logger.Info("InstallModes now support target namespaces. Transitioning to Pending...")
			// Check occurred before switch, safe to transition to pending
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "InstallModes now support target namespaces", now, a.recorder)
			return
		}

		// Check if failed due to conflicting OperatorGroups
		if out.Status.Reason == v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.Info("OperatorGroup no longer intersecting with conflicting owner. Transitioning to Pending...")
			// Check occurred before switch, safe to transition to pending
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "OperatorGroup no longer intersecting with conflicting owner", now, a.recorder)
			return
		}

		// Check if failed due to an attempt to modify a static OperatorGroup
		if out.Status.Reason == v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs {
			logger.Info("static OperatorGroup and intersecting groups now support providedAPIs...")
			// Check occurred before switch, safe to transition to pending
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "static OperatorGroup and intersecting groups now support providedAPIs", now, a.recorder)
			return
		}

		// Check if requirements exist
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil && out.Status.Reason != v1alpha1.CSVReasonInvalidStrategy {
			logger.Warn("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, a.recorder)
			return
		} else if !met {
			logger.Debug("CSV Requirements are not met")
			out.SetRequirementStatus(statuses)
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, fmt.Sprintf("requirements not met"), now, a.recorder)
			return
		}

		// Check if any generated resources are missing and that OLM can action on them
		if err := a.checkAPIServiceResources(out, certs.PEMSHA256); err != nil {
			if a.apiServiceResourceErrorActionable(err) {
				logger.WithError(err).Debug("API Resources are unavailable")
				// Check if API services are adoptable. If not, keep CSV as Failed state
				out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonAPIServiceResourcesNeedReinstall, err.Error(), now, a.recorder)
			}
			return
		}

		// Check if it's time to refresh owned APIService certs
		if install.ShouldRotateCerts(out) {
			logger.Debug("CSV owns resources that require a cert refresh")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "owned APIServices need cert refresh", now, a.recorder)
			return
		}

		// Check install status
		strategy, err = a.updateDeploymentSpecsWithApiServiceData(out, strategy)
		if err != nil {
			logger.WithError(err).Debug("Unable to calculate expected deployment")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall, "calculated deployment install is bad", now, a.recorder)
			return
		}
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall); installErr != nil {
			// Re-sync if kube-apiserver was unavailable
			if k8serrors.IsServiceUnavailable(installErr) {
				logger.WithError(installErr).Info("could not update install status")
				syncError = installErr
				return
			}
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Warnf("needs reinstall: %s", installErr)
		}

	case v1alpha1.CSVPhaseReplacing:
		// determine CSVs that are safe to delete by finding a replacement chain to a CSV that's running
		// since we don't know what order we'll process replacements, we have to guard against breaking that chain

		// if this isn't the earliest csv in a replacement chain, skip gc.
		// marking an intermediate for deletion will break the replacement chain
		if prev := a.isReplacing(out); prev != nil {
			logger.Debugf("being replaced, but is not a leaf. skipping gc")
			return
		}

		// If there is a succeeded replacement, mark this for deletion
		if next := a.isBeingReplaced(out, a.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny)); next != nil {
			if next.Status.Phase == v1alpha1.CSVPhaseSucceeded {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseDeleting, v1alpha1.CSVReasonReplaced, "has been replaced by a newer ClusterServiceVersion that has successfully installed.", now, a.recorder)
			} else {
				// If there's a replacement, but it's not yet succeeded, requeue both (this is an active replacement)
				if err := a.csvQueueSet.Requeue(next.GetNamespace(), next.GetName()); err != nil {
					a.logger.Warn(err.Error())
				}
				if err := a.csvQueueSet.Requeue(out.GetNamespace(), out.GetName()); err != nil {
					a.logger.Warn(err.Error())
				}
			}
		} else {
			syncError = fmt.Errorf("marked as replacement, but no replacement CSV found in cluster")
		}
	case v1alpha1.CSVPhaseDeleting:
		syncError = a.client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Delete(context.TODO(), out.GetName(), *metav1.NewDeleteOptions(0))
		if syncError != nil {
			logger.Debugf("unable to get delete csv marked for deletion: %s", syncError.Error())
		}
	}

	return
}

// csvSet gathers all CSVs in the given namespace into a map keyed by CSV name; if metav1.NamespaceAll gets the set across all namespaces
func (a *Operator) csvSet(namespace string, phase v1alpha1.ClusterServiceVersionPhase) map[string]*v1alpha1.ClusterServiceVersion {
	return a.csvSetGenerator.WithNamespace(namespace, phase)
}

// checkReplacementsAndUpdateStatus returns an error if we can find a newer CSV and sets the status if so
func (a *Operator) checkReplacementsAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) error {
	if csv.Status.Phase == v1alpha1.CSVPhaseReplacing || csv.Status.Phase == v1alpha1.CSVPhaseDeleting {
		return nil
	}
	if replacement := a.isBeingReplaced(csv, a.csvSet(csv.GetNamespace(), v1alpha1.CSVPhaseAny)); replacement != nil {
		a.logger.Infof("newer csv replacing %s, no-op", csv.GetName())
		msg := fmt.Sprintf("being replaced by csv: %s", replacement.GetName())
		csv.SetPhaseWithEvent(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVReasonBeingReplaced, msg, a.now(), a.recorder)
		metrics.CSVUpgradeCount.Inc()

		return fmt.Errorf("replacing")
	}
	return nil
}

func (a *Operator) updateInstallStatus(csv *v1alpha1.ClusterServiceVersion, installer install.StrategyInstaller, strategy install.Strategy, requeuePhase v1alpha1.ClusterServiceVersionPhase, requeueConditionReason v1alpha1.ConditionReason) error {
	strategyInstalled, strategyErr := installer.CheckInstalled(strategy)
	now := a.now()

	if strategyErr != nil {
		a.logger.WithError(strategyErr).Debug("operator not installed")
	}

	apiServicesInstalled, apiServiceErr := a.areAPIServicesAvailable(csv)
	webhooksInstalled, webhookErr := a.areWebhooksAvailable(csv)

	if strategyInstalled && apiServicesInstalled && webhooksInstalled {
		// if there's no error, we're successfully running
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors", now, a.recorder)
		return nil
	}

	if err := findFirstError(k8serrors.IsServiceUnavailable, strategyErr, apiServiceErr, webhookErr); err != nil {
		return err
	}

	// installcheck determined we can't progress (e.g. deployment failed to come up in time)
	if install.IsErrorUnrecoverable(strategyErr) {
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install failed: %s", strategyErr), now, a.recorder)
		return strategyErr
	}

	if apiServiceErr != nil {
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonAPIServiceInstallFailed, fmt.Sprintf("APIService install failed: %s", apiServiceErr), now, a.recorder)
		return apiServiceErr
	}

	if !apiServicesInstalled {
		csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, fmt.Sprintf("APIServices not installed"), now, a.recorder)
		if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
			a.logger.Warn(err.Error())
		}

		return fmt.Errorf("APIServices not installed")
	}

	if !webhooksInstalled || webhookErr != nil {
		msg := "Webhooks not installed"
		csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, fmt.Sprintf(msg), now, a.recorder)
		if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
			a.logger.Warn(err.Error())
		}

		return fmt.Errorf(msg)
	}

	if strategyErr != nil {
		reasonForError := install.ReasonForError(strategyErr)
		if reasonForError == install.StrategyErrDeploymentUpdated || reasonForError == install.StrategyErrReasonAnnotationsMissing {
			csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseInstallReady, requeueConditionReason, fmt.Sprintf("installing: %s", strategyErr), now, a.recorder)
		} else {
			csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, fmt.Sprintf("installing: %s", strategyErr), now, a.recorder)
		}
		if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
			a.logger.Warn(err.Error())
		}

		return strategyErr
	}

	return nil
}

func findFirstError(f func(error) bool, errs ...error) error {
	for _, err := range errs {
		if f(err) {
			return err
		}
	}
	return nil
}

// parseStrategiesAndUpdateStatus returns a StrategyInstaller and a Strategy for a CSV if it can, else it sets a status on the CSV and returns
func (a *Operator) parseStrategiesAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) (install.StrategyInstaller, install.Strategy) {
	strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		csv.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err), a.now(), a.recorder)
		return nil, nil
	}

	previousCSV := a.isReplacing(csv)
	var previousStrategy install.Strategy
	if previousCSV != nil {
		err = a.csvQueueSet.Requeue(previousCSV.Namespace, previousCSV.Name)
		if err != nil {
			a.logger.Warn(err.Error())
		}

		previousStrategy, err = a.resolver.UnmarshalStrategy(previousCSV.Spec.InstallStrategy)
		if err != nil {
			previousStrategy = nil
		}
	}

	// If an admin has specified a service account to the operator group
	// associated with the namespace then we should use a scoped client that is
	// bound to the service account.
	querierFunc := a.serviceAccountQuerier.NamespaceQuerier(csv.GetNamespace())
	attenuate, err := a.clientAttenuator.AttenuateToServiceAccount(querierFunc)
	if err != nil {
		a.logger.Errorf("failed to get a client for operator deployment- %v", err)
		return nil, nil
	}
	kubeclient, err := a.clientFactory.WithConfigTransformer(attenuate).NewOperatorClient()

	strName := strategy.GetStrategyName()
	installer := a.resolver.InstallerForStrategy(strName, kubeclient, a.lister, csv, csv.GetAnnotations(), csv.GetAllAPIServiceDescriptions(), csv.Spec.WebhookDefinitions, previousStrategy)
	return installer, strategy
}

func (a *Operator) crdOwnerConflicts(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) error {
	csvsInChain := a.getReplacementChain(in, csvsInNamespace)
	// find csvs in the namespace that are not part of the replacement chain
	for name, csv := range csvsInNamespace {
		if _, ok := csvsInChain[name]; ok {
			continue
		}
		for _, crd := range in.Spec.CustomResourceDefinitions.Owned {
			if name != in.GetName() && csv.OwnsCRD(crd.Name) {
				return ErrCRDOwnerConflict
			}
		}
	}

	return nil
}

func (a *Operator) getReplacementChain(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) map[string]struct{} {
	current := in.GetName()
	csvsInChain := map[string]struct{}{
		current: {},
	}

	replacement := func(csvName string) *string {
		for _, csv := range csvsInNamespace {
			if csv.Spec.Replaces == csvName {
				name := csv.GetName()
				return &name
			}
		}
		return nil
	}

	replaces := func(replaces string) *string {
		for _, csv := range csvsInNamespace {
			name := csv.GetName()
			if name == replaces {
				rep := csv.Spec.Replaces
				return &rep
			}
		}
		return nil
	}

	next := replacement(current)
	for next != nil {
		if _, ok := csvsInChain[*next]; ok {
			break // cycle
		}
		csvsInChain[*next] = struct{}{}
		current = *next
		next = replacement(current)
	}

	current = in.Spec.Replaces
	prev := replaces(current)
	if prev != nil {
		csvsInChain[current] = struct{}{}
	}
	for prev != nil && *prev != "" {
		if _, ok := csvsInChain[*prev]; ok {
			break // cycle
		}
		current = *prev
		csvsInChain[current] = struct{}{}
		prev = replaces(current)
	}
	return csvsInChain
}

func (a *Operator) apiServiceOwnerConflicts(csv *v1alpha1.ClusterServiceVersion) error {
	for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
		// Check if the APIService exists
		apiService, err := a.lister.APIRegistrationV1().APIServiceLister().Get(desc.GetName())
		if err != nil && !k8serrors.IsNotFound(err) && !k8serrors.IsGone(err) {
			return err
		}

		if apiService == nil {
			continue
		}

		adoptable, err := install.IsAPIServiceAdoptable(a.lister, csv, apiService)
		if err != nil {
			a.logger.WithFields(log.Fields{"obj": "apiService", "labels": apiService.GetLabels()}).Errorf("adoption check failed - %v", err)
		}

		if !adoptable {
			return ErrAPIServiceOwnerConflict
		}
	}

	return nil
}

func (a *Operator) isBeingReplaced(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) (replacedBy *v1alpha1.ClusterServiceVersion) {
	return a.csvReplaceFinder.IsBeingReplaced(in, csvsInNamespace)
}

func (a *Operator) isReplacing(in *v1alpha1.ClusterServiceVersion) *v1alpha1.ClusterServiceVersion {
	return a.csvReplaceFinder.IsReplacing(in)
}

func (a *Operator) handleDeletion(obj interface{}) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}

		metaObj, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a metav1.Object %#v", obj))
			return
		}
	}
	logger := a.logger.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
		"self":      metaObj.GetSelfLink(),
	})
	logger.Debug("handling resource deletion")

	logger.Debug("requeueing owner csvs due to deletion")
	a.requeueOwnerCSVs(metaObj)

	// Requeue CSVs with provided and required labels (for CRDs)
	if labelSets, err := a.apiLabeler.LabelSetsFor(metaObj); err != nil {
		logger.WithError(err).Warn("couldn't create label set")
	} else if len(labelSets) > 0 {
		logger.Debug("requeueing providing/requiring csvs due to deletion")
		a.requeueCSVsByLabelSet(logger, labelSets...)
	}
}

func (a *Operator) requeueCSVsByLabelSet(logger *logrus.Entry, labelSets ...labels.Set) {
	keys, err := index.LabelIndexKeys(a.csvIndexers, labelSets...)
	if err != nil {
		logger.WithError(err).Debug("issue getting csvs by label index")
		return
	}

	for _, key := range keys {
		if err := a.csvQueueSet.RequeueByKey(key); err != nil {
			logger.WithError(err).Debug("cannot requeue requiring/providing csv")
		} else {
			logger.WithField("key", key).Debug("csv successfully requeued on crd change")
		}
	}
}

func (a *Operator) requeueOwnerCSVs(ownee metav1.Object) {
	logger := a.logger.WithFields(logrus.Fields{
		"ownee":     ownee.GetName(),
		"selflink":  ownee.GetSelfLink(),
		"namespace": ownee.GetNamespace(),
	})

	// Attempt to requeue CSV owners in the same namespace as the object
	owners := ownerutil.GetOwnersByKind(ownee, v1alpha1.ClusterServiceVersionKind)
	if len(owners) > 0 && ownee.GetNamespace() != metav1.NamespaceAll {
		for _, ownerCSV := range owners {
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ownee.GetNamespace()).Get(ownerCSV.Name)
			if k8serrors.IsNotFound(err) {
				logger.Debugf("skipping requeue since CSV %v is not in cache", ownerCSV.Name)
				continue
			}
			// Since cross-namespace CSVs can't exist we're guaranteed the owner will be in the same namespace
			err = a.csvQueueSet.Requeue(ownee.GetNamespace(), ownerCSV.Name)
			if err != nil {
				logger.Warn(err.Error())
			}
		}
		return
	}

	// Requeue owners based on labels
	if name, ns, ok := ownerutil.GetOwnerByKindLabel(ownee, v1alpha1.ClusterServiceVersionKind); ok {
		_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
		if k8serrors.IsNotFound(err) {
			logger.Debugf("skipping requeue since CSV %v is not in cache", name)
			return
		}

		err = a.csvQueueSet.Requeue(ns, name)
		if err != nil {
			logger.Warn(err.Error())
		}
	}
}

func (a *Operator) cleanupCSVDeployments(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) {
	// Extract the InstallStrategy for the deployment
	strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		logger.Warn("could not parse install strategy while cleaning up CSV deployment")
		return
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		logger.Warnf("could not cast install strategy as type %T", strategyDetailsDeployment)
		return
	}

	// Delete deployments
	for _, spec := range strategyDetailsDeployment.DeploymentSpecs {
		logger := logger.WithField("deployment", spec.Name)
		logger.Debug("cleaning up CSV deployment")
		if err := a.opClient.DeleteDeployment(csv.GetNamespace(), spec.Name, &metav1.DeleteOptions{}); err != nil {
			logger.WithField("err", err).Warn("error cleaning up CSV deployment")
		}
	}
}

// ensureLabels merges a label set with a CSV's labels and attempts to update the CSV if the merged set differs from the CSV's original labels.
func (a *Operator) ensureLabels(in *v1alpha1.ClusterServiceVersion, labelSets ...labels.Set) (*v1alpha1.ClusterServiceVersion, error) {
	csvLabelSet := labels.Set(in.GetLabels())
	merged := csvLabelSet
	for _, labelSet := range labelSets {
		merged = labels.Merge(merged, labelSet)
	}
	if labels.Equals(csvLabelSet, merged) {
		return in, nil
	}

	logger := a.logger.WithFields(logrus.Fields{"labels": merged, "csv": in.GetName(), "ns": in.GetNamespace()})
	logger.Info("updated labels")

	out := in.DeepCopy()
	out.SetLabels(merged)
	out, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Update(context.TODO(), out, metav1.UpdateOptions{})
	return out, err
}
