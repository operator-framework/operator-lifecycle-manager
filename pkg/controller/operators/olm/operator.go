package olm

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

var (
	ErrRequirementsNotMet      = errors.New("requirements were not met")
	ErrCRDOwnerConflict        = errors.New("CRD owned by another ClusterServiceVersion")
	ErrAPIServiceOwnerConflict = errors.New("APIService owned by another ClusterServiceVersion")
)

var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

const (
	FallbackWakeupInterval = 30 * time.Second
)

type Operator struct {
	*queueinformer.Operator
	csvQueueSet   queueinformer.ResourceQueueSet
	ogQueueSet    queueinformer.ResourceQueueSet
	client        versioned.Interface
	resolver      install.StrategyResolverInterface
	apiReconciler resolver.APIIntersectionReconciler
	lister        operatorlister.OperatorLister
	recorder      record.EventRecorder
}

func NewOperator(logger *logrus.Logger, crClient versioned.Interface, opClient operatorclient.ClientInterface, strategyResolver install.StrategyResolverInterface, wakeupInterval time.Duration, namespaces []string) (*Operator, error) {
	if wakeupInterval < 0 {
		wakeupInterval = FallbackWakeupInterval
	}
	if len(namespaces) < 1 {
		namespaces = []string{metav1.NamespaceAll}
	}

	queueOperator, err := queueinformer.NewOperatorFromClient(opClient, logger)
	if err != nil {
		return nil, err
	}
	eventRecorder, err := event.NewRecorder(opClient.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
	if err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:      queueOperator,
		csvQueueSet:   make(queueinformer.ResourceQueueSet),
		ogQueueSet:    make(queueinformer.ResourceQueueSet),
		client:        crClient,
		resolver:      strategyResolver,
		apiReconciler: resolver.APIIntersectionReconcileFunc(resolver.ReconcileAPIIntersection),
		lister:        operatorlister.NewLister(),
		recorder:      eventRecorder,
	}

	// Set up RBAC informers
	roleInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Rbac().V1().Roles()
	roleQueueInformer := queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "roles"),
		roleInformer.Informer(),
		op.syncObject,
		nil,
		"roles",
		metrics.NewMetricsNil(),
		logger,
	)
	op.RegisterQueueInformer(roleQueueInformer)
	op.lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, roleInformer.Lister())

	roleBindingInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Rbac().V1().RoleBindings()
	roleBindingQueueInformer := queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "rolebindings"),
		roleBindingInformer.Informer(),
		op.syncObject,
		nil,
		"rolebindings",
		metrics.NewMetricsNil(),
		logger,
	)
	op.RegisterQueueInformer(roleBindingQueueInformer)
	op.lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, roleBindingInformer.Lister())

	clusterRoleInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Rbac().V1().ClusterRoles()
	clusterRoleQueueInformer := queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "clusterroles"),
		clusterRoleInformer.Informer(),
		op.syncObject,
		nil,
		"clusterroles",
		metrics.NewMetricsNil(),
		logger,
	)
	op.RegisterQueueInformer(clusterRoleQueueInformer)
	op.lister.RbacV1().RegisterClusterRoleLister(clusterRoleInformer.Lister())

	clusterRoleBindingInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Rbac().V1().ClusterRoleBindings()
	clusterRoleBindingQueueInformer := queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "clusterrolebindings"),
		clusterRoleBindingInformer.Informer(),
		op.syncObject,
		nil,
		"clusterrolebindings",
		metrics.NewMetricsNil(),
		logger,
	)
	op.lister.RbacV1().RegisterClusterRoleBindingLister(clusterRoleBindingInformer.Lister())
	op.RegisterQueueInformer(clusterRoleBindingQueueInformer)

	// register namespace queueinformer
	namespaceInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Core().V1().Namespaces()
	namespaceQueueInformer := queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "namespaces"),
		namespaceInformer.Informer(),
		op.syncObject,
		nil,
		"namespaces",
		metrics.NewMetricsNil(),
		logger,
	)
	op.RegisterQueueInformer(namespaceQueueInformer)
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())

	// Register APIService QueueInformer
	apiServiceInformer := kagg.NewSharedInformerFactory(opClient.ApiregistrationV1Interface(), wakeupInterval).Apiregistration().V1().APIServices()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "apiservices"),
		apiServiceInformer.Informer(),
		op.syncObject,
		&cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleDeletion,
		},
		"apiservices",
		metrics.NewMetricsNil(),
		logger,
	))
	op.lister.APIRegistrationV1().RegisterAPIServiceLister(apiServiceInformer.Lister())

	// Register CustomResourceDefinition QueueInformer
	customResourceDefinitionInformer := extv1beta1.NewSharedInformerFactory(opClient.ApiextensionsV1beta1Interface(), wakeupInterval).Apiextensions().V1beta1().CustomResourceDefinitions()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "customresourcedefinitions"),
		customResourceDefinitionInformer.Informer(),
		op.syncObject,
		&cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleDeletion,
		},
		"customresourcedefinitions",
		metrics.NewMetricsNil(),
		logger,
	))
	op.lister.APIExtensionsV1beta1().RegisterCustomResourceDefinitionLister(customResourceDefinitionInformer.Lister())

	// Register Secret QueueInformer
	secretInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Core().V1().Secrets()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "secrets"),
		secretInformer.Informer(),
		op.syncObject,
		&cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleDeletion,
		},
		"secrets",
		metrics.NewMetricsNil(),
		logger,
	))
	op.lister.CoreV1().RegisterSecretLister(metav1.NamespaceAll, secretInformer.Lister())

	// Register Service QueueInformer
	serviceInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Core().V1().Services()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "services"),
		serviceInformer.Informer(),
		op.syncObject,
		&cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleDeletion,
		},
		"services",
		metrics.NewMetricsNil(),
		logger,
	))
	op.lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, serviceInformer.Lister())

	// Register ServiceAccount QueueInformer
	serviceAccountInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Core().V1().ServiceAccounts()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "serviceaccounts"),
		serviceAccountInformer.Informer(),
		op.syncObject,
		&cache.ResourceEventHandlerFuncs{
			DeleteFunc: op.handleDeletion,
		},
		"serviceaccounts",
		metrics.NewMetricsNil(),
		logger,
	))
	op.lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, serviceAccountInformer.Lister())

	// csvInformers for each namespace all use the same backing queue keys are namespaced
	csvHandlers := &cache.ResourceEventHandlerFuncs{
		DeleteFunc: op.handleClusterServiceVersionDeletion,
	}
	for _, namespace := range namespaces {
		logger.WithField("namespace", namespace).Infof("watching CSVs")
		sharedInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		csvInformer := sharedInformerFactory.Operators().V1alpha1().ClusterServiceVersions()
		op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(namespace, csvInformer.Lister())

		// Register queue and QueueInformer
		queueName := fmt.Sprintf("%s/clusterserviceversions", namespace)
		csvQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), queueName)
		csvQueueInformer := queueinformer.NewInformer(csvQueue, csvInformer.Informer(), op.syncClusterServiceVersion, csvHandlers, queueName, metrics.NewMetricsCSV(op.lister.OperatorsV1alpha1().ClusterServiceVersionLister()), logger)
		op.RegisterQueueInformer(csvQueueInformer)
		op.csvQueueSet[namespace] = csvQueue
	}

	// Set up watch on deployments
	depHandlers := &cache.ResourceEventHandlerFuncs{
		// TODO: pass closure that forgets queue item after calling custom deletion handler.
		DeleteFunc: op.handleDeletion,
	}
	for _, namespace := range namespaces {
		logger.WithField("namespace", namespace).Infof("watching deployments")
		depInformer := informers.NewSharedInformerFactoryWithOptions(opClient.KubernetesInterface(), wakeupInterval, informers.WithNamespace(namespace)).Apps().V1().Deployments()
		op.lister.AppsV1().RegisterDeploymentLister(namespace, depInformer.Lister())

		// Register queue and QueueInformer
		queueName := fmt.Sprintf("%s/csv-deployments", namespace)
		depQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), queueName)
		depQueueInformer := queueinformer.NewInformer(depQueue, depInformer.Informer(), op.syncObject, depHandlers, queueName, metrics.NewMetricsNil(), logger)
		op.RegisterQueueInformer(depQueueInformer)
	}

	// Create an informer for the operator group
	for _, namespace := range namespaces {
		logger.WithField("namespace", namespace).Infof("watching OperatorGroups")
		sharedInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		operatorGroupInformer := sharedInformerFactory.Operators().V1alpha2().OperatorGroups()
		op.lister.OperatorsV1alpha2().RegisterOperatorGroupLister(namespace, operatorGroupInformer.Lister())

		// Register queue and QueueInformer
		queueName := fmt.Sprintf("%s/operatorgroups", namespace)
		operatorGroupQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), queueName)
		operatorGroupQueueInformer := queueinformer.NewInformer(operatorGroupQueue, operatorGroupInformer.Informer(), op.syncOperatorGroups, nil, queueName, metrics.NewMetricsNil(), logger)
		op.RegisterQueueInformer(operatorGroupQueueInformer)
		op.ogQueueSet[namespace] = operatorGroupQueue
	}

	return op, nil
}

func (a *Operator) syncObject(obj interface{}) (syncError error) {
	// Assert as runtime.Object
	runtimeObj, ok := obj.(runtime.Object)
	if !ok {
		syncError = errors.New("object sync: casting to runtime.Object failed")
		a.Log.Warn(syncError.Error())
		return
	}

	gvk := runtimeObj.GetObjectKind().GroupVersionKind()
	logger := a.Log.WithFields(logrus.Fields{
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
	logger = a.Log.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
	})

	logger.Debug("syncing")

	// Requeue all owner CSVs
	if ownerutil.IsOwnedByKind(metaObj, v1alpha1.ClusterServiceVersionKind) {
		logger.Debug("requeueing owner CSVs")
		a.requeueOwnerCSVs(metaObj)
	}

	// Requeues objects that can't have ownerrefs (cluster -> namespace, cross-namespace)
	if ownerutil.IsOwnedByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind) {
		logger.Debug("requeueing owner CSVs")
		a.requeueOwnerCSVs(metaObj)
	}

	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil
	}

	// Check to see if any operator groups are associated with this namespace
	operatorGroupList, err := a.lister.OperatorsV1alpha2().OperatorGroupLister().OperatorGroups(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		syncError = fmt.Errorf("lister failed: %v", err)
		logger.Warn(syncError.Error())
		return
	}
	for _, opGroup := range operatorGroupList {
		namespaceMap, err := a.getOperatorGroupTargets(opGroup)
		if err != nil {
			syncError = fmt.Errorf("operator namespace lookup failed: %v", err)
			logger.Warn(syncError.Error())
			return
		}
		_, directMatch := namespaceMap[namespace.GetName()]
		_, indirectMatch := namespaceMap[corev1.NamespaceAll]
		if directMatch || indirectMatch {
			logger.Debugf("Namespace %v matches operator group %v", namespace.GetName(), opGroup.GetName())
			if err := a.ogQueueSet.Requeue(opGroup.GetName(), opGroup.GetNamespace()); err != nil {
				syncError = fmt.Errorf("operator group %v requeue failed: %v", opGroup.GetName(), err)
				logger.Warn(syncError.Error())
				return
			}
		}
	}
	return nil
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

	logger := a.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})

	defer func(csv v1alpha1.ClusterServiceVersion) {
		logger.Debug("removing csv from queue set")
		a.csvQueueSet.Remove(csv.GetName(), csv.GetNamespace())

		// Requeue all OperatorGroups in the namespace
		logger.Debug("requeueing operatorgroups in namespace")
		operatorGroups, err := a.lister.OperatorsV1alpha2().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).List(labels.Everything())
		if err != nil {
			logger.WithError(err).Warnf("an error occurred while listing operatorgroups to requeue after csv deletion")
			return
		}

		for _, operatorGroup := range operatorGroups {
			logger := logger.WithField("operatorgroup", operatorGroup.GetName())
			logger.Debug("requeueing")
			if err := a.ogQueueSet.Requeue(operatorGroup.GetName(), operatorGroup.GetNamespace()); err != nil {
				logger.WithError(err).Debug("error requeueing operatorgroup")
			}
		}
	}(*clusterServiceVersion)

	targetNamespaces, ok := clusterServiceVersion.Annotations[v1alpha2.OperatorGroupTargetsAnnotationKey]
	if !ok {
		logger.Debug("missing target namespaces annotation on csv")
		return
	}

	operatorNamespace, ok := clusterServiceVersion.Annotations[v1alpha2.OperatorGroupNamespaceAnnotationKey]
	if !ok {
		logger.Debug("missing operator namespace annotation on csv")
		return
	}

	if _, ok = clusterServiceVersion.Annotations[v1alpha2.OperatorGroupAnnotationKey]; !ok {
		logger.Debug("missing operatorgroup name annotation on csv")
		return
	}

	if clusterServiceVersion.IsCopied() {
		logger.Debug("deleted csv is copied. skipping additional cleanup steps")
		return
	}

	logger.Info("gcing children")
	namespaces := []string{}
	if targetNamespaces == "" {
		namespaceList, err := a.OpClient.KubernetesInterface().CoreV1().Namespaces().List(metav1.ListOptions{})
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
			a.csvQueueSet.Requeue(clusterServiceVersion.GetName(), namespace)
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
			err := a.OpClient.DeleteAPIService(apiServiceName, &metav1.DeleteOptions{})
			if err != nil {
				logger.WithError(err).Warn("cannot delete orphaned api service")
			}
		}
	}
}

func (a *Operator) removeDanglingChildCSVs(csv *v1alpha1.ClusterServiceVersion) error {
	logger := a.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       csv.GetName(),
		"namespace": csv.GetNamespace(),
		"phase":     csv.Status.Phase,
	})

	operatorNamespace, ok := csv.Annotations[v1alpha2.OperatorGroupNamespaceAnnotationKey]
	if !ok {
		logger.Debug("missing operator namespace annotation on copied CSV")
		// TODO: Should we clean up the CSV if we don't know where its parent is?
		return fmt.Errorf("missing operator namespace annotation on copied CSV")
	}

	delete := false
	parent, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(operatorNamespace).Get(csv.GetName())
	if k8serrors.IsNotFound(err) || k8serrors.IsGone(err) {
		logger.Debug("deleting copied CSV since parent is missing")
		delete = true
	} else if parent != nil && parent.Status.Phase == v1alpha1.CSVPhaseFailed && parent.Status.Reason == v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
		logger.Debug("deleting copied CSV since parent has intersecting operatorgroup conflict")
		delete = true
	}

	if delete {
		if err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Delete(csv.GetName(), &metav1.DeleteOptions{}); err != nil {
			return err
		}
	}

	return nil
}

// syncClusterServiceVersion is the method that gets called when we see a CSV event in the cluster
func (a *Operator) syncClusterServiceVersion(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		a.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	logger := a.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})
	logger.Info("syncing CSV")

	outCSV, syncError := a.transitionCSVState(*clusterServiceVersion)

	// status changed, update CSV
	if !(outCSV.Status.LastUpdateTime == clusterServiceVersion.Status.LastUpdateTime &&
		outCSV.Status.Phase == clusterServiceVersion.Status.Phase &&
		outCSV.Status.Reason == clusterServiceVersion.Status.Reason &&
		outCSV.Status.Message == clusterServiceVersion.Status.Message) {

		// Update CSV with status of transition. Log errors if we can't write them to the status.
		_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(outCSV.GetNamespace()).UpdateStatus(outCSV)
		if err != nil {
			updateErr := errors.New("error updating ClusterServiceVersion status: " + err.Error())
			if syncError == nil {
				logger.Info(updateErr)
				return updateErr
			}
			syncError = fmt.Errorf("error transitioning ClusterServiceVersion: %s and error updating CSV status: %s", syncError, updateErr)
		}
	}

	operatorGroup := a.operatorGroupForActiveCSV(logger, outCSV)
	if operatorGroup == nil {
		logger.WithField("reason", "no operatorgroup found for active CSV").Info("skipping CSV resource copy to target namespaces")
		return
	}

	allNamespaces := make([]string, 0)
	pruneNamespaces := make([]string, 0)
	targetNamespaces := make([]string, 0)
	namespaceObjs, err := a.lister.CoreV1().NamespaceLister().List(labels.Everything())
	if err != nil {
		return err
	}

	for _, ns := range namespaceObjs {
		allNamespaces = append(allNamespaces, ns.GetName())
	}
	if len(operatorGroup.Status.Namespaces) == 1 && operatorGroup.Status.Namespaces[0] == corev1.NamespaceAll {
		targetNamespaces = allNamespaces
	} else {
		targetNamespaces = operatorGroup.Status.Namespaces
		pruneNamespaces = sliceCompare(allNamespaces, targetNamespaces)
		logger.Debugf("Found namespaces to clean %v", pruneNamespaces)
	}

	// Check if we need to do any copying / annotation for the operatorgroup
	if err := a.copyCsvToTargetNamespace(clusterServiceVersion, operatorGroup, targetNamespaces, pruneNamespaces); err != nil {
		logger.WithError(err).Info("couldn't copy CSV to target namespaces")
	}

	// Ensure operator has access to targetnamespaces
	if err := a.ensureRBACInTargetNamespace(outCSV, operatorGroup); err != nil {
		logger.WithError(err).Info("couldn't ensure RBAC in target namespaces")
	}

	// Ensure cluster roles exist for using provided apis
	if err := a.ensureClusterRolesForCSV(outCSV, operatorGroup); err != nil {
		logger.WithError(err).Info("couldn't ensure clusterroles for provided api types")
	}

	return
}

// operatorGroupForCSV returns the OperatorGroup for the CSV only if the CSV is active one in the group
func (a *Operator) operatorGroupForActiveCSV(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) *v1alpha2.OperatorGroup {
	annotations := csv.GetAnnotations()

	// Not part of a group yet
	if annotations == nil {
		logger.Info("not part of any operatorgroup, no annotations")
		return nil
	}

	// Not in the OperatorGroup namespace
	if annotations[v1alpha2.OperatorGroupNamespaceAnnotationKey] != csv.GetNamespace() {
		logger.Info("not in operatorgroup namespace")
		return nil
	}

	operatorGroupName, ok := annotations[v1alpha2.OperatorGroupAnnotationKey]

	// No OperatorGroup annotation
	if !ok {
		logger.Info("no olm.operatorGroup annotation")
		return nil
	}

	logger = logger.WithField("operatorgroup", operatorGroupName)

	operatorGroup, err := a.lister.OperatorsV1alpha2().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).Get(operatorGroupName)
	// OperatorGroup not found
	if err != nil {
		logger.Info("operatorgroup not found")
		return nil
	}

	targets, ok := annotations[v1alpha2.OperatorGroupTargetsAnnotationKey]

	// No target annotation
	if !ok {
		logger.Info("no olm.targetNamespaces annotation")
		return nil
	}

	// Target namespaces don't match
	if targets != strings.Join(operatorGroup.Status.Namespaces, ",") {
		logger.Info("olm.targetNamespaces annotation doesn't match operatorgroup status")
		return nil
	}

	return operatorGroup
}

// transitionCSVState moves the CSV status state machine along based on the current value and the current cluster state.
func (a *Operator) transitionCSVState(in v1alpha1.ClusterServiceVersion) (out *v1alpha1.ClusterServiceVersion, syncError error) {
	logger := a.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       in.GetName(),
		"namespace": in.GetNamespace(),
		"phase":     in.Status.Phase,
	})

	out = in.DeepCopy()
	now := timeNow()

	if out.IsCopied() {
		logger.Info("skipping copied csv transition")
		syncError = a.removeDanglingChildCSVs(out)
		return
	}

	// Check if the current CSV is being replaced, return with replacing status if so
	if err := a.checkReplacementsAndUpdateStatus(out); err != nil {
		logger.WithField("err", err).Info("replacement check")
		return
	}

	// Attempt to associate an OperatorGroup with the CSV.
	operatorGroups, err := a.lister.OperatorsV1alpha2().OperatorGroupLister().OperatorGroups(out.GetNamespace()).List(labels.Everything())
	if err != nil {
		logger.Errorf("error occurred while attempting to associate csv with operatorgroup")
		syncError = err
	}
	var operatorGroup *v1alpha2.OperatorGroup

	switch len(operatorGroups) {
	case 0:
		syncError = fmt.Errorf("csv in namespace with no operatorgroups")
		logger.Warn(syncError.Error())
		out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoOperatorGroup, syncError.Error(), now, a.recorder)
		return
	case 1:
		operatorGroup = a.operatorGroupForActiveCSV(logger, out)
		if operatorGroup == nil {
			operatorGroup = operatorGroups[0]
			logger = logger.WithField("opgroup", operatorGroup.GetName())

			if a.operatorGroupAnnotationsDiffer(&out.ObjectMeta, operatorGroup) {
				a.setOperatorGroupAnnotations(&out.ObjectMeta, operatorGroup, true)
				if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Update(out); err != nil {
					logger.WithError(err).Warn("error adding operatorgroup annotations")
					syncError = err
				}
			}

			return
		}
		logger.Info("csv in operatorgroup")
	default:
		syncError = fmt.Errorf("csv created in namespace with multiple operatorgroups, can't pick one automatically")
		logger.WithError(syncError).Warn("csv failed to become an operatorgroup member")
		if out.Status.Reason != v1alpha1.CSVReasonTooManyOperatorGroups {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonTooManyOperatorGroups, syncError.Error(), now, a.recorder)
		}
		return
	}

	modeSet, err := v1alpha1.NewInstallModeSet(out.Spec.InstallModes)
	if err != nil {
		syncError = err
		logger.WithError(err).Warn("csv has invalid installmodes")
		if out.Status.Reason != v1alpha1.CSVReasonInvalidInstallModes {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidInstallModes, syncError.Error(), now, a.recorder)
		}
		return
	}

	// Check if the CSV supports its operatorgroup's selected namespaces
	targets, ok := out.GetAnnotations()[v1alpha2.OperatorGroupTargetsAnnotationKey]
	if ok {
		namespaces := strings.Split(targets, ",")

		if err := modeSet.Supports(out.GetNamespace(), namespaces); err != nil {
			logger.WithField("reason", err.Error()).Info("installmodeset does not support operatorgroups namespace selection")
			if out.Status.Reason != v1alpha1.CSVReasonUnsupportedOperatorGroup {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonUnsupportedOperatorGroup, err.Error(), now, a.recorder)
			}
			return
		}
	} else {
		logger.Info("csv missing olm.targetNamespaces annotation")
		if out.Status.Reason != v1alpha1.CSVReasonNoTargetNamespaces {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoTargetNamespaces, "csv missing olm.targetNamespaces annotation", now, a.recorder)
		}
		return
	}

	// Check for intersecting provided APIs in intersecting OperatorGroups
	options := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name!=%s,metadata.namespace!=%s", operatorGroup.GetName(), operatorGroup.GetNamespace()),
	}
	otherGroups, err := a.client.OperatorsV1alpha2().OperatorGroups(metav1.NamespaceAll).List(options)

	groupSurface := resolver.NewOperatorGroup(*operatorGroup)
	otherGroupSurfaces := resolver.NewOperatorGroupSurfaces(otherGroups.Items...)

	operatorSurface, err := resolver.NewOperatorFromCSV(out)
	if err != nil {
		// TODO: Add failure status to CSV
		syncError = err
		return
	}
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
		unionedAnnotations[v1alpha2.OperatorGroupProvidedAPIsAnnotationKey] = union.String()
		operatorGroup.SetAnnotations(unionedAnnotations)
		if _, err := a.client.OperatorsV1alpha2().OperatorGroups(operatorGroup.GetNamespace()).Update(operatorGroup); err != nil && !k8serrors.IsNotFound(err) {
			syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", v1alpha2.OperatorGroupProvidedAPIsAnnotationKey, err)
		}
		a.csvQueueSet.Requeue(out.GetName(), out.GetNamespace())
		return
	case result == resolver.RemoveAPIs:
		// Remove the CSV's provided APIs from its OperatorGroup's annotation
		logger.WithField("apis", providedAPIs).Debug("removing csv provided apis from operatorgroup")
		difference := groupSurface.ProvidedAPIs().Difference(providedAPIs)
		if diffedAnnotations := operatorGroup.GetAnnotations(); diffedAnnotations != nil {
			diffedAnnotations[v1alpha2.OperatorGroupProvidedAPIsAnnotationKey] = difference.String()
			operatorGroup.SetAnnotations(diffedAnnotations)
			if _, err := a.client.OperatorsV1alpha2().OperatorGroups(operatorGroup.GetNamespace()).Update(operatorGroup); err != nil && !k8serrors.IsNotFound(err) {
				syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", v1alpha2.OperatorGroupProvidedAPIsAnnotationKey, err)
			}
		}
		a.csvQueueSet.Requeue(out.GetName(), out.GetNamespace())
		return
	default:
		logger.WithField("apis", providedAPIs).Debug("no intersecting operatorgroups provide the same apis")
	}

	switch out.Status.Phase {
	case v1alpha1.CSVPhaseNone:
		logger.Info("scheduling ClusterServiceVersion for requirement verification")
		out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "requirements not yet checked", now, a.recorder)
	case v1alpha1.CSVPhasePending:
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil {
			// TODO: account for Bad Rule as well
			logger.Info("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, a.recorder)
			return
		}
		out.SetRequirementStatus(statuses)

		if !met {
			logger.Info("requirements were not met")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, "one or more requirements couldn't be found", now, a.recorder)
			syncError = ErrRequirementsNotMet
			return
		}

		// Check for CRD ownership conflicts
		csvSet := a.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny)
		if syncError = a.crdOwnerConflicts(out, csvSet); syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, fmt.Sprintf("crd owner conflict: %s", syncError), now, a.recorder)
			return
		}

		// check for APIServices ownership conflicts
		if syncError = a.apiServiceOwnerConflicts(out, csvSet); syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, fmt.Sprintf("apiService owner conflict: %s", syncError), now, a.recorder)
			return
		}

		logger.Info("scheduling ClusterServiceVersion for install")
		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstallReady, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting install", now, a.recorder)
	case v1alpha1.CSVPhaseInstallReady:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
			return
		}

		// Install owned APIServices and update strategy with serving cert data
		strategy, syncError = a.installOwnedAPIServiceRequirements(out, strategy)
		if syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install API services failed: %s", syncError), now, a.recorder)
			return
		}

		if syncError = installer.Install(strategy); syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", syncError), now, a.recorder)
			return
		}

		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonInstallSuccessful, "waiting for install components to report healthy", now, a.recorder)
		err := a.csvQueueSet.Requeue(out.GetName(), out.GetNamespace())
		if err != nil {
			a.Log.Warn(err.Error())
		}
		return
	case v1alpha1.CSVPhaseInstalling:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
			return
		}

		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonWaiting); installErr == nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Infof("install strategy successful")
		}
	case v1alpha1.CSVPhaseSucceeded:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
			return
		}

		// Check install status
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentUnhealthy); installErr != nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Warnf("unhealthy component: %s", installErr)
			return
		}

		// Ensure requirements are still present
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil {
			logger.Info("invalid install strategy")
			out.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now)
			return
		} else if !met {
			out.SetRequirementStatus(statuses)
			out.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonRequirementsNotMet, fmt.Sprintf("requirements no longer met"), now)
			return
		}

		// Check if any generated resources are missing
		if resErr := a.checkAPIServiceResources(out, certs.PEMSHA256); len(resErr) > 0 {
			out.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonAPIServiceResourceIssue, resErr[0].Error(), now)
			return
		}

		// Check if it's time to refresh owned APIService certs
		if a.shouldRotateCerts(out) {
			out.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "owned APIServices need cert refresh", now)
			return
		}

	case v1alpha1.CSVPhaseFailed:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
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

		// Check install status
		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall); installErr != nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Warnf("needs reinstall: %s", installErr)
			return
		}

		// Check if requirements exist
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil {
			logger.Warn("invalid install strategy")
			out.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now)
			return
		} else if !met {
			out.SetRequirementStatus(statuses)
			out.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, fmt.Sprintf("requirements not met"), now)
			return
		}

		// Check if any generated resources are missing
		if resErr := a.checkAPIServiceResources(out, certs.PEMSHA256); len(resErr) > 0 {
			// Check if API services are adoptable. If not, keep CSV as Failed state
			if a.apiServiceResourceErrorsActionable(resErr) {
				out.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonAPIServiceResourcesNeedReinstall, resErr[0].Error(), now)
				return
			}
		}

		// Check if it's time to refresh owned APIService certs
		if a.shouldRotateCerts(out) {
			out.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "owned APIServices need cert refresh", now)
			return
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

		// if we can find a newer version that's successfully installed, we're safe to mark all intermediates
		for _, csv := range a.findIntermediatesForDeletion(out) {
			// we only mark them in this step, in case some get deleted but others fail and break the replacement chain
			csv.SetPhaseWithEvent(v1alpha1.CSVPhaseDeleting, v1alpha1.CSVReasonReplaced, "has been replaced by a newer ClusterServiceVersion that has successfully installed.", now, a.recorder)

			// ignore errors and success here; this step is just an optimization to speed up GC
			_, _ = a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).UpdateStatus(csv)
			err := a.csvQueueSet.Requeue(csv.GetName(), csv.GetNamespace())
			if err != nil {
				a.Log.Warn(err.Error())
			}
		}

		// if there's no newer version, requeue for processing (likely will be GCable before resync)
		err := a.csvQueueSet.Requeue(out.GetName(), out.GetNamespace())
		if err != nil {
			a.Log.Warn(err.Error())
		}
	case v1alpha1.CSVPhaseDeleting:
		var immediate int64 = 0
		syncError = a.client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Delete(out.GetName(), &metav1.DeleteOptions{GracePeriodSeconds: &immediate})
		if syncError != nil {
			logger.Debugf("unable to get delete csv marked for deletion: %s", syncError.Error())
		}
	}

	return
}

// findIntermediatesForDeletion starts at csv and follows the replacement chain until one is running and active
func (a *Operator) findIntermediatesForDeletion(csv *v1alpha1.ClusterServiceVersion) (csvs []*v1alpha1.ClusterServiceVersion) {
	csvsInNamespace := a.csvSet(csv.GetNamespace(), v1alpha1.CSVPhaseAny)
	current := csv

	// isBeingReplaced returns a copy
	next := a.isBeingReplaced(current, csvsInNamespace)
	for next != nil {
		csvs = append(csvs, current)
		a.Log.Debugf("checking to see if %s is running so we can delete %s", next.GetName(), csv.GetName())
		installer, nextStrategy, currentStrategy := a.parseStrategiesAndUpdateStatus(next)
		if nextStrategy == nil {
			a.Log.Debugf("couldn't get strategy for %s", next.GetName())
			continue
		}
		if currentStrategy == nil {
			a.Log.Debugf("couldn't get strategy for %s", next.GetName())
			continue
		}
		installed, _ := installer.CheckInstalled(nextStrategy)
		if installed && !next.IsObsolete() && next.Status.Phase == v1alpha1.CSVPhaseSucceeded {
			return csvs
		}
		current = next
		next = a.isBeingReplaced(current, csvsInNamespace)
	}

	return nil
}

// csvSet gathers all CSVs in the given namespace into a map keyed by CSV name; if metav1.NamespaceAll gets the set across all namespaces
func (a *Operator) csvSet(namespace string, phase v1alpha1.ClusterServiceVersionPhase) map[string]*v1alpha1.ClusterServiceVersion {
	csvsInNamespace, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(namespace).List(labels.Everything())

	if err != nil {
		a.Log.Warnf("could not list CSVs while constructing CSV set")
		return nil
	}

	csvs := make(map[string]*v1alpha1.ClusterServiceVersion, len(csvsInNamespace))
	for _, csv := range csvsInNamespace {
		if phase != v1alpha1.CSVPhaseAny && csv.Status.Phase != phase {
			continue
		}
		csvs[csv.Name] = csv.DeepCopy()
	}
	return csvs
}

// checkReplacementsAndUpdateStatus returns an error if we can find a newer CSV and sets the status if so
func (a *Operator) checkReplacementsAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) error {
	if csv.Status.Phase == v1alpha1.CSVPhaseReplacing || csv.Status.Phase == v1alpha1.CSVPhaseDeleting {
		return nil
	}
	if replacement := a.isBeingReplaced(csv, a.csvSet(csv.GetNamespace(), v1alpha1.CSVPhaseAny)); replacement != nil {
		a.Log.Infof("newer ClusterServiceVersion replacing %s, no-op", csv.SelfLink)
		msg := fmt.Sprintf("being replaced by csv: %s", replacement.SelfLink)
		csv.SetPhase(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVReasonBeingReplaced, msg, timeNow())
		metrics.CSVUpgradeCount.Inc()

		return fmt.Errorf("replacing")
	}
	return nil
}

func (a *Operator) updateInstallStatus(csv *v1alpha1.ClusterServiceVersion, installer install.StrategyInstaller, strategy install.Strategy, requeuePhase v1alpha1.ClusterServiceVersionPhase, requeueConditionReason v1alpha1.ConditionReason) error {
	apiServicesInstalled, apiServiceErr := a.areAPIServicesAvailable(csv.Spec.APIServiceDefinitions.Owned)
	strategyInstalled, strategyErr := installer.CheckInstalled(strategy)
	now := timeNow()

	if strategyInstalled && apiServicesInstalled {
		// if there's no error, we're successfully running
		if csv.Status.Phase != v1alpha1.CSVPhaseSucceeded {
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors", now)
		}
		return nil
	}

	// installcheck determined we can't progress (e.g. deployment failed to come up in time)
	if install.IsErrorUnrecoverable(strategyErr) {
		csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install failed: %s", strategyErr), now)
		return strategyErr
	}

	if apiServiceErr != nil {
		csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonAPIServiceInstallFailed, fmt.Sprintf("APIService install failed: %s", apiServiceErr), now)
		return apiServiceErr
	}

	if !apiServicesInstalled {
		csv.SetPhase(requeuePhase, requeueConditionReason, fmt.Sprintf("APIServices not installed"), now)
		err := a.csvQueueSet.Requeue(csv.GetName(), csv.GetNamespace())
		if err != nil {
			a.Log.Warn(err.Error())
		}

		return fmt.Errorf("APIServices not installed")
	}

	if strategyErr != nil {
		csv.SetPhase(requeuePhase, requeueConditionReason, fmt.Sprintf("installing: %s", strategyErr), now)
		return strategyErr
	}

	return nil
}

// parseStrategiesAndUpdateStatus returns a StrategyInstaller and a Strategy for a CSV if it can, else it sets a status on the CSV and returns
func (a *Operator) parseStrategiesAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) (install.StrategyInstaller, install.Strategy, install.Strategy) {
	strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err), timeNow())
		return nil, nil, nil
	}

	previousCSV := a.isReplacing(csv)
	var previousStrategy install.Strategy
	if previousCSV != nil {
		err = a.csvQueueSet.Requeue(previousCSV.Name, previousCSV.Namespace)
		if err != nil {
			a.Log.Warn(err.Error())
		}

		previousStrategy, err = a.resolver.UnmarshalStrategy(previousCSV.Spec.InstallStrategy)
		if err != nil {
			previousStrategy = nil
		}
	}

	strName := strategy.GetStrategyName()
	installer := a.resolver.InstallerForStrategy(strName, a.OpClient, a.lister, csv, csv.Annotations, previousStrategy)
	return installer, strategy, previousStrategy
}

func (a *Operator) crdOwnerConflicts(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) error {
	owned := false
	for _, crd := range in.Spec.CustomResourceDefinitions.Owned {
		for csvName, csv := range csvsInNamespace {
			if csvName == in.GetName() {
				continue
			}
			if csv.OwnsCRD(crd.Name) {
				owned = true
			}
			if owned && in.Spec.Replaces == csvName {
				return nil
			}
		}
	}
	if owned {
		return ErrCRDOwnerConflict
	}
	return nil
}

func (a *Operator) apiServiceOwnerConflicts(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) error {
	owned := false
	for _, api := range in.Spec.APIServiceDefinitions.Owned {
		name := fmt.Sprintf("%s.%s", api.Version, api.Group)
		for csvName, csv := range csvsInNamespace {
			if csvName == in.GetName() {
				continue
			}
			if csv.OwnsAPIService(name) {
				owned = true
			}
			if owned && in.Spec.Replaces == csvName {
				return nil
			}
		}
	}
	if owned {
		return ErrAPIServiceOwnerConflict
	}
	return nil
}

func (a *Operator) isBeingReplaced(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) (replacedBy *v1alpha1.ClusterServiceVersion) {
	for _, csv := range csvsInNamespace {
		a.Log.Infof("checking %s", csv.GetName())
		if csv.Spec.Replaces == in.GetName() {
			a.Log.Infof("%s replaced by %s", in.GetName(), csv.GetName())
			replacedBy = csv.DeepCopy()
			return
		}
	}
	return
}

func (a *Operator) isReplacing(in *v1alpha1.ClusterServiceVersion) *v1alpha1.ClusterServiceVersion {
	a.Log.Debugf("checking if csv is replacing an older version")
	if in.Spec.Replaces == "" {
		return nil
	}
	previous, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(in.GetNamespace()).Get(in.Spec.Replaces)
	if err != nil {
		a.Log.Debugf("unable to get previous csv: %s", err.Error())
		return nil
	}
	return previous
}

func (a *Operator) handleDeletion(obj interface{}) {
	ownee, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Couldn't get object from tombstone %#v", obj))
			return
		}

		ownee, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Tombstone contained object that is not a metav1.Object %#v", obj))
			return
		}
	}

	a.requeueOwnerCSVs(ownee)
}

func (a *Operator) requeueOwnerCSVs(ownee metav1.Object) {
	logger := a.Log.WithFields(logrus.Fields{
		"ownee":     ownee.GetName(),
		"selflink":  ownee.GetSelfLink(),
		"namespace": ownee.GetNamespace(),
	})

	// Attempt to requeue CSV owners in the same namespace as the object
	owners := ownerutil.GetOwnersByKind(ownee, v1alpha1.ClusterServiceVersionKind)
	if len(owners) == 0 {
		logger.Debugf("No ownerreferences found")
		return
	}

	if ownee.GetNamespace() != metav1.NamespaceAll {
		for _, ownerCSV := range owners {
			// Since cross-namespace CSVs can't exist we're guaranteed the owner will be in the same namespace
			err := a.csvQueueSet.Requeue(ownerCSV.Name, ownee.GetNamespace())
			if err != nil {
				a.Log.Warn(err.Error())
			}
		}

		return
	}

	// Get all existing CSVs from the indexer
	csvs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().List(labels.Everything())
	if err != nil {
		logger.Warnf("error attempting to list all CSVs in indexer: %s", err.Error())
		return
	}
	if len(csvs) == 0 {
		logger.Infof("no existing CSVs found")
		return
	}

	csvSet := make(map[types.UID]*v1alpha1.ClusterServiceVersion, len(csvs))
	for _, csv := range csvs {
		csvSet[csv.GetUID()] = csv
	}
	logger.WithField("clusterwide", len(csvs)).Debug("number of csvs")

	// Requeue existing owner CSVs
	for _, owner := range owners {
		csv, ok := csvSet[owner.UID]
		if !ok {
			logger.Warnf("owner %v does not exist", owner.UID)
			continue
		}

		err = a.csvQueueSet.Requeue(csv.GetName(), csv.GetNamespace())
		if err != nil {
			a.Log.Warn(err.Error())
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
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		logger.Warnf("could not cast install strategy as type %T", strategyDetailsDeployment)
		return
	}

	// Delete deployments
	for _, spec := range strategyDetailsDeployment.DeploymentSpecs {
		logger := logger.WithField("deployment", spec.Name)
		logger.Debug("cleaning up CSV deployment")
		if err := a.OpClient.DeleteDeployment(csv.GetNamespace(), spec.Name, &metav1.DeleteOptions{}); err != nil {
			logger.WithField("err", err).Warn("error cleaning up CSV deployment")
		}
	}
}
