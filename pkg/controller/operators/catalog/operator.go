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

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/labeller"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/validatingroundtripper"
	errorwrap "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/connectivity"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/yaml"
	batchv1applyconfigurations "k8s.io/client-go/applyconfigurations/batch/v1"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	rbacv1applyconfigurations "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/metadata/metadatalister"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/pager"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	utilclock "k8s.io/utils/clock"

	"github.com/operator-framework/api/pkg/operators/reference"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	operatorsv1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog/subscription"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/pruning"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/grpc"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	resolvercache "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/catalogsource"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clients"
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
	ogQueueSet               *queueinformer.ResourceQueueSet
	nsResolveQueue           workqueue.TypedRateLimitingInterface[types.NamespacedName]
	namespace                string
	recorder                 record.EventRecorder
	sources                  *grpc.SourceStore
	sourcesLastUpdate        sharedtime.SharedTime
	resolver                 resolver.StepResolver
	reconciler               reconciler.RegistryReconcilerFactory
	catalogSubscriberIndexer map[string]cache.Indexer
	clientAttenuator         *scoped.ClientAttenuator
	serviceAccountQuerier    *scoped.UserDefinedServiceAccountQuerier
	bundleUnpacker           bundle.Unpacker
	installPlanTimeout       time.Duration
	bundleUnpackTimeout      time.Duration
	clientFactory            clients.Factory
	muInstallPlan            sync.Mutex
	resolverSourceProvider   *resolver.RegistrySourceProvider
}

type CatalogSourceSyncFunc func(logger *logrus.Entry, in *v1alpha1.CatalogSource) (out *v1alpha1.CatalogSource, continueSync bool, syncError error)

// NewOperator creates a new Catalog Operator.
func NewOperator(ctx context.Context, kubeconfigPath string, clock utilclock.Clock, logger *logrus.Logger, resync time.Duration, configmapRegistryImage, opmImage, utilImage string, operatorNamespace string, scheme *runtime.Scheme, installPlanTimeout time.Duration, bundleUnpackTimeout time.Duration, workloadUserID int64) (*Operator, error) {
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

	// create a config that validates we're creating objects with labels
	validatingConfig := validatingroundtripper.Wrap(config, scheme)

	// Create a new client for dynamic types (CRs)
	dynamicClient, err := dynamic.NewForConfig(validatingConfig)
	if err != nil {
		return nil, err
	}

	metadataClient, err := metadata.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Create a new queueinformer-based operator.
	opClient, err := operatorclient.NewClientFromRestConfig(validatingConfig)
	if err != nil {
		return nil, err
	}

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

	canFilter, err := labeller.Validate(ctx, logger, metadataClient, crClient, labeller.IdentityCatalogOperator)
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
		ogQueueSet:               queueinformer.NewEmptyResourceQueueSet(),
		catalogSubscriberIndexer: map[string]cache.Indexer{},
		serviceAccountQuerier:    scoped.NewUserDefinedServiceAccountQuerier(logger, crClient),
		clientAttenuator:         scoped.NewClientAttenuator(logger, validatingConfig, opClient),
		installPlanTimeout:       installPlanTimeout,
		bundleUnpackTimeout:      bundleUnpackTimeout,
		clientFactory:            clients.NewFactory(validatingConfig),
	}
	op.sources = grpc.NewSourceStore(logger, 10*time.Second, 10*time.Minute, op.syncSourceState)
	op.resolverSourceProvider = resolver.SourceProviderFromRegistryClientProvider(op.sources, lister.OperatorsV1alpha1().CatalogSourceLister(), logger)
	op.reconciler = reconciler.NewRegistryReconcilerFactory(lister, opClient, configmapRegistryImage, op.now, ssaClient, workloadUserID, opmImage, utilImage)
	res := resolver.NewOperatorStepResolver(lister, crClient, operatorNamespace, op.resolverSourceProvider, logger)
	op.resolver = resolver.NewInstrumentedResolver(res, metrics.RegisterDependencyResolutionSuccess, metrics.RegisterDependencyResolutionFailure)

	// Wire OLM CR sharedIndexInformers
	crInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(op.client, resyncPeriod())

	// Fields are pruned from local copies of the objects managed
	// by this informer in order to reduce cached size.
	prunedCSVInformer := cache.NewSharedIndexInformer(
		pruning.NewListerWatcher(op.client, metav1.NamespaceAll,
			func(options *metav1.ListOptions) {
				options.LabelSelector = fmt.Sprintf("!%s", v1alpha1.CopiedLabelKey)
			},
			pruning.PrunerFunc(func(csv *v1alpha1.ClusterServiceVersion) {
				*csv = v1alpha1.ClusterServiceVersion{
					TypeMeta: csv.TypeMeta,
					ObjectMeta: metav1.ObjectMeta{
						Name:        csv.Name,
						Namespace:   csv.Namespace,
						Labels:      csv.Labels,
						Annotations: csv.Annotations,
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						CustomResourceDefinitions: csv.Spec.CustomResourceDefinitions,
						APIServiceDefinitions:     csv.Spec.APIServiceDefinitions,
						Replaces:                  csv.Spec.Replaces,
						Version:                   csv.Spec.Version,
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase:  csv.Status.Phase,
						Reason: csv.Status.Reason,
					},
				}
			})),
		&v1alpha1.ClusterServiceVersion{},
		resyncPeriod(),
		cache.Indexers{
			cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
		},
	)
	csvLister := operatorsv1alpha1listers.NewClusterServiceVersionLister(prunedCSVInformer.GetIndexer())
	op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(metav1.NamespaceAll, csvLister)
	if err := op.RegisterInformer(prunedCSVInformer); err != nil {
		return nil, err
	}

	// TODO: Add namespace resolve sync

	// Wire InstallPlans
	ipInformer := crInformerFactory.Operators().V1alpha1().InstallPlans()
	op.lister.OperatorsV1alpha1().RegisterInstallPlanLister(metav1.NamespaceAll, ipInformer.Lister())
	ipQueue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: "ips",
		})
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

	operatorGroupInformer := crInformerFactory.Operators().V1().OperatorGroups()
	op.lister.OperatorsV1().RegisterOperatorGroupLister(metav1.NamespaceAll, operatorGroupInformer.Lister())
	ogQueue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: "ogs",
		})
	op.ogQueueSet.Set(metav1.NamespaceAll, ogQueue)
	operatorGroupQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(ogQueue),
		queueinformer.WithInformer(operatorGroupInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncOperatorGroups).ToSyncer()),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(operatorGroupQueueInformer); err != nil {
		return nil, err
	}

	// Wire CatalogSources
	catsrcInformer := crInformerFactory.Operators().V1alpha1().CatalogSources()
	op.lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catsrcInformer.Lister())
	catsrcQueue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: "catsrcs",
		})
	op.catsrcQueueSet.Set(metav1.NamespaceAll, catsrcQueue)
	catsrcQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithMetricsProvider(metrics.NewMetricsCatalogSource(op.lister.OperatorsV1alpha1().CatalogSourceLister())),
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(catsrcQueue),
		queueinformer.WithInformer(catsrcInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncCatalogSources).ToSyncer()),
		queueinformer.WithDeletionHandler(op.handleCatSrcDeletion),
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

	subQueue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: "subs",
		})
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
		subscription.WithAppendedReconcilers(subscription.ReconcilerFromLegacySyncHandler(op.syncSubscriptions)),
		subscription.WithRegistryReconcilerFactory(op.reconciler),
		subscription.WithGlobalCatalogNamespace(op.namespace),
		subscription.WithSourceProvider(op.resolverSourceProvider),
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
	k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), resyncPeriod(), func() []informers.SharedInformerOption {
		if !canFilter {
			return nil
		}
		return []informers.SharedInformerOption{informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labels.SelectorFromSet(labels.Set{install.OLMManagedLabelKey: install.OLMManagedLabelValue}).String()
		})}
	}()...)
	sharedIndexInformers := []cache.SharedIndexInformer{}

	// Wire Roles
	roleInformer := k8sInformerFactory.Rbac().V1().Roles()
	op.lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, roleInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, roleInformer.Informer())

	complete := map[schema.GroupVersionResource][]bool{}
	completeLock := &sync.Mutex{}

	labelObjects := func(gvr schema.GroupVersionResource, informer cache.SharedIndexInformer, sync func(done func() bool) queueinformer.LegacySyncHandler) error {
		if canFilter {
			return nil
		}

		// for each GVR, we may have more than one labelling controller active; each of which detects
		// when it is done; we allocate a space in complete[gvr][idx] to hold that outcome and track it
		var idx int
		if _, exists := complete[gvr]; exists {
			idx = len(complete[gvr])
			complete[gvr] = append(complete[gvr], false)
		} else {
			idx = 0
			complete[gvr] = []bool{false}
		}
		logger := op.logger.WithFields(logrus.Fields{"gvr": gvr.String(), "index": idx})
		logger.Info("registering labeller")

		queue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](), workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: gvr.String(),
		})
		queueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithQueue(queue),
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(informer),
			queueinformer.WithSyncer(sync(func() bool {
				// this function is called by the processor when it detects that it's work is done - so, for that
				// particular labelling action on that particular GVR, all objects are in the correct state. when
				// that action is done, we need to further know if that was the last action to be completed, as
				// when every action we know about has been completed, we re-start the process to allow the future
				// invocation of this process to filter informers (canFilter = true) and elide all this logic
				completeLock.Lock()
				logger.Info("labeller complete")
				complete[gvr][idx] = true
				allDone := true
				for _, items := range complete {
					for _, done := range items {
						allDone = allDone && done
					}
				}
				completeLock.Unlock()
				return allDone
			}).ToSyncer()),
		)
		if err != nil {
			return err
		}

		if err := op.RegisterQueueInformer(queueInformer); err != nil {
			return err
		}

		return nil
	}

	rolesgvk := rbacv1.SchemeGroupVersion.WithResource("roles")
	if err := labelObjects(rolesgvk, roleInformer.Informer(), labeller.ObjectLabeler[*rbacv1.Role, *rbacv1applyconfigurations.RoleApplyConfiguration](
		ctx, op.logger, labeller.Filter(rolesgvk),
		roleInformer.Lister().List,
		rbacv1applyconfigurations.Role,
		func(namespace string, ctx context.Context, cfg *rbacv1applyconfigurations.RoleApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.Role, error) {
			return op.opClient.KubernetesInterface().RbacV1().Roles(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}
	if err := labelObjects(rolesgvk, roleInformer.Informer(), labeller.ContentHashLabeler[*rbacv1.Role, *rbacv1applyconfigurations.RoleApplyConfiguration](
		ctx, op.logger, labeller.ContentHashFilter,
		func(role *rbacv1.Role) (string, error) {
			return resolver.PolicyRuleHashLabelValue(role.Rules)
		},
		roleInformer.Lister().List,
		rbacv1applyconfigurations.Role,
		func(namespace string, ctx context.Context, cfg *rbacv1applyconfigurations.RoleApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.Role, error) {
			return op.opClient.KubernetesInterface().RbacV1().Roles(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Wire RoleBindings
	roleBindingInformer := k8sInformerFactory.Rbac().V1().RoleBindings()
	op.lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, roleBindingInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, roleBindingInformer.Informer())

	rolebindingsgvk := rbacv1.SchemeGroupVersion.WithResource("rolebindings")
	if err := labelObjects(rolebindingsgvk, roleBindingInformer.Informer(), labeller.ObjectLabeler[*rbacv1.RoleBinding, *rbacv1applyconfigurations.RoleBindingApplyConfiguration](
		ctx, op.logger, labeller.Filter(rolebindingsgvk),
		roleBindingInformer.Lister().List,
		rbacv1applyconfigurations.RoleBinding,
		func(namespace string, ctx context.Context, cfg *rbacv1applyconfigurations.RoleBindingApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.RoleBinding, error) {
			return op.opClient.KubernetesInterface().RbacV1().RoleBindings(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}
	if err := labelObjects(rolebindingsgvk, roleBindingInformer.Informer(), labeller.ContentHashLabeler[*rbacv1.RoleBinding, *rbacv1applyconfigurations.RoleBindingApplyConfiguration](
		ctx, op.logger, labeller.ContentHashFilter,
		func(roleBinding *rbacv1.RoleBinding) (string, error) {
			return resolver.RoleReferenceAndSubjectHashLabelValue(roleBinding.RoleRef, roleBinding.Subjects)
		},
		roleBindingInformer.Lister().List,
		rbacv1applyconfigurations.RoleBinding,
		func(namespace string, ctx context.Context, cfg *rbacv1applyconfigurations.RoleBindingApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.RoleBinding, error) {
			return op.opClient.KubernetesInterface().RbacV1().RoleBindings(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Wire ServiceAccounts
	serviceAccountInformer := k8sInformerFactory.Core().V1().ServiceAccounts()
	op.lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, serviceAccountInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, serviceAccountInformer.Informer())

	serviceaccountsgvk := corev1.SchemeGroupVersion.WithResource("serviceaccounts")
	if err := labelObjects(serviceaccountsgvk, serviceAccountInformer.Informer(), labeller.ObjectLabeler[*corev1.ServiceAccount, *corev1applyconfigurations.ServiceAccountApplyConfiguration](
		ctx, op.logger, labeller.ServiceAccountFilter(func(namespace, name string) bool {
			operatorGroups, err := operatorGroupInformer.Lister().OperatorGroups(namespace).List(labels.Everything())
			if err != nil {
				return false
			}
			for _, operatorGroup := range operatorGroups {
				if operatorGroup.Spec.ServiceAccountName == name {
					return true
				}
			}
			return false
		}),
		serviceAccountInformer.Lister().List,
		corev1applyconfigurations.ServiceAccount,
		func(namespace string, ctx context.Context, cfg *corev1applyconfigurations.ServiceAccountApplyConfiguration, opts metav1.ApplyOptions) (*corev1.ServiceAccount, error) {
			return op.opClient.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Wire Services
	serviceInformer := k8sInformerFactory.Core().V1().Services()
	op.lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, serviceInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, serviceInformer.Informer())

	servicesgvk := corev1.SchemeGroupVersion.WithResource("services")
	if err := labelObjects(servicesgvk, serviceInformer.Informer(), labeller.ObjectLabeler[*corev1.Service, *corev1applyconfigurations.ServiceApplyConfiguration](
		ctx, op.logger, labeller.Filter(servicesgvk),
		serviceInformer.Lister().List,
		corev1applyconfigurations.Service,
		func(namespace string, ctx context.Context, cfg *corev1applyconfigurations.ServiceApplyConfiguration, opts metav1.ApplyOptions) (*corev1.Service, error) {
			return op.opClient.KubernetesInterface().CoreV1().Services(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	{
		gvr := servicesgvk
		informer := serviceInformer.Informer()

		logger := op.logger.WithFields(logrus.Fields{"gvr": gvr.String()})
		logger.Info("registering owner reference fixer")

		queue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](), workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: gvr.String(),
		})
		queueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithQueue(queue),
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(informer),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(func(obj interface{}) error {
				service, ok := obj.(*corev1.Service)
				if !ok {
					err := fmt.Errorf("wrong type %T, expected %T: %#v", obj, new(*corev1.Service), obj)
					logger.WithError(err).Error("casting failed")
					return fmt.Errorf("casting failed: %w", err)
				}

				deduped := deduplicateOwnerReferences(service.OwnerReferences)
				if len(deduped) != len(service.OwnerReferences) {
					localCopy := service.DeepCopy()
					localCopy.OwnerReferences = deduped
					if _, err := op.opClient.KubernetesInterface().CoreV1().Services(service.Namespace).Update(ctx, localCopy, metav1.UpdateOptions{}); err != nil {
						return err
					}
				}
				return nil
			}).ToSyncer()),
		)
		if err != nil {
			return nil, err
		}

		if err := op.RegisterQueueInformer(queueInformer); err != nil {
			return nil, err
		}
	}

	// Wire Pods for CatalogSource
	catsrcReq, err := labels.NewRequirement(reconciler.CatalogSourceLabelKey, selection.Exists, nil)
	if err != nil {
		return nil, err
	}

	csPodLabels := labels.NewSelector()
	csPodLabels = csPodLabels.Add(*catsrcReq)
	csPodInformer := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), resyncPeriod(), informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.LabelSelector = csPodLabels.String()
	})).Core().V1().Pods()
	op.lister.CoreV1().RegisterPodLister(metav1.NamespaceAll, csPodInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, csPodInformer.Informer())

	podsgvk := corev1.SchemeGroupVersion.WithResource("pods")
	if err := labelObjects(podsgvk, csPodInformer.Informer(), labeller.ObjectLabeler[*corev1.Pod, *corev1applyconfigurations.PodApplyConfiguration](
		ctx, op.logger, labeller.Filter(podsgvk),
		csPodInformer.Lister().List,
		corev1applyconfigurations.Pod,
		func(namespace string, ctx context.Context, cfg *corev1applyconfigurations.PodApplyConfiguration, opts metav1.ApplyOptions) (*corev1.Pod, error) {
			return op.opClient.KubernetesInterface().CoreV1().Pods(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Wire Pods for BundleUnpack job
	buReq, err := labels.NewRequirement(bundle.BundleUnpackPodLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}

	buPodLabels := labels.NewSelector()
	buPodLabels = buPodLabels.Add(*buReq)
	buPodInformer := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), resyncPeriod(), informers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.LabelSelector = buPodLabels.String()
	})).Core().V1().Pods()
	sharedIndexInformers = append(sharedIndexInformers, buPodInformer.Informer())

	// Wire ConfigMaps
	configMapInformer := k8sInformerFactory.Core().V1().ConfigMaps()
	op.lister.CoreV1().RegisterConfigMapLister(metav1.NamespaceAll, configMapInformer.Lister())
	sharedIndexInformers = append(sharedIndexInformers, configMapInformer.Informer())
	configmapsgvk := corev1.SchemeGroupVersion.WithResource("configmaps")
	if err := labelObjects(configmapsgvk, configMapInformer.Informer(), labeller.ObjectLabeler[*corev1.ConfigMap, *corev1applyconfigurations.ConfigMapApplyConfiguration](
		ctx, op.logger, labeller.Filter(configmapsgvk),
		configMapInformer.Lister().List,
		corev1applyconfigurations.ConfigMap,
		func(namespace string, ctx context.Context, cfg *corev1applyconfigurations.ConfigMapApplyConfiguration, opts metav1.ApplyOptions) (*corev1.ConfigMap, error) {
			return op.opClient.KubernetesInterface().CoreV1().ConfigMaps(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Wire Jobs
	jobInformer := k8sInformerFactory.Batch().V1().Jobs()
	sharedIndexInformers = append(sharedIndexInformers, jobInformer.Informer())

	jobsgvk := batchv1.SchemeGroupVersion.WithResource("jobs")
	if err := labelObjects(jobsgvk, jobInformer.Informer(), labeller.ObjectLabeler[*batchv1.Job, *batchv1applyconfigurations.JobApplyConfiguration](
		ctx, op.logger, labeller.JobFilter(func(namespace, name string) (metav1.Object, error) {
			return configMapInformer.Lister().ConfigMaps(namespace).Get(name)
		}),
		jobInformer.Lister().List,
		batchv1applyconfigurations.Job,
		func(namespace string, ctx context.Context, cfg *batchv1applyconfigurations.JobApplyConfiguration, opts metav1.ApplyOptions) (*batchv1.Job, error) {
			return op.opClient.KubernetesInterface().BatchV1().Jobs(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Generate and register QueueInformers for k8s resources
	k8sSyncer := queueinformer.LegacySyncHandler(op.syncObject).ToSyncer()
	for _, informer := range sharedIndexInformers {
		queueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithLogger(op.logger),
			queueinformer.WithInformer(informer),
			queueinformer.WithSyncer(k8sSyncer),
			queueinformer.WithDeletionHandler(op.handleDeletion),
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
		bundle.WithLogger(op.logger),
		bundle.WithClient(op.opClient.KubernetesInterface()),
		bundle.WithCatalogSourceLister(catsrcInformer.Lister()),
		bundle.WithConfigMapLister(configMapInformer.Lister()),
		bundle.WithJobLister(jobInformer.Lister()),
		bundle.WithPodLister(buPodInformer.Lister()),
		bundle.WithRoleLister(roleInformer.Lister()),
		bundle.WithRoleBindingLister(roleBindingInformer.Lister()),
		bundle.WithOPMImage(opmImage),
		bundle.WithUtilImage(utilImage),
		bundle.WithNow(op.now),
		bundle.WithUnpackTimeout(op.bundleUnpackTimeout),
		bundle.WithUserID(workloadUserID),
	)
	if err != nil {
		return nil, err
	}

	// Register CustomResourceDefinition QueueInformer. Object metadata requests are used
	// by this informer in order to reduce cached size.
	gvr := apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions")
	crdInformer := metadatainformer.NewFilteredMetadataInformer(
		metadataClient,
		gvr,
		metav1.NamespaceAll,
		resyncPeriod(),
		cache.Indexers{},
		nil,
	).Informer()
	crdLister := metadatalister.New(crdInformer.GetIndexer(), gvr)
	op.lister.APIExtensionsV1().RegisterCustomResourceDefinitionLister(crdLister)
	crdQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithInformer(crdInformer),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncObject).ToSyncer()),
		queueinformer.WithDeletionHandler(op.handleDeletion),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(crdQueueInformer); err != nil {
		return nil, err
	}

	customresourcedefinitionsgvk := apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions")
	if err := labelObjects(customresourcedefinitionsgvk, crdInformer, labeller.ObjectPatchLabeler(
		ctx, op.logger, labeller.Filter(customresourcedefinitionsgvk),
		crdLister.List,
		op.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Patch,
	)); err != nil {
		return nil, err
	}

	// Namespace sync for resolving subscriptions
	namespaceInformer := informers.NewSharedInformerFactory(op.opClient.KubernetesInterface(), resyncPeriod()).Core().V1().Namespaces()
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())
	op.nsResolveQueue = workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: "resolve",
		})
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
	metrics.RegisterCatalogSourceState(state.Key.Name, state.Key.Namespace, state.State)

	switch state.State {
	case connectivity.Ready:
		o.resolverSourceProvider.Invalidate(resolvercache.SourceKey(state.Key))
		if o.namespace == state.Key.Namespace {
			namespaces, err := index.CatalogSubscriberNamespaces(o.catalogSubscriberIndexer,
				state.Key.Name, state.Key.Namespace)

			if err == nil {
				for ns := range namespaces {
					o.nsResolveQueue.Add(types.NamespacedName{Name: ns})
				}
			}
		}

		o.nsResolveQueue.Add(types.NamespacedName{Name: state.Key.Namespace})
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
}

func (o *Operator) handleCatSrcDeletion(obj interface{}) {
	catsrc, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}

		catsrc, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Namespace %#v", obj))
			return
		}
	}
	sourceKey := registry.CatalogKey{Name: catsrc.GetName(), Namespace: catsrc.GetNamespace()}
	if err := o.sources.Remove(sourceKey); err != nil {
		o.logger.WithError(err).Warn("error closing client")
	}
	o.logger.WithField("source", sourceKey).Info("removed client for deleted catalogsource")

	metrics.DeleteCatalogSourceStateMetric(catsrc.GetName(), catsrc.GetNamespace())
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

	// The sourceType is valid, clear all status (other than status conditions array) if there's existing invalid spec reason
	if out.Status.Reason == v1alpha1.CatalogSourceSpecInvalidError {
		out.Status = v1alpha1.CatalogSourceStatus{
			Conditions: out.Status.Conditions,
		}
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

	logger = logger.WithFields(logrus.Fields{
		"configmap.namespace": in.Namespace,
		"configmap.name":      in.Spec.ConfigMap,
	})
	logger.Info("checking catsrc configmap state")

	var updateLabel bool
	// Get the catalog source's config map
	configMap, err := o.lister.CoreV1().ConfigMapLister().ConfigMaps(in.GetNamespace()).Get(in.Spec.ConfigMap)
	// Attempt to look up the CM via api call if there is a cache miss
	if apierrors.IsNotFound(err) {
		configMap, err = o.opClient.KubernetesInterface().CoreV1().ConfigMaps(in.GetNamespace()).Get(context.TODO(), in.Spec.ConfigMap, metav1.GetOptions{})
		// Found cm in the cluster, add managed label to configmap
		if err == nil {
			labels := configMap.GetLabels()
			if labels == nil {
				labels = make(map[string]string)
			}

			labels[install.OLMManagedLabelKey] = "false"
			configMap.SetLabels(labels)
			updateLabel = true
		}
	}
	if err != nil {
		syncError = fmt.Errorf("failed to get catalog config map %s: %s", in.Spec.ConfigMap, err)
		out.SetError(v1alpha1.CatalogSourceConfigMapError, syncError)
		return
	}

	if wasOwned := ownerutil.EnsureOwner(configMap, in); !wasOwned || updateLabel {
		configMap, err = o.opClient.KubernetesInterface().CoreV1().ConfigMaps(configMap.GetNamespace()).Update(context.TODO(), configMap, metav1.UpdateOptions{})
		if err != nil {
			syncError = fmt.Errorf("unable to write owner onto catalog source configmap - %v", err)
			out.SetError(v1alpha1.CatalogSourceConfigMapError, syncError)
			return
		}

		logger.Info("adopted configmap")
	}

	if in.Status.ConfigMapResource == nil || !in.Status.ConfigMapResource.IsAMatch(&configMap.ObjectMeta) {
		logger.Info("updating catsrc configmap state")
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

	logger.Info("synchronizing registry server")
	sourceKey := registry.CatalogKey{Name: in.GetName(), Namespace: in.GetNamespace()}
	srcReconciler := o.reconciler.ReconcilerForSource(in)
	if srcReconciler == nil {
		// TODO: Add failure status on catalogsource and remove from sources
		syncError = fmt.Errorf("no reconciler for source type %s", in.Spec.SourceType)
		out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
		return
	}

	healthy, err := srcReconciler.CheckRegistryServer(logger, in)
	if err != nil {
		syncError = err
		out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
		return
	}

	logger.WithField("health", healthy).Infof("checked registry server health")

	if healthy && in.Status.RegistryServiceStatus != nil {
		logger.Info("registry state good")
		continueSync = true
		// return here if catalog does not have polling enabled
		if !out.Poll() {
			logger.Info("polling not enabled, nothing more to do")
			return
		}
	}

	// Registry pod hasn't been created or hasn't been updated since the last configmap update, recreate it
	logger.Info("ensuring registry server")

	err = srcReconciler.EnsureRegistryServer(logger, out)
	if err != nil {
		if _, ok := err.(reconciler.UpdateNotReadyErr); ok {
			logger.Info("requeueing registry server for catalog update check: update pod not yet ready")
			o.catsrcQueueSet.RequeueAfter(out.GetNamespace(), out.GetName(), reconciler.CatalogPollingRequeuePeriod)
			return
		}
		syncError = fmt.Errorf("couldn't ensure registry server - %v", err)
		out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
		return
	}

	logger.Info("ensured registry server")

	// requeue the catalog sync based on the polling interval, for accurate syncs of catalogs with polling enabled
	if out.Spec.UpdateStrategy != nil && out.Spec.UpdateStrategy.RegistryPoll != nil {
		if out.Spec.UpdateStrategy.Interval == nil {
			syncError = fmt.Errorf("empty polling interval; cannot requeue registry server sync without a provided polling interval")
			out.SetError(v1alpha1.CatalogSourceIntervalInvalidError, syncError)
			return
		}
		if out.Spec.UpdateStrategy.RegistryPoll.ParsingError != "" && out.Status.Reason != v1alpha1.CatalogSourceIntervalInvalidError {
			out.SetError(v1alpha1.CatalogSourceIntervalInvalidError, errors.New(out.Spec.UpdateStrategy.RegistryPoll.ParsingError))
		}
		logger.Infof("requeuing registry server sync based on polling interval %s", out.Spec.UpdateStrategy.Interval.Duration.String())
		resyncPeriod := reconciler.SyncRegistryUpdateInterval(out, time.Now())
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

	if source.Address != address {
		source, syncError = connectFunc()
		if syncError != nil {
			out.SetError(v1alpha1.CatalogSourceRegistryServerError, syncError)
			return
		}

		// Set connection status and return.
		updateConnectionStateFunc(out, source)
	}

	// GRPCConnectionState update must fail before
	if out.Status.GRPCConnectionState == nil {
		updateConnectionStateFunc(out, source)
	}

	// connection is already good, but we need to update the sync time
	if o.sourcesLastUpdate.After(out.Status.GRPCConnectionState.LastConnectTime.Time) {
		// Set connection status and return.
		out.Status.GRPCConnectionState.LastConnectTime = now
		out.Status.GRPCConnectionState.LastObservedState = source.ConnectionState.String()
		out.Status.GRPCConnectionState.Address = source.Address
	}

	return
}

func (o *Operator) syncCatalogSources(obj interface{}) (syncError error) {
	catsrc, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		o.logger.Infof("wrong type: %#v", obj)
		syncError = nil
		return
	}

	logger := o.logger.WithFields(logrus.Fields{
		"catalogsource.namespace": catsrc.Namespace,
		"catalogsource.name":      catsrc.Name,
		"id":                      queueinformer.NewLoopID(),
	})
	logger.Info("syncing catalog source")

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

	updateErr := catalogsource.UpdateStatus(logger, o.client, out)
	if syncError == nil && updateErr != nil {
		syncError = updateErr
	}

	return
}

func (o *Operator) syncResolvingNamespace(obj interface{}) error {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		o.logger.Infof("wrong type: %#v", obj)
		return fmt.Errorf("casting Namespace failed")
	}
	namespace := ns.GetName()

	logger := o.logger.WithFields(logrus.Fields{
		"namespace": namespace,
		"id":        queueinformer.NewLoopID(),
	})

	o.gcInstallPlans(logger, namespace)

	// get the set of sources that should be used for resolution and best-effort get their connections working
	logger.Info("resolving sources")

	logger.Info("checking if subscriptions need update")

	subs, err := o.listSubscriptions(namespace)
	if err != nil {
		logger.WithError(err).Debug("couldn't list subscriptions")
		return err
	}

	// If there are no subscriptions, don't attempt to sync the namespace.
	if len(subs) == 0 {
		logger.Info(fmt.Sprintf("No subscriptions were found in namespace %v", namespace))
		return nil
	}

	ogLister := o.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(namespace)
	failForwardEnabled, err := resolver.IsFailForwardEnabled(ogLister)
	if err != nil {
		return err
	}

	unpackTimeout, err := bundle.OperatorGroupBundleUnpackTimeout(ogLister)
	if err != nil {
		return err
	}

	minUnpackRetryInterval, err := bundle.OperatorGroupBundleUnpackRetryInterval(ogLister)
	if err != nil {
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
		sub, changedIP, err := o.ensureSubscriptionInstallPlanState(logger, sub, failForwardEnabled)
		if err != nil {
			logger.Infof("error ensuring installplan state: %v", err)
			return err
		}
		subscriptionUpdated = subscriptionUpdated || changedIP

		// record the current state of the desired corresponding CSV in the status. no-op if we don't know the csv yet.
		sub, changedCSV, err := o.ensureSubscriptionCSVState(logger, sub, failForwardEnabled)
		if err != nil {
			logger.Infof("error recording current state of CSV in status: %v", err)
			return err
		}

		subscriptionUpdated = subscriptionUpdated || changedCSV
		subs[i] = sub
	}
	if subscriptionUpdated {
		logger.Info("subscriptions were updated, wait for a new resolution")
		return nil
	}

	shouldUpdate := false
	for _, sub := range subs {
		shouldUpdate = shouldUpdate || !o.nothingToUpdate(logger, sub)
	}
	if !shouldUpdate {
		logger.Info("all subscriptions up to date")
		return nil
	}

	logger.Info("resolving subscriptions in namespace")

	// resolve a set of steps to apply to a cluster, a set of subscriptions to create/update, and any errors
	steps, bundleLookups, updatedSubs, err := o.resolver.ResolveSteps(namespace)
	if err != nil {
		go o.recorder.Event(ns, corev1.EventTypeWarning, "ResolutionFailed", err.Error())
		// If the error is constraints not satisfiable, then simply project the
		// resolution failure event and move on without returning the error.
		// Returning the error only triggers the namespace resync which is unnecessary
		// given not-satisfiable error is terminal and most likely require intervention
		// from users/admins. Resyncing the namespace again is unlikely to resolve
		// not-satisfiable error
		if _, ok := err.(solver.NotSatisfiable); ok {
			logger.WithError(err).Debug("resolution failed")
			_, updateErr := o.updateSubscriptionStatuses(
				o.setSubsCond(subs, v1alpha1.SubscriptionCondition{
					Type:    v1alpha1.SubscriptionResolutionFailed,
					Reason:  "ConstraintsNotSatisfiable",
					Message: err.Error(),
					Status:  corev1.ConditionTrue,
				}))
			if updateErr != nil {
				logger.WithError(updateErr).Debug("failed to update subs conditions")
				return updateErr
			}
			return nil
		}

		_, updateErr := o.updateSubscriptionStatuses(
			o.setSubsCond(subs, v1alpha1.SubscriptionCondition{
				Type:    v1alpha1.SubscriptionResolutionFailed,
				Reason:  "ErrorPreventedResolution",
				Message: err.Error(),
				Status:  corev1.ConditionTrue,
			}))
		if updateErr != nil {
			logger.WithError(updateErr).Debug("failed to update subs conditions")
			return updateErr
		}
		return err
	}

	// Attempt to unpack bundles before installing
	// Note: This should probably use the attenuated client to prevent users from resolving resources they otherwise don't have access to.
	if len(bundleLookups) > 0 {
		logger.Info("unpacking bundles")

		var unpacked bool
		unpacked, steps, bundleLookups, err = o.unpackBundles(namespace, steps, bundleLookups, unpackTimeout, minUnpackRetryInterval)
		if err != nil {
			// If the error was fatal capture and fail
			if olmerrors.IsFatal(err) {
				_, updateErr := o.updateSubscriptionStatuses(
					o.setSubsCond(subs, v1alpha1.SubscriptionCondition{
						Type:    v1alpha1.SubscriptionBundleUnpackFailed,
						Reason:  "ErrorPreventedUnpacking",
						Message: err.Error(),
						Status:  corev1.ConditionTrue,
					}))
				if updateErr != nil {
					logger.WithError(updateErr).Debug("failed to update subs conditions")
					return updateErr
				}
				return nil
			}
			// Retry sync if non-fatal error
			return fmt.Errorf("bundle unpacking failed with an error: %w", err)
		}

		// Check BundleLookup status conditions to see if the BundleLookupFailed condtion is true
		// which means bundle lookup has failed and subscriptions need to be updated
		// with a condition indicating the failure.
		isFailed, cond := hasBundleLookupFailureCondition(bundleLookups)
		if isFailed {
			err := fmt.Errorf("bundle unpacking failed. Reason: %v, and Message: %v", cond.Reason, cond.Message)
			logger.Infof("%v", err)

			_, updateErr := o.updateSubscriptionStatuses(
				o.setSubsCond(subs, v1alpha1.SubscriptionCondition{
					Type:    v1alpha1.SubscriptionBundleUnpackFailed,
					Reason:  "BundleUnpackFailed",
					Message: err.Error(),
					Status:  corev1.ConditionTrue,
				}))
			if updateErr != nil {
				logger.WithError(updateErr).Debug("failed to update subs conditions")
				return updateErr
			}
			// Since this is likely requires intervention we do not want to
			// requeue too often. We return no error here and rely on a
			// periodic resync which will help to automatically resolve
			// some issues such as unreachable bundle images caused by
			// bad catalog updates.
			return nil
		}

		// This means that the unpack job is still running (most likely) or
		// there was some issue which we did not handle above.
		if !unpacked {
			_, updateErr := o.updateSubscriptionStatuses(
				o.setSubsCond(subs, v1alpha1.SubscriptionCondition{
					Type:   v1alpha1.SubscriptionBundleUnpacking,
					Reason: "UnpackingInProgress",
					Status: corev1.ConditionTrue,
				}))
			if updateErr != nil {
				logger.WithError(updateErr).Debug("failed to update subs conditions")
				return updateErr
			}

			logger.Info("unpacking is not complete yet, requeueing")
			o.nsResolveQueue.AddAfter(types.NamespacedName{Name: namespace}, 5*time.Second)
			return nil
		}
	}

	// create installplan if anything updated
	if len(updatedSubs) > 0 {
		logger.Info("resolution caused subscription changes, creating installplan")
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
			err := fmt.Errorf("error ensuring InstallPlan: %s", err)
			logger.Infof("%v", err)

			_, updateErr := o.updateSubscriptionStatuses(
				o.setSubsCond(subs, v1alpha1.SubscriptionCondition{
					Type:    v1alpha1.SubscriptionBundleUnpackFailed,
					Reason:  "EnsureInstallPlanFailed",
					Message: err.Error(),
					Status:  corev1.ConditionTrue,
				}))
			if updateErr != nil {
				logger.WithError(updateErr).Debug("failed to update subs conditions")
				return updateErr
			}
			return err
		}
		updatedSubs = o.setIPReference(updatedSubs, maxGeneration+1, installPlanReference)
	} else {
		logger.Infof("no subscriptions were updated")
	}

	// Make sure that we no longer indicate unpacking progress
	o.removeSubsCond(subs, v1alpha1.SubscriptionBundleUnpacking)

	// Remove BundleUnpackFailed condition from subscriptions
	o.removeSubsCond(subs, v1alpha1.SubscriptionBundleUnpackFailed)

	// Remove resolutionfailed condition from subscriptions
	o.removeSubsCond(subs, v1alpha1.SubscriptionResolutionFailed)

	newSub := true
	for _, updatedSub := range updatedSubs {
		updatedSub.Status.RemoveConditions(v1alpha1.SubscriptionResolutionFailed)
		for i, sub := range subs {
			if sub.Name == updatedSub.Name && sub.Namespace == updatedSub.Namespace {
				subs[i] = updatedSub
				newSub = false
				break
			}
		}
		if newSub {
			subs = append(subs, updatedSub)
			continue
		}
		newSub = true
	}

	// Update subscriptions with all changes so far
	_, updateErr := o.updateSubscriptionStatuses(subs)
	if updateErr != nil {
		logger.WithError(updateErr).Warn("failed to update subscription conditions")
		return updateErr
	}

	return nil
}

func (o *Operator) syncSubscriptions(obj interface{}) error {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		o.logger.Infof("wrong type: %#v", obj)
		return fmt.Errorf("casting Subscription failed")
	}

	o.nsResolveQueue.Add(types.NamespacedName{Name: sub.GetNamespace()})

	return nil
}

// syncOperatorGroups requeues the namespace resolution queue on changes to an operatorgroup
// This is because the operatorgroup is now an input to resolution via the global catalog exclusion annotation
func (o *Operator) syncOperatorGroups(obj interface{}) error {
	og, ok := obj.(*operatorsv1.OperatorGroup)
	if !ok {
		o.logger.Infof("wrong type: %#v", obj)
		return fmt.Errorf("casting OperatorGroup failed")
	}

	o.nsResolveQueue.Add(types.NamespacedName{Name: og.GetNamespace()})

	return nil
}

func (o *Operator) nothingToUpdate(logger *logrus.Entry, sub *v1alpha1.Subscription) bool {
	if sub.Status.InstallPlanRef != nil && sub.Status.State == v1alpha1.SubscriptionStateUpgradePending {
		logger.Infof("skipping update: installplan already created")
		return true
	}
	return false
}

func (o *Operator) ensureSubscriptionInstallPlanState(logger *logrus.Entry, sub *v1alpha1.Subscription, failForwardEnabled bool) (*v1alpha1.Subscription, bool, error) {
	if sub.Status.InstallPlanRef != nil || sub.Status.Install != nil {
		return sub, false, nil
	}

	logger.Info("checking for existing installplan")

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
	if failForwardEnabled && ip.Status.Phase == v1alpha1.InstallPlanPhaseFailed {
		out.Status.State = v1alpha1.SubscriptionStateFailed
	}
	out.Status.CurrentCSV = out.Spec.StartingCSV
	out.Status.LastUpdated = o.now()

	return out, true, nil
}

func (o *Operator) ensureSubscriptionCSVState(logger *logrus.Entry, sub *v1alpha1.Subscription, failForwardEnabled bool) (*v1alpha1.Subscription, bool, error) {
	if sub.Status.CurrentCSV == "" {
		return sub, false, nil
	}

	_, err := o.client.OperatorsV1alpha1().ClusterServiceVersions(sub.GetNamespace()).Get(context.TODO(), sub.Status.CurrentCSV, metav1.GetOptions{})
	out := sub.DeepCopy()
	if err != nil {
		logger.WithError(err).WithField("currentCSV", sub.Status.CurrentCSV).Debug("error fetching csv listed in subscription status")
		out.Status.State = v1alpha1.SubscriptionStateUpgradePending
		if failForwardEnabled && sub.Status.InstallPlanRef != nil {
			ip, err := o.client.OperatorsV1alpha1().InstallPlans(sub.GetNamespace()).Get(context.TODO(), sub.Status.InstallPlanRef.Name, metav1.GetOptions{})
			if err != nil {
				logger.WithError(err).WithField("currentCSV", sub.Status.CurrentCSV).Debug("error fetching installplan listed in subscription status")
			} else if ip.Status.Phase == v1alpha1.InstallPlanPhaseFailed {
				out.Status.State = v1alpha1.SubscriptionStateFailed
			}
		}
	} else {
		out.Status.State = v1alpha1.SubscriptionStateAtLatest
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
		return nil, false, fmt.Errorf("error updating Subscription status: %s", err.Error())
	}

	// subscription status represents cluster state
	return updatedSub, true, nil
}

func (o *Operator) setIPReference(subs []*v1alpha1.Subscription, gen int, installPlanRef *corev1.ObjectReference) []*v1alpha1.Subscription {
	lastUpdated := o.now()
	for _, sub := range subs {
		sub.Status.LastUpdated = lastUpdated
		if installPlanRef != nil {
			sub.Status.InstallPlanRef = installPlanRef
			sub.Status.Install = v1alpha1.NewInstallPlanReference(installPlanRef)
			sub.Status.State = v1alpha1.SubscriptionStateUpgradePending
			sub.Status.InstallPlanGeneration = gen
		}
	}
	return subs
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

	// There are multiple(2) worker threads process the namespaceQueue.
	// Both worker can work at the same time when 2 separate updates are made for the namespace.
	// The following sequence causes 2 installplans are created for a subscription
	// 1. worker 1 doesn't find the installplan
	// 2. worker 2 doesn't find the installplan
	// 3. both worker 1 and 2 create the installplan
	//
	// This lock prevents the step 2 in the sequence so that only one installplan is created for a subscription.
	// The sequence is like the following with this lock
	// 1. worker 1 locks
	// 2. worker 1 doesn't find the installplan
	// 3. worker 2 wait for unlock       <--- difference
	// 4. worker 1 creates the installplan
	// 5. worker 1 unlocks
	// 6. worker 2 locks
	// 7. worker 2 finds the installplan <--- difference
	// 8. worker 2 unlocks
	o.muInstallPlan.Lock()
	defer o.muInstallPlan.Unlock()

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

// setSubsCond will set the condition to the subscription if it doesn't already
// exist or if it is different
// Only return the list of updated subscriptions
func (o *Operator) setSubsCond(subs []*v1alpha1.Subscription, cond v1alpha1.SubscriptionCondition) []*v1alpha1.Subscription {
	var (
		lastUpdated = o.now()
		subList     []*v1alpha1.Subscription
	)

	for _, sub := range subs {
		subCond := sub.Status.GetCondition(cond.Type)
		if subCond.Equals(cond) {
			continue
		}
		sub.Status.LastUpdated = lastUpdated
		sub.Status.SetCondition(cond)
		subList = append(subList, sub)
	}
	return subList
}

// removeSubsCond removes the given condition from all of the subscriptions in the input
func (o *Operator) removeSubsCond(subs []*v1alpha1.Subscription, condType v1alpha1.SubscriptionConditionType) {
	lastUpdated := o.now()
	for _, sub := range subs {
		cond := sub.Status.GetCondition(condType)
		// if status is ConditionUnknown, the condition doesn't exist. Just skip
		if cond.Status == corev1.ConditionUnknown {
			continue
		}
		sub.Status.LastUpdated = lastUpdated
		sub.Status.RemoveConditions(condType)
	}
}

func (o *Operator) updateSubscriptionStatuses(subs []*v1alpha1.Subscription) ([]*v1alpha1.Subscription, error) {
	var (
		errs       []error
		mu         sync.Mutex
		wg         sync.WaitGroup
		getOpts    = metav1.GetOptions{}
		updateOpts = metav1.UpdateOptions{}
	)

	for _, sub := range subs {
		wg.Add(1)
		go func(sub *v1alpha1.Subscription) {
			defer wg.Done()

			update := func() error {
				// Update the status of the latest revision
				latest, err := o.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).Get(context.TODO(), sub.GetName(), getOpts)
				if err != nil {
					return err
				}
				latest.Status = sub.Status
				*sub = *latest
				_, err = o.client.OperatorsV1alpha1().Subscriptions(sub.Namespace).UpdateStatus(context.TODO(), latest, updateOpts)
				return err
			}
			if err := retry.RetryOnConflict(retry.DefaultRetry, update); err != nil {
				mu.Lock()
				defer mu.Unlock()
				errs = append(errs, err)
			}
		}(sub)
	}
	wg.Wait()
	return subs, utilerrors.NewAggregate(errs)
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

func (o *Operator) unpackBundles(namespace string, installPlanSteps []*v1alpha1.Step, bundleLookups []v1alpha1.BundleLookup, unpackTimeout, unpackRetryInterval time.Duration) (bool, []*v1alpha1.Step, []v1alpha1.BundleLookup, error) {
	unpacked := true

	outBundleLookups := make([]v1alpha1.BundleLookup, len(bundleLookups))
	for i := range bundleLookups {
		bundleLookups[i].DeepCopyInto(&outBundleLookups[i])
	}
	outInstallPlanSteps := make([]*v1alpha1.Step, len(installPlanSteps))
	for i := range installPlanSteps {
		outInstallPlanSteps[i] = installPlanSteps[i].DeepCopy()
	}

	var errs []error
	for i := 0; i < len(outBundleLookups); i++ {
		lookup := outBundleLookups[i]
		res, err := o.bundleUnpacker.UnpackBundle(&lookup, unpackTimeout, unpackRetryInterval)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		outBundleLookups[i] = *res.BundleLookup

		// if the failed condition is present it means the bundle unpacking has failed
		failedCondition := res.GetCondition(v1alpha1.BundleLookupFailed)
		if failedCondition.Status == corev1.ConditionTrue {
			unpacked = false
			continue
		}

		// if the bundle lookup pending condition is present it means that the bundle has not been unpacked
		// status=true means we're still waiting for the job to unpack to configmap
		pendingCondition := res.GetCondition(v1alpha1.BundleLookupPending)
		if pendingCondition.Status == corev1.ConditionTrue {
			unpacked = false
			continue
		}

		// if packed condition is missing, bundle has already been unpacked into steps, continue
		if res.GetCondition(resolver.BundleLookupConditionPacked).Status == corev1.ConditionUnknown {
			continue
		}

		// Ensure that bundle can be applied by the current version of OLM by converting to bundleSteps
		bundleSteps, err := resolver.NewStepsFromBundle(res.Bundle(), namespace, res.Replaces, res.CatalogSourceRef.Name, res.CatalogSourceRef.Namespace)
		if err != nil {
			if fatal := olmerrors.IsFatal(err); fatal {
				return false, nil, nil, err
			}

			errs = append(errs, fmt.Errorf("failed to turn bundle into steps: %v", err))
			unpacked = false
			continue
		}

		// step manifests are replaced with references to the configmap containing them
		for i, s := range bundleSteps {
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
			bundleSteps[i] = s
		}
		res.RemoveCondition(resolver.BundleLookupConditionPacked)
		outBundleLookups[i] = *res.BundleLookup
		outInstallPlanSteps = append(outInstallPlanSteps, bundleSteps...)
	}

	if err := utilerrors.NewAggregate(errs); err != nil {
		o.logger.Infof("failed to unpack bundles: %v", err)
		return false, nil, nil, err
	}

	return unpacked, outInstallPlanSteps, outBundleLookups, nil
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
		o.logger.Infof("wrong type: %#v", obj)
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

	// Complete and Failed are terminal phases
	if plan.Status.Phase == v1alpha1.InstallPlanPhaseFailed || plan.Status.Phase == v1alpha1.InstallPlanPhaseComplete {
		return
	}

	querier := o.serviceAccountQuerier.NamespaceQuerier(plan.GetNamespace())
	ref, err := querier()
	out := plan.DeepCopy()
	if err != nil {
		// Set status condition/message and retry sync if any error
		ipFailError := fmt.Errorf("attenuated service account query failed - %v", err)
		logger.Info(ipFailError.Error())
		_, err := o.setInstallPlanInstalledCond(out, v1alpha1.InstallPlanReasonInstallCheckFailed, err.Error(), logger)
		if err != nil {
			syncError = err
			return
		}
		syncError = ipFailError
		return
	}
	// reset condition/message if it had been set in previous sync. This condition is being reset since any delay in the next steps
	// (bundle unpacking/plan step errors being retried for a duration) could lead to this condition sticking around, even after
	// the serviceAccountQuerier returns no error since the error has been resolved (by creating the required resources), which would
	// be confusing to the user

	// NOTE: this makes the assumption that the InstallPlanInstalledCheckFailed reason is only set in the previous if clause, which is
	// true in the current iteration of the catalog operator. Any future implementation change that aims at setting the reason as
	// InstallPlanInstalledCheckFailed must make sure that either this assumption is not breached, or the condition being set elsewhere
	// is not being unset here unintentionally.
	if cond := out.Status.GetCondition(v1alpha1.InstallPlanInstalled); cond.Reason == v1alpha1.InstallPlanReasonInstallCheckFailed {
		plan, err = o.setInstallPlanInstalledCond(out, v1alpha1.InstallPlanConditionReason(corev1.ConditionUnknown), "", logger)
		if err != nil {
			syncError = err
			return
		}
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

	outInstallPlan, syncError := transitionInstallPlanState(logger.Logger, o, *plan, o.now(), o.installPlanTimeout)

	if syncError != nil {
		logger = logger.WithField("syncError", syncError)
	}

	if outInstallPlan.Status.Phase == v1alpha1.InstallPlanPhaseInstalling {
		defer o.ipQueueSet.RequeueAfter(outInstallPlan.GetNamespace(), outInstallPlan.GetName(), time.Second*5)
	}

	defer o.requeueSubscriptionForInstallPlan(plan, logger)

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

func hasBundleLookupFailureCondition(bundleLookups []v1alpha1.BundleLookup) (bool, *v1alpha1.BundleLookupCondition) {
	for _, bundleLookup := range bundleLookups {
		for _, cond := range bundleLookup.Conditions {
			if cond.Type == v1alpha1.BundleLookupFailed && cond.Status == corev1.ConditionTrue {
				return true, &cond
			}
		}
	}
	return false, nil
}

func (o *Operator) requeueSubscriptionForInstallPlan(plan *v1alpha1.InstallPlan, logger *logrus.Entry) {
	// Notify subscription loop of installplan changes
	owners := ownerutil.GetOwnersByKind(plan, v1alpha1.SubscriptionKind)

	if len(owners) == 0 {
		logger.Trace("no installplan owner subscriptions found to requeue")
		return
	}

	for _, owner := range owners {
		logger.WithField("owner", owner).Debug("requeueing installplan owner")
		if err := o.subQueueSet.Requeue(plan.GetNamespace(), owner.Name); err != nil {
			logger.WithError(err).Warn("error requeuing installplan owner")
		}
	}
}

func (o *Operator) setInstallPlanInstalledCond(ip *v1alpha1.InstallPlan, reason v1alpha1.InstallPlanConditionReason, message string, logger *logrus.Entry) (*v1alpha1.InstallPlan, error) {
	now := o.now()
	ip.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanInstalled, reason, message, &now))
	outIP, err := o.client.OperatorsV1alpha1().InstallPlans(ip.GetNamespace()).UpdateStatus(context.TODO(), ip, metav1.UpdateOptions{})
	if err != nil {
		logger.WithError(err).Error("error updating InstallPlan status")
		return nil, fmt.Errorf("error updating InstallPlan status: %w", err)
	}
	return outIP, nil
}

type installPlanTransitioner interface {
	ExecutePlan(*v1alpha1.InstallPlan) error
}

var _ installPlanTransitioner = &Operator{}

func transitionInstallPlanState(log logrus.FieldLogger, transitioner installPlanTransitioner, in v1alpha1.InstallPlan, now metav1.Time, timeout time.Duration) (*v1alpha1.InstallPlan, error) {
	out := in.DeepCopy()

	switch in.Status.Phase {
	case v1alpha1.InstallPlanPhaseRequiresApproval:
		if out.Spec.Approved {
			out.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
			out.Status.Message = ""
			log.Debugf("approved, setting to %s", out.Status.Phase)
		} else {
			log.Debug("not approved, skipping sync")
		}
		return out, nil

	case v1alpha1.InstallPlanPhaseInstalling:
		if out.Status.StartTime == nil {
			out.Status.StartTime = &now
		}
		log.Debug("attempting to install")
		if err := transitioner.ExecutePlan(out); err != nil {
			if apierrors.IsForbidden(err) || now.Sub(out.Status.StartTime.Time) < timeout {
				// forbidden problems are never terminal since we don't know when a user might provide
				// the service account they specified with more permissions
				out.Status.Message = fmt.Sprintf("retrying execution due to error: %s", err.Error())
			} else {
				out.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanInstalled,
					v1alpha1.InstallPlanReasonComponentFailed, err.Error(), &now))
				out.Status.Phase = v1alpha1.InstallPlanPhaseFailed
				out.Status.Message = err.Error()
			}
			return out, err
		} else if !out.Status.NeedsRequeue() {
			// Loop over one final time to check and see if everything is good.
			out.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanInstalled, &now))
			out.Status.Phase = v1alpha1.InstallPlanPhaseComplete
			out.Status.Message = ""
		}
		return out, nil
	default:
		return out, nil
	}
}

// Validate all existing served versions against new CRD's validation (if changed)
func validateV1CRDCompatibility(dynamicClient dynamic.Interface, oldCRD *apiextensionsv1.CustomResourceDefinition, newCRD *apiextensionsv1.CustomResourceDefinition) error {
	logrus.Debugf("Comparing %#v to %#v", oldCRD.Spec.Versions, newCRD.Spec.Versions)

	oldVersionSet := sets.New[string]()
	for _, oldVersion := range oldCRD.Spec.Versions {
		if !oldVersionSet.Has(oldVersion.Name) && oldVersion.Served {
			oldVersionSet.Insert(oldVersion.Name)
		}
	}

	validationsMap := make(map[string]*apiextensions.CustomResourceValidation, 0)
	for _, newVersion := range newCRD.Spec.Versions {
		if oldVersionSet.Has(newVersion.Name) && newVersion.Served {
			// If the new CRD's version is present in the cluster and still
			// served then fill the map entry with the new validation
			convertedValidation := &apiextensions.CustomResourceValidation{}
			if err := apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(newVersion.Schema, convertedValidation, nil); err != nil {
				return err
			}
			validationsMap[newVersion.Name] = convertedValidation
		}
	}
	return validateExistingCRs(dynamicClient, schema.GroupResource{Group: newCRD.Spec.Group, Resource: newCRD.Spec.Names.Plural}, validationsMap)
}

// Validate all existing served versions against new CRD's validation (if changed)
func validateV1Beta1CRDCompatibility(dynamicClient dynamic.Interface, oldCRD *apiextensionsv1beta1.CustomResourceDefinition, newCRD *apiextensionsv1beta1.CustomResourceDefinition) error {
	logrus.Debugf("Comparing %#v to %#v", oldCRD.Spec.Validation, newCRD.Spec.Validation)
	oldVersionSet := sets.New[string]()
	if len(oldCRD.Spec.Versions) == 0 {
		// apiextensionsv1beta1 special case: if spec.Versions is empty, use the global version and validation
		oldVersionSet.Insert(oldCRD.Spec.Version)
	}
	for _, oldVersion := range oldCRD.Spec.Versions {
		// collect served versions from spec.Versions if the list is present
		if !oldVersionSet.Has(oldVersion.Name) && oldVersion.Served {
			oldVersionSet.Insert(oldVersion.Name)
		}
	}

	validationsMap := make(map[string]*apiextensions.CustomResourceValidation, 0)
	gr := schema.GroupResource{Group: newCRD.Spec.Group, Resource: newCRD.Spec.Names.Plural}
	if len(newCRD.Spec.Versions) == 0 {
		// apiextensionsv1beta1 special case: if spec.Versions of newCRD is empty, use the global version and validation
		if oldVersionSet.Has(newCRD.Spec.Version) {
			convertedValidation := &apiextensions.CustomResourceValidation{}
			if err := apiextensionsv1beta1.Convert_v1beta1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(newCRD.Spec.Validation, convertedValidation, nil); err != nil {
				return err
			}
			validationsMap[newCRD.Spec.Version] = convertedValidation
		}
	}
	for _, newVersion := range newCRD.Spec.Versions {
		if oldVersionSet.Has(newVersion.Name) && newVersion.Served {
			// If the new CRD's version is present in the cluster and still
			// served then fill the map entry with the new validation
			if newCRD.Spec.Validation != nil {
				// apiextensionsv1beta1 special case: spec.Validation and spec.Versions[].Schema are mutually exclusive;
				// if spec.Versions is non-empty and spec.Validation is set then we can validate once against any
				// single existing version.
				convertedValidation := &apiextensions.CustomResourceValidation{}
				if err := apiextensionsv1beta1.Convert_v1beta1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(newCRD.Spec.Validation, convertedValidation, nil); err != nil {
					return err
				}
				return validateExistingCRs(dynamicClient, gr, map[string]*apiextensions.CustomResourceValidation{newVersion.Name: convertedValidation})
			}
			convertedValidation := &apiextensions.CustomResourceValidation{}
			if err := apiextensionsv1beta1.Convert_v1beta1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(newVersion.Schema, convertedValidation, nil); err != nil {
				return err
			}
			validationsMap[newVersion.Name] = convertedValidation
		}
	}
	return validateExistingCRs(dynamicClient, gr, validationsMap)
}

type validationError struct {
	error
}

// validateExistingCRs lists all CRs for each version entry in validationsMap, then validates each using the paired validation.
func validateExistingCRs(dynamicClient dynamic.Interface, gr schema.GroupResource, validationsMap map[string]*apiextensions.CustomResourceValidation) error {
	for version, schemaValidation := range validationsMap {
		// create validator from given crdValidation
		validator, _, err := validation.NewSchemaValidator(schemaValidation.OpenAPIV3Schema)
		if err != nil {
			return fmt.Errorf("error creating validator for schema version %s: %s", version, err)
		}
		gvr := schema.GroupVersionResource{Group: gr.Group, Version: version, Resource: gr.Resource}
		pager := pager.New(pager.SimplePageFunc(func(opts metav1.ListOptions) (runtime.Object, error) {
			return dynamicClient.Resource(gvr).List(context.TODO(), opts)
		}))
		validationFn := func(obj runtime.Object) error {
			// lister will only provide unstructured objects as runtime.Object, so this should never fail to convert
			// if it does, it's a programming error
			cr := obj.(*unstructured.Unstructured)
			err = validation.ValidateCustomResource(field.NewPath(""), cr.UnstructuredContent(), validator).ToAggregate()
			if err != nil {
				var namespacedName string
				if cr.GetNamespace() == "" {
					namespacedName = cr.GetName()
				} else {
					namespacedName = fmt.Sprintf("%s/%s", cr.GetNamespace(), cr.GetName())
				}
				return validationError{fmt.Errorf("error validating %s %q: updated validation is too restrictive: %v", cr.GroupVersionKind(), namespacedName, err)}
			}
			return nil
		}
		err = pager.EachListItem(context.Background(), metav1.ListOptions{}, validationFn)
		if err != nil {
			return err
		}
	}
	return nil
}

type warningRecorder struct {
	m        sync.Mutex
	warnings []string
}

func (wr *warningRecorder) HandleWarningHeader(code int, agent string, text string) {
	if code != 299 {
		return
	}
	wr.m.Lock()
	defer wr.m.Unlock()
	wr.warnings = append(wr.warnings, text)
}

func (wr *warningRecorder) PopWarnings() []string {
	wr.m.Lock()
	defer wr.m.Unlock()

	result := wr.warnings
	wr.warnings = nil
	return result
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
	existingCRDOwners, err := o.getExistingAPIOwners(plan.GetNamespace())
	if err != nil {
		return err
	}

	var wr warningRecorder
	factory := o.clientFactory.WithConfigTransformer(clients.SetWarningHandler(&wr))

	// Does the namespace have an operator group that specifies a user defined
	// service account? If so, then we should use a scoped client for plan
	// execution.
	attenuate, err := o.clientAttenuator.AttenuateToServiceAccount(scoped.StaticQuerier(plan.Status.AttenuatedServiceAccountRef))
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution: %v", err)
		return err
	}
	attenuatedFactory := factory.WithConfigTransformer(attenuate)
	kubeclient, err := attenuatedFactory.NewOperatorClient()
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution: %v", err)
		return err
	}
	crclient, err := attenuatedFactory.NewKubernetesClient()
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution: %v", err)
		return err
	}
	dynamicClient, err := attenuatedFactory.NewDynamicClient()
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution: %v", err)
		return err
	}

	ensurer := newStepEnsurer(kubeclient, crclient, dynamicClient)
	r := newManifestResolver(plan.GetNamespace(), o.lister.CoreV1().ConfigMapLister(), o.logger)

	discoveryQuerier := newDiscoveryQuerier(o.opClient.KubernetesInterface().Discovery())

	// CRDs should be installed via the default OLM (cluster-admin) client and not the scoped client specified by the AttenuatedServiceAccount
	// the StepBuilder is currently only implemented for CRD types
	// TODO give the StepBuilder both OLM and scoped clients when it supports new scoped types
	builderKubeClient, err := factory.NewOperatorClient()
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution- %v", err)
		return err
	}
	builderDynamicClient, err := factory.NewDynamicClient()
	if err != nil {
		o.logger.Errorf("failed to get a client for plan execution- %v", err)
		return err
	}
	b := newBuilder(plan, o.lister.OperatorsV1alpha1().ClusterServiceVersionLister(), builderKubeClient, builderDynamicClient, r, o.logger, o.recorder)

	for i, step := range plan.Status.Plan {
		if err := func(i int, step *v1alpha1.Step) error {
			wr.PopWarnings()
			defer func() {
				warnings := wr.PopWarnings()
				if len(warnings) == 0 {
					return
				}
				var obj runtime.Object
				if ref, err := reference.GetReference(plan); err != nil {
					o.logger.WithError(err).Warnf("error getting plan reference")
					obj = plan
				} else {
					ref.FieldPath = fmt.Sprintf("status.plan[%d]", i)
					obj = ref
				}
				msg := fmt.Sprintf("%d warning(s) generated during operator installation (%s %q): %s", len(warnings), step.Resource.Kind, step.Resource.Name, strings.Join(warnings, ", "))
				if step.Resolving != "" {
					msg = fmt.Sprintf("%d warning(s) generated during installation of operator %q (%s %q): %s", len(warnings), step.Resolving, step.Resource.Kind, step.Resource.Name, strings.Join(warnings, ", "))
				}
				o.recorder.Event(obj, corev1.EventTypeWarning, "AppliedWithWarnings", msg)
				metrics.EmitInstallPlanWarning()
			}()

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
				return nil
			}

			switch step.Status {
			case v1alpha1.StepStatusPresent, v1alpha1.StepStatusCreated, v1alpha1.StepStatusWaitingForAPI:
				return nil
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
					if csv.Labels == nil {
						csv.Labels = map[string]string{}
					}
					csv.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if sub.Labels == nil {
						sub.Labels = map[string]string{}
					}
					sub.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if s.Labels == nil {
						s.Labels = map[string]string{}
					}
					s.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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

					if cr.Labels == nil {
						cr.Labels = map[string]string{}
					}
					cr.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if rb.Labels == nil {
						rb.Labels = map[string]string{}
					}
					rb.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if r.Labels == nil {
						r.Labels = map[string]string{}
					}
					r.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if rb.Labels == nil {
						rb.Labels = map[string]string{}
					}
					rb.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if sa.Labels == nil {
						sa.Labels = map[string]string{}
					}
					sa.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if s.Labels == nil {
						s.Labels = map[string]string{}
					}
					s.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					if cfg.Labels == nil {
						cfg.Labels = map[string]string{}
					}
					cfg.Labels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

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
					l := unstructuredObject.GetLabels()
					if l == nil {
						l = map[string]string{}
					}
					l[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
					unstructuredObject.SetLabels(l)

					// Set up the dynamic client ResourceInterface and set ownerrefs
					var resourceInterface dynamic.ResourceInterface
					if r.Namespaced {
						unstructuredObject.SetNamespace(namespace)
						resourceInterface = dynamicClient.Resource(gvr).Namespace(namespace)
					} else {
						resourceInterface = dynamicClient.Resource(gvr)
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
			return nil
		}(i, step); err != nil {
			if apierrors.IsNotFound(err) {
				// Check for APIVersions present in the installplan steps that are not available on the server.
				// The check is made via discovery per step in the plan. Transient communication failures to the api-server are handled by the plan retry logic.
				notFoundErr := discoveryQuerier.WithStepResource(step.Resource).QueryForGVK()
				if notFoundErr != nil {
					return notFoundErr
				}
			}
			return err
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

// getExistingAPIOwners creates a map of CRD names to existing owner CSVs in the given namespace
func (o *Operator) getExistingAPIOwners(namespace string) (map[string][]string, error) {
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
	return metav1.APIResource{}, olmerrors.GroupVersionKindNotFoundError{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind}
}

func deduplicateOwnerReferences(refs []metav1.OwnerReference) []metav1.OwnerReference {
	return sets.New(refs...).UnsortedList()
}
