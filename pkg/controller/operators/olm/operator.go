package olm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/labeller"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/plugins"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	appsv1applyconfigurations "k8s.io/client-go/applyconfigurations/apps/v1"
	rbacv1applyconfigurations "k8s.io/client-go/applyconfigurations/rbac/v1"
	"k8s.io/client-go/informers"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/metadata/metadatainformer"
	"k8s.io/client-go/metadata/metadatalister"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"
	utilclock "k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
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

// this unexported operator plugin slice provides an entrypoint for
// downstream to inject its own plugins to augment the controller behavior
var operatorPlugInFactoryFuncs []plugins.OperatorPlugInFactoryFunc

type Operator struct {
	queueinformer.Operator

	clock                        utilclock.Clock
	logger                       *logrus.Logger
	opClient                     operatorclient.ClientInterface
	client                       versioned.Interface
	lister                       operatorlister.OperatorLister
	protectedCopiedCSVNamespaces map[string]struct{}
	copiedCSVLister              metadatalister.Lister
	ogQueueSet                   *queueinformer.ResourceQueueSet
	csvQueueSet                  *queueinformer.ResourceQueueSet
	olmConfigQueue               workqueue.TypedRateLimitingInterface[any]
	csvCopyQueueSet              *queueinformer.ResourceQueueSet
	copiedCSVGCQueueSet          *queueinformer.ResourceQueueSet
	nsQueueSet                   workqueue.TypedRateLimitingInterface[any]
	apiServiceQueue              workqueue.TypedRateLimitingInterface[any]
	csvIndexers                  map[string]cache.Indexer
	recorder                     record.EventRecorder
	resolver                     install.StrategyResolverInterface
	apiReconciler                APIIntersectionReconciler
	apiLabeler                   labeler.Labeler
	csvSetGenerator              csvutility.SetGenerator
	csvReplaceFinder             csvutility.ReplaceFinder
	csvNotification              csvutility.WatchNotification
	serviceAccountSyncer         *scoped.UserDefinedServiceAccountSyncer
	clientAttenuator             *scoped.ClientAttenuator
	serviceAccountQuerier        *scoped.UserDefinedServiceAccountQuerier
	clientFactory                clients.Factory
	plugins                      []plugins.OperatorPlugin
	informersByNamespace         map[string]*plugins.Informers
	informersFiltered            bool

	ruleChecker     func(*v1alpha1.ClusterServiceVersion) *install.CSVRuleChecker
	ruleCheckerLock sync.RWMutex
	resyncPeriod    func() time.Duration
	ctx             context.Context
}

func (a *Operator) Informers() map[string]*plugins.Informers {
	return a.informersByNamespace
}

func (a *Operator) getRuleChecker() func(*v1alpha1.ClusterServiceVersion) *install.CSVRuleChecker {
	var ruleChecker func(*v1alpha1.ClusterServiceVersion) *install.CSVRuleChecker
	a.ruleCheckerLock.RLock()
	ruleChecker = a.ruleChecker
	a.ruleCheckerLock.RUnlock()
	if ruleChecker != nil {
		return ruleChecker
	}

	a.ruleCheckerLock.Lock()
	defer a.ruleCheckerLock.Unlock()
	if a.ruleChecker != nil {
		return a.ruleChecker
	}

	sif := informers.NewSharedInformerFactoryWithOptions(a.opClient.KubernetesInterface(), a.resyncPeriod())
	rolesLister := sif.Rbac().V1().Roles().Lister()
	roleBindingsLister := sif.Rbac().V1().RoleBindings().Lister()
	clusterRolesLister := sif.Rbac().V1().ClusterRoles().Lister()
	clusterRoleBindingsLister := sif.Rbac().V1().ClusterRoleBindings().Lister()

	sif.Start(a.ctx.Done())
	sif.WaitForCacheSync(a.ctx.Done())

	if a.ctx.Err() != nil {
		a.ruleChecker = nil
		return nil
	}

	a.ruleChecker = func(csv *v1alpha1.ClusterServiceVersion) *install.CSVRuleChecker {
		return install.NewCSVRuleChecker(
			rolesLister, roleBindingsLister,
			clusterRolesLister, clusterRoleBindingsLister,
			csv,
		)
	}
	return a.ruleChecker
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
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		return nil, err
	}

	canFilter, err := labeller.Validate(ctx, config.logger, config.metadataClient, config.externalClient, labeller.IdentityOLMOperator)
	if err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:    queueOperator,
		clock:       config.clock,
		logger:      config.logger,
		opClient:    config.operatorClient,
		client:      config.externalClient,
		ogQueueSet:  queueinformer.NewEmptyResourceQueueSet(),
		csvQueueSet: queueinformer.NewEmptyResourceQueueSet(),
		olmConfigQueue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{
				Name: "olmConfig",
			}),

		csvCopyQueueSet:     queueinformer.NewEmptyResourceQueueSet(),
		copiedCSVGCQueueSet: queueinformer.NewEmptyResourceQueueSet(),
		apiServiceQueue: workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{
				Name: "apiservice",
			}),
		resolver:                     config.strategyResolver,
		apiReconciler:                config.apiReconciler,
		lister:                       lister,
		recorder:                     eventRecorder,
		apiLabeler:                   config.apiLabeler,
		csvIndexers:                  map[string]cache.Indexer{},
		csvSetGenerator:              csvutility.NewSetGenerator(config.logger, lister),
		csvReplaceFinder:             csvutility.NewReplaceFinder(config.logger, config.externalClient),
		serviceAccountSyncer:         scoped.NewUserDefinedServiceAccountSyncer(config.logger, scheme, config.operatorClient, config.externalClient),
		clientAttenuator:             scoped.NewClientAttenuator(config.logger, config.restConfig, config.operatorClient),
		serviceAccountQuerier:        scoped.NewUserDefinedServiceAccountQuerier(config.logger, config.externalClient),
		clientFactory:                clients.NewFactory(config.restConfig),
		protectedCopiedCSVNamespaces: config.protectedCopiedCSVNamespaces,
		resyncPeriod:                 config.resyncPeriod,
		ruleCheckerLock:              sync.RWMutex{},
		ctx:                          ctx,
		informersFiltered:            canFilter,
	}

	informersByNamespace := map[string]*plugins.Informers{}
	// Set up syncing for namespace-scoped resources
	k8sSyncer := queueinformer.LegacySyncHandler(op.syncObject).ToSyncerWithDelete(op.handleDeletion)
	for _, namespace := range config.watchedNamespaces {
		informersByNamespace[namespace] = &plugins.Informers{}
		// Wire CSVs
		csvInformer := externalversions.NewSharedInformerFactoryWithOptions(
			op.client,
			config.resyncPeriod(),
			externalversions.WithNamespace(namespace),
			externalversions.WithTweakListOptions(func(options *metav1.ListOptions) {
				options.LabelSelector = fmt.Sprintf("!%s", v1alpha1.CopiedLabelKey)
			}),
		).Operators().V1alpha1().ClusterServiceVersions()
		informersByNamespace[namespace].CSVInformer = csvInformer
		op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, csvInformer.Lister())
		csvQueue := workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{
				Name: fmt.Sprintf("%s/csv", namespace),
			})
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
		csvCopyQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[any](), fmt.Sprintf("%s/csv-copy", namespace))
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

		// A separate informer solely for CSV copies. Object metadata requests are used
		// by this informer in order to reduce cached size.
		gvr := v1alpha1.SchemeGroupVersion.WithResource("clusterserviceversions")
		copiedCSVInformer := metadatainformer.NewFilteredMetadataInformer(
			config.metadataClient,
			gvr,
			namespace,
			config.resyncPeriod(),
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			func(options *metav1.ListOptions) {
				options.LabelSelector = v1alpha1.CopiedLabelKey
			},
		).Informer()
		op.copiedCSVLister = metadatalister.New(copiedCSVInformer.GetIndexer(), gvr)
		informersByNamespace[namespace].CopiedCSVInformer = copiedCSVInformer
		informersByNamespace[namespace].CopiedCSVLister = op.copiedCSVLister

		// Register separate queue for gcing copied csvs
		copiedCSVGCQueue := workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{
				Name: fmt.Sprintf("%s/csv-gc", namespace),
			})
		op.copiedCSVGCQueueSet.Set(namespace, copiedCSVGCQueue)
		copiedCSVGCQueueInformer, err := queueinformer.NewQueueInformer(
			ctx,
			queueinformer.WithInformer(copiedCSVInformer),
			queueinformer.WithLogger(op.logger),
			queueinformer.WithQueue(copiedCSVGCQueue),
			queueinformer.WithIndexer(copiedCSVInformer.GetIndexer()),
			queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncGcCsv).ToSyncer()),
		)
		if err != nil {
			return nil, err
		}
		if err := op.RegisterQueueInformer(copiedCSVGCQueueInformer); err != nil {
			return nil, err
		}

		// Wire OperatorGroup reconciliation
		extInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(op.client, config.resyncPeriod(), externalversions.WithNamespace(namespace))
		operatorGroupInformer := extInformerFactory.Operators().V1().OperatorGroups()
		informersByNamespace[namespace].OperatorGroupInformer = operatorGroupInformer
		op.lister.OperatorsV1().RegisterOperatorGroupLister(namespace, operatorGroupInformer.Lister())
		ogQueue := workqueue.NewTypedRateLimitingQueueWithConfig[any](
			workqueue.DefaultTypedControllerRateLimiter[any](),
			workqueue.TypedRateLimitingQueueConfig[any]{
				Name: fmt.Sprintf("%s/og", namespace),
			})
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
		informersByNamespace[namespace].OperatorConditionInformer = opConditionInformer
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
		informersByNamespace[namespace].SubscriptionInformer = subInformer
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
		k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), config.resyncPeriod(), func() []informers.SharedInformerOption {
			opts := []informers.SharedInformerOption{
				informers.WithNamespace(namespace),
			}
			if canFilter {
				opts = append(opts, informers.WithTweakListOptions(func(options *metav1.ListOptions) {
					options.LabelSelector = labels.SelectorFromSet(labels.Set{install.OLMManagedLabelKey: install.OLMManagedLabelValue}).String()
				}))
			}
			return opts
		}()...)
		depInformer := k8sInformerFactory.Apps().V1().Deployments()
		informersByNamespace[namespace].DeploymentInformer = depInformer
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
		informersByNamespace[namespace].RoleInformer = roleInformer
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
		informersByNamespace[namespace].RoleBindingInformer = roleBindingInformer
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
		informersByNamespace[namespace].SecretInformer = secretInformer
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
		informersByNamespace[namespace].ServiceInformer = serviceInformer
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
		informersByNamespace[namespace].ServiceAccountInformer = serviceAccountInformer
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
	}

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

		queue := workqueue.NewTypedRateLimitingQueueWithConfig[any](workqueue.DefaultTypedControllerRateLimiter[any](), workqueue.TypedRateLimitingQueueConfig[any]{
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

	deploymentsgvk := appsv1.SchemeGroupVersion.WithResource("deployments")
	if err := labelObjects(deploymentsgvk, informersByNamespace[metav1.NamespaceAll].DeploymentInformer.Informer(), labeller.ObjectLabeler[*appsv1.Deployment, *appsv1applyconfigurations.DeploymentApplyConfiguration](
		ctx, op.logger, labeller.Filter(deploymentsgvk),
		informersByNamespace[metav1.NamespaceAll].DeploymentInformer.Lister().List,
		appsv1applyconfigurations.Deployment,
		func(namespace string, ctx context.Context, cfg *appsv1applyconfigurations.DeploymentApplyConfiguration, opts metav1.ApplyOptions) (*appsv1.Deployment, error) {
			return op.opClient.KubernetesInterface().AppsV1().Deployments(namespace).Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// Register QueueInformer for olmConfig
	olmConfigInformer := externalversions.NewSharedInformerFactoryWithOptions(
		op.client,
		config.resyncPeriod(),
	).Operators().V1().OLMConfigs()
	informersByNamespace[metav1.NamespaceAll].OLMConfigInformer = olmConfigInformer
	olmConfigQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithInformer(olmConfigInformer.Informer()),
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(op.olmConfigQueue),
		queueinformer.WithIndexer(olmConfigInformer.Informer().GetIndexer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncOLMConfig).ToSyncer()),
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(olmConfigQueueInformer); err != nil {
		return nil, err
	}

	k8sInformerFactory := informers.NewSharedInformerFactoryWithOptions(op.opClient.KubernetesInterface(), config.resyncPeriod(), func() []informers.SharedInformerOption {
		if !canFilter {
			return nil
		}
		return []informers.SharedInformerOption{informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labels.SelectorFromSet(labels.Set{install.OLMManagedLabelKey: install.OLMManagedLabelValue}).String()
		})}
	}()...)
	clusterRoleInformer := k8sInformerFactory.Rbac().V1().ClusterRoles()
	informersByNamespace[metav1.NamespaceAll].ClusterRoleInformer = clusterRoleInformer
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

	clusterrolesgvk := rbacv1.SchemeGroupVersion.WithResource("clusterroles")
	if err := labelObjects(clusterrolesgvk, clusterRoleInformer.Informer(), labeller.ObjectLabeler[*rbacv1.ClusterRole, *rbacv1applyconfigurations.ClusterRoleApplyConfiguration](
		ctx, op.logger, labeller.Filter(clusterrolesgvk),
		clusterRoleInformer.Lister().List,
		func(name, _ string) *rbacv1applyconfigurations.ClusterRoleApplyConfiguration {
			return rbacv1applyconfigurations.ClusterRole(name)
		},
		func(_ string, ctx context.Context, cfg *rbacv1applyconfigurations.ClusterRoleApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.ClusterRole, error) {
			return op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}
	if err := labelObjects(clusterrolesgvk, clusterRoleInformer.Informer(), labeller.ContentHashLabeler[*rbacv1.ClusterRole, *rbacv1applyconfigurations.ClusterRoleApplyConfiguration](
		ctx, op.logger, labeller.ContentHashFilter,
		func(clusterRole *rbacv1.ClusterRole) (string, error) {
			return resolver.PolicyRuleHashLabelValue(clusterRole.Rules)
		},
		clusterRoleInformer.Lister().List,
		func(name, _ string) *rbacv1applyconfigurations.ClusterRoleApplyConfiguration {
			return rbacv1applyconfigurations.ClusterRole(name)
		},
		func(_ string, ctx context.Context, cfg *rbacv1applyconfigurations.ClusterRoleApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.ClusterRole, error) {
			return op.opClient.KubernetesInterface().RbacV1().ClusterRoles().Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	clusterRoleBindingInformer := k8sInformerFactory.Rbac().V1().ClusterRoleBindings()
	informersByNamespace[metav1.NamespaceAll].ClusterRoleBindingInformer = clusterRoleBindingInformer
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

	clusterrolebindingssgvk := rbacv1.SchemeGroupVersion.WithResource("clusterrolebindings")
	if err := labelObjects(clusterrolebindingssgvk, clusterRoleBindingInformer.Informer(), labeller.ObjectLabeler[*rbacv1.ClusterRoleBinding, *rbacv1applyconfigurations.ClusterRoleBindingApplyConfiguration](
		ctx, op.logger, labeller.Filter(clusterrolebindingssgvk),
		clusterRoleBindingInformer.Lister().List,
		func(name, _ string) *rbacv1applyconfigurations.ClusterRoleBindingApplyConfiguration {
			return rbacv1applyconfigurations.ClusterRoleBinding(name)
		},
		func(_ string, ctx context.Context, cfg *rbacv1applyconfigurations.ClusterRoleBindingApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.ClusterRoleBinding, error) {
			return op.opClient.KubernetesInterface().RbacV1().ClusterRoleBindings().Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}
	if err := labelObjects(clusterrolebindingssgvk, clusterRoleBindingInformer.Informer(), labeller.ContentHashLabeler[*rbacv1.ClusterRoleBinding, *rbacv1applyconfigurations.ClusterRoleBindingApplyConfiguration](
		ctx, op.logger, labeller.ContentHashFilter,
		func(clusterRoleBinding *rbacv1.ClusterRoleBinding) (string, error) {
			return resolver.RoleReferenceAndSubjectHashLabelValue(clusterRoleBinding.RoleRef, clusterRoleBinding.Subjects)
		},
		clusterRoleBindingInformer.Lister().List,
		func(name, _ string) *rbacv1applyconfigurations.ClusterRoleBindingApplyConfiguration {
			return rbacv1applyconfigurations.ClusterRoleBinding(name)
		},
		func(_ string, ctx context.Context, cfg *rbacv1applyconfigurations.ClusterRoleBindingApplyConfiguration, opts metav1.ApplyOptions) (*rbacv1.ClusterRoleBinding, error) {
			return op.opClient.KubernetesInterface().RbacV1().ClusterRoleBindings().Apply(ctx, cfg, opts)
		},
	)); err != nil {
		return nil, err
	}

	// register namespace queueinformer using a new informer factory - since namespaces won't have the labels
	// that other k8s objects will
	namespaceInformer := informers.NewSharedInformerFactory(op.opClient.KubernetesInterface(), config.resyncPeriod()).Core().V1().Namespaces()
	informersByNamespace[metav1.NamespaceAll].NamespaceInformer = namespaceInformer
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())
	op.nsQueueSet = workqueue.NewTypedRateLimitingQueueWithConfig[any](
		workqueue.DefaultTypedControllerRateLimiter[any](),
		workqueue.TypedRateLimitingQueueConfig[any]{
			Name: "resolver",
		})
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
	informersByNamespace[metav1.NamespaceAll].APIServiceInformer = apiServiceInformer
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

	// Register CustomResourceDefinition QueueInformer. Object metadata requests are used
	// by this informer in order to reduce cached size.
	gvr := apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions")
	crdInformer := metadatainformer.NewFilteredMetadataInformer(
		config.metadataClient,
		gvr,
		metav1.NamespaceAll,
		config.resyncPeriod(),
		cache.Indexers{},
		nil,
	).Informer()
	crdLister := metadatalister.New(crdInformer.GetIndexer(), gvr)
	informersByNamespace[metav1.NamespaceAll].CRDInformer = crdInformer
	informersByNamespace[metav1.NamespaceAll].CRDLister = crdLister
	op.lister.APIExtensionsV1().RegisterCustomResourceDefinitionLister(crdLister)
	crdQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithInformer(crdInformer),
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
	op.informersByNamespace = informersByNamespace

	// initialize plugins
	for _, makePlugIn := range operatorPlugInFactoryFuncs {
		plugin, err := makePlugIn(ctx, config, op)
		if err != nil {
			return nil, fmt.Errorf("error creating plugin: %s", err)
		}
		op.plugins = append(op.plugins, plugin)
	}

	if len(operatorPlugInFactoryFuncs) > 0 {
		go func() {
			// block until operator is done
			<-op.Done()

			// shutdown plug-ins
			for _, plugin := range op.plugins {
				if err := plugin.Shutdown(); err != nil {
					if op.logger != nil {
						op.logger.Warnf("error shutting down plug-in: %s", err)
					}
				}
			}
		}()
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
		if apierrors.IsNotFound(err) {
			logger.Debug("Deleting api service since owning namespace is not found")
			syncError = a.opClient.DeleteAPIService(apiService.GetName(), &metav1.DeleteOptions{})
			return
		}

		_, err = a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
		if apierrors.IsNotFound(err) {
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

func (a *Operator) EnsureCSVMetric() error {
	csvs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().List(labels.Everything())
	if err != nil {
		return err
	}
	for _, csv := range csvs {
		logger := a.logger.WithFields(logrus.Fields{
			"name":      csv.GetName(),
			"namespace": csv.GetNamespace(),
			"self":      csv.GetSelfLink(),
		})
		logger.Debug("emitting metrics for existing CSV")
		metrics.EmitCSVMetric(csv, csv)
	}
	return nil
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

	// Objects that can't have ownerrefs (cluster -> namespace, cross-namespace)
	// are handled by finalizer

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
			csv = csv.DeepCopy()
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
		if NewNamespaceSet(group.Status.Namespaces).Contains(namespace.GetName()) {
			if err := a.ogQueueSet.Requeue(group.Namespace, group.Name); err != nil {
				logger.WithError(err).Warn("error requeuing operatorgroup")
			}
		}
	}
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

	operatorGroupList, err := a.lister.OperatorsV1().OperatorGroupLister().List(labels.Everything())
	if err != nil {
		logger.WithError(err).Warn("lister failed")
		return err
	}

	desiredGroupLabels := make(map[string]string)
	for _, group := range operatorGroupList {
		namespaceSet := NewNamespaceSet(group.Status.Namespaces)

		// Apply the label if not an All Namespaces OperatorGroup.
		if namespaceSet.Contains(namespace.GetName()) && !namespaceSet.IsAllNamespaces() {
			k, v, err := group.OGLabelKeyAndValue()
			if err != nil {
				return err
			}
			desiredGroupLabels[k] = v
		}
	}

	if changed := func() bool {
		for ke, ve := range namespace.Labels {
			if !operatorsv1.IsOperatorGroupLabel(ke) {
				continue
			}
			if vd, ok := desiredGroupLabels[ke]; !ok || vd != ve {
				return true
			}
		}
		for kd, vd := range desiredGroupLabels {
			if ve, ok := namespace.Labels[kd]; !ok || ve != vd {
				return true
			}
		}
		return false
	}(); !changed {
		return nil
	}

	namespace = namespace.DeepCopy()
	for k := range namespace.Labels {
		if operatorsv1.IsOperatorGroupLabel(k) {
			delete(namespace.Labels, k)
		}
	}
	if namespace.Labels == nil && len(desiredGroupLabels) > 0 {
		namespace.Labels = make(map[string]string)
	}
	for k, v := range desiredGroupLabels {
		namespace.Labels[k] = v
	}

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

	logger.Debug("start deleting CSV")
	defer logger.Debug("end deleting CSV")

	metrics.DeleteCSVMetric(clusterServiceVersion)

	if clusterServiceVersion.IsCopied() {
		logger.Warning("deleted csv is copied. skipping additional cleanup steps") // should not happen?
		return
	}

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

	targetNamespaces, ok := clusterServiceVersion.Annotations[operatorsv1.OperatorGroupTargetsAnnotationKey]
	if !ok {
		logger.Debug("missing target namespaces annotation on csv")
		return
	}

	operatorNamespace, ok := clusterServiceVersion.Annotations[operatorsv1.OperatorGroupNamespaceAnnotationKey]
	if !ok {
		logger.Debug("missing operator namespace annotation on csv")
		return
	}

	if _, ok = clusterServiceVersion.Annotations[operatorsv1.OperatorGroupAnnotationKey]; !ok {
		logger.Debug("missing operatorgroup name annotation on csv")
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
			if err := a.copiedCSVGCQueueSet.Requeue(namespace, clusterServiceVersion.GetName()); err != nil {
				logger.WithError(err).Warn("unable to requeue")
			}
		}
	}

	for _, desc := range clusterServiceVersion.Spec.APIServiceDefinitions.Owned {
		apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
		fetched, err := a.lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
		if apierrors.IsNotFound(err) {
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

	// Conversion webhooks are defined within a CRD.
	// In an effort to prevent customer dataloss, OLM does not delete CRDs associated with a CSV when it is deleted.
	// Deleting a CSV that introduced a conversion webhook removes the deployment that serviced the conversion webhook calls.
	// If a conversion webhook is defined and the service isn't available, all requests against the CR associated with the CRD will fail.
	// This ultimately breaks kubernetes garbage collection and prevents OLM from reinstalling the CSV as CR validation against the new CRD's
	// openapiv3 schema fails.
	// As such, when a CSV is deleted OLM will check if it is being replaced. If the CSV is not being replaced, OLM will remove the conversion
	// webhook from the CRD definition.
	csvs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(clusterServiceVersion.GetNamespace()).List(labels.Everything())
	if err != nil {
		logger.Errorf("error listing csvs: %v\n", err)
	}
	for _, csv := range csvs {
		if csv.Spec.Replaces == clusterServiceVersion.GetName() {
			return
		}
	}

	for _, desc := range clusterServiceVersion.Spec.WebhookDefinitions {
		if desc.Type != v1alpha1.ConversionWebhook || len(desc.ConversionCRDs) == 0 {
			continue
		}

		for i, crdName := range desc.ConversionCRDs {
			crd, err := a.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
			if err != nil {
				logger.Errorf("error getting CRD %v which was defined in CSVs spec.WebhookDefinition[%d]: %v\n", crdName, i, err)
				continue
			}

			copy := crd.DeepCopy()
			copy.Spec.Conversion.Strategy = apiextensionsv1.NoneConverter
			copy.Spec.Conversion.Webhook = nil

			if _, err = a.opClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Update(context.TODO(), copy, metav1.UpdateOptions{}); err != nil {
				logger.Errorf("error updating conversion strategy for CRD %v: %v\n", crdName, err)
			}
		}
	}
}

func (a *Operator) removeDanglingChildCSVs(csv *metav1.PartialObjectMetadata) error {
	logger := a.logger.WithFields(logrus.Fields{
		"id":          queueinformer.NewLoopID(),
		"csv":         csv.GetName(),
		"namespace":   csv.GetNamespace(),
		"labels":      csv.GetLabels(),
		"annotations": csv.GetAnnotations(),
	})

	if !v1alpha1.IsCopied(csv) {
		logger.Warning("removeDanglingChild called on a parent. this is a no-op but should be avoided.")
		return nil
	}

	operatorNamespace, ok := csv.Annotations[operatorsv1.OperatorGroupNamespaceAnnotationKey]
	if !ok {
		logger.Debug("missing operator namespace annotation on copied CSV")
		return a.deleteChild(csv, logger)
	}

	logger = logger.WithField("parentNamespace", operatorNamespace)
	parent, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(operatorNamespace).Get(csv.GetName())
	if apierrors.IsNotFound(err) || apierrors.IsGone(err) || parent == nil {
		logger.Debug("deleting copied CSV since parent is missing")
		return a.deleteChild(csv, logger)
	}

	if parent.Status.Phase == v1alpha1.CSVPhaseFailed && parent.Status.Reason == v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
		logger.Debug("deleting copied CSV since parent has intersecting operatorgroup conflict")
		return a.deleteChild(csv, logger)
	}

	if annotations := parent.GetAnnotations(); annotations != nil {
		if !NewNamespaceSetFromString(annotations[operatorsv1.OperatorGroupTargetsAnnotationKey]).Contains(csv.GetNamespace()) {
			logger.WithField("parentTargets", annotations[operatorsv1.OperatorGroupTargetsAnnotationKey]).
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

func (a *Operator) deleteChild(csv *metav1.PartialObjectMetadata, logger *logrus.Entry) error {
	logger.Debug("gcing csv")
	return a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Delete(context.TODO(), csv.GetName(), metav1.DeleteOptions{})
}

// Return values, err, ok; ok == true: continue Reconcile, ok == false: exit Reconcile
func (a *Operator) processFinalizer(csv *v1alpha1.ClusterServiceVersion, log *logrus.Entry) (error, bool) {
	myFinalizerName := "operators.coreos.com/csv-cleanup"

	if csv.ObjectMeta.DeletionTimestamp.IsZero() {
		// CSV is not being deleted, add finalizer if not present
		if !controllerutil.ContainsFinalizer(csv, myFinalizerName) {
			controllerutil.AddFinalizer(csv, myFinalizerName)
			_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(a.ctx, csv, metav1.UpdateOptions{})
			if err != nil {
				log.WithError(err).Error("Adding finalizer")
				return err, false
			}
		}
		return nil, true
	}

	if !controllerutil.ContainsFinalizer(csv, myFinalizerName) {
		// Finalizer has been removed; stop reconciliation as the CSV is being deleted
		return nil, false
	}

	log.Info("started finalizer")
	defer log.Info("completed finalizer")

	// CSV is being deleted and the finalizer still present; do any clean up
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	listOptions := metav1.ListOptions{
		LabelSelector: ownerSelector.String(),
	}
	deleteOptions := metav1.DeleteOptions{}
	// Look for resources owned by this CSV, and delete them.
	log.WithFields(logrus.Fields{"selector": ownerSelector}).Info("Cleaning up resources after CSV deletion")
	var errs []error

	err := a.opClient.KubernetesInterface().RbacV1().ClusterRoleBindings().DeleteCollection(a.ctx, deleteOptions, listOptions)
	if client.IgnoreNotFound(err) != nil {
		log.WithError(err).Error("Deleting ClusterRoleBindings on CSV delete")
		errs = append(errs, err)
	}

	err = a.opClient.KubernetesInterface().RbacV1().ClusterRoles().DeleteCollection(a.ctx, deleteOptions, listOptions)
	if client.IgnoreNotFound(err) != nil {
		log.WithError(err).Error("Deleting ClusterRoles on CSV delete")
		errs = append(errs, err)
	}
	err = a.opClient.KubernetesInterface().AdmissionregistrationV1().MutatingWebhookConfigurations().DeleteCollection(a.ctx, deleteOptions, listOptions)
	if client.IgnoreNotFound(err) != nil {
		log.WithError(err).Error("Deleting MutatingWebhookConfigurations on CSV delete")
		errs = append(errs, err)
	}

	err = a.opClient.KubernetesInterface().AdmissionregistrationV1().ValidatingWebhookConfigurations().DeleteCollection(a.ctx, deleteOptions, listOptions)
	if client.IgnoreNotFound(err) != nil {
		log.WithError(err).Error("Deleting ValidatingWebhookConfigurations on CSV delete")
		errs = append(errs, err)
	}

	// Make sure things are deleted
	crbList, err := a.lister.RbacV1().ClusterRoleBindingLister().List(ownerSelector)
	if err != nil {
		errs = append(errs, err)
	} else if len(crbList) != 0 {
		errs = append(errs, fmt.Errorf("waiting for ClusterRoleBindings to delete"))
	}

	crList, err := a.lister.RbacV1().ClusterRoleLister().List(ownerSelector)
	if err != nil {
		errs = append(errs, err)
	} else if len(crList) != 0 {
		errs = append(errs, fmt.Errorf("waiting for ClusterRoles to delete"))
	}

	// Return any errors
	if err := utilerrors.NewAggregate(errs); err != nil {
		return err, false
	}

	// If no errors, remove our finalizer from the CSV and update
	controllerutil.RemoveFinalizer(csv, myFinalizerName)
	_, err = a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(a.ctx, csv, metav1.UpdateOptions{})
	if err != nil {
		log.WithError(err).Error("Removing finalizer")
		return err, false
	}

	// Stop reconciliation as the csv is being deleted
	return nil, false
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
	logger.Debug("start syncing CSV")
	defer logger.Debug("end syncing CSV")

	// get an up-to-date clusterServiceVersion from the cache
	clusterServiceVersion, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(clusterServiceVersion.GetNamespace()).Get(clusterServiceVersion.GetName())
	if apierrors.IsNotFound(err) {
		logger.Info("CSV has beeen deleted")
		return nil
	} else if err != nil {
		logger.Info("Error getting latest version of CSV")
		return err
	}

	if err, ok := a.processFinalizer(clusterServiceVersion, logger); !ok {
		return err
	}

	if a.csvNotification != nil {
		a.csvNotification.OnAddOrUpdate(clusterServiceVersion)
	}

	if clusterServiceVersion.IsCopied() {
		logger.Warning("skipping copied csv transition") // should not happen?
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

func (a *Operator) allNamespaceOperatorGroups() ([]*operatorsv1.OperatorGroup, error) {
	operatorGroups, err := a.lister.OperatorsV1().OperatorGroupLister().List(labels.Everything())
	if err != nil {
		return nil, err
	}

	result := []*operatorsv1.OperatorGroup{}
	for _, operatorGroup := range operatorGroups {
		if NewNamespaceSet(operatorGroup.Status.Namespaces).IsAllNamespaces() {
			result = append(result, operatorGroup.DeepCopy())
		}
	}
	return result, nil
}

func (a *Operator) syncOLMConfig(obj interface{}) (syncError error) {
	a.logger.Debug("Processing olmConfig")
	olmConfig, ok := obj.(*operatorsv1.OLMConfig)
	if !ok {
		return fmt.Errorf("casting OLMConfig failed")
	}

	// Generate an array of allNamespace OperatorGroups
	allNSOperatorGroups, err := a.allNamespaceOperatorGroups()
	if err != nil {
		return err
	}

	nonCopiedCSVRequirement, err := labels.NewRequirement(v1alpha1.CopiedLabelKey, selection.DoesNotExist, []string{})
	if err != nil {
		return err
	}

	csvIsRequeued := false
	for _, og := range allNSOperatorGroups {
		// Get all copied CSVs owned by this operatorGroup
		copiedCSVRequirement, err := labels.NewRequirement(v1alpha1.CopiedLabelKey, selection.Equals, []string{og.GetNamespace()})
		if err != nil {
			return err
		}

		copiedCSVs, err := a.copiedCSVLister.List(labels.NewSelector().Add(*copiedCSVRequirement))
		if err != nil {
			return err
		}

		// Create a map that points from CSV name to a map of namespaces it is copied to
		// for quick lookups.
		copiedCSVNamespaces := map[string]map[string]struct{}{}
		for _, copiedCSV := range copiedCSVs {
			if _, ok := copiedCSVNamespaces[copiedCSV.GetName()]; !ok {
				copiedCSVNamespaces[copiedCSV.GetName()] = map[string]struct{}{}
			}
			copiedCSVNamespaces[copiedCSV.GetName()][copiedCSV.GetNamespace()] = struct{}{}
		}

		csvs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(og.GetNamespace()).List(labels.NewSelector().Add(*nonCopiedCSVRequirement))
		if err != nil {
			return err
		}

		namespaces, err := a.lister.CoreV1().NamespaceLister().List(labels.Everything())
		if err != nil {
			return err
		}

		copiedCSVEvaluatorFunc := getCopiedCSVEvaluatorFunc(olmConfig.CopiedCSVsAreEnabled(), namespaces, a.protectedCopiedCSVNamespaces)

		for _, csv := range csvs {
			// Ignore NS where actual CSV is installed
			if copiedCSVEvaluatorFunc(copiedCSVNamespaces[csv.GetName()]) {
				continue
			}

			if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
				a.logger.WithError(err).Warn("unable to requeue")
			}
			csvIsRequeued = true
		}
	}

	// Update the olmConfig status if it has changed.
	condition := getCopiedCSVsCondition(olmConfig.CopiedCSVsAreEnabled(), csvIsRequeued)
	if !isStatusConditionPresentAndAreTypeReasonMessageStatusEqual(olmConfig.Status.Conditions, condition) {
		meta.SetStatusCondition(&olmConfig.Status.Conditions, condition)
		if _, err := a.client.OperatorsV1().OLMConfigs().UpdateStatus(context.TODO(), olmConfig, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}

	return nil
}

// getCopiedCSVEvaluatorFunc returns a function that evaluates if the a set of Copied CSVs exist in the expected namespaces.
func getCopiedCSVEvaluatorFunc(copiedCSVsEnabled bool, namespaces []*corev1.Namespace, protectedCopiedCSVNamespaces map[string]struct{}) func(map[string]struct{}) bool {
	if copiedCSVsEnabled {
		// Exclude the namespace hosting the original CSV
		expectedCopiedCSVCount := -1
		for _, ns := range namespaces {
			if ns.Status.Phase == corev1.NamespaceActive {
				expectedCopiedCSVCount++
			}
		}
		return func(m map[string]struct{}) bool {
			return expectedCopiedCSVCount == len(m)
		}
	}

	// Check that Copied CSVs exist in protected namespaces.
	return func(m map[string]struct{}) bool {
		if len(protectedCopiedCSVNamespaces) != len(m) {
			return false
		}

		for protectedNS := range protectedCopiedCSVNamespaces {
			if _, ok := m[protectedNS]; !ok {
				return false
			}
		}

		return true
	}
}

func isStatusConditionPresentAndAreTypeReasonMessageStatusEqual(conditions []metav1.Condition, condition metav1.Condition) bool {
	foundCondition := meta.FindStatusCondition(conditions, condition.Type)
	if foundCondition == nil {
		return false
	}
	return foundCondition.Type == condition.Type &&
		foundCondition.Reason == condition.Reason &&
		foundCondition.Message == condition.Message &&
		foundCondition.Status == condition.Status
}

func getCopiedCSVsCondition(enabled, csvIsRequeued bool) metav1.Condition {
	condition := metav1.Condition{
		Type:               operatorsv1.DisabledCopiedCSVsConditionType,
		LastTransitionTime: metav1.Now(),
		Status:             metav1.ConditionFalse,
	}
	if enabled {
		condition.Reason = "CopiedCSVsEnabled"
		condition.Message = "Copied CSVs are enabled and present across the cluster"
		if csvIsRequeued {
			condition.Message = "Copied CSVs are enabled and at least one copied CSVs is missing"
		}
		return condition
	}

	condition.Reason = "CopiedCSVsDisabled"
	if csvIsRequeued {
		condition.Message = "Copied CSVs are disabled and at least one unexpected copied CSV was found for an operator installed in AllNamespace mode"
		return condition
	}

	condition.Status = metav1.ConditionTrue
	condition.Message = "Copied CSVs are disabled and no unexpected copied CSVs were found for operators installed in AllNamespace mode"

	return condition
}

func (a *Operator) syncCopyCSV(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		a.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	olmConfig, err := a.client.OperatorsV1().OLMConfigs().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if err == nil {
		go a.olmConfigQueue.AddAfter(olmConfig, time.Second*5)
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

	copiedCSVsAreEnabled, err := a.copiedCSVsAreEnabled()
	if err != nil {
		return err
	}

	// Check if we need to do any copying / annotation for the operatorgroup
	namespaceSet := NewNamespaceSet(operatorGroup.Status.Namespaces)
	if copiedCSVsAreEnabled || !namespaceSet.IsAllNamespaces() {
		if err := a.ensureCSVsInNamespaces(clusterServiceVersion, operatorGroup, namespaceSet); err != nil {
			logger.WithError(err).Info("couldn't copy CSV to target namespaces")
			syncError = err
		}

		// If the CSV was installed in AllNamespace mode, remove any "CSV Copying Disabled" events
		// in which the related object's name, namespace, and uid match the given CSV's.
		if namespaceSet.IsAllNamespaces() {
			if err := a.deleteCSVCopyingDisabledEvent(clusterServiceVersion); err != nil {
				return err
			}
		}
		return
	}

	requirement, err := labels.NewRequirement(v1alpha1.CopiedLabelKey, selection.Equals, []string{clusterServiceVersion.Namespace})
	if err != nil {
		return err
	}

	copiedCSVs, err := a.copiedCSVLister.List(labels.NewSelector().Add(*requirement))
	if err != nil {
		return err
	}

	// Ensure that the Copied CSVs exist in the protected namespaces.
	protectedNamespaces := []string{}
	for ns := range a.protectedCopiedCSVNamespaces {
		if ns == clusterServiceVersion.GetNamespace() {
			continue
		}
		protectedNamespaces = append(protectedNamespaces, ns)
	}

	if err := a.ensureCSVsInNamespaces(clusterServiceVersion, operatorGroup, NewNamespaceSet(protectedNamespaces)); err != nil {
		return err
	}

	// Delete Copied CSVs in namespaces that are not protected.
	for _, copiedCSV := range copiedCSVs {
		if _, ok := a.protectedCopiedCSVNamespaces[copiedCSV.Namespace]; ok {
			continue
		}
		err := a.client.OperatorsV1alpha1().ClusterServiceVersions(copiedCSV.Namespace).Delete(context.TODO(), copiedCSV.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	if err := a.createCSVCopyingDisabledEvent(clusterServiceVersion); err != nil {
		return err
	}

	return
}

// copiedCSVsAreEnabled determines if csv copying is enabled for OLM.
//
// This method will first attempt to get the "cluster" olmConfig resource,
// if any error other than "IsNotFound" is encountered, false and the error
// will be returned.
//
// If the "cluster" olmConfig resource is found, the value of
// olmConfig.spec.features.disableCopiedCSVs will be returned along with a
// nil error.
//
// If the "cluster" olmConfig resource is not found, true will be returned
// without an error.
func (a *Operator) copiedCSVsAreEnabled() (bool, error) {
	olmConfig, err := a.client.OperatorsV1().OLMConfigs().Get(context.TODO(), "cluster", metav1.GetOptions{})
	if err != nil {
		// Default to true if olmConfig singleton cannot be found
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		// If there was an error that wasn't an IsNotFound, return the error
		return false, err
	}

	// If there was no error, return value based on olmConfig singleton
	return olmConfig.CopiedCSVsAreEnabled(), nil
}

func (a *Operator) getCopiedCSVDisabledEventsForCSV(csv *v1alpha1.ClusterServiceVersion) ([]corev1.Event, error) {
	result := []corev1.Event{}
	if csv == nil {
		return result, nil
	}

	events, err := a.opClient.KubernetesInterface().CoreV1().Events(csv.GetNamespace()).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, event := range events.Items {
		if event.InvolvedObject.Namespace == csv.GetNamespace() &&
			event.InvolvedObject.Name == csv.GetName() &&
			event.InvolvedObject.UID == csv.GetUID() &&
			event.Reason == operatorsv1.DisabledCopiedCSVsConditionType {
			result = append(result, *event.DeepCopy())
		}
	}

	return result, nil
}

func (a *Operator) deleteCSVCopyingDisabledEvent(csv *v1alpha1.ClusterServiceVersion) error {
	events, err := a.getCopiedCSVDisabledEventsForCSV(csv)
	if err != nil {
		return err
	}

	// Remove existing events.
	return a.deleteEvents(events)
}

func (a *Operator) deleteEvents(events []corev1.Event) error {
	for _, event := range events {
		err := a.opClient.KubernetesInterface().EventsV1().Events(event.GetNamespace()).Delete(context.TODO(), event.GetName(), metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (a *Operator) createCSVCopyingDisabledEvent(csv *v1alpha1.ClusterServiceVersion) error {
	events, err := a.getCopiedCSVDisabledEventsForCSV(csv)
	if err != nil {
		return err
	}

	if len(events) == 1 {
		return nil
	}

	// Remove existing events.
	if len(events) > 1 {
		if err := a.deleteEvents(events); err != nil {
			return err
		}
	}

	a.recorder.Eventf(csv, corev1.EventTypeWarning, operatorsv1.DisabledCopiedCSVsConditionType, "CSV copying disabled for %s/%s", csv.GetNamespace(), csv.GetName())

	return nil
}

func (a *Operator) syncGcCsv(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*metav1.PartialObjectMetadata)
	if !ok {
		a.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}
	if v1alpha1.IsCopied(clusterServiceVersion) {
		syncError = a.removeDanglingChildCSVs(clusterServiceVersion)
		return
	}
	return
}

// operatorGroupFromAnnotations returns the OperatorGroup for the CSV only if the CSV is active one in the group
func (a *Operator) operatorGroupFromAnnotations(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) *operatorsv1.OperatorGroup {
	annotations := csv.GetAnnotations()

	// Not part of a group yet
	if annotations == nil {
		logger.Info("not part of any operatorgroup, no annotations")
		return nil
	}

	// Not in the OperatorGroup namespace
	if annotations[operatorsv1.OperatorGroupNamespaceAnnotationKey] != csv.GetNamespace() {
		logger.Info("not in operatorgroup namespace")
		return nil
	}

	operatorGroupName, ok := annotations[operatorsv1.OperatorGroupAnnotationKey]

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

	targets, ok := annotations[operatorsv1.OperatorGroupTargetsAnnotationKey]

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

	return operatorGroup.DeepCopy()
}

func (a *Operator) operatorGroupForCSV(csv *v1alpha1.ClusterServiceVersion, logger *logrus.Entry) (*operatorsv1.OperatorGroup, error) {
	now := a.now()

	// Attempt to associate an OperatorGroup with the CSV.
	operatorGroups, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).List(labels.Everything())
	if err != nil {
		logger.Errorf("error occurred while attempting to associate csv with operatorgroup")
		return nil, err
	}
	var operatorGroup *operatorsv1.OperatorGroup

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
		return operatorGroup.DeepCopy(), nil
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

	operatorSurface, err := apiSurfaceOfCSV(out)
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
	targets, ok := out.GetAnnotations()[operatorsv1.OperatorGroupTargetsAnnotationKey]
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
	if err != nil {
		logger.WithError(err).Warn("failed to list operatorgroups")
		return
	}
	otherGroups := make([]operatorsv1.OperatorGroup, 0, len(allGroups))
	for _, g := range allGroups {
		if g.GetName() != operatorGroup.GetName() || g.GetNamespace() != operatorGroup.GetNamespace() {
			otherGroups = append(otherGroups, *g)
		}
	}

	groupSurface := NewOperatorGroup(operatorGroup)
	otherGroupSurfaces := NewOperatorGroupSurfaces(otherGroups...)
	providedAPIs := operatorSurface.ProvidedAPIs.StripPlural()

	switch result := a.apiReconciler.Reconcile(providedAPIs, groupSurface, otherGroupSurfaces...); {
	case operatorGroup.Spec.StaticProvidedAPIs && (result == AddAPIs || result == RemoveAPIs):
		// Transition the CSV to FAILED with status reason "CannotModifyStaticOperatorGroupProvidedAPIs"
		if out.Status.Reason != v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.WithField("apis", providedAPIs).Warn("cannot modify provided apis of static provided api operatorgroup")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs, "static provided api operatorgroup cannot be modified by these apis", now, a.recorder)
			a.cleanupCSVDeployments(logger, out)
		}
		return
	case result == APIConflict:
		// Transition the CSV to FAILED with status reason "InterOperatorGroupOwnerConflict"
		if out.Status.Reason != v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.WithField("apis", providedAPIs).Warn("intersecting operatorgroups provide the same apis")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, "intersecting operatorgroups provide the same apis", now, a.recorder)
			a.cleanupCSVDeployments(logger, out)
		}
		return
	case result == AddAPIs:
		// Add the CSV's provided APIs to its OperatorGroup's annotation
		logger.WithField("apis", providedAPIs).Debug("adding csv provided apis to operatorgroup")
		union := groupSurface.ProvidedAPIs().Union(providedAPIs)
		unionedAnnotations := operatorGroup.GetAnnotations()
		if unionedAnnotations == nil {
			unionedAnnotations = make(map[string]string)
		}
		if unionedAnnotations[operatorsv1.OperatorGroupProvidedAPIsAnnotationKey] == union.String() {
			// resolver may think apis need adding with invalid input, so continue when there's no work
			// to be done so that the CSV can progress far enough to get requirements checked
			a.logger.Debug("operator group annotations up to date, continuing")
			break
		}
		unionedAnnotations[operatorsv1.OperatorGroupProvidedAPIsAnnotationKey] = union.String()
		operatorGroup.SetAnnotations(unionedAnnotations)
		if _, err := a.client.OperatorsV1().OperatorGroups(operatorGroup.GetNamespace()).Update(context.TODO(), operatorGroup, metav1.UpdateOptions{}); err != nil && !apierrors.IsNotFound(err) {
			syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", operatorsv1.OperatorGroupProvidedAPIsAnnotationKey, err)
		}
		if err := a.csvQueueSet.Requeue(out.GetNamespace(), out.GetName()); err != nil {
			a.logger.WithError(err).Warn("unable to requeue")
		}
		return
	case result == RemoveAPIs:
		// Remove the CSV's provided APIs from its OperatorGroup's annotation
		logger.WithField("apis", providedAPIs).Debug("removing csv provided apis from operatorgroup")
		difference := groupSurface.ProvidedAPIs().Difference(providedAPIs)
		if diffedAnnotations := operatorGroup.GetAnnotations(); diffedAnnotations != nil {
			diffedAnnotations[operatorsv1.OperatorGroupProvidedAPIsAnnotationKey] = difference.String()
			operatorGroup.SetAnnotations(diffedAnnotations)
			if _, err := a.client.OperatorsV1().OperatorGroups(operatorGroup.GetNamespace()).Update(context.TODO(), operatorGroup, metav1.UpdateOptions{}); err != nil && !apierrors.IsNotFound(err) {
				syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", operatorsv1.OperatorGroupProvidedAPIsAnnotationKey, err)
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
				logger.WithError(fmt.Errorf("repeated WebhookDescription name %s", desc.GenerateName)).Warn("CSV is invalid")
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

		if installer.CertsRotated() {
			now := metav1.Now()
			rotateTime := metav1.NewTime(installer.CertsRotateAt())
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

		strategy, err := a.updateDeploymentSpecsWithAPIServiceData(out, strategy)
		if err != nil {
			logger.WithError(err).Debug("Unable to calculate expected deployment")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall, "calculated deployment install is bad", now, a.recorder)
			return
		}
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonWaiting); installErr != nil {
			// Re-sync if kube-apiserver was unavailable
			if apierrors.IsServiceUnavailable(installErr) {
				logger.WithError(installErr).Info("could not update install status")
				syncError = installErr
				return
			}
			// Set phase to failed if it's been a long time since the last transition (5 minutes)
			if out.Status.LastTransitionTime != nil && a.now().Sub(out.Status.LastTransitionTime.Time) >= 5*time.Minute {
				logger.Warn("install timed out")
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, "install timeout", now, a.recorder)
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
		if shouldRotate, err := installer.ShouldRotateCerts(strategy); err != nil {
			logger.WithError(err).Info("cert validity check")
			return
		} else if shouldRotate {
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
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonRequirementsNotMet, "requirements no longer met", now, a.recorder)
			return
		}

		// Check install status
		strategy, err = a.updateDeploymentSpecsWithAPIServiceData(out, strategy)
		if err != nil {
			logger.WithError(err).Debug("Unable to calculate expected deployment")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall, "calculated deployment install is bad", now, a.recorder)
			return
		}
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentUnhealthy); installErr != nil {
			// Re-sync if kube-apiserver was unavailable
			if apierrors.IsServiceUnavailable(installErr) {
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
		// Transition to the replacing phase if FailForward is enabled and a CSV exists that replaces the operator.
		if operatorGroup.UpgradeStrategy() == operatorsv1.UpgradeStrategyUnsafeFailForward {
			if replacement := a.isBeingReplaced(out, a.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny)); replacement != nil {
				msg := fmt.Sprintf("Fail Forward is enabled, allowing %s csv to be replaced by csv: %s", out.Status.Phase, replacement.GetName())
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVReasonBeingReplaced, msg, a.now(), a.recorder)
				metrics.CSVUpgradeCount.Inc()
				return
			}
		}
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
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, "requirements not met", now, a.recorder)
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
		if shouldRotate, err := installer.ShouldRotateCerts(strategy); err != nil {
			logger.WithError(err).Info("cert validity check")
			return
		} else if shouldRotate {
			logger.Debug("CSV owns resources that require a cert refresh")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "owned APIServices need cert refresh", now, a.recorder)
			return
		}

		// Check install status
		strategy, err = a.updateDeploymentSpecsWithAPIServiceData(out, strategy)
		if err != nil {
			logger.WithError(err).Debug("Unable to calculate expected deployment")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall, "calculated deployment install is bad", now, a.recorder)
			return
		}
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall); installErr != nil {
			// Re-sync if kube-apiserver was unavailable
			if apierrors.IsServiceUnavailable(installErr) {
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
		next := a.isBeingReplaced(out, a.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny))
		// Get the newest CSV in the replacement chain if fail forward upgrades are enabled.
		if operatorGroup.UpgradeStrategy() == operatorsv1.UpgradeStrategyUnsafeFailForward {
			csvs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(next.GetNamespace()).List(labels.Everything())
			if err != nil {
				syncError = err
				return
			}

			lastCSVInChain, err := resolver.WalkReplacementChain(next, resolver.ReplacementMapping(csvs), resolver.WithUniqueCSVs())
			if err != nil {
				syncError = err
				return
			}

			if lastCSVInChain == nil {
				syncError = fmt.Errorf("fail forward upgrades enabled, unable to identify last CSV in replacement chain")
				return
			}

			next = lastCSVInChain
		}
		if next != nil {
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
		syncError = a.client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Delete(context.TODO(), out.GetName(), metav1.DeleteOptions{})
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

	if err := findFirstError(apierrors.IsServiceUnavailable, strategyErr, apiServiceErr, webhookErr); err != nil {
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
		msg := "apiServices not installed"
		csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, msg, now, a.recorder)
		if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
			a.logger.Warn(err.Error())
		}

		return fmt.Errorf(msg)
	}

	if webhookErr != nil {
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseInstallReady, requeueConditionReason, fmt.Sprintf("Webhook install failed: %s", webhookErr), now, a.recorder)
		return webhookErr
	}

	if !webhooksInstalled {
		msg := "webhooks not installed"
		csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, msg, now, a.recorder)
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
		a.logger.Errorf("failed to get a client for operator deployment - %v", err)
		return nil, nil
	}
	kubeclient, err := a.clientFactory.WithConfigTransformer(attenuate).NewOperatorClient()
	if err != nil {
		a.logger.Errorf("failed to get an operator client for operator deployment - %v", err)
		return nil, nil
	}

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
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsGone(err) {
			return err
		}

		if apiService == nil {
			continue
		}

		adoptable, err := install.IsAPIServiceAdoptable(a.lister, csv, apiService)
		if err != nil {
			a.logger.WithFields(logrus.Fields{"obj": "apiService", "labels": apiService.GetLabels()}).Errorf("adoption check failed - %v", err)
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
			if apierrors.IsNotFound(err) {
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
		if apierrors.IsNotFound(err) {
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
