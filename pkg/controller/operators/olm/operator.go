package olm

import (
	"errors"
	"fmt"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	operatorgrouplister "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/annotator"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	log "github.com/sirupsen/logrus"
	"k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	cappsv1 "k8s.io/client-go/listers/apps/v1"
	cv1 "k8s.io/client-go/listers/core/v1"
	crbacv1 "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")
var ErrCRDOwnerConflict = errors.New("CRD owned by another ClusterServiceVersion")

//TODO(jpeeler): copied from catalog/operator.go
var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

const (
	FallbackWakeupInterval = 30 * time.Second
)

type Operator struct {
	*queueinformer.Operator
	csvQueue                 workqueue.RateLimitingInterface
	client                   versioned.Interface
	resolver                 install.StrategyResolverInterface
	lister                   operatorlister.OperatorLister
	roleLister               crbacv1.RoleLister
	roleBindingLister        crbacv1.RoleBindingLister
	clusterRoleLister        crbacv1.ClusterRoleLister
	clusterRoleBindingLister crbacv1.ClusterRoleBindingLister
	operatorGroupLister      map[string]operatorgrouplister.OperatorGroupLister
	deploymentLister         map[string]cappsv1.DeploymentLister
	namespaceLister          cv1.NamespaceLister
	annotator                *annotator.Annotator
	recorder                 record.EventRecorder
	cleanupFunc              func()
}

func NewOperator(crClient versioned.Interface, opClient operatorclient.ClientInterface, resolver install.StrategyResolverInterface, wakeupInterval time.Duration, annotations map[string]string, namespaces []string) (*Operator, error) {
	if wakeupInterval < 0 {
		wakeupInterval = FallbackWakeupInterval
	}
	if len(namespaces) < 1 {
		namespaces = []string{metav1.NamespaceAll}
	}

	queueOperator, err := queueinformer.NewOperatorFromClient(opClient)
	if err != nil {
		return nil, err
	}
	namespaceAnnotator := annotator.NewAnnotator(queueOperator.OpClient, annotations)
	eventRecorder, err := event.NewRecorder(opClient.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
	if err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:  queueOperator,
		client:    crClient,
		lister:    operatorlister.NewLister(),
		resolver:  resolver,
		annotator: namespaceAnnotator,
		recorder:  eventRecorder,
		cleanupFunc: func() {
			namespaceAnnotator.CleanNamespaceAnnotations(namespaces)
		},
	}

	// Set up RBAC informers
	informerFactory := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval)
	roleInformer := informerFactory.Rbac().V1().Roles()
	roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
	clusterRoleInformer := informerFactory.Rbac().V1().ClusterRoles()
	clusterRoleBindingInformer := informerFactory.Rbac().V1().ClusterRoleBindings()
	namespaceInformer := informerFactory.Core().V1().Namespaces()

	// register namespace queueinformer
	queueInformer := queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "namespaces"),
		namespaceInformer.Informer(),
		op.syncNamespace,
		nil,
		"namespace",
		metrics.NewMetricsNil(),
	)
	op.RegisterQueueInformer(queueInformer)
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())

	// Register RBAC QueueInformers
	rbacInformers := []cache.SharedIndexInformer{
		roleInformer.Informer(),
		roleBindingInformer.Informer(),
		clusterRoleInformer.Informer(),
		clusterRoleBindingInformer.Informer(),
	}

	rbacQueueInformers := queueinformer.New(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "rbac"),
		rbacInformers,
		op.syncRBAC,
		nil,
		"namespace",
		metrics.NewMetricsNil(),
	)
	for _, informer := range rbacQueueInformers {
		op.RegisterQueueInformer(informer)
	}

	// Set listers (for RBAC CSV requirement checking)
	op.lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, roleInformer.Lister())
	op.lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, roleBindingInformer.Lister())
	op.lister.RbacV1().RegisterClusterRoleLister(clusterRoleInformer.Lister())
	op.lister.RbacV1().RegisterClusterRoleBindingLister(clusterRoleBindingInformer.Lister())

	// Register APIService QueueInformers
	apiServiceInformer := kagg.NewSharedInformerFactory(opClient.ApiregistrationV1Interface(), wakeupInterval).Apiregistration().V1().APIServices()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "apiservices"),
		apiServiceInformer.Informer(),
		op.syncAPIServices,
		nil,
		"apiservices",
		metrics.NewMetricsNil(),
	))
	op.lister.APIRegistrationV1().RegisterAPIServiceLister(apiServiceInformer.Lister())

	// Register Secret QueueInformer
	secretInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Core().V1().Secrets()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "secrets"),
		secretInformer.Informer(),
		op.syncSecrets,
		nil,
		"secrets",
		metrics.NewMetricsNil(),
	))
	op.lister.CoreV1().RegisterSecretLister(metav1.NamespaceAll, secretInformer.Lister())

	// Register Service QueueInformer
	serviceInformer := informers.NewSharedInformerFactory(opClient.KubernetesInterface(), wakeupInterval).Core().V1().Services()
	op.RegisterQueueInformer(queueinformer.NewInformer(
		workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "services"),
		serviceInformer.Informer(),
		op.syncServices,
		nil,
		"services",
		metrics.NewMetricsNil(),
	))
	op.lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, serviceInformer.Lister())

	// Set up watch on CSVs
	csvInformers := []cache.SharedIndexInformer{}
	for _, namespace := range namespaces {
		log.Debugf("watching for CSVs in namespace %s", namespace)
		sharedInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		informer := sharedInformerFactory.Operators().V1alpha1().ClusterServiceVersions().Informer()
		csvInformers = append(csvInformers, informer)
	}

	// csvInformers for each namespace all use the same backing queue
	// queue keys are namespaced
	csvQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "clusterserviceversions")
	queueInformers := queueinformer.New(
		csvQueue,
		csvInformers,
		op.syncClusterServiceVersion,
		nil,
		"csv",
		metrics.NewMetricsCSV(op.client),
	)
	for _, informer := range queueInformers {
		op.RegisterQueueInformer(informer)
	}
	op.csvQueue = csvQueue

	// set up watch on deployments
	depInformers := []cache.SharedIndexInformer{}
	op.deploymentLister = make(map[string]cappsv1.DeploymentLister, len(namespaces))
	for _, namespace := range namespaces {
		log.Debugf("watching deployments in namespace %s", namespace)
		informerFactory := informers.NewSharedInformerFactoryWithOptions(opClient.KubernetesInterface(), wakeupInterval, informers.WithNamespace(namespace))
		informer := informerFactory.Apps().V1().Deployments()
		depInformers = append(depInformers, informer.Informer())
		op.deploymentLister[namespace] = informer.Lister()
	}

	depQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "csv-deployments")
	depQueueInformers := queueinformer.New(
		depQueue,
		depInformers,
		op.syncDeployment,
		nil,
		"deployment",
		metrics.NewMetricsNil(),
	)
	for _, informer := range depQueueInformers {
		op.RegisterQueueInformer(informer)
	}

	// Create an informer for the operator group
	operatorGroupInformers := []cache.SharedIndexInformer{}
	op.operatorGroupLister = make(map[string]operatorgrouplister.OperatorGroupLister, len(namespaces))
	for _, namespace := range namespaces {
		informerFactory := externalversions.NewSharedInformerFactoryWithOptions(crClient, wakeupInterval, externalversions.WithNamespace(namespace))
		informer := informerFactory.Operators().V1alpha2().OperatorGroups()
		operatorGroupInformers = append(operatorGroupInformers, informer.Informer())
		op.operatorGroupLister[namespace] = informer.Lister()
	}

	// Register OperatorGroup informers.
	operatorGroupQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "operatorgroups")
	operatorGroupQueueInformer := queueinformer.New(
		operatorGroupQueue,
		operatorGroupInformers,
		op.syncOperatorGroups,
		nil,
		"operatorgroups",
		metrics.NewMetricsNil(),
	)
	for _, informer := range operatorGroupQueueInformer {
		op.RegisterQueueInformer(informer)
	}

	return op, nil
}

func (a *Operator) Cleanup() {
	a.cleanupFunc()
}

func (a *Operator) requeueCSV(name, namespace string) {
	// we can build the key directly, will need to change if queue uses different key scheme
	key := fmt.Sprintf("%s/%s", namespace, name)
	a.csvQueue.AddRateLimited(key)

	return
}

func (a *Operator) syncDeployment(obj interface{}) (syncError error) {
	deployment, ok := obj.(*v1.Deployment)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Deployment failed")
	}
	if ownerutil.IsOwnedByKind(deployment, v1alpha1.ClusterServiceVersionKind) {
		oref := ownerutil.GetOwnerByKind(deployment, v1alpha1.ClusterServiceVersionKind)
		a.requeueCSV(oref.Name, deployment.GetNamespace())
	}

	return nil
}

func (a *Operator) syncRBAC(obj interface{}) (syncError error) {
	clusterLevel := false
	switch v := obj.(type) {
	case *rbacv1.Role:
		log.Debugf("sync Role %s in namespace %s", v.GetName(), v.GetNamespace())
	case *rbacv1.RoleBinding:
		log.Debugf("sync RoleBinding %s in namespace %s", v.GetName(), v.GetNamespace())
	case *rbacv1.ClusterRole:
		log.Debugf("sync ClusterRole %s", v.GetName())
		clusterLevel = true
	case *rbacv1.ClusterRoleBinding:
		log.Debugf("sync ClusterRoleBinding %s", v.GetName())
		clusterLevel = true
	default:
		syncError = errors.New("attempted to sync non RBAC resource with RBAC sync handler")
		log.Debugf(syncError.Error())
		return
	}

	if clusterLevel {
		// Cannot requeue namespaced owner CSVs if cluster-scoped
		return nil
	}

	// Assert as metav1.Object
	rbac, ok := obj.(metav1.Object)
	if !ok {
		syncError = errors.New("casting to runtime.Object failed")
		log.Debugf(syncError.Error())
		return
	}

	// Requeue all owner CSVs
	for _, oref := range ownerutil.GetOwnersByKind(rbac, v1alpha1.ClusterServiceVersionKind) {
		a.requeueCSV(oref.Name, rbac.GetNamespace())
	}

	return nil
}

func (a *Operator) syncSecrets(obj interface{}) (syncError error) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Secret failed")
	}

	// Requeue all owner CSVs
	logger := log.WithFields(log.Fields{
		"secret":    secret.GetName(),
		"namespace": secret.GetNamespace(),
	})
	for _, oref := range ownerutil.GetOwnersByKind(secret, v1alpha1.ClusterServiceVersionKind) {
		logger.Infof("requeuing CSV %s", oref.Name)
		// Note: If CSVs can own secrets outside of their namespace then this can result in
		// requeuing non-existant CSVs
		a.requeueCSV(oref.Name, secret.GetNamespace())
	}

	return nil
}

func (a *Operator) syncServices(obj interface{}) (syncError error) {
	service, ok := obj.(*corev1.Service)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Service failed")
	}

	// Requeue all owner CSVs
	logger := log.WithFields(log.Fields{
		"service":   service.GetName(),
		"namespace": service.GetNamespace(),
	})
	for _, oref := range ownerutil.GetOwnersByKind(service, v1alpha1.ClusterServiceVersionKind) {
		logger.Infof("requeuing CSV %s", oref.Name)
		a.requeueCSV(oref.Name, service.GetNamespace())
	}

	return nil
}

// syncClusterServiceVersion is the method that gets called when we see a CSV event in the cluster
func (a *Operator) syncClusterServiceVersion(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}
	logger := log.WithFields(log.Fields{
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})
	logger.Info("syncing")

	outCSV, syncError := a.transitionCSVState(*clusterServiceVersion)

	// no changes in status, don't update
	if outCSV.Status.Phase == clusterServiceVersion.Status.Phase && outCSV.Status.Reason == clusterServiceVersion.Status.Reason && outCSV.Status.Message == clusterServiceVersion.Status.Message {
		return
	}

	// Update CSV with status of transition. Log errors if we can't write them to the status.
	_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(clusterServiceVersion.GetNamespace()).UpdateStatus(outCSV)
	if err != nil {
		updateErr := errors.New("error updating ClusterServiceVersion status: " + err.Error())
		if syncError == nil {
			logger.Info(updateErr)
			return updateErr
		}
		syncError = fmt.Errorf("error transitioning ClusterServiceVersion: %s and error updating CSV status: %s", syncError, updateErr)
	}
	return
}

// transitionCSVState moves the CSV status state machine along based on the current value and the current cluster state.
func (a *Operator) transitionCSVState(in v1alpha1.ClusterServiceVersion) (out *v1alpha1.ClusterServiceVersion, syncError error) {
	logger := log.WithFields(log.Fields{
		"csv":       in.GetName(),
		"namespace": in.GetNamespace(),
		"phase":     in.Status.Phase,
	})

	out = in.DeepCopy()

	// check if the current CSV is being replaced, return with replacing status if so
	if err := a.checkReplacementsAndUpdateStatus(out); err != nil {
		logger.WithField("err", err).Info("replacement check")
		return
	}

	switch out.Status.Phase {
	case v1alpha1.CSVPhaseNone:
		logger.Infof("scheduling ClusterServiceVersion for requirement verification")
		out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "requirements not yet checked", a.recorder)
	case v1alpha1.CSVPhasePending:
		met, statuses, err := a.requirementAndPermissionStatus(out)
		if err != nil {
			logger.Info("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), a.recorder)
			return
		}
		out.SetRequirementStatus(statuses)

		if !met {
			logger.Info("requirements were not met")
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, "one or more requirements couldn't be found", a.recorder)
			syncError = ErrRequirementsNotMet
			return
		}

		// check for CRD ownership conflicts
		if syncError = a.crdOwnerConflicts(out, a.csvsInNamespace(out.GetNamespace())); syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, fmt.Sprintf("owner conflict: %s", syncError), a.recorder)
			return
		}

		logger.Info("scheduling ClusterServiceVersion for install")
		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstallReady, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting install", a.recorder)
	case v1alpha1.CSVPhaseInstallReady:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
			return
		}

		// Install owned APIServices and update strategy with serving cert data
		strategy, syncError = a.installOwnedAPIServiceRequirements(out, strategy)
		if syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install API services failed: %s", syncError), a.recorder)
			return
		}

		if syncError = installer.Install(strategy); syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", syncError), a.recorder)
			return
		}

		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonInstallSuccessful, "waiting for install components to report healthy", a.recorder)
		a.requeueCSV(out.GetName(), out.GetNamespace())
		return
	case v1alpha1.CSVPhaseInstalling:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
			return
		}

		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVReasonWaiting); installErr == nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Infof("install strategy successful")
		}
	case v1alpha1.CSVPhaseSucceeded:
		installer, strategy, _ := a.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			// parseStrategiesAndUpdateStatus sets CSV status
			return
		}

		// Check if any generated resources are missing
		if resErr := a.checkAPIServiceResources(out, certs.PEMSHA256); resErr != nil {
			out.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonAPIServiceResourceIssue, resErr.Error())
			return
		}

		if installErr := a.updateInstallStatus(out, installer, strategy, v1alpha1.CSVReasonComponentUnhealthy); installErr != nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Infof("unhealthy component: %s", installErr)
		}

		// Check if it's time to refresh owned APIService certs
		if a.shouldRotateCerts(out) {
			out.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedCertRotation, "owned APIServices need cert refresh")
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
			csv.SetPhaseWithEvent(v1alpha1.CSVPhaseDeleting, v1alpha1.CSVReasonReplaced, "has been replaced by a newer ClusterServiceVersion that has successfully installed.", a.recorder)
			// ignore errors and success here; this step is just an optimization to speed up GC
			a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).UpdateStatus(csv)
			a.requeueCSV(csv.GetName(), csv.GetNamespace())
		}

		// if there's no newer version, requeue for processing (likely will be GCable before resync)
		a.requeueCSV(out.GetName(), out.GetNamespace())
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
	csvsInNamespace := a.csvsInNamespace(csv.GetNamespace())
	current := csv

	// isBeingReplaced returns a copy
	next := a.isBeingReplaced(current, csvsInNamespace)
	for next != nil {
		csvs = append(csvs, current)
		log.Debugf("checking to see if %s is running so we can delete %s", next.GetName(), csv.GetName())
		installer, nextStrategy, currentStrategy := a.parseStrategiesAndUpdateStatus(next)
		if nextStrategy == nil {
			log.Debugf("couldn't get strategy for %s", next.GetName())
			continue
		}
		if currentStrategy == nil {
			log.Debugf("couldn't get strategy for %s", next.GetName())
			continue
		}
		installed, _ := installer.CheckInstalled(nextStrategy)
		if installed && !next.IsObsolete() {
			return csvs
		}
		current = next
		next = a.isBeingReplaced(current, csvsInNamespace)
	}
	return nil
}

// csvsInNamespace finds all CSVs in a namespace
func (a *Operator) csvsInNamespace(namespace string) map[string]*v1alpha1.ClusterServiceVersion {
	csvsInNamespace, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(metav1.ListOptions{})
	if err != nil {
		return nil
	}
	csvs := make(map[string]*v1alpha1.ClusterServiceVersion, len(csvsInNamespace.Items))
	for _, csv := range csvsInNamespace.Items {
		csvs[csv.Name] = csv.DeepCopy()
	}
	return csvs
}

// checkReplacementsAndUpdateStatus returns an error if we can find a newer CSV and sets the status if so
func (a *Operator) checkReplacementsAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) error {
	if csv.Status.Phase == v1alpha1.CSVPhaseReplacing || csv.Status.Phase == v1alpha1.CSVPhaseDeleting {
		return nil
	}
	if replacement := a.isBeingReplaced(csv, a.csvsInNamespace(csv.GetNamespace())); replacement != nil {
		log.Infof("newer ClusterServiceVersion replacing %s, no-op", csv.SelfLink)
		msg := fmt.Sprintf("being replaced by csv: %s", replacement.SelfLink)
		csv.SetPhase(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVReasonBeingReplaced, msg)
		metrics.CSVUpgradeCount.Inc()

		return fmt.Errorf("replacing")
	}
	return nil
}

func (a *Operator) updateInstallStatus(csv *v1alpha1.ClusterServiceVersion, installer install.StrategyInstaller, strategy install.Strategy, requeueConditionReason v1alpha1.ConditionReason) error {
	apiServicesInstalled, apiServiceErr := a.areAPIServicesAvailable(csv.Spec.APIServiceDefinitions.Owned)
	strategyInstalled, strategyErr := installer.CheckInstalled(strategy)
	if strategyInstalled && apiServicesInstalled {
		// if there's no error, we're successfully running
		if csv.Status.Phase != v1alpha1.CSVPhaseSucceeded {
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors")
		}
		return nil
	}

	// TODO(Nick): check if apiServiceErr is unrecoverable

	// installcheck determined we can't progress (e.g. deployment failed to come up in time)
	if install.IsErrorUnrecoverable(strategyErr) {
		csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install failed: %s", strategyErr))
		return strategyErr
	}

	// if there's an error checking install that shouldn't fail the strategy, requeue with message
	if apiServiceErr != nil {
		csv.SetPhase(v1alpha1.CSVPhaseInstalling, requeueConditionReason, fmt.Sprintf("APIServices installing: %s", apiServiceErr))
		a.requeueCSV(csv.GetName(), csv.GetNamespace())
		return apiServiceErr
	}

	if !apiServicesInstalled {
		csv.SetPhase(v1alpha1.CSVPhaseInstalling, requeueConditionReason, fmt.Sprintf("APIServices not installed"))
		a.requeueCSV(csv.GetName(), csv.GetNamespace())
		return fmt.Errorf("APIServices not installed")
	}

	if strategyErr != nil {
		csv.SetPhase(v1alpha1.CSVPhaseInstalling, requeueConditionReason, fmt.Sprintf("installing: %s", strategyErr))
		return strategyErr
	}

	return nil
}

// parseStrategiesAndUpdateStatus returns a StrategyInstaller and a Strategy for a CSV if it can, else it sets a status on the CSV and returns
func (a *Operator) parseStrategiesAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) (install.StrategyInstaller, install.Strategy, install.Strategy) {
	strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err))
		return nil, nil, nil
	}

	previousCSV := a.isReplacing(csv)
	var previousStrategy install.Strategy
	if previousCSV != nil {
		a.requeueCSV(previousCSV.Name, previousCSV.Namespace)
		previousStrategy, err = a.resolver.UnmarshalStrategy(previousCSV.Spec.InstallStrategy)
		if err != nil {
			previousStrategy = nil
		}
	}

	strName := strategy.GetStrategyName()
	installer := a.resolver.InstallerForStrategy(strName, a.OpClient, csv, previousStrategy)
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

// syncNamespace is the method that gets called when we see a namespace event in the cluster
func (a *Operator) syncNamespace(obj interface{}) (syncError error) {
	namespace, ok := obj.(*corev1.Namespace)
	namespaceName := namespace.GetName()
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Namespace failed")
	}

	log.Infof("syncing Namespace: %s", namespaceName)
	if err := a.annotator.AnnotateNamespace(namespace); err != nil {
		log.Infof("error annotating namespace '%s'", namespaceName)
		return err
	}

	return nil
}

func (a *Operator) isBeingReplaced(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) (replacedBy *v1alpha1.ClusterServiceVersion) {
	for _, csv := range csvsInNamespace {
		log.Infof("checking %s", csv.GetName())
		if csv.Spec.Replaces == in.GetName() {
			log.Infof("%s replaced by %s", in.GetName(), csv.GetName())
			replacedBy = csv.DeepCopy()
			return
		}
	}
	return
}

func (a *Operator) isReplacing(in *v1alpha1.ClusterServiceVersion) *v1alpha1.ClusterServiceVersion {
	log.Debugf("checking if csv is replacing an older version")
	if in.Spec.Replaces == "" {
		return nil
	}
	previous, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(in.GetNamespace()).Get(in.Spec.Replaces, metav1.GetOptions{})
	if err != nil {
		log.Debugf("unable to get previous csv: %s", err.Error())
		return nil
	}
	return previous
}
