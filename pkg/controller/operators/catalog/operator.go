package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	errorwrap "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/connectivity"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	extinf "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/api/pkg/operators/reference"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog/subscription"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/grpc"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	index "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/index"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
	sharedtime "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/time"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

const (
	crdKind                = "CustomResourceDefinition"
	secretKind             = "Secret"
	clusterRoleKind        = "ClusterRole"
	clusterRoleBindingKind = "ClusterRoleBinding"
	configMapKind          = "ConfigMap"
	csvKind                = "ClusterServiceVersion"
	serviceAccountKind     = "ServiceAccount"
	serviceKind            = "Service"
	roleKind               = "Role"
	roleBindingKind        = "RoleBinding"
	generatedByKey         = "olm.generated-by"
	maxInstallPlanCount    = 5
	maxDeletesPerSweep     = 5
	RegistryFieldManager   = "olm.registry"
)

// Operator represents a Kubernetes operator that executes InstallPlans by
// resolving dependencies in a catalog.
type Operator struct {
	queueinformer.Operator

	logger                   *logrus.Logger
	clock                    utilclock.Clock
	opClient                 operatorclient.ClientInterface
	client                   versioned.Interface
	dynamicClient            dynamic.Interface
	lister                   operatorlister.OperatorLister
	catsrcQueueSet           *queueinformer.ResourceQueueSet
	subQueueSet              *queueinformer.ResourceQueueSet
	ipQueueSet               *queueinformer.ResourceQueueSet
	nsResolveQueue           workqueue.RateLimitingInterface
	namespace                string
	recorder                 record.EventRecorder
	sources                  *grpc.SourceStore
	sourcesLastUpdate        sharedtime.SharedTime
	resolver                 resolver.StepResolver
	reconciler               reconciler.RegistryReconcilerFactory
	csvProvidedAPIsIndexer   map[string]cache.Indexer
	catalogSubscriberIndexer map[string]cache.Indexer
	clientAttenuator         *scoped.ClientAttenuator
	serviceAccountQuerier    *scoped.UserDefinedServiceAccountQuerier
	bundleUnpacker           bundle.Unpacker
}

type CatalogSourceSyncFunc func(logger *logrus.Entry, in *v1alpha1.CatalogSource) (out *v1alpha1.CatalogSource, continueSync bool, syncError error)

// NewOperator creates a new Catalog Operator.
func NewOperator(ctx context.Context, kubeconfigPath string, clock utilclock.Clock, logger *logrus.Logger, resync time.Duration, configmapRegistryImage, utilImage string, operatorNamespace string, scheme *runtime.Scheme) (*Operator, error) {
	resyncPeriod := queueinformer.ResyncWithJitter(resync, 0.2)
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create a new client for OLM types (CRs)
	crClient, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Create a new client for dynamic types (CRs)
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Create a new queueinformer-based operator.
	opClient := operatorclient.NewClientFromConfig(kubeconfigPath, logger)
	queueOperator, err := queueinformer.NewOperator(opClient.KubernetesInterface().Discovery(), queueinformer.WithOperatorLogger(logger))
	if err != nil {
		return nil, err
	}

	// Create an OperatorLister
	lister := operatorlister.NewLister()

	// eventRecorder can emit events
	eventRecorder, err := event.NewRecorder(opClient.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
	if err != nil {
		return nil, err
	}

	ssaClient, err := controllerclient.NewForConfig(config, scheme, RegistryFieldManager)
	if err != nil {
		return nil, err
	}

	// Allocate the new instance of an Operator.
	op := &Operator{
		Operator:                 queueOperator,
		logger:                   logger,
		clock:                    clock,
		opClient:                 opClient,
		dynamicClient:            dynamicClient,
		client:                   crClient,
		lister:                   lister,
		namespace:                operatorNamespace,
		recorder:                 eventRecorder,
		catsrcQueueSet:           queueinformer.NewEmptyResourceQueueSet(),
		subQueueSet:              queueinformer.NewEmptyResourceQueueSet(),
		ipQueueSet:               queueinformer.NewEmptyResourceQueueSet(),
		csvProvidedAPIsIndexer:   map[string]cache.Indexer{},
		catalogSubscriberIndexer: map[string]cache.Indexer{},
		serviceAccountQuerier:    scoped.NewUserDefinedServiceAccountQuerier(logger, crClient),
		clientAttenuator:         scoped.NewClientAttenuator(logger, config, opClient, crClient, dynamicClient),
	}
	op.sources = grpc.NewSourceStore(logger, 10*time.Second, 10*time.Minute, op.syncSourceState)
	op.reconciler = reconciler.NewRegistryReconcilerFactory(lister, opClient, configmapRegistryImage, op.now, ssaClient)
	res := resolver.NewOperatorStepResolver(lister, crClient, opClient.KubernetesInterface(), operatorNamespace, op.sources, logger)
	op.resolver = resolver.NewInstrumentedResolver(res, metrics.RegisterDependencyResolutionSuccess, metrics.RegisterDependencyResolutionFailure)

	// Wire OLM CR sharedIndexInformers
	crInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(op.client, resyncPeriod())

	// Wire CSVs
	csvInformer := crInformerFactory.Operators().V1alpha1().ClusterServiceVersions()
	op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(metav1.NamespaceAll, csvInformer.Lister())
	if err := op.RegisterInformer(csvInformer.Informer()); err != nil {
		return nil, err
	}

	if err := csvInformer.Informer().AddIndexers(cache.Indexers{index.ProvidedAPIsIndexFuncKey: index.ProvidedAPIsIndexFunc}); err != nil {
		return nil, err
	}
	csvIndexer := csvInformer.Informer().GetIndexer()
	op.csvProvidedAPIsIndexer[metav1.NamespaceAll] = csvIndexer

	// TODO: Add namespace resolve sync

	// Wire InstallPlans
	ipInformer := crInformerFactory.Operators().V1alpha1().InstallPlans()
	op.lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, ipInformer.Lister())
	ipQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ips")
	op.ipQueueSet.Set(metav1.NamespaceAll, ipQueue)
	ipQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithMetricsProvider(metrics.NewMetricsInstallPlan(op.lister.OperatorsV1alpha1().InstallPlanLister())),
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(ipQueue),
		queueinformer.WithInformer(ipInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncInstallPlans).ToSyncer()),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(ipQueueInformer); err != nil {
		return nil, err
	}

	// Wire CatalogSources
	catsrcInformer := crInformerFactory.Operators().V1alpha1().CatalogSources()
	op.lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catsrcInformer.Lister())
	catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catsrcs")
	op.catsrcQueueSet.Set(metav1.NamespaceAll, catsrcQueue)
	catsrcQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithMetricsProvider(metrics.NewMetricsCatalogSource(op.lister.OperatorsV1alpha1().CatalogSourceLister())),
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(catsrcQueue),
		queueinformer.WithInformer(catsrcInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncCatalogSources).ToSyncerWithDelete(op.handleCatSrcDeletion)),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(catsrcQueueInformer); err != nil {
		return nil, err
	}

	// Wire Subscriptions
	subInformer := crInformerFactory.Operators().V1alpha1().Subscriptions()
	op.lister.OperatorsV1alpha1().RegisterSubscriptionLister(metav1.NamespaceAll, subInformer.Lister())
	if err := subInformer.Informer().AddIndexers(cache.Indexers{index.PresentCatalogIndexFuncKey: index.PresentCatalogIndexFunc}); err != nil {
		return nil, err
	}
	subIndexer := subInformer.Informer().GetIndexer()
	op.catalogSubscriberIndexer[metav1.NamespaceAll] = subIndexer

	subQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "subs")
	op.subQueueSet.Set(metav1.NamespaceAll, subQueue)
	subSyncer, err := subscription.NewSyncer(
		ctx,
		subscription.WithLogger(op.logger),
		subscription.WithClient(op.client),
		subscription.WithOperatorLister(op.lister),
		subscription.WithSubscriptionInformer(subInformer.Informer()),
		subscription.WithCatalogInformer(catsrcInformer.Informer()),
		subscription.WithInstallPlanInformer(ipInformer.Informer()),
		subscription.WithSubscriptionQueue(subQueue),
		subscription.WithAppendedReconcilers(subscription.ReconcilerFromLegacySyncHandler(op.syncSubscriptions, nil)),
		subscription.WithRegistryReconcilerFactory(op.reconciler),
		subscription.WithGlobalCatalogNamespace(op.namespace),
	)
	if err != nil {
		return nil, err
	}
	subQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithMetricsProvider(metrics.NewMetricsSubscription(op.lister.OperatorsV1alpha1().SubscriptionLister())),
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(subQueue),
		queueinformer.WithInformer(subInformer.Informer()),
		queueinformer.WithSyncer(subSyncer),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(subQueueInformer); err != nil {
		return nil, err
	}

	// Wire k8s sharedIndexInformers
	k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), resyncPeriod())
	sharedIndexInformers := []cache.SharedIndexInformer{}

	// Wire Roles
	roleInformer := k8sInformerFactory.Rbac().V1().Roles()
	op.lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, roleInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, roleInformer.Informer())

	// Wire RoleBindings
	roleBindingInformer := k8sInformerFactory.Rbac().V1().RoleBindings()
	op.lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, roleBindingInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, roleBindingInformer.Informer())

	// Wire ServiceAccounts
	serviceAccountInformer := k8sInformerFactory.Core().V1().ServiceAccounts()
	op.lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, serviceAccountInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, serviceAccountInformer.Informer())

	// Wire Services
	serviceInformer := k8sInformerFactory.Core().V1().Services()
	op.lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, serviceInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, serviceInformer.Informer())

	// Wire Pods
	podInformer := k8sInformerFactory.Core().V1().Pods()
	op.lister.CoreV1().RegisterPodLister(metav1.NamespaceAll, podInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, podInformer.Informer())

	// Wire ConfigMaps
	configMapInformer := k8sInformerFactory.Core().V1().ConfigMaps()
	op.lister.CoreV1().RegisterConfigMapLister(metav1.NamespaceAll, configMapInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, configMapInformer.Informer())

	// Wire Jobs
	jobInformer := k8sInformerFactory.Batch().V1().Jobs()
	sharedIndexInformers = append(sharedIndexInformers, jobInformer.Informer())

	// Generate and register QueueInformers for k8s resources
	k8sSyncer := queueinformer.LegacySyncHandler(op.syncObject).ToSyncerWithDelete(op.handleDeletion)
	for _, informer := range sharedIndexInformers {
		queueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(informer),
			queueinformer.WithSyncer(k8sSyncer),
		)
		if err != nil {
			return nil, err
		}

		if err := op.RegisterQueueInformer(queueInformer); err != nil {
			return nil, err
		}
	}

	// Setup the BundleUnpacker
	op.bundleUnpacker, err = bundle.NewConfigmapUnpacker(
		bundle.WithClient(op.opClient.KubernetesInterface()),
		bundle.WithCatalogSourceLister(catsrcInformer.Lister()),
		bundle.WithConfigMapLister(configMapInformer.Lister()),
		bundle.WithJobLister(jobInformer.Lister()),
		bundle.WithRoleLister(roleInformer.Lister()),
		bundle.WithRoleBindingLister(roleBindingInformer.Lister()),
		bundle.WithOPMImage(configmapRegistryImage),
		bundle.WithUtilImage(utilImage),
		bundle.WithNow(op.now),
	)
	if err != nil {
		return nil, err
	}

	// Register CustomResourceDefinition QueueInformer
	crdInformer := extinf.NewSharedInformerFactory(op.opClient.ApiextensionsInterface(), resyncPeriod()).Apiextensions().V1().CustomResourceDefinitions()
	op.lister.APIExtensionsV1().RegisterCustomResourceDefinitionLister(crdInformer.Lister())
	crdQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithInformer(crdInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncObject).ToSyncerWithDelete(op.handleDeletion)),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(crdQueueInformer); err != nil {
		return nil, err
	}

	// Namespace sync for resolving subscriptions
	namespaceInformer := informers.NewSharedInformerFactory(op.opClient.KubernetesInterface(), resyncPeriod()).Core().V1().Namespaces()
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())
	op.nsResolveQueue = workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resolver")
	namespaceQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(op.nsResolveQueue),
		queueinformer.WithInformer(namespaceInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncResolvingNamespace).ToSyncer()),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(namespaceQueueInformer); err != nil {
		return nil, err
	}

	op.sources.Start(context.Background())

	return op, nil
}

func (o *Operator) now() metav1.Time {
	return metav1.NewTime(o.clock.Now().UTC())
}

func (o *Operator) syncSourceState(state grpc.SourceState) {
	o.sourcesLastUpdate.Set(o.now().Time)

	o.logger.Infof("state.Key.Namespace=%s state.Key.Name=%s state.State=%s", state.Key.Namespace, state.Key.Name, state.State.String())

	switch state.State {
	case connectivity.Ready:
		o.resolver.Expire(state.Key)
		if o.namespace == state.Key.Namespace {
			namespaces, err := index.CatalogSubscriberNamespaces(o.catalogSubscriberIndexer,
				state.Key.Name, state.Key.Namespace)

			if err == nil {
				for ns := range namespaces {
					o.nsResolveQueue.Add(ns)
				}
			}
		}

		o.nsResolveQueue.Add(state.Key.Namespace)
	}
	if err := o.catsrcQueueSet.Requeue(state.Key.Namespace, state.Key.Name); err != nil {
		o.logger.WithError(err).Info("couldn't requeue catalogsource from catalog status change")
	}
}

func (o *Operator) requeueOwners(obj metav1.Object) {
	namespace := obj.GetNamespace()
	logger := o.logger.WithFields(logrus.Fields{
		"name":      obj.GetName(),
		"namespace": namespace,
	})

	for _, owner := range obj.GetOwnerReferences() {
		var queueSet *queueinformer.ResourceQueueSet
		switch kind := owner.Kind; kind {
		case v1alpha1.CatalogSourceKind:
			if err := o.catsrcQueueSet.Requeue(namespace, owner.Name); err != nil {
				logger.Warn(err.Error())
			}
			queueSet = o.catsrcQueueSet
		case v1alpha1.SubscriptionKind:
			if err := o.catsrcQueueSet.Requeue(namespace, owner.Name); err != nil {
				logger.Warn(err.Error())
			}
			queueSet = o.subQueueSet
		default:
			logger.WithField("kind", kind).Trace("untracked owner kind")
		}

		if queueSet != nil {
			logger.WithField("ref", owner).Trace("requeuing owner")
			if err := queueSet.Requeue(namespace, owner.Name); err != nil {
				logger.Warn(err.Error())
			}
		}
	}
}

func (o *Operator) syncObject(obj interface{}) (syncError error) {
	// Assert as metav1.Object
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		syncError = errors.New("casting to metav1 object failed")
		o.logger.Warn(syncError.Error())
		return
	}

	o.requeueOwners(metaObj)

	return o.triggerInstallPlanRetry(obj)
}

func (o *Operator) handleDeletion(obj interface{}) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}

		metaObj, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a metav1 object %#v", obj))
			return
		}
	}

	o.logger.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
	}).Debug("handling object deletion")

	o.requeueOwners(metaObj)

	return
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
	sourceKey := registry.CatalogKey{Name: catsrc.GetName(), Namespace: catsrc.GetNamespace()}
	if err := o.sources.Remove(sourceKey); err != nil {
		o.logger.WithError(err).Warn("error closing client")
	}
	o.logger.WithField("source", sourceKey).Info("removed client for deleted catalogsource")
}

func validateSourceType(logger *logrus.Entry, in *v1alpha1.CatalogSource) (out *v1alpha1.CatalogSource, continueSync bool, _ error) {
	out = in
	var err error
	switch sourceType := out.Spec.SourceType; sourceType {
	case v1alpha1.SourceTypeInternal, v1alpha1.SourceTypeConfigmap:
		if out.Spec.ConfigMap == "" {
			err = fmt.Errorf("configmap name unset: must be set for sourcetype: %s", sourceType)
		}
	case v1alpha1.SourceTypeGrpc:
		if out.Spec.Image == "" && out.Spec.Address == "" {
			err = fmt.Errorf("image and address unset: at least one must be set for sourcetype: %s", sourceType)
		}
	default:
		err = fmt.Errorf("unknown sourcetype: %s", sourceType)
	}
	if err != nil {
		out.SetError(v1alpha1.CatalogSourceSpecInvalidError, err)
		return
	}

	// The sourceType is valid, clear (all) status if there's existing invalid spec reason
	if out.Status.Reason == v1alpha1.CatalogSourceSpecInvalidError {
		out.Status = v1alpha1.CatalogSourceStatus{}
	}
	continueSync = true

	return
}

func (o *Operator) syncConfigMap(logger *logrus.Entry, in *v1alpha1.CatalogSource) (out *v1alpha1.CatalogSource, continueSync bool, syncError error) {
	out = in
	if !(in.Spec.SourceType == v1alpha1.SourceTypeInternal || in.Spec.SourceType == v1alpha1.SourceTypeConfigmap) {
		continueSync = true
		return
	}

	out = in.DeepCopy()

	logger.Debug("checking catsrc configmap state")

	// Get the catalog source's config map
	configMap, err := o.lister.CoreV1().ConfigMapLister().ConfigMaps(in.GetNamespace()).Get(in.Spec.ConfigMap)
	if err != nil {
		syncError = fmt.Errorf("failed to get catalog config map %s: %s", in.Spec.ConfigMap, err)
		out.SetError(v1alpha1.CatalogSourceConfigMapError, syncError)
		return
	}

	if wasOwned := ownerutil.EnsureOwner(configMap, in); !wasOwned {
		configMap, err = o.opClient.KubernetesInterface().CoreV1().ConfigMaps(configMap.GetNamespace()).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		if err != nil {
			syncError = fmt.Errorf("unable to write owner onto catalog source configmap - %v", err)
			out.SetError(v1alpha1.CatalogSourceConfigMapError, syncError)
			return
		}

		logger.Debug("adopted configmap")
	}

	if in.Status.ConfigMapResource == nil || !in.Status.ConfigMapResource.IsAMatch(&configMap.ObjectMeta) {
		logger.Debug("updating catsrc configmap state")
		// configmap ref nonexistent or updated, write out the new configmap ref to status and exit
		out.Status.ConfigMapResource = &v1alpha1.ConfigMapResourceReference{
			Name:            configMap.GetName(),
			Namespace:       configMap.GetNamespace(),
			UID:             configMap.GetUID(),
			ResourceVersion: configMap.GetResourceVersion(),
			LastUpdateTime:  o.now(),
		}

		return
	}

	continueSync = true
	return
}

func (o *Operator) syncRegistryServer(logger *logrus.Entry, in *v1alpha1.CatalogSource) (out *v1alpha1.CatalogSource, continueSync bool, syncError error) {
	out = in.DeepCopy()

	sourceKey := registry.CatalogKey{Name: in.GetName(), Namespace: in.GetNamespace()}
	srcReconciler := o.reconciler.ReconcilerForSource(in)
	if srcReconciler == nil {
		// TODO: Add failure status on catalogsource and remove from sources
		syncError = fmt.Errorf("no reconciler for source type %s", in.Spec.SourceType)
		out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
		return
	}

	healthy, err := srcReconciler.CheckRegistryServer(in)
	if err != nil {
		syncError = err
		out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
		return
	}

	logger.Debugf("check registry server healthy: %t", healthy)

	if healthy && in.Status.RegistryServiceStatus != nil {
		logger.Debug("registry state good")
		continueSync = true
		// return here if catalog does not have polling enabled
		if !out.Poll() {
			return
		}
	}

	// Registry pod hasn't been created or hasn't been updated since the last configmap update, recreate it
	logger.Debug("ensuring registry server")

	err = srcReconciler.EnsureRegistryServer(out)
	if err != nil {
		if _, ok := err.(reconciler.UpdateNotReadyErr); ok {
			logger.Debug("requeueing registry server for catalog update check: update pod not yet ready")
			o.catsrcQueueSet.RequeueAfter(out.GetNamespace(), out.GetName(), reconciler.CatalogPollingRequeuePeriod)
			return
		} else {
			syncError = fmt.Errorf("couldn't ensure registry server - %v", err)
			out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
			return
		}
	}

	logger.Debug("ensured registry server")

	// requeue the catalog sync based on the polling interval, for accurate syncs of catalogs with polling enabled
	if out.Spec.UpdateStrategy != nil {
		logger.Debugf("requeuing registry server sync based on polling interval %s", out.Spec.UpdateStrategy.Interval.Duration.String())
		resyncPeriod := reconciler.SyncRegistryUpdateInterval(out)
		o.catsrcQueueSet.RequeueAfter(out.GetNamespace(), out.GetName(), queueinformer.ResyncWithJitter(resyncPeriod, 0.1)())
		return
	}

	if err := o.sources.Remove(sourceKey); err != nil {
		o.logger.WithError(err).Debug("error closing client connection")
	}

	return
}

func (o *Operator) syncConnection(logger *logrus.Entry, in *v1alpha1.CatalogSource) (out *v1alpha1.CatalogSource, continueSync bool, syncError error) {
	out = in.DeepCopy()

	sourceKey := registry.CatalogKey{Name: in.GetName(), Namespace: in.GetNamespace()}
	// update operator's view of sources
	now := o.now()
	address := in.Address()

	connectFunc := func() (source *grpc.SourceMeta, connErr error) {
		newSource, err := o.sources.Add(sourceKey, address)
		if err != nil {
			connErr = fmt.Errorf("couldn't connect to registry - %v", err)
			return
		}

		if newSource == nil {
			connErr = errors.New("couldn't connect to registry")
			return
		}

		source = &newSource.SourceMeta
		return
	}

	updateConnectionStateFunc := func(out *v1alpha1.CatalogSource, source *grpc.SourceMeta) {
		out.Status.GRPCConnectionState = &v1alpha1.GRPCConnectionState{
			Address:           source.Address,
			LastObservedState: source.ConnectionState.String(),
			LastConnectTime:   source.LastConnect,
		}
	}

	source := o.sources.GetMeta(sourceKey)
	if source == nil {
		source, syncError = connectFunc()
		if syncError != nil {
			out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
			return
		}

		// Set connection status and return.
		updateConnectionStateFunc(out, source)
		return
	}

	logger = logger.WithField("address", address).WithField("currentSource", sourceKey)

	if source.Address != address {
		source, syncError = connectFunc()
		if syncError != nil {
			out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
			return
		}

		// Set connection status and return.
		updateConnectionStateFunc(out, source)
	}

	// connection is already good, but we need to update the sync time
	if out.Status.GRPCConnectionState != nil && o.sourcesLastUpdate.After(out.Status.GRPCConnectionState.LastConnectTime.Time) {
		// Set connection status and return.
		out.Status.GRPCConnectionState.LastConnectTime = now
		out.Status.GRPCConnectionState.LastObservedState = source.ConnectionState.String()
	}

	return
}

func (o *Operator) syncCatalogSources(obj interface{}) (syncError error) {
	catsrc, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		o.logger.Debugf("wrong type: %#v", obj)
		syncError = fmt.Errorf("casting CatalogSource failed")
		return
	}

	logger := o.logger.WithFields(logrus.Fields{
		"source": catsrc.GetName(),
		"id":     queueinformer.NewLoopID(),
	})
	logger.Debug("syncing catsrc")

	syncFunc := func(in *v1alpha1.CatalogSource, chain []CatalogSourceSyncFunc) (out *v1alpha1.CatalogSource, syncErr error) {
		out = in
		for _, syncFunc := range chain {
			cont := false
			out, cont, syncErr = syncFunc(logger, in)
			if syncErr != nil {
				return
			}

			if !cont {
				return
			}

			in = out
		}

		return
	}

	equalFunc := func(a, b *v1alpha1.CatalogSourceStatus) bool {
		return reflect.DeepEqual(a, b)
	}

	updateStatusFunc := func(catsrc *v1alpha1.CatalogSource) error {
		latest, err := o.client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Get(context.TODO(), catsrc.GetName(), metav1.GetOptions{})
		if err != nil {
			logger.Errorf("error getting catalogsource - %v", err)
			return err
		}

		out := latest.DeepCopy()
		out.Status = catsrc.Status

		if _, err := o.client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{}); err != nil {
			logger.Errorf("error while setting catalogsource status condition - %v", err)
			return err
		}

		return nil
	}

	chain := []CatalogSourceSyncFunc{
		validateSourceType,
		o.syncConfigMap,
		o.syncRegistryServer,
		o.syncConnection,
	}

	in := catsrc.DeepCopy()
	in.SetError("", nil)

	out, syncError := syncFunc(in, chain)

	if out == nil {
		return
	}

	if equalFunc(&catsrc.Status, &out.Status) {
		return
	}

	updateErr := updateStatusFunc(out)
	if syncError == nil && updateErr != nil {
		syncError = updateErr
	}

	return
}

func (o *Operator) syncResolvingNamespace(obj interface{}) error {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		o.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Namespace failed")
	}
	namespace := ns.GetName()

	logger := o.logger.WithFields(logrus.Fields{
		"namespace": namespace,
		"id":        queueinformer.NewLoopID(),
	})

	o.gcInstallPlans(logger, namespace)

	// get the set of sources that should be used for resolution and best-effort get their connections working
	logger.Debug("resolving sources")

	querier := resolver.NewNamespaceSourceQuerier(o.sources.AsClients(o.namespace, namespace))

	logger.Debug("checking if subscriptions need update")

	subs, err := o.listSubscriptions(namespace)
	if err != nil {
		logger.WithError(err).Debug("couldn't list subscriptions")
		return err
	}

	// TODO: parallel
	maxGeneration := 0
	subscriptionUpdated := false
	for i, sub := range subs {
		logger := logger.WithFields(logrus.Fields{
			"sub":     sub.GetName(),
			"source":  sub.Spec.CatalogSource,
			"pkg":     sub.Spec.Package,
			"channel": sub.Spec.Channel,
		})

		if sub.Status.InstallPlanGeneration > maxGeneration {
			maxGeneration = sub.Status.InstallPlanGeneration
		}

		// ensure the installplan reference is correct
		sub, changedIP, err := o.ensureSubscriptionInstallPlanState(logger, sub)
		if err != nil {
			logger.Debugf("error ensuring installplan state: %v", err)
			return err
		}
		subscriptionUpdated = subscriptionUpdated || changedIP

		// record the current state of the desired corresponding CSV in the status. no-op if we don't know the csv yet.
		sub, changedCSV, err := o.ensureSubscriptionCSVState(logger, sub, querier)
		if err != nil {
			logger.Debugf("error recording current state of CSV in status: %v", err)
			return err
		}

		subscriptionUpdated = subscriptionUpdated || changedCSV
		subs[i] = sub
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
	steps, bundleLookups, updatedSubs, err := o.resolver.ResolveSteps(namespace, querier)
	if err != nil {
		go o.recorder.Event(ns, corev1.EventTypeWarning, "ResolutionFailed", err.Error())
		return err
	}

	// create installplan if anything updated
	if len(updatedSubs) > 0 {
		logger.Debug("resolution caused subscription changes, creating installplan")
		// Finish calculating max generation by checking the existing installplans
		installPlans, err := o.listInstallPlans(namespace)
		if err != nil {
			return err
		}
		for _, ip := range installPlans {
			if gen := ip.Spec.Generation; gen > maxGeneration {
				maxGeneration = gen
			}
		}

		// any subscription in the namespace with manual approval will force generated installplans to be manual
		// TODO: this is an odd artifact of the older resolver, and will probably confuse users. approval mode could be on the operatorgroup?
		installPlanApproval := v1alpha1.ApprovalAutomatic
		for _, sub := range subs {
			if sub.Spec.InstallPlanApproval == v1alpha1.ApprovalManual {
				installPlanApproval = v1alpha1.ApprovalManual
				break
			}
		}

		installPlanReference, err := o.ensureInstallPlan(logger, namespace, maxGeneration+1, subs, installPlanApproval, steps, bundleLookups)
		if err != nil {
			logger.WithError(err).Debug("error ensuring installplan")
			return err
		}
		if err := o.updateSubscriptionStatus(namespace, maxGeneration+1, updatedSubs, installPlanReference); err != nil {
			logger.WithError(err).Debug("error ensuring subscription installplan state")
			return err
		}
	} else {
		logger.Debugf("no subscriptions were updated")
	}

	return nil
}

func (o *Operator) syncSubscriptions(obj interface{}) error {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		o.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Subscription failed")
	}

	o.nsResolveQueue.Add(sub.GetNamespace())

	return nil
}

func (o *Operator) nothingToUpdate(logger *logrus.Entry, sub *v1alpha1.Subscription) bool {
	// Only sync if catalog has been updated since last sync time
	if o.sourcesLastUpdate.Before(sub.Status.LastUpdated.Time) && sub.Status.State != v1alpha1.SubscriptionStateNone && sub.Status.State != v1alpha1.SubscriptionStateUpgradeAvailable {
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
	if sub.Status.InstallPlanRef != nil || sub.Status.Install != nil {
		return sub, false, nil
	}

	logger.Debug("checking for existing installplan")

	// check if there's an installplan that created this subscription (only if it doesn't have a reference yet)
	// this indicates it was newly resolved by another operator, and we should reference that installplan in the status
	ipName, ok := sub.GetAnnotations()[generatedByKey]
	if !ok {
		return sub, false, nil
	}

	ip, err := o.client.OperatorsV1alpha1().InstallPlans(sub.GetNamespace()).Get(context.TODO(), ipName, metav1.GetOptions{})
	if err != nil {
		logger.WithField("installplan", ipName).Warn("unable to get installplan from cache")
		return nil, false, err
	}
	logger.WithField("installplan", ipName).Debug("found installplan that generated subscription")

	out := sub.DeepCopy()
	ref, err := reference.GetReference(ip)
	if err != nil {
		logger.WithError(err).Warn("unable to generate installplan reference")
		return nil, false, err
	}
	out.Status.InstallPlanRef = ref
	out.Status.Install = v1alpha1.NewInstallPlanReference(ref)
	out.Status.State = v1alpha1.SubscriptionStateUpgradePending
	out.Status.CurrentCSV = out.Spec.StartingCSV
	out.Status.LastUpdated = o.now()

	updated, err := o.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{})
	if err != nil {
		return nil, false, err
	}

	return updated, true, nil
}

func (o *Operator) ensureSubscriptionCSVState(logger *logrus.Entry, sub *v1alpha1.Subscription, querier resolver.SourceQuerier) (*v1alpha1.Subscription, bool, error) {
	if sub.Status.CurrentCSV == "" {
		return sub, false, nil
	}

	csv, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(sub.GetNamespace()).Get(context.TODO(), sub.Status.CurrentCSV, metav1.GetOptions{})
	out := sub.DeepCopy()
	if err != nil {
		logger.WithError(err).WithField("currentCSV", sub.Status.CurrentCSV).Debug("error fetching csv listed in subscription status")
		out.Status.State = v1alpha1.SubscriptionStateUpgradePending
	} else {
		// Check if an update is available for the current csv
		if err := querier.Queryable(); err != nil {
			return nil, false, err
		}
		b, _, _ := querier.FindReplacement(&csv.Spec.Version.Version, sub.Status.CurrentCSV, sub.Spec.Package, sub.Spec.Channel, registry.CatalogKey{Name: sub.Spec.CatalogSource, Namespace: sub.Spec.CatalogSourceNamespace})
		if b != nil {
			o.logger.Tracef("replacement %s bundle found for current bundle %s", b.CsvName, sub.Status.CurrentCSV)
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
	out.Status.LastUpdated = o.now()

	// Update Subscription with status of transition. Log errors if we can't write them to the status.
	updatedSub, err := o.client.OperatorsV1alpha1().Subscriptions(out.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{})
	if err != nil {
		logger.WithError(err).Info("error updating subscription status")
		return nil, false, fmt.Errorf("error updating Subscription status: " + err.Error())
	}

	// subscription status represents cluster state
	return updatedSub, true, nil
}

func (o *Operator) updateSubscriptionStatus(namespace string, gen int, subs []*v1alpha1.Subscription, installPlanRef *corev1.ObjectReference) error {
	var (
		errs        []error
		mu          sync.Mutex
		wg          sync.WaitGroup
		getOpts     = metav1.GetOptions{}
		lastUpdated = o.now()
	)
	for _, sub := range subs {
		sub.Status.LastUpdated = lastUpdated
		if installPlanRef != nil {
			sub.Status.InstallPlanRef = installPlanRef
			sub.Status.Install = v1alpha1.NewInstallPlanReference(installPlanRef)
			sub.Status.State = v1alpha1.SubscriptionStateUpgradePending
			sub.Status.InstallPlanGeneration = gen
		}

		wg.Add(1)
		go func(s v1alpha1.Subscription) {
			defer wg.Done()

			update := func() error {
				// Update the status of the latest revision
				latest, err := o.client.OperatorsV1alpha1().Subscriptions(s.GetNamespace()).Get(context.TODO(), s.GetName(), getOpts)
				if err != nil {
					return err
				}

				latest.Status = s.Status
				_, err = o.client.OperatorsV1alpha1().Subscriptions(namespace).UpdateStatus(context.TODO(), latest, metav1.UpdateOptions{})

				return err
			}
			if err := retry.RetryOnConflict(retry.DefaultRetry, update); err != nil {
				mu.Lock()
				defer mu.Unlock()
				errs = append(errs, err)
			}
		}(*sub)
	}
	wg.Wait()

	return utilerrors.NewAggregate(errs)
}

func (o *Operator) ensureInstallPlan(logger *logrus.Entry, namespace string, gen int, subs []*v1alpha1.Subscription, installPlanApproval v1alpha1.Approval, steps []*v1alpha1.Step, bundleLookups []v1alpha1.BundleLookup) (*corev1.ObjectReference, error) {
	if len(steps) == 0 && len(bundleLookups) == 0 {
		return nil, nil
	}

	// Check if any existing installplans are creating the same resources
	installPlans, err := o.listInstallPlans(namespace)
	if err != nil {
		return nil, err
	}

	for _, installPlan := range installPlans {
		if installPlan.Spec.Generation == gen {
			return reference.GetReference(installPlan)
		}
	}
	logger.Warn("no installplan found with matching generation, creating new one")

	return o.createInstallPlan(namespace, gen, subs, installPlanApproval, steps, bundleLookups)
}

func (o *Operator) createInstallPlan(namespace string, gen int, subs []*v1alpha1.Subscription, installPlanApproval v1alpha1.Approval, steps []*v1alpha1.Step, bundleLookups []v1alpha1.BundleLookup) (*corev1.ObjectReference, error) {
	if len(steps) == 0 && len(bundleLookups) == 0 {
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
	for _, b := range bundleLookups {
		csvNames = append(csvNames, b.Identifier)
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
			Generation:                 gen,
		},
	}
	for _, sub := range subs {
		ownerutil.AddNonBlockingOwner(ip, sub)
	}

	res, err := o.client.OperatorsV1alpha1().InstallPlans(namespace).Create(context.TODO(), ip, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	res.Status = v1alpha1.InstallPlanStatus{
		Phase:          phase,
		Plan:           steps,
		CatalogSources: catalogSources,
		BundleLookups:  bundleLookups,
	}
	res, err = o.client.OperatorsV1alpha1().InstallPlans(namespace).UpdateStatus(context.TODO(), res, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	return reference.GetReference(res)
}

type UnpackedBundleReference struct {
	Kind                   string `json:"kind"`
	Name                   string `json:"name"`
	Namespace              string `json:"namespace"`
	CatalogSourceName      string `json:"catalogSourceName"`
	CatalogSourceNamespace string `json:"catalogSourceNamespace"`
	Replaces               string `json:"replaces"`
	Properties             string `json:"properties"`
}

// unpackBundles makes one walk through the bundlelookups and attempts to progress them
func (o *Operator) unpackBundles(plan *v1alpha1.InstallPlan) (bool, *v1alpha1.InstallPlan, error) {
	out := plan.DeepCopy()
	unpacked := true

	var errs []error
	for i := 0; i < len(out.Status.BundleLookups); i++ {
		lookup := out.Status.BundleLookups[i]
		res, err := o.bundleUnpacker.UnpackBundle(&lookup)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out.Status.BundleLookups[i] = *res.BundleLookup

		// if pending condition is present, still waiting for the job to unpack to configmap
		if res.GetCondition(v1alpha1.BundleLookupPending).Status == corev1.ConditionTrue {
			unpacked = false
			continue
		}

		// if packed condition is missing, bundle has already been unpacked into steps, continue
		if res.GetCondition(resolver.BundleLookupConditionPacked).Status == corev1.ConditionUnknown {
			continue
		}

		// Ensure that bundle can be applied by the current version of OLM by converting to steps
		steps, err := resolver.NewStepsFromBundle(res.Bundle(), out.GetNamespace(), res.Replaces, res.CatalogSourceRef.Name, res.CatalogSourceRef.Namespace)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to turn bundle into steps: %v", err))
			unpacked = false
			continue
		}

		// step manifests are replaced with references to the configmap containing them
		for i, s := range steps {
			ref := UnpackedBundleReference{
				Kind:                   "ConfigMap",
				Namespace:              res.CatalogSourceRef.Namespace,
				Name:                   res.Name(),
				CatalogSourceName:      res.CatalogSourceRef.Name,
				CatalogSourceNamespace: res.CatalogSourceRef.Namespace,
				Replaces:               res.Replaces,
				Properties:             res.Properties,
			}
			r, err := json.Marshal(&ref)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to generate reference for configmap: %v", err))
				unpacked = false
				continue
			}
			s.Resource.Manifest = string(r)
			steps[i] = s
		}
		res.RemoveCondition(resolver.BundleLookupConditionPacked)
		out.Status.BundleLookups[i] = *res.BundleLookup
		out.Status.Plan = append(out.Status.Plan, steps...)
	}

	if err := utilerrors.NewAggregate(errs); err != nil {
		o.logger.Debugf("failed to unpack bundles: %v", err)
		return false, nil, err
	}

	return unpacked, out, nil
}

// gcInstallPlans garbage collects installplans that are too old
// installplans are ownerrefd to all subscription inputs, so they will not otherwise
// be GCd unless all inputs have been deleted.
func (o *Operator) gcInstallPlans(log logrus.FieldLogger, namespace string) {
	allIps, err := o.lister.OperatorsV1alpha1().InstallPlanLister().InstallPlans(namespace).List(labels.Everything())
	if err != nil {
		log.Warn("unable to list installplans for GC")
	}

	if len(allIps) <= maxInstallPlanCount {
		return
	}

	// we only consider maxDeletesPerSweep more than the allowed number of installplans for delete at one time
	ips := allIps
	if len(ips) > maxInstallPlanCount+maxDeletesPerSweep {
		ips = allIps[:maxInstallPlanCount+maxDeletesPerSweep]
	}

	byGen := map[int][]*v1alpha1.InstallPlan{}
	for _, ip := range ips {
		gen, ok := byGen[ip.Spec.Generation]
		if !ok {
			gen = make([]*v1alpha1.InstallPlan, 0)
		}
		byGen[ip.Spec.Generation] = append(gen, ip)
	}

	gens := make([]int, 0)
	for i := range byGen {
		gens = append(gens, i)
	}

	sort.Ints(gens)

	toDelete := make([]*v1alpha1.InstallPlan, 0)

	for _, i := range gens {
		g := byGen[i]

		if len(ips)-len(toDelete) <= maxInstallPlanCount {
			break
		}

		// if removing all installplans at this generation doesn't dip below the max, safe to delete all of them
		if len(ips)-len(toDelete)-len(g) >= maxInstallPlanCount {
			toDelete = append(toDelete, g...)
			continue
		}

		// CreationTimestamp sorting shouldn't ever be hit unless there is a bug that causes installplans to be
		// generated without bumping the generation. It is here as a safeguard only.

		// sort by creation time
		sort.Slice(g, func(i, j int) bool {
			if !g[i].CreationTimestamp.Equal(&g[j].CreationTimestamp) {
				return g[i].CreationTimestamp.Before(&g[j].CreationTimestamp)
			}
			// final fallback to lexicographic sort, in case many installplans are created with the same timestamp
			return g[i].GetName() < g[j].GetName()
		})
		toDelete = append(toDelete, g[:len(ips)-len(toDelete)-maxInstallPlanCount]...)
	}

	for _, i := range toDelete {
		if err := o.client.OperatorsV1alpha1().InstallPlans(namespace).Delete(context.TODO(), i.GetName(), metav1.DeleteOptions{}); err != nil {
			log.WithField("deleting", i.GetName()).WithError(err).Warn("error GCing old installplan - may have already been deleted")
		}
	}
}

func (o *Operator) syncInstallPlans(obj interface{}) (syncError error) {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		o.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting InstallPlan failed")
	}

	logger := o.logger.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"ip":        plan.GetName(),
		"namespace": plan.GetNamespace(),
		"phase":     plan.Status.Phase,
	})

	logger.Info("syncing")

	if len(plan.Status.Plan) == 0 && len(plan.Status.BundleLookups) == 0 {
		logger.Info("skip processing installplan without status - subscription sync responsible for initial status")
		return
	}

	querier := o.serviceAccountQuerier.NamespaceQuerier(plan.GetNamespace())
	ref, err := querier()
	if err != nil {
		syncError = fmt.Errorf("attenuated service account query failed - %v", err)
		return
	}

	if ref != nil {
		out := plan.DeepCopy()
		out.Status.AttenuatedServiceAccountRef = ref

		if !reflect.DeepEqual(plan, out) {
			if _, updateErr := o.client.OperatorsV1alpha1().InstallPlans(out.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{}); updateErr != nil {
				syncError = fmt.Errorf("failed to attach attenuated ServiceAccount to status - %v", updateErr)
				return
			}

			logger.WithField("attenuated-sa", ref.Name).Info("successfully attached attenuated ServiceAccount to status")
			return
		}
	}

	// Attempt to unpack bundles before installing
	// Note: This should probably use the attenuated client to prevent users from resolving resources they otherwise don't have access to.
	if len(plan.Status.BundleLookups) > 0 {
		unpacked, out, err := o.unpackBundles(plan)
		if err != nil {
			syncError = fmt.Errorf("bundle unpacking failed: %v", err)
			return
		}

		if !reflect.DeepEqual(plan.Status, out.Status) {
			logger.Warnf("status not equal, updating...")
			if _, err := o.client.OperatorsV1alpha1().InstallPlans(out.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{}); err != nil {
				syncError = fmt.Errorf("failed to update installplan bundle lookups: %v", err)
			}

			return
		}

		// TODO: Remove in favor of job and configmap informer requeuing
		if !unpacked {
			err := o.ipQueueSet.RequeueAfter(plan.GetNamespace(), plan.GetName(), 5*time.Second)
			if err != nil {
				syncError = err
				return
			}
			logger.Debug("install plan not yet populated from bundle image, requeueing")

			return
		}
	}

	outInstallPlan, syncError := transitionInstallPlanState(logger.Logger, o, *plan, o.now())

	if syncError != nil {
		logger = logger.WithField("syncError", syncError)
	}

	if outInstallPlan.Status.Phase == v1alpha1.InstallPlanPhaseInstalling {
		defer o.ipQueueSet.RequeueAfter(outInstallPlan.GetNamespace(), outInstallPlan.GetName(), time.Second*5)
	}

	defer func() {
		// Notify subscription loop of installplan changes
		if owners := ownerutil.GetOwnersByKind(plan, v1alpha1.SubscriptionKind); len(owners) > 0 {
			for _, owner := range owners {
				logger.WithField("owner", owner).Debug("requeueing installplan owner")
				if err := o.subQueueSet.Requeue(plan.GetNamespace(), owner.Name); err != nil {
					logger.WithError(err).Warn("error requeuing installplan owner")
				}
			}
			return
		}
		logger.Trace("no installplan owner subscriptions found to requeue")
	}()

	// Update InstallPlan with status of transition. Log errors if we can't write them to the status.
	if _, err := o.client.OperatorsV1alpha1().InstallPlans(plan.GetNamespace()).UpdateStatus(context.TODO(), outInstallPlan, metav1.UpdateOptions{}); err != nil {
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

func transitionInstallPlanState(log *logrus.Logger, transitioner installPlanTransitioner, in v1alpha1.InstallPlan, now metav1.Time) (*v1alpha1.InstallPlan, error) {
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
				v1alpha1.InstallPlanReasonComponentFailed, err.Error(), &now))
			out.Status.Phase = v1alpha1.InstallPlanPhaseFailed
			return out, err
		}
		// Loop over one final time to check and see if everything is good.
		if !out.Status.NeedsRequeue() {
			out.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanInstalled, &now))
			out.Status.Phase = v1alpha1.InstallPlanPhaseComplete
		}
		return out, nil
	default:
		return out, nil
	}
}

// ResolvePlan modifies an InstallPlan to contain a Plan in its Status field.
func (o *Operator) ResolvePlan(plan *v1alpha1.InstallPlan) error {
	return nil
}

// Validate all existing served versions against new CRD's validation (if changed)
func validateV1CRDCompatibility(dynamicClient dynamic.Interface, oldCRD *apiextensionsv1.CustomResourceDefinition, newCRD *apiextensionsv1.CustomResourceDefinition) error {
	logrus.Debugf("Comparing %#v to %#v", oldCRD.Spec.Versions, newCRD.Spec.Versions)

	// If validation schema is unchanged, return right away
	newestSchema := newCRD.Spec.Versions[len(newCRD.Spec.Versions)-1].Schema
	for i, oldVersion := range oldCRD.Spec.Versions {
		if !reflect.DeepEqual(oldVersion.Schema, newestSchema) {
			break
		}
		if i == len(oldCRD.Spec.Versions)-1 {
			// we are on the last iteration
			// schema has not changed between versions at this point.
			return nil
		}
	}

	convertedCRD := &apiextensions.CustomResourceDefinition{}
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(newCRD, convertedCRD, nil); err != nil {
		return err
	}
	for _, version := range oldCRD.Spec.Versions {
		if version.Served {
			gvr := schema.GroupVersionResource{Group: oldCRD.Spec.Group, Version: version.Name, Resource: oldCRD.Spec.Names.Plural}
			err := validateExistingCRs(dynamicClient, gvr, convertedCRD)
			if err != nil {
				return err
			}
		}
	}

	logrus.Debugf("Successfully validated CRD %s\n", newCRD.Name)
	return nil
}

// Validate all existing served versions against new CRD's validation (if changed)
func validateV1Beta1CRDCompatibility(dynamicClient dynamic.Interface, oldCRD *apiextensionsv1beta1.CustomResourceDefinition, newCRD *apiextensionsv1beta1.CustomResourceDefinition) error {
	logrus.Debugf("Comparing %#v to %#v", oldCRD.Spec.Validation, newCRD.Spec.Validation)

	// TODO return early of all versions are equal
	convertedCRD := &apiextensions.CustomResourceDefinition{}
	if err := apiextensionsv1beta1.Convert_v1beta1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(newCRD, convertedCRD, nil); err != nil {
		return err
	}
	for _, version := range oldCRD.Spec.Versions {
		if version.Served {
			gvr := schema.GroupVersionResource{Group: oldCRD.Spec.Group, Version: version.Name, Resource: oldCRD.Spec.Names.Plural}
			err := validateExistingCRs(dynamicClient, gvr, convertedCRD)
			if err != nil {
				return err
			}
		}
	}

	if oldCRD.Spec.Version != "" {
		gvr := schema.GroupVersionResource{Group: oldCRD.Spec.Group, Version: oldCRD.Spec.Version, Resource: oldCRD.Spec.Names.Plural}
		err := validateExistingCRs(dynamicClient, gvr, convertedCRD)
		if err != nil {
			return err
		}
	}
	logrus.Debugf("Successfully validated CRD %s\n", newCRD.Name)
	return nil
}

func validateExistingCRs(dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, newCRD *apiextensions.CustomResourceDefinition) error {
	// make dynamic client
	crList, err := dynamicClient.Resource(gvr).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing resources in GroupVersionResource %#v: %s", gvr, err)
	}
	for _, cr := range crList.Items {
		validator, _, err := validation.NewSchemaValidator(newCRD.Spec.Validation)
		if err != nil {
			return fmt.Errorf("error creating validator for schema %#v: %s", newCRD.Spec.Validation, err)
		}
		err = validation.ValidateCustomResource(field.NewPath(""), cr.UnstructuredContent(), validator).ToAggregate()
		if err != nil {
			return fmt.Errorf("error validating custom resource against new schema %#v: %s", newCRD.Spec.Validation, err)
		}
	}
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

	// Does the namespace have an operator group that specifies a user defined
	// service account? If so, then we should use a scoped client for plan
	// execution.
	kubeclient, crclient, dynamicClient, err := o.clientAttenuator.AttenuateClientWithServiceAccount(plan.Status.AttenuatedServiceAccountRef)
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution- %v", err)
		return err
	}

	ensurer := newStepEnsurer(kubeclient, crclient, dynamicClient)
	r := newManifestResolver(plan.GetNamespace(), o.lister.CoreV1().ConfigMapLister(), o.logger)
	b := newBuilder(kubeclient, dynamicClient, o.csvProvidedAPIsIndexer, r, o.logger)

	for i, step := range plan.Status.Plan {
		doStep := true
		s, err := b.create(*step)
		if err != nil {
			if _, ok := err.(notSupportedStepperErr); ok {
				// stepper not implemented for this type yet
				// stepper currently only implemented for CRD types
				doStep = false
			} else {
				return err
			}
		}
		if doStep {
			status, err := s.Status()
			if err != nil {
				return err
			}
			plan.Status.Plan[i].Status = status
			continue
		}

		switch step.Status {
		case v1alpha1.StepStatusPresent, v1alpha1.StepStatusCreated, v1alpha1.StepStatusWaitingForAPI:
			continue
		case v1alpha1.StepStatusUnknown, v1alpha1.StepStatusNotPresent:
			manifest, err := r.ManifestForStep(step)
			if err != nil {
				return err
			}
			o.logger.WithFields(logrus.Fields{"kind": step.Resource.Kind, "name": step.Resource.Name}).Debug("execute resource")
			switch step.Resource.Kind {
			case v1alpha1.ClusterServiceVersionKind:
				// Marshal the manifest into a CSV instance.
				var csv v1alpha1.ClusterServiceVersion
				err := json.Unmarshal([]byte(manifest), &csv)
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

				status, err := ensurer.EnsureClusterServiceVersion(&csv)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case v1alpha1.SubscriptionKind:
				// Marshal the manifest into a subscription instance.
				var sub v1alpha1.Subscription
				err := json.Unmarshal([]byte(manifest), &sub)
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

				status, err := ensurer.EnsureSubscription(&sub)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case resolver.BundleSecretKind:
				var s corev1.Secret
				err := json.Unmarshal([]byte(manifest), &s)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// add ownerrefs on the secret that point to the CSV in the bundle
				if step.Resolving != "" {
					owner := &v1alpha1.ClusterServiceVersion{}
					owner.SetNamespace(plan.GetNamespace())
					owner.SetName(step.Resolving)
					ownerutil.AddNonBlockingOwner(&s, owner)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(s.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for secret %s", s.GetName())
				}
				s.SetOwnerReferences(updated)
				s.SetNamespace(namespace)

				status, err := ensurer.EnsureBundleSecret(plan.Namespace, &s)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case secretKind:
				status, err := ensurer.EnsureSecret(o.namespace, plan.GetNamespace(), step.Resource.Name)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case clusterRoleKind:
				// Marshal the manifest into a ClusterRole instance.
				var cr rbacv1.ClusterRole
				err := json.Unmarshal([]byte(manifest), &cr)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				status, err := ensurer.EnsureClusterRole(&cr, step)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case clusterRoleBindingKind:
				// Marshal the manifest into a RoleBinding instance.
				var rb rbacv1.ClusterRoleBinding
				err := json.Unmarshal([]byte(manifest), &rb)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				status, err := ensurer.EnsureClusterRoleBinding(&rb, step)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case roleKind:
				// Marshal the manifest into a Role instance.
				var r rbacv1.Role
				err := json.Unmarshal([]byte(manifest), &r)
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

				status, err := ensurer.EnsureRole(plan.Namespace, &r)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case roleBindingKind:
				// Marshal the manifest into a RoleBinding instance.
				var rb rbacv1.RoleBinding
				err := json.Unmarshal([]byte(manifest), &rb)
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

				status, err := ensurer.EnsureRoleBinding(plan.Namespace, &rb)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case serviceAccountKind:
				// Marshal the manifest into a ServiceAccount instance.
				var sa corev1.ServiceAccount
				err := json.Unmarshal([]byte(manifest), &sa)
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

				status, err := ensurer.EnsureServiceAccount(namespace, &sa)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case serviceKind:
				// Marshal the manifest into a Service instance
				var s corev1.Service
				err := json.Unmarshal([]byte(manifest), &s)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// add ownerrefs on the service that point to the CSV in the bundle
				if step.Resolving != "" {
					owner := &v1alpha1.ClusterServiceVersion{}
					owner.SetNamespace(plan.GetNamespace())
					owner.SetName(step.Resolving)
					ownerutil.AddNonBlockingOwner(&s, owner)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(s.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for service: %s", s.GetName())
				}
				s.SetOwnerReferences(updated)
				s.SetNamespace(namespace)

				status, err := ensurer.EnsureService(namespace, &s)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			case configMapKind:
				var cfg corev1.ConfigMap
				err := json.Unmarshal([]byte(manifest), &cfg)
				if err != nil {
					return errorwrap.Wrapf(err, "error parsing step manifest: %s", step.Resource.Name)
				}

				// add ownerrefs on the configmap that point to the CSV in the bundle
				if step.Resolving != "" {
					owner := &v1alpha1.ClusterServiceVersion{}
					owner.SetNamespace(plan.GetNamespace())
					owner.SetName(step.Resolving)
					ownerutil.AddNonBlockingOwner(&cfg, owner)
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(cfg.OwnerReferences, plan.Namespace)
				if err != nil {
					return errorwrap.Wrapf(err, "error generating ownerrefs for configmap: %s", cfg.GetName())
				}
				cfg.SetOwnerReferences(updated)
				cfg.SetNamespace(namespace)

				status, err := ensurer.EnsureConfigMap(plan.Namespace, &cfg)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status

			default:
				if !isSupported(step.Resource.Kind) {
					// Not a supported resource
					plan.Status.Plan[i].Status = v1alpha1.StepStatusUnsupportedResource
					return v1alpha1.ErrInvalidInstallPlan
				}

				// Marshal the manifest into an unstructured object
				dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 10)
				unstructuredObject := &unstructured.Unstructured{}
				if err := dec.Decode(unstructuredObject); err != nil {
					return errorwrap.Wrapf(err, "error decoding %s object to an unstructured object", step.Resource.Name)
				}

				// Get the resource from the GVK.
				gvk := unstructuredObject.GroupVersionKind()
				r, err := o.apiresourceFromGVK(gvk)
				if err != nil {
					return err
				}

				// Create the GVR
				gvr := schema.GroupVersionResource{
					Group:    gvk.Group,
					Version:  gvk.Version,
					Resource: r.Name,
				}

				if step.Resolving != "" {
					owner := &v1alpha1.ClusterServiceVersion{}
					owner.SetNamespace(plan.GetNamespace())
					owner.SetName(step.Resolving)

					if r.Namespaced {
						// Set OwnerReferences for namespace-scoped resource
						ownerutil.AddNonBlockingOwner(unstructuredObject, owner)

						// Update UIDs on all CSV OwnerReferences
						updated, err := o.getUpdatedOwnerReferences(unstructuredObject.GetOwnerReferences(), plan.Namespace)
						if err != nil {
							return errorwrap.Wrapf(err, "error generating ownerrefs for unstructured object: %s", unstructuredObject.GetName())
						}

						unstructuredObject.SetOwnerReferences(updated)
					} else {
						// Add owner labels to cluster-scoped resource
						if err := ownerutil.AddOwnerLabels(unstructuredObject, owner); err != nil {
							return err
						}
					}
				}

				// Set up the dynamic client ResourceInterface and set ownerrefs
				var resourceInterface dynamic.ResourceInterface
				if r.Namespaced {
					unstructuredObject.SetNamespace(namespace)
					resourceInterface = o.dynamicClient.Resource(gvr).Namespace(namespace)
				} else {
					resourceInterface = o.dynamicClient.Resource(gvr)
				}

				// Ensure Unstructured Object
				status, err := ensurer.EnsureUnstructuredObject(resourceInterface, unstructuredObject)
				if err != nil {
					return err
				}

				plan.Status.Plan[i].Status = status
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
	csvList, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(context.TODO(), metav1.ListOptions{})

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
			csv, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), owner.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			owner.UID = csv.GetUID()
			updated[i] = owner
		}
	}
	return updated, nil
}

func (o *Operator) listSubscriptions(namespace string) (subs []*v1alpha1.Subscription, err error) {
	list, err := o.client.OperatorsV1alpha1().Subscriptions(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return
	}

	subs = make([]*v1alpha1.Subscription, 0)
	for i := range list.Items {
		subs = append(subs, &list.Items[i])
	}

	return
}

func (o *Operator) listInstallPlans(namespace string) (ips []*v1alpha1.InstallPlan, err error) {
	list, err := o.client.OperatorsV1alpha1().InstallPlans(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return
	}

	ips = make([]*v1alpha1.InstallPlan, 0)
	for i := range list.Items {
		ips = append(ips, &list.Items[i])
	}

	return
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

func (o *Operator) apiresourceFromGVK(gvk schema.GroupVersionKind) (metav1.APIResource, error) {
	logger := o.logger.WithFields(logrus.Fields{
		"group":   gvk.Group,
		"version": gvk.Version,
		"kind":    gvk.Kind,
	})

	resources, err := o.opClient.KubernetesInterface().Discovery().ServerResourcesForGroupVersion(gvk.GroupVersion().String())
	if err != nil {
		logger.WithField("err", err).Info("could not query for GVK in api discovery")
		return metav1.APIResource{}, err
	}
	for _, r := range resources.APIResources {
		if r.Kind == gvk.Kind {
			return r, nil
		}
	}
	logger.Info("couldn't find GVK in api discovery")
	return metav1.APIResource{}, olmerrors.GroupVersionKindNotFoundError{gvk.Group, gvk.Version, gvk.Kind}
}

const (
	PrometheusRuleKind        = "PrometheusRule"
	ServiceMonitorKind        = "ServiceMonitor"
	PodDisruptionBudgetKind   = "PodDisruptionBudget"
	PriorityClassKind         = "PriorityClass"
	VerticalPodAutoscalerKind = "VerticalPodAutoscaler"
	ConsoleYAMLSampleKind     = "ConsoleYAMLSample"
)

var supportedKinds = map[string]struct{}{
	PrometheusRuleKind:        {},
	ServiceMonitorKind:        {},
	PodDisruptionBudgetKind:   {},
	PriorityClassKind:         {},
	VerticalPodAutoscalerKind: {},
	ConsoleYAMLSampleKind:     {},
}

// isSupported returns true if OLM supports this type of CustomResource.
func isSupported(kind string) bool {
	_, ok := supportedKinds[kind]
	return ok
}
