package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
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

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
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
	roleKind               = "Role"
	roleBindingKind        = "RoleBinding"
)

//for test stubbing and for ensuring standardization of timezones to UTC
var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

// Operator represents a Kubernetes operator that executes InstallPlans by
// resolving dependencies in a catalog.
type Operator struct {
	*queueinformer.Operator
	client             versioned.Interface
	lister             operatorlister.OperatorLister
	namespace          string
	sources            map[registry.ResourceKey]registry.Source
	sourcesLock        sync.RWMutex
	sourcesLastUpdate  metav1.Time
	dependencyResolver resolver.DependencyResolver
	subQueue           workqueue.RateLimitingInterface
	catSrcQueue                 workqueue.RateLimitingInterface
	configmapRegistryReconciler *registry.ConfigMapRegistryReconciler
}

// NewOperator creates a new Catalog Operator.
func NewOperator(kubeconfigPath string, logger *logrus.Logger, wakeupInterval time.Duration, configmapRegistryImage, operatorNamespace string, watchedNamespaces ...string) (*Operator, error) {
	// Default to watching all namespaces.
	if watchedNamespaces == nil {
		watchedNamespaces = []string{metav1.NamespaceAll}
	}

	// Create a new client for ALM types (CRs)
	crClient, err := client.NewClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create an OperatorLister
	lister := operatorlister.NewLister()

	// Create an informer for each watched namespace.
	ipSharedIndexInformers := []cache.SharedIndexInformer{}
	subSharedIndexInformers := []cache.SharedIndexInformer{}
	for _, namespace := range watchedNamespaces {
		nsInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		ipSharedIndexInformers = append(ipSharedIndexInformers, nsInformerFactory.Operators().V1alpha1().InstallPlans().Informer())
		subSharedIndexInformers = append(subSharedIndexInformers, nsInformerFactory.Operators().V1alpha1().Subscriptions().Informer())
		lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, nsInformerFactory.Operators().V1alpha1().Subscriptions().Lister())
	}

	// Create an informer for each catalog namespace
	catsrcSharedIndexInformers := []cache.SharedIndexInformer{}
	for _, namespace := range []string{operatorNamespace} {
		nsInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		catsrcSharedIndexInformers = append(catsrcSharedIndexInformers, nsInformerFactory.Operators().V1alpha1().CatalogSources().Informer())
	}

	// Create a new queueinformer-based operator.
	queueOperator, err := queueinformer.NewOperator(kubeconfigPath, logger)
	if err != nil {
		return nil, err
	}

	// Allocate the new instance of an Operator.
	op := &Operator{
		Operator:           queueOperator,
		client:             crClient,
		lister:             lister,
		namespace:          operatorNamespace,
		lister:             operatorlister.NewLister(),
		sources:            make(map[registry.ResourceKey]registry.Source),
		dependencyResolver: &resolver.MultiSourceResolver{},
	}

	// Register CatalogSource informers.
	catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogsources")
	catsrcQueueInformer := queueinformer.New(
		catsrcQueue,
		catsrcSharedIndexInformers,
		op.syncCatalogSources,
		nil,
		"catsrc",
		metrics.NewMetricsCatalogSource(op.client),
		logger,
	)
	for _, informer := range catsrcQueueInformer {
		op.RegisterQueueInformer(informer)
	}
	op.catSrcQueue = catsrcQueue

	// Register InstallPlan informers.
	ipQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "installplans")
	ipQueueInformers := queueinformer.New(
		ipQueue,
		ipSharedIndexInformers,
		op.syncInstallPlans,
		nil,
		"installplan",
		metrics.NewMetricsInstallPlan(op.client),
		logger,
	)
	for _, informer := range ipQueueInformers {
		op.RegisterQueueInformer(informer)
	}

	// Register Subscription informers.
	subscriptionQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "subscriptions")
	subscriptionQueueInformers := queueinformer.New(
		subscriptionQueue,
		subSharedIndexInformers,
		op.syncSubscriptions,
		nil,
		"subscription",
		metrics.NewMetricsSubscription(op.client),
		logger,
	)
	op.subQueue = subscriptionQueue
	for _, informer := range subscriptionQueueInformers {
		op.RegisterQueueInformer(informer)
	}

	handleDelete := &cache.ResourceEventHandlerFuncs{
		DeleteFunc: op.handleDeletion,
	}
	// Set up informers for requeuing catalogs
	for _, namespace := range watchedNamespaces {
		roleQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "role")
		roleBindingQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "rolebinding")
		serviceAccountQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "serviceaccount")
		serviceQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "service")
		podQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "pod")
		configmapQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "configmap")

		informers.NewSharedInformerFactoryWithOptions(op.OpClient.KubernetesInterface(), wakeupInterval, informers.WithNamespace(namespace))
		informerFactory := informers.NewSharedInformerFactory(op.OpClient.KubernetesInterface(), wakeupInterval)
		roleInformer := informerFactory.Rbac().V1().Roles()
		roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
		serviceAccountInformer := informerFactory.Core().V1().ServiceAccounts()
		serviceInformer := informerFactory.Core().V1().Services()
		podInformer := informerFactory.Core().V1().Pods()
		configMapInformer := informerFactory.Core().V1().ConfigMaps()

		queueInformers := []*queueinformer.QueueInformer{
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

		op.lister.RbacV1().RegisterRoleLister(namespace, roleInformer.Lister())
		op.lister.RbacV1().RegisterRoleBindingLister(namespace, roleBindingInformer.Lister())
		op.lister.CoreV1().RegisterServiceAccountLister(namespace, serviceAccountInformer.Lister())
		op.lister.CoreV1().RegisterServiceLister(namespace, serviceInformer.Lister())
		op.lister.CoreV1().RegisterPodLister(namespace, podInformer.Lister())
		op.lister.CoreV1().RegisterConfigMapLister(namespace, configMapInformer.Lister())
	}
	op.configmapRegistryReconciler = &registry.ConfigMapRegistryReconciler{
		Image:    configmapRegistryImage,
		OpClient: op.OpClient,
		Lister:   op.lister,
	}
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

	logger.Debug("syncing")

	if ownerutil.IsOwnedByKind(metaObj, v1alpha1.CatalogSourceKind) {
		logger.Debug("requeueing owner CatalogSource")
		owner := ownerutil.GetOwnerByKind(metaObj, v1alpha1.CatalogSourceKind)
		o.catSrcQueue.AddRateLimited(fmt.Sprintf("%s/%s", metaObj.GetNamespace(), owner.Name))
	}

	return nil
}

func (o *Operator) handleDeletion(obj interface{}) {
	ownee, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Couldn't get object from tombstone %#v", obj))
			return
		}

		ownee, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Tombstone contained object that is not a Namespace %#v", obj))
			return
		}
	}

	if ownerutil.IsOwnedByKind(ownee, v1alpha1.CatalogSourceKind) {
		owner := ownerutil.GetOwnerByKind(ownee, v1alpha1.CatalogSourceKind)
		o.catSrcQueue.AddRateLimited(fmt.Sprintf("%s/%s", ownee.GetNamespace(), owner.Name))
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
	})

	if catsrc.Spec.SourceType == v1alpha1.SourceTypeInternal || catsrc.Spec.SourceType == v1alpha1.SourceTypeConfigmap {
		return o.syncConfigMapSource(logger.Logger, catsrc)
	}

	logger.WithField("sourceType", catsrc.Spec.SourceType).Warn("unknown source type")

	// TODO: write status about invalid source type

	return nil
}

func (o *Operator) syncConfigMapSource(logger *logrus.Logger, catsrc *v1alpha1.CatalogSource) (syncError error) {

	// Get the catalog source's config map
	configMap, err := o.lister.CoreV1().ConfigMapLister().ConfigMaps(catsrc.GetNamespace()).Get(catsrc.Spec.ConfigMap)
	if err != nil {
		return fmt.Errorf("failed to get catalog config map %s: %s", catsrc.Spec.ConfigMap, err)
	}

	sourceKey := registry.ResourceKey{Name: catsrc.GetName(), Namespace: catsrc.GetNamespace()}

	if catsrc.Status.ConfigMapResource == nil || catsrc.Status.ConfigMapResource.UID != configMap.GetUID() || catsrc.Status.ConfigMapResource.ResourceVersion != configMap.GetResourceVersion() {
		// configmap ref nonexistant or updated, write out the new configmap ref to status and exit
		out := catsrc.DeepCopy()
		out.Status.ConfigMapResource = &v1alpha1.ConfigMapResourceReference{
			Name:            configMap.GetName(),
			Namespace:       configMap.GetNamespace(),
			UID:             configMap.GetUID(),
			ResourceVersion: configMap.GetResourceVersion(),
		}
		out.Status.LastSync = timeNow()

		// update source map
		o.sourcesLock.Lock()
		defer o.sourcesLock.Unlock()
		src, err := registry.NewInMemoryFromConfigMap(o.OpClient, out.GetNamespace(), out.Spec.ConfigMap)
		o.sources[sourceKey] = src
		if err != nil {
			return err
		}

		// update status
		if _, err = o.client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(out); err != nil {
			return err
		}
		o.sourcesLastUpdate = timeNow()
		return nil
	}

	// configmap not parsed to memory, but also not out of date
	if _, ok := o.sources[sourceKey]; !ok {
		// update source map
		o.sourcesLock.Lock()
		defer o.sourcesLock.Unlock()
		src, err := registry.NewInMemoryFromConfigMap(o.OpClient, catsrc.GetNamespace(), catsrc.Spec.ConfigMap)
		o.sources[sourceKey] = src
		if err != nil {
			return err
		}
		o.sourcesLastUpdate = timeNow()
	}

	// configmap ref is up to date, continue parsing
	if catsrc.Status.RegistryServiceStatus == nil || catsrc.Status.RegistryServiceStatus.CreatedAt.Before(&catsrc.Status.LastSync) {
		// if registry pod hasn't been created or hasn't been updated since the last configmap update, recreate it

		out := catsrc.DeepCopy()
		if err := o.configmapRegistryReconciler.EnsureRegistryServer(out); err != nil {
			logger.WithError(err).Warn("couldn't ensure registry server")
			return err
		}

		if !catsrc.Status.LastSync.Before(&out.Status.LastSync) {
			return nil
		}

		// update status
		if _, err = o.client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(out); err != nil {
			return err
		}
		o.sourcesLastUpdate = timeNow()
		return nil
	}

	logger := logrus.WithFields(logrus.Fields{"catalogSource": out.GetName(), "catalogNamespace": out.GetNamespace()})

	// Sync any dependent Subscriptions
	subs, err := o.lister.OperatorsV1alpha1().SubscriptionLister().List(labels.Everything())
	if err != nil {
		logger.Warnf("could not list Subscriptions")
		return nil
	}

	for _, sub := range subs {
		subLogger := logger.WithFields(logrus.Fields{"subscriptionCatalogSource": sub.Spec.CatalogSource, "subscriptionCatalogNamespace": sub.Spec.CatalogSourceNamespace})
		catalogNamespace := sub.Spec.CatalogSourceNamespace
		if catalogNamespace == "" {
			catalogNamespace = o.namespace
		}
		subLogger.Debug("checking subscription")
		if sub.Spec.CatalogSource == out.GetName() && catalogNamespace == out.GetNamespace() {
			logger.Debug("requeueing subscription")
			o.requeueSubscription(sub.GetName(), sub.GetNamespace())
		}
	}

	return nil
}

func (o *Operator) syncSubscriptions(obj interface{}) (syncError error) {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Subscription failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"sub":       sub.GetName(),
		"namespace": sub.GetNamespace(),
		"source":    sub.Spec.CatalogSource,
		"pkg":       sub.Spec.Package,
		"channel":   sub.Spec.Channel,
	})

	logger.Infof("syncing")

	var updatedSub *v1alpha1.Subscription
	updatedSub, syncError = o.syncSubscription(sub)

	if updatedSub == nil || updatedSub.Status.State == sub.Status.State {
		return
	}
	if syncError != nil {
		logger = logger.WithField("syncError", syncError)
	}
	updatedSub.Status.LastUpdated = timeNow()

	// Update Subscription with status of transition. Log errors if we can't write them to the status.
	if _, err := o.client.OperatorsV1alpha1().Subscriptions(updatedSub.GetNamespace()).UpdateStatus(updatedSub); err != nil {
		logger = logger.WithField("updateError", err.Error())
		updateErr := errors.New("error updating Subscription status: " + err.Error())
		if syncError == nil {
			logger.Info("error updating Subscription status")
			return updateErr
		}
		logger.Info("error transitioning Subscription")
		syncError = fmt.Errorf("error transitioning Subscription: %s and error updating Subscription status: %s", syncError, updateErr)
	}

	return
}

func (o *Operator) requeueSubscription(name, namespace string) {
	// we can build the key directly, will need to change if queue uses different key scheme
	key := fmt.Sprintf("%s/%s", namespace, name)
	o.subQueue.AddRateLimited(key)
	return
}

func (o *Operator) syncInstallPlans(obj interface{}) (syncError error) {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting InstallPlan failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"ip":        plan.GetName(),
		"namespace": plan.GetNamespace(),
		"phase":     plan.Status.Phase,
	})

	logger.Info("syncing")
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
		o.requeueSubscription(oref.Name, outInstallPlan.GetNamespace())
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
	case v1alpha1.InstallPlanPhaseNone:
		log.Debugf("setting phase to %s", v1alpha1.InstallPlanPhasePlanning)
		out.Status.Phase = v1alpha1.InstallPlanPhasePlanning
		return out, nil

	case v1alpha1.InstallPlanPhasePlanning:
		log.Debug("attempting to resolve")
		if err := transitioner.ResolvePlan(out); err != nil {
			out.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanResolved,
				v1alpha1.InstallPlanReasonInstallCheckFailed, err))
			out.Status.Phase = v1alpha1.InstallPlanPhaseFailed
			return out, err
		}
		out.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanResolved))

		if out.Spec.Approval == v1alpha1.ApprovalManual && out.Spec.Approved != true {
			out.Status.Phase = v1alpha1.InstallPlanPhaseRequiresApproval
		} else {
			out.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		}
		return out, nil

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
	if plan.Status.Phase != v1alpha1.InstallPlanPhasePlanning {
		panic("attempted to create a plan that wasn't in the planning phase")
	}

	if len(o.sources) == 0 {
		return fmt.Errorf("cannot resolve InstallPlan without any Catalog Sources")
	}

	// Take a snapshot of the included catalog sources
	includedNamespaces := map[string]struct{}{
		o.namespace:    {},
		plan.Namespace: {},
	}
	sourcesSnapshot := o.getSourcesSnapshot(plan, includedNamespaces)

	// Take a snapshot of the existing CRD owners
	existingCRDOwners, err := o.getExistingCRDOwners(plan.Namespace)
	if err != nil {
		return err
	}

	// Attempt to resolve the InstallPlan
	steps, usedSources, err := o.dependencyResolver.ResolveInstallPlan(sourcesSnapshot, existingCRDOwners, CatalogLabel, plan)
	if err != nil {
		return err
	}

	// Set the resolved steps
	plan.Status.Plan = steps
	plan.Status.CatalogSources = []string{}

	// Add secrets for each used catalog source
	for _, sourceKey := range usedSources {
		// Append the used catalog source
		plan.Status.CatalogSources = append(plan.Status.CatalogSources, sourceKey.Name)

		// Get the catalog source
		catsrc, err := o.client.OperatorsV1alpha1().CatalogSources(sourceKey.Namespace).Get(sourceKey.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		for _, secretName := range catsrc.Spec.Secrets {
			// Attempt to look up the secret
			_, err := o.OpClient.KubernetesInterface().CoreV1().Secrets(sourceKey.Namespace).Get(secretName, metav1.GetOptions{})
			status := v1alpha1.StepStatusUnknown
			if k8serrors.IsNotFound(err) {
				status = v1alpha1.StepStatusNotPresent
			} else if err == nil {
				status = v1alpha1.StepStatusPresent
			} else {
				return err
			}

			// Prepend any required secrets to the plan for that catalog source
			plan.Status.Plan = append([]*v1alpha1.Step{{
				Resolving: "",
				Resource: v1alpha1.StepResource{
					Name:    secretName,
					Kind:    "Secret",
					Group:   "",
					Version: "v1",
				},
				Status: status,
			}}, plan.Status.Plan...)
		}
	}

	return nil
}

// ExecutePlan applies a planned InstallPlan to a namespace.
func (o *Operator) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	if plan.Status.Phase != v1alpha1.InstallPlanPhaseInstalling {
		panic("attempted to install a plan that wasn't in the installing phase")
	}

	// Get the set of initial installplan csv names
	initialCSVNames := getCSVNameSet(plan)
	// Get pre-existing CRD owners to make decisions about applying resolved CSVs
	existingCRDOwners, err := o.getExistingCRDOwners(plan.GetNamespace())
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
					return err
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
					return err
				}

				// Check if the resolved CSV is in the initial set
				if _, ok := initialCSVNames[csv.GetName()]; !ok {
					// Check for pre-existing CSVs that own the same CRDs
					competingOwners, err := competingCRDOwnersExist(plan.GetNamespace(), &csv, existingCRDOwners)
					if err != nil {
						return err
					}

					// TODO: decide on fail/continue logic for pre-existing dependent CSVs that own the same CRD(s)
					if competingOwners {
						// For now, error out
						return fmt.Errorf("Pre-existing CRD owners found for owned CRD(s) of dependent CSV %s", csv.GetName())
					}
				}

				// Attempt to create the CSV.
				_, err = o.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Create(&csv)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case secretKind:
				// Get the pre-existing secret.
				secret, err := o.OpClient.KubernetesInterface().CoreV1().Secrets(o.namespace).Get(step.Resource.Name, metav1.GetOptions{})
				if k8serrors.IsNotFound(err) {
					return fmt.Errorf("secret %s does not exist", step.Resource.Name)
				} else if err != nil {
					return err
				}

				// Set the namespace to the InstallPlan's namespace and attempt to
				// create a new secret.
				secret.Namespace = plan.Namespace
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
					return err
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(cr.OwnerReferences, plan.Namespace)
				if err != nil {
					return err
				}
				cr.OwnerReferences = updated

				// Attempt to create the ClusterRole.
				_, err = o.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(&cr)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}
			case clusterRoleBindingKind:
				// Marshal the manifest into a RoleBinding instance.
				var rb rbacv1.ClusterRoleBinding
				err := json.Unmarshal([]byte(step.Resource.Manifest), &rb)
				if err != nil {
					return err
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(rb.OwnerReferences, plan.Namespace)
				if err != nil {
					return err
				}
				rb.OwnerReferences = updated

				// Attempt to create the ClusterRoleBinding.
				_, err = o.OpClient.KubernetesInterface().RbacV1().ClusterRoleBindings().Create(&rb)
				if k8serrors.IsAlreadyExists(err) {
					rb.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateClusterRoleBinding(&rb)
					if err != nil {
						return err
					}

					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case roleKind:
				// Marshal the manifest into a Role instance.
				var r rbacv1.Role
				err := json.Unmarshal([]byte(step.Resource.Manifest), &r)
				if err != nil {
					return err
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(r.OwnerReferences, plan.Namespace)
				if err != nil {
					return err
				}
				r.OwnerReferences = updated

				// Attempt to create the Role.
				_, err = o.OpClient.KubernetesInterface().RbacV1().Roles(plan.Namespace).Create(&r)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					r.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateRole(&r)
					if err != nil {
						return err
					}

					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case roleBindingKind:
				// Marshal the manifest into a RoleBinding instance.
				var rb rbacv1.RoleBinding
				err := json.Unmarshal([]byte(step.Resource.Manifest), &rb)
				if err != nil {
					return err
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(rb.OwnerReferences, plan.Namespace)
				if err != nil {
					return err
				}
				rb.OwnerReferences = updated

				// Attempt to create the RoleBinding.
				_, err = o.OpClient.KubernetesInterface().RbacV1().RoleBindings(plan.Namespace).Create(&rb)
				if k8serrors.IsAlreadyExists(err) {
					rb.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateRoleBinding(&rb)
					if err != nil {
						return err
					}

					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case serviceAccountKind:
				// Marshal the manifest into a ServiceAccount instance.
				var sa corev1.ServiceAccount
				err := json.Unmarshal([]byte(step.Resource.Manifest), &sa)
				if err != nil {
					return err
				}

				// Update UIDs on all CSV OwnerReferences
				updated, err := o.getUpdatedOwnerReferences(sa.OwnerReferences, plan.Namespace)
				if err != nil {
					return err
				}
				sa.OwnerReferences = updated

				// Attempt to create the ServiceAccount.
				_, err = o.OpClient.KubernetesInterface().CoreV1().ServiceAccounts(plan.Namespace).Create(&sa)
				if k8serrors.IsAlreadyExists(err) {
					// If it already exists we need to patch the existing SA with the new OwnerReferences
					sa.SetNamespace(plan.Namespace)
					_, err = o.OpClient.UpdateServiceAccount(&sa)
					if err != nil {
						return err
					}

					// Mark as present
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
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

func (o *Operator) getSourcesSnapshot(plan *v1alpha1.InstallPlan, includedNamespaces map[string]struct{}) []registry.SourceRef {
	o.sourcesLock.RLock()
	defer o.sourcesLock.RUnlock()
	sourcesSnapshot := []registry.SourceRef{}

	for key, source := range o.sources {
		// Only copy catalog sources in included namespaces
		if _, ok := includedNamespaces[key.Namespace]; ok {
			ref := registry.SourceRef{
				Source:    source,
				SourceKey: key,
			}
			if key.Name == plan.Spec.CatalogSource && key.Namespace == plan.Spec.CatalogSourceNamespace {
				// Prepend preffered catalog source
				sourcesSnapshot = append([]registry.SourceRef{ref}, sourcesSnapshot...)
			} else {
				// Append the catalog source
				sourcesSnapshot = append(sourcesSnapshot, ref)
			}
		}
	}

	return sourcesSnapshot
}

// getExistingCRDOwners creates a map of CRD names to existing owner CSVs in the given namespace
func (o *Operator) getExistingCRDOwners(namespace string) (map[string][]string, error) {
	// Get a list of CSV CRs in the namespace
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
	}

	return owners, nil
}

func (o *Operator) getUpdatedOwnerReferences(refs []metav1.OwnerReference, namespace string) ([]metav1.OwnerReference, error) {
	updated := append([]metav1.OwnerReference(nil), refs...)

	for i, owner := range refs {
		if owner.Kind == v1alpha1.ClusterServiceVersionKind {
			csv, err := o.client.Operators().ClusterServiceVersions(namespace).Get(owner.Name, metav1.GetOptions{})
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
