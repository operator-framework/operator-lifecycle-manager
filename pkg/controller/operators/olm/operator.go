package olm

import (
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	ops "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	index "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/index"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/labeler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	csvutility "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/csv"
)

var (
	ErrRequirementsNotMet      = errors.New("requirements were not met")
	ErrCRDOwnerConflict        = errors.New("conflicting CRD owner in namespace")
	ErrAPIServiceOwnerConflict = errors.New("unable to adopt APIService")
)

type Operator struct {
	*ops.Operator

	recorder         record.EventRecorder
	resolver         install.StrategyResolverInterface
	apiReconciler    resolver.APIIntersectionReconciler
	apiLabeler       labeler.Labeler
	csvQueue         workqueue.RateLimitingInterface
	ogQueue          workqueue.RateLimitingInterface
	csvIndexer       cache.Indexer
	copyQueueIndexer *queueinformer.QueueIndexer
	gcQueueIndexer   *queueinformer.QueueIndexer
	csvSetGenerator	 csvutility.SetGenerator
	csvReplaceFinder csvutility.ReplaceFinder
}

func (o *Operator) syncAPIService(obj interface{}) (syncError error) {
	apiSvc, ok := obj.(*apiregistrationv1.APIService)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting APIService failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"id":     queueinformer.NewLoopID(),
		"name": apiSvc.GetName(),
	})
	logger.Info("syncing apiservice")

	if name, ns, ok := ownerutil.GetOwnerByKindLabel(apiSvc, v1alpha1.ClusterServiceVersionKind); ok {
		_, err := o.Lister.CoreV1().NamespaceLister().Get(ns)
		if k8serrors.IsNotFound(err) {
			logger.Debug("deleting api service since owning namespace is not found")
			syncError = o.OpClient.DeleteAPIService(apiSvc.GetName(), &metav1.DeleteOptions{})
			return
		}

		_, err = o.Lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(name)
		if k8serrors.IsNotFound(err) {
			logger.Debug("deleting api service since owning csv is not found")
			syncError = o.OpClient.DeleteAPIService(apiSvc.GetName(), &metav1.DeleteOptions{})
			return
		} else if err != nil {
			syncError = err
			return
		} else {
			if ownerutil.IsOwnedByKindLabel(apiSvc, v1alpha1.ClusterServiceVersionKind) {
				logger.Debug("requeueing owner csvs")
				o.requeueOwnerCSVs(apiSvc)
			}
		}
	}

	return nil
}

func (o *Operator) GetCSVSetGenerator() csvutility.SetGenerator {
	return o.csvSetGenerator
}

func (o *Operator) GetReplaceFinder() csvutility.ReplaceFinder {
	return o.csvReplaceFinder
}

func (o *Operator) syncObject(obj interface{}) (syncError error) {
	// Assert as metav1.Object
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		syncError = errors.New("object sync: casting to metav1.Object failed")
		o.Log.Warn(syncError.Error())
		return
	}
	logger := o.Log.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
		"self":      metaObj.GetSelfLink(),
	})

	// Requeue all owner CSVs
	if ownerutil.IsOwnedByKind(metaObj, v1alpha1.ClusterServiceVersionKind) {
		logger.Debug("requeueing owner csvs")
		o.requeueOwnerCSVs(metaObj)
	}

	// Requeues objects that can't have ownerrefs (cluster -> namespace, cross-namespace)
	if ownerutil.IsOwnedByKindLabel(metaObj, v1alpha1.ClusterServiceVersionKind) {
		logger.Debug("requeueing owner csvs")
		o.requeueOwnerCSVs(metaObj)
	}

	// Requeue CSVs with provided and required labels (for CRDs)
	if labelSets, err := o.apiLabeler.LabelSetsFor(metaObj); err != nil {
		logger.WithError(err).Warn("couldn't create label set")
	} else if len(labelSets) > 0 {
		logger.Debug("requeueing providing/requiring csvs")
		o.requeueCSVsByLabelSet(logger, labelSets...)
	}

	return nil
}

func (o *Operator) namespaceAddedOrRemoved(obj interface{}) {
	// Check to see if any operator groups are associated with this namespace
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
			return
		}

		namespace, ok = tombstone.Obj.(*corev1.Namespace)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("tombstone contained object that is not a Namespace %#v", obj))
			return
		}
	}

	logger := o.Log.WithFields(logrus.Fields{
		"namespace": namespace.GetName(),
	})

	operatorGroups, err := o.Lister.OperatorsV1().OperatorGroupLister().OperatorGroups(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		logger.WithError(err).Warn("failed to list operatorgroups on namespace change")
		return
	}

	for _, og := range operatorGroups {
		if resolver.NewNamespaceSet(og.Status.Namespaces).Contains(namespace.GetName()) {
			if key, err := cache.MetaNamespaceKeyFunc(og); err == nil {
				o.ogQueue.Add(key)
			} else {
				logger.WithError(err).WithFields(logrus.Fields{
					"operatorgroup":           og.GetName(),
					"operatorgroup-namespace": og.GetNamespace(),
				}).Warn("error requeuing on namespace change")
			}
		}
	}
	return
}

func (o *Operator) handleClusterServiceVersionDeletion(obj interface{}) {
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

	logger := o.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})

	defer func(csv v1alpha1.ClusterServiceVersion) {
		if clusterServiceVersion.IsCopied() {
			logger.Debug("deleted csv is copied. skipping operatorgroup requeue")
			return
		}

		// Requeue all OperatorGroups in the namespace
		logger.Debug("requeueing operatorgroups in namespace")
		operatorGroups, err := o.Lister.OperatorsV1().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).List(labels.Everything())
		if err != nil {
			logger.WithError(err).Warnf("an error occurred while listing operatorgroups to requeue after csv deletion")
			return
		}

		for _, og := range operatorGroups {
			logger := logger.WithField("operatorgroup", og.GetName())
			key, err := cache.MetaNamespaceKeyFunc(og)
			if err != nil {
				logger.WithError(err).Warn("error requeuing on namespace change")
				continue
			}

			o.ogQueue.Add(key)
			logger.Debug("requeued")
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
	namespaces := []string{}
	if targetNamespaces == "" {
		namespaceList, err := o.OpClient.KubernetesInterface().CoreV1().Namespaces().List(metav1.ListOptions{})
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
			o.gcQueueIndexer.Add(defaultKey(namespace, clusterServiceVersion.GetName()))
		}
	}

	for _, desc := range clusterServiceVersion.Spec.APIServiceDefinitions.Owned {
		apiServiceName := fmt.Sprintf("%s.%s", desc.Version, desc.Group)
		fetched, err := o.Lister.APIRegistrationV1().APIServiceLister().Get(apiServiceName)
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
			err := o.OpClient.DeleteAPIService(apiServiceName, &metav1.DeleteOptions{})
			if err != nil {
				logger.WithError(err).Warn("cannot delete orphaned api service")
			}
		}
	}
}

func (o *Operator) removeDanglingChildCSVs(csv *v1alpha1.ClusterServiceVersion) error {
	logger := o.Log.WithFields(logrus.Fields{
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
		return o.deleteChild(csv, logger)
	}

	logger = logger.WithField("parentNamespace", operatorNamespace)
	parent, err := o.Lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(operatorNamespace).Get(csv.GetName())
	if k8serrors.IsNotFound(err) || k8serrors.IsGone(err) || parent == nil {
		logger.Debug("deleting copied CSV since parent is missing")
		return o.deleteChild(csv, logger)
	}

	if parent.Status.Phase == v1alpha1.CSVPhaseFailed && parent.Status.Reason == v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
		logger.Debug("deleting copied CSV since parent has intersecting operatorgroup conflict")
		return o.deleteChild(csv, logger)
	}

	if annotations := parent.GetAnnotations(); annotations != nil {
		if !resolver.NewNamespaceSetFromString(annotations[v1.OperatorGroupTargetsAnnotationKey]).Contains(csv.GetNamespace()) {
			logger.WithField("parentTargets", annotations[v1.OperatorGroupTargetsAnnotationKey]).
				Debug("deleting copied CSV since parent no longer lists this as a target namespace")
			return o.deleteChild(csv, logger)
		}
	}

	return nil
}

func (o *Operator) deleteChild(csv *v1alpha1.ClusterServiceVersion, logger *logrus.Entry) error {
	logger.Debug("gcing csv")
	return o.Client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Delete(csv.GetName(), metav1.NewDeleteOptions(0))
}

// syncClusterServiceVersion is the method that gets called when we see a CSV event in the cluster
func (o *Operator) syncClusterServiceVersion(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})
	logger.Debug("syncing CSV")

	if clusterServiceVersion.IsCopied() {
		logger.Debug("skipping copied csv transition, schedule for gc check")
		o.gcQueueIndexer.Enqueue(clusterServiceVersion)
		return
	}

	outCSV, syncError := o.transitionCSVState(*clusterServiceVersion)

	if outCSV == nil {
		return
	}

	// status changed, update CSV
	if !(outCSV.Status.LastUpdateTime == clusterServiceVersion.Status.LastUpdateTime &&
		outCSV.Status.Phase == clusterServiceVersion.Status.Phase &&
		outCSV.Status.Reason == clusterServiceVersion.Status.Reason &&
		outCSV.Status.Message == clusterServiceVersion.Status.Message) {

		// Update CSV with status of transition. Log errors if we can't write them to the status.
		_, err := o.Client.OperatorsV1alpha1().ClusterServiceVersions(outCSV.GetNamespace()).UpdateStatus(outCSV)
		if err != nil {
			updateErr := errors.New("error updating ClusterServiceVersion status: " + err.Error())
			if syncError == nil {
				logger.Info(updateErr)
				syncError = updateErr
			} else {
				syncError = fmt.Errorf("error transitioning ClusterServiceVersion: %s and error updating CSV status: %s", syncError, updateErr)
			}
		}
	}

	operatorGroup := o.operatorGroupFromAnnotations(logger, clusterServiceVersion)
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
	if err := o.ensureRBACInTargetNamespace(clusterServiceVersion, operatorGroup); err != nil {
		logger.WithError(err).Info("couldn't ensure RBAC in target namespaces")
		syncError = err
	}

	if !outCSV.IsUncopiable() {
		o.copyQueueIndexer.Enqueue(outCSV)
	}

	return
}

func (o *Operator) syncCopyCSV(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       clusterServiceVersion.GetName(),
		"namespace": clusterServiceVersion.GetNamespace(),
		"phase":     clusterServiceVersion.Status.Phase,
	})

	logger.Debug("copying CSV")

	operatorGroup := o.operatorGroupFromAnnotations(logger, clusterServiceVersion)
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
	if err := o.ensureCSVsInNamespaces(clusterServiceVersion, operatorGroup, resolver.NewNamespaceSet(operatorGroup.Status.Namespaces)); err != nil {
		logger.WithError(err).Info("couldn't copy CSV to target namespaces")
		syncError = err
	}

	return
}

func (o *Operator) gcCSV(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		o.Log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}
	if clusterServiceVersion.IsCopied() {
		syncError = o.removeDanglingChildCSVs(clusterServiceVersion)
		return
	}
	return
}

// operatorGroupFromAnnotations returns the OperatorGroup for the CSV only if the CSV is active one in the group
func (o *Operator) operatorGroupFromAnnotations(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) *v1.OperatorGroup {
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

	operatorGroup, err := o.Lister.OperatorsV1().OperatorGroupLister().OperatorGroups(csv.GetNamespace()).Get(operatorGroupName)
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
	if targets != strings.Join(operatorGroup.Status.Namespaces, ",") {
		logger.Info("olm.targetNamespaces annotation doesn't match operatorgroup status")
		return nil
	}

	return operatorGroup
}

func (o *Operator) operatorGroupForCSV(csv *v1alpha1.ClusterServiceVersion, logger *logrus.Entry) (*v1.OperatorGroup, error) {
	now := o.Now()

	// Attempt to associate an OperatorGroup with the CSV.
	operatorGroups, err := o.Client.OperatorsV1().OperatorGroups(csv.GetNamespace()).List(metav1.ListOptions{})
	if err != nil {
		logger.Errorf("error occurred while attempting to associate csv with operatorgroup")
		return nil, err
	}
	var operatorGroup *v1.OperatorGroup

	switch len(operatorGroups.Items) {
	case 0:
		err = fmt.Errorf("csv in namespace with no operatorgroups")
		logger.Warn(err)
		csv.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoOperatorGroup, err.Error(), now, o.recorder)
		return nil, err
	case 1:
		operatorGroup = &operatorGroups.Items[0]
		logger = logger.WithField("opgroup", operatorGroup.GetName())
		if o.operatorGroupAnnotationsDiffer(&csv.ObjectMeta, operatorGroup) {
			o.setOperatorGroupAnnotations(&csv.ObjectMeta, operatorGroup, true)
			if _, err := o.Client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(csv); err != nil {
				logger.WithError(err).Warn("error adding operatorgroup annotations")
				return nil, err
			}
			if targetNamespaceList, err := o.getOperatorGroupTargets(operatorGroup); err == nil && len(targetNamespaceList) == 0 {
				csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoTargetNamespaces, "no targetNamespaces are matched operatorgroups namespace selection", now, o.recorder)
			}
			return nil, nil
		}
		logger.Info("csv in operatorgroup")
		return operatorGroup, nil
	default:
		err = fmt.Errorf("csv created in namespace with multiple operatorgroups, can't pick one automatically")
		logger.WithError(err).Warn("csv failed to become an operatorgroup member")
		if csv.Status.Reason != v1alpha1.CSVReasonTooManyOperatorGroups {
			csv.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonTooManyOperatorGroups, err.Error(), now, o.recorder)
		}
		return nil, err
	}
}

// transitionCSVState moves the CSV status state machine along based on the current value and the current cluster state.
func (o *Operator) transitionCSVState(in v1alpha1.ClusterServiceVersion) (out *v1alpha1.ClusterServiceVersion, syncError error) {
	logger := o.Log.WithFields(logrus.Fields{
		"id":        queueinformer.NewLoopID(),
		"csv":       in.GetName(),
		"namespace": in.GetNamespace(),
		"phase":     in.Status.Phase,
	})

	out = in.DeepCopy()
	now := o.Now()

	operatorSurface, err := resolver.NewOperatorFromV1Alpha1CSV(out)
	if err != nil {
		// TODO: Add failure status to CSV
		syncError = err
		return
	}

	// Ensure required and provided API labels
	if labelSets, err := o.apiLabeler.LabelSetsFor(operatorSurface); err != nil {
		logger.WithError(err).Warn("couldn't create label set")
	} else if len(labelSets) > 0 {
		updated, err := o.ensureLabels(out, labelSets...)
		if err != nil {
			logger.WithError(err).Warn("issue ensuring csv api labels")
			syncError = err
			return
		}
		// Update the underlying value of out to preserve changes
		*out = *updated
	}

	// Verify CSV operatorgroup (and update annotations if needed)
	operatorGroup, err := o.operatorGroupForCSV(out, logger)
	if operatorGroup == nil {
		// when err is nil, we still want to exit, but we don't want to re-add the csv ratelimited to the queue
		syncError = err
		logger.WithError(err).Info("operatorgroup incorrect")
		return
	}

	if err := o.ensureDeploymentAnnotations(logger, out); err != nil {
		return nil, err
	}

	modeSet, err := v1alpha1.NewInstallModeSet(out.Spec.InstallModes)
	if err != nil {
		syncError = err
		logger.WithError(err).Warn("csv has invalid installmodes")
		out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidInstallModes, syncError.Error(), now, o.recorder)
		return
	}

	// Check if the CSV supports its operatorgroup's selected namespaces
	targets, ok := out.GetAnnotations()[v1.OperatorGroupTargetsAnnotationKey]
	if ok {
		namespaces := strings.Split(targets, ",")

		if err := modeSet.Supports(out.GetNamespace(), namespaces); err != nil {
			logger.WithField("reason", err.Error()).Info("installmodeset does not support operatorgroups namespace selection")
			out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonUnsupportedOperatorGroup, err.Error(), now, o.recorder)
			return
		}
	} else {
		logger.Info("csv missing olm.targetNamespaces annotation")
		out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonNoTargetNamespaces, "csv missing olm.targetNamespaces annotation", now, o.recorder)
		return
	}

	// Check for intersecting provided APIs in intersecting OperatorGroups
	options := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name!=%s,metadata.namespace!=%s", operatorGroup.GetName(), operatorGroup.GetNamespace()),
	}
	otherGroups, err := o.Client.OperatorsV1().OperatorGroups(metav1.NamespaceAll).List(options)

	groupSurface := resolver.NewOperatorGroup(operatorGroup)
	otherGroupSurfaces := resolver.NewOperatorGroupSurfaces(otherGroups.Items...)
	providedAPIs := operatorSurface.ProvidedAPIs().StripPlural()

	switch result := o.apiReconciler.Reconcile(providedAPIs, groupSurface, otherGroupSurfaces...); {
	case operatorGroup.Spec.StaticProvidedAPIs && (result == resolver.AddAPIs || result == resolver.RemoveAPIs):
		// Transition the CSV to FAILED with status reason "CannotModifyStaticOperatorGroupProvidedAPIs"
		if out.Status.Reason != v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.WithField("apis", providedAPIs).Warn("cannot modify provided apis of static provided api operatorgroup")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs, "static provided api operatorgroup cannot be modified by these apis", now, o.recorder)
			o.cleanupCSVDeployments(logger, out)
		}
		return
	case result == resolver.APIConflict:
		// Transition the CSV to FAILED with status reason "InterOperatorGroupOwnerConflict"
		if out.Status.Reason != v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.WithField("apis", providedAPIs).Warn("intersecting operatorgroups provide the same apis")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, "intersecting operatorgroups provide the same apis", now, o.recorder)
			o.cleanupCSVDeployments(logger, out)
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
		unionedAnnotations[v1.OperatorGroupProvidedAPIsAnnotationKey] = union.String()
		operatorGroup.SetAnnotations(unionedAnnotations)
		if _, err := o.Client.OperatorsV1().OperatorGroups(operatorGroup.GetNamespace()).Update(operatorGroup); err != nil && !k8serrors.IsNotFound(err) {
			syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", v1.OperatorGroupProvidedAPIsAnnotationKey, err)
		}
		if key, err := cache.MetaNamespaceKeyFunc(out); err == nil {
			o.csvQueue.Add(key)
		} else {
			o.Log.WithError(err).Warn("failed to requeue csv")
		}
		return
	case result == resolver.RemoveAPIs:
		// Remove the CSV's provided APIs from its OperatorGroup's annotation
		logger.WithField("apis", providedAPIs).Debug("removing csv provided apis from operatorgroup")
		difference := groupSurface.ProvidedAPIs().Difference(providedAPIs)
		if diffedAnnotations := operatorGroup.GetAnnotations(); diffedAnnotations != nil {
			diffedAnnotations[v1.OperatorGroupProvidedAPIsAnnotationKey] = difference.String()
			operatorGroup.SetAnnotations(diffedAnnotations)
			if _, err := o.Client.OperatorsV1().OperatorGroups(operatorGroup.GetNamespace()).Update(operatorGroup); err != nil && !k8serrors.IsNotFound(err) {
				syncError = fmt.Errorf("could not update operatorgroups %s annotation: %v", v1.OperatorGroupProvidedAPIsAnnotationKey, err)
			}
		}
		if key, err := cache.MetaNamespaceKeyFunc(out); err == nil {
			o.csvQueue.Add(key)
		} else {
			o.Log.WithError(err).Warn("failed to requeue csv")
		}
		return
	default:
		logger.WithField("apis", providedAPIs).Debug("no intersecting operatorgroups provide the same apis")
	}

	switch out.Status.Phase {
	case v1alpha1.CSVPhaseNone:
		logger.Info("scheduling ClusterServiceVersion for requirement verification")
		out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "requirements not yet checked", now, o.recorder)
	case v1alpha1.CSVPhasePending:
		met, statuses, err := o.requirementAndPermissionStatus(out)
		if err != nil {
			// TODO: account for Bad Rule as well
			logger.Info("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, o.recorder)
			return
		}
		out.SetRequirementStatus(statuses)

		// Check if we need to requeue the previous
		if prev := o.isReplacing(out); prev != nil {
			if prev.Status.Phase == v1alpha1.CSVPhaseSucceeded {
				if key, err := cache.MetaNamespaceKeyFunc(prev); err == nil {
					o.csvQueue.Add(key)
				} else {
					o.Log.WithError(err).Warn("error requeueing previous csv")
				}
			}
		}

		if !met {
			logger.Info("requirements were not met")
			out.SetPhaseWithEventIfChanged(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, "one or more requirements couldn't be found", now, o.recorder)
			syncError = ErrRequirementsNotMet
			return
		}

		// Check for CRD ownership conflicts
		if syncError = o.crdOwnerConflicts(out, o.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny)); syncError != nil {
			if syncError == ErrCRDOwnerConflict {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, syncError.Error(), now, o.recorder)
			}
			return
		}

		// Check for APIServices ownership conflicts
		if syncError = o.apiServiceOwnerConflicts(out); syncError != nil {
			if syncError == ErrAPIServiceOwnerConflict {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonOwnerConflict, syncError.Error(), now, o.recorder)
			}
			return
		}

		// Check if we're not ready to install part of the replacement chain yet
		if prev := o.isReplacing(out); prev != nil {
			if prev.Status.Phase != v1alpha1.CSVPhaseReplacing {
				return
			}
		}

		logger.Info("scheduling ClusterServiceVersion for install")
		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstallReady, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting install", now, o.recorder)
	case v1alpha1.CSVPhaseInstallReady:
		installer, strategy := o.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		// Install owned APIServices and update strategy with serving cert data
		strategy, syncError = o.installOwnedAPIServiceRequirements(out, strategy)
		if syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install API services failed: %s", syncError), now, o.recorder)
			return
		}

		if syncError = installer.Install(strategy); syncError != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", syncError), now, o.recorder)
			return
		}

		out.SetPhaseWithEvent(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonInstallSuccessful, "waiting for install components to report healthy", now, o.recorder)
		if key, err := cache.MetaNamespaceKeyFunc(out); err == nil {
			o.csvQueue.Add(key)
		} else {
			o.Log.WithError(err).Warn("error requeuing csv")
		}

		return

	case v1alpha1.CSVPhaseInstalling:
		installer, strategy := o.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		if installErr := o.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonWaiting); installErr == nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Infof("install strategy successful")
		} else {
			// Set phase to failed if it's been a long time since the last transition (5 minutes)
			if metav1.Now().Sub(out.Status.LastTransitionTime.Time) >= 5*time.Minute {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install timeout"), now, o.recorder)
			}
		}

	case v1alpha1.CSVPhaseSucceeded:
		// Check if the current CSV is being replaced, return with replacing status if so
		if err := o.checkReplacementsAndUpdateStatus(out); err != nil {
			logger.WithError(err).Info("replacement check")
			return
		}

		installer, strategy := o.parseStrategiesAndUpdateStatus(out)
		if strategy == nil {
			return
		}

		// Check if any generated resources are missing
		if err := o.checkAPIServiceResources(out, certs.PEMSHA256); err != nil {
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonAPIServiceResourceIssue, err.Error(), now, o.recorder)
			return
		}

		// Check if it's time to refresh owned APIService certs
		if o.shouldRotateCerts(out) {
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "owned APIServices need cert refresh", now, o.recorder)
			return
		}

		// Ensure requirements are still present
		met, statuses, err := o.requirementAndPermissionStatus(out)
		if err != nil {
			logger.Info("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, o.recorder)
			return
		} else if !met {
			out.SetRequirementStatus(statuses)
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonRequirementsNotMet, fmt.Sprintf("requirements no longer met"), now, o.recorder)
			return
		}

		// Check install status
		if installErr := o.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentUnhealthy); installErr != nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Warnf("unhealthy component: %s", installErr)
			return
		}

		// Ensure cluster roles exist for using provided apis
		if err := o.ensureClusterRolesForCSV(out, operatorGroup); err != nil {
			logger.WithError(err).Info("couldn't ensure clusterroles for provided api types")
			syncError = err
			return
		}

	case v1alpha1.CSVPhaseFailed:
		installer, strategy := o.parseStrategiesAndUpdateStatus(out)
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
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "InstallModes now support target namespaces", now, o.recorder)
			return
		}

		// Check if failed due to conflicting OperatorGroups
		if out.Status.Reason == v1alpha1.CSVReasonInterOperatorGroupOwnerConflict {
			logger.Info("OperatorGroup no longer intersecting with conflicting owner. Transitioning to Pending...")
			// Check occurred before switch, safe to transition to pending
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "OperatorGroup no longer intersecting with conflicting owner", now, o.recorder)
			return
		}

		// Check if failed due to an attempt to modify a static OperatorGroup
		if out.Status.Reason == v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs {
			logger.Info("static OperatorGroup and intersecting groups now support providedAPIs...")
			// Check occurred before switch, safe to transition to pending
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "static OperatorGroup and intersecting groups now support providedAPIs", now, o.recorder)
			return
		}

		// Check if requirements exist
		met, statuses, err := o.requirementAndPermissionStatus(out)
		if err != nil && out.Status.Reason != v1alpha1.CSVReasonInvalidStrategy {
			logger.Warn("invalid install strategy")
			out.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err.Error()), now, o.recorder)
			return
		} else if !met {
			out.SetRequirementStatus(statuses)
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, fmt.Sprintf("requirements not met"), now, o.recorder)
			return
		}

		// Check if any generated resources are missing and that OLM can action on them
		if err := o.checkAPIServiceResources(out, certs.PEMSHA256); err != nil {
			if o.apiServiceResourceErrorActionable(err) {
				// Check if API services are adoptable. If not, keep CSV as Failed state
				out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonAPIServiceResourcesNeedReinstall, err.Error(), now, o.recorder)
			}
			return
		}

		// Check if it's time to refresh owned APIService certs
		if o.shouldRotateCerts(out) {
			out.SetPhaseWithEvent(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsCertRotation, "owned APIServices need cert refresh", now, o.recorder)
			return
		}

		// Check install status
		if installErr := o.updateInstallStatus(out, installer, strategy, v1alpha1.CSVPhasePending, v1alpha1.CSVReasonNeedsReinstall); installErr != nil {
			logger.WithField("strategy", out.Spec.InstallStrategy.StrategyName).Warnf("needs reinstall: %s", installErr)
		}

	case v1alpha1.CSVPhaseReplacing:
		// determine CSVs that are safe to delete by finding a replacement chain to a CSV that's running
		// since we don't know what order we'll process replacements, we have to guard against breaking that chain

		// if this isn't the earliest csv in a replacement chain, skip gc.
		// marking an intermediate for deletion will break the replacement chain
		if prev := o.isReplacing(out); prev != nil {
			logger.Debugf("being replaced, but is not a leaf. skipping gc")
			return
		}

		// If there is a succeeded replacement, mark this for deletion
		if next := o.isBeingReplaced(out, o.csvSet(out.GetNamespace(), v1alpha1.CSVPhaseAny)); next != nil {
			if next.Status.Phase == v1alpha1.CSVPhaseSucceeded {
				out.SetPhaseWithEvent(v1alpha1.CSVPhaseDeleting, v1alpha1.CSVReasonReplaced, "has been replaced by a newer ClusterServiceVersion that has successfully installed.", now, o.recorder)
			} else {
				// If there's a replacement, but it's not yet succeeded, requeue both (this is an active replacement)
				if key, err := cache.MetaNamespaceKeyFunc(next); err == nil {
					o.csvQueue.Add(key)
				} else {
					o.Log.WithError(err).WithField("csv", next.GetName()).Warn("error requeueing next csv")
				}
				if key, err := cache.MetaNamespaceKeyFunc(out); err == nil {
					o.csvQueue.Add(key)
				} else {
					o.Log.WithError(err).WithField("csv", out).Warn("error requeueing current csv")
				}
			}
		} else {
			syncError = fmt.Errorf("csv marked as replacee, but no replacement was found in cluster")
		}
	case v1alpha1.CSVPhaseDeleting:
		syncError = o.Client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Delete(out.GetName(), metav1.NewDeleteOptions(0))
		if syncError != nil {
			logger.Debugf("unable to get delete csv marked for deletion: %s", syncError.Error())
		}
	}

	return
}

// csvSet gathers all CSVs in the given namespace into a map keyed by CSV name; if metav1.NamespaceAll gets the set across all namespaces
func (o *Operator) csvSet(namespace string, phase v1alpha1.ClusterServiceVersionPhase) map[string]*v1alpha1.ClusterServiceVersion {
	return o.csvSetGenerator.WithNamespace(namespace, phase)
}

// checkReplacementsAndUpdateStatus returns an error if we can find a newer CSV and sets the status if so
func (o *Operator) checkReplacementsAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) error {
	if csv.Status.Phase == v1alpha1.CSVPhaseReplacing || csv.Status.Phase == v1alpha1.CSVPhaseDeleting {
		return nil
	}
	if replacement := o.isBeingReplaced(csv, o.csvSet(csv.GetNamespace(), v1alpha1.CSVPhaseAny)); replacement != nil {
		o.Log.Infof("newer ClusterServiceVersion replacing %s, no-op", csv.SelfLink)
		msg := fmt.Sprintf("being replaced by csv: %s", replacement.GetName())
		csv.SetPhaseWithEvent(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVReasonBeingReplaced, msg, o.Now(), o.recorder)
		metrics.CSVUpgradeCount.Inc()

		return fmt.Errorf("replacing")
	}
	return nil
}

func (o *Operator) updateInstallStatus(csv *v1alpha1.ClusterServiceVersion, installer install.StrategyInstaller, strategy install.Strategy, requeuePhase v1alpha1.ClusterServiceVersionPhase, requeueConditionReason v1alpha1.ConditionReason) error {
	apiServicesInstalled, apiServiceErr := o.areAPIServicesAvailable(csv)
	strategyInstalled, strategyErr := installer.CheckInstalled(strategy)
	now := o.Now()

	if strategyInstalled && apiServicesInstalled {
		// if there's no error, we're successfully running
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors", now, o.recorder)
		return nil
	}

	// installcheck determined we can't progress (e.g. deployment failed to come up in time)
	if install.IsErrorUnrecoverable(strategyErr) {
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install failed: %s", strategyErr), now, o.recorder)
		return strategyErr
	}

	if apiServiceErr != nil {
		csv.SetPhaseWithEventIfChanged(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonAPIServiceInstallFailed, fmt.Sprintf("APIService install failed: %s", apiServiceErr), now, o.recorder)
		return apiServiceErr
	}

	if !apiServicesInstalled {
		csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, fmt.Sprintf("APIServices not installed"), now, o.recorder)
		if key, err := cache.MetaNamespaceKeyFunc(csv); err == nil {
			o.csvQueue.Add(key)
		} else {
			o.Log.WithError(err).Warn("error requeueing csv")
		}

		return fmt.Errorf("APIServices not installed")
	}

	if strategyErr != nil {
		csv.SetPhaseWithEventIfChanged(requeuePhase, requeueConditionReason, fmt.Sprintf("installing: %s", strategyErr), now, o.recorder)
		if key, err := cache.MetaNamespaceKeyFunc(csv); err == nil {
			o.csvQueue.Add(key)
		} else {
			o.Log.WithError(err).Warn("error requeueing csv")
		}

		return strategyErr
	}

	return nil
}

// parseStrategiesAndUpdateStatus returns a StrategyInstaller and a Strategy for a CSV if it can, else it sets a status on the CSV and returns
func (o *Operator) parseStrategiesAndUpdateStatus(csv *v1alpha1.ClusterServiceVersion) (install.StrategyInstaller, install.Strategy) {
	strategy, err := o.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		csv.SetPhaseWithEvent(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err), o.Now(), o.recorder)
		return nil, nil
	}

	prev := o.isReplacing(csv)
	var prevStrategy install.Strategy
	if prev != nil {
		if key, err := cache.MetaNamespaceKeyFunc(prev); err == nil {
			o.csvQueue.Add(key)
		} else {
			o.Log.WithError(err).WithField("previous", prev.GetName()).Warn("error requeueing previous csv")
		}

		prevStrategy, err = o.resolver.UnmarshalStrategy(prev.Spec.InstallStrategy)
		if err != nil {
			prevStrategy = nil
		}
	}

	strName := strategy.GetStrategyName()
	installer := o.resolver.InstallerForStrategy(strName, o.OpClient, o.Lister, csv, csv.Annotations, prevStrategy)
	return installer, strategy
}

func (o *Operator) crdOwnerConflicts(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) error {
	csvsInChain := o.getReplacementChain(in, csvsInNamespace)
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

func (o *Operator) getReplacementChain(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) map[string]struct{} {
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
		current = *prev
		csvsInChain[current] = struct{}{}
		prev = replaces(current)
	}
	return csvsInChain
}

func (o *Operator) apiServiceOwnerConflicts(csv *v1alpha1.ClusterServiceVersion) error {
	// Get replacing CSV if exists
	replacing, err := o.Lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(csv.GetNamespace()).Get(csv.Spec.Replaces)
	if err != nil && !k8serrors.IsNotFound(err) && !k8serrors.IsGone(err) {
		return err
	}

	owners := []ownerutil.Owner{csv}
	if replacing != nil {
		owners = append(owners, replacing)
	}

	for _, desc := range csv.GetOwnedAPIServiceDescriptions() {
		// Check if the APIService exists
		apiService, err := o.Lister.APIRegistrationV1().APIServiceLister().Get(desc.GetName())
		if err != nil && !k8serrors.IsNotFound(err) && !k8serrors.IsGone(err) {
			return err
		}

		if apiService == nil {
			continue
		}

		if !ownerutil.AdoptableLabels(apiService.GetLabels(), true, owners...) {
			return ErrAPIServiceOwnerConflict
		}
	}

	return nil
}

func (o *Operator) isBeingReplaced(in *v1alpha1.ClusterServiceVersion, csvsInNamespace map[string]*v1alpha1.ClusterServiceVersion) (replacedBy *v1alpha1.ClusterServiceVersion) {
	return o.csvReplaceFinder.IsBeingReplaced(in, csvsInNamespace)
}

func (o *Operator) isReplacing(in *v1alpha1.ClusterServiceVersion) *v1alpha1.ClusterServiceVersion {
	return o.csvReplaceFinder.IsReplacing(in)
}

func (o *Operator) handleDeletion(obj interface{}) {
	metaObj, ok := obj.(metav1.Object)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Couldn't get object from tombstone %#v", obj))
			return
		}

		metaObj, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Tombstone contained object that is not a metav1.Object %#v", obj))
			return
		}
	}
	logger := o.Log.WithFields(logrus.Fields{
		"name":      metaObj.GetName(),
		"namespace": metaObj.GetNamespace(),
		"self":      metaObj.GetSelfLink(),
	})
	logger.Debug("handling resource deletion")

	logger.Debug("requeueing owner csvs")
	o.requeueOwnerCSVs(metaObj)

	// Requeue CSVs with provided and required labels (for CRDs)
	if labelSets, err := o.apiLabeler.LabelSetsFor(metaObj); err != nil {
		logger.WithError(err).Warn("couldn't create label set")
	} else if len(labelSets) > 0 {
		logger.Debug("requeueing providing/requiring csvs")
		o.requeueCSVsByLabelSet(logger, labelSets...)
	}
}

func (o *Operator) requeueCSVsByLabelSet(logger *logrus.Entry, labelSets ...labels.Set) {
	keys, err := index.LabelIndexKeys(o.csvIndexer, labelSets...)
	if err != nil {
		logger.WithError(err).Debug("issue getting csvs by label index")
		return
	}

	for _, key := range keys {
		o.csvQueue.Add(key)
		logger.WithField("key", key).Debug("csv successfully requeued on crd change")
	}
}

func (o *Operator) requeueOwnerCSVs(ownee metav1.Object) {
	logger := o.Log.WithFields(logrus.Fields{
		"ownee":     ownee.GetName(),
		"selflink":  ownee.GetSelfLink(),
		"namespace": ownee.GetNamespace(),
	})

	// Attempt to requeue CSV owners in the same namespace as the object
	owners := ownerutil.GetOwnersByKind(ownee, v1alpha1.ClusterServiceVersionKind)
	if len(owners) > 0 && ownee.GetNamespace() != metav1.NamespaceAll {
		for _, owner := range owners {
			// Since cross-namespace CSVs can't exist, we're guaranteed the owner will be in the same namespace
			o.csvQueue.Add(defaultKey(ownee.GetNamespace(), owner.Name))
			logger.WithField("owner", owner.Name).Debug("requeued owner")
		}
		return
	}

	// Requeue owners based on labels
	if name, ns, ok := ownerutil.GetOwnerByKindLabel(ownee, v1alpha1.ClusterServiceVersionKind); ok {
		o.csvQueue.Add(defaultKey(ns, name))
		logger.WithFields(logrus.Fields{
			"owner":           name,
			"owner-namespace": ns,
		}).Debug("requeued owner")
	}
}

func (o *Operator) cleanupCSVDeployments(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) {
	// Extract the InstallStrategy for the deployment
	strategy, err := o.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
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
		if err := o.OpClient.DeleteDeployment(csv.GetNamespace(), spec.Name, &metav1.DeleteOptions{}); err != nil {
			logger.WithField("err", err).Warn("error cleaning up CSV deployment")
		}
	}
}

func (o *Operator) ensureDeploymentAnnotations(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) error {
	if !csv.IsSafeToUpdateOperatorGroupAnnotations() {
		return nil
	}

	// Get csv operatorgroup annotations
	annotations := o.copyOperatorGroupAnnotations(&csv.ObjectMeta)

	// Extract the InstallStrategy for the deployment
	strategy, err := o.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		logger.Warn("could not parse install strategy while cleaning up CSV deployment")
		return nil
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		logger.Warnf("could not cast install strategy as type %T", strategyDetailsDeployment)
		return nil
	}

	existingDeployments, err := o.Lister.AppsV1().DeploymentLister().Deployments(csv.GetNamespace()).List(ownerutil.CSVOwnerSelector(csv))
	if err != nil {
		return err
	}

	// compare deployments to see if any need to be created/updated
	updateErrs := []error{}
	for _, dep := range existingDeployments {
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = map[string]string{}
		}
		for key, value := range annotations {
			dep.Spec.Template.Annotations[key] = value
		}
		if _, _, err := o.OpClient.UpdateDeployment(dep); err != nil {
			updateErrs = append(updateErrs, err)
		}
	}
	logger.Info("updated annotations to match current operatorgroup")

	return utilerrors.NewAggregate(updateErrs)
}

// ensureLabels merges a label set with a CSV's labels and attempts to update the CSV if the merged set differs from the CSV's original labels.
func (o *Operator) ensureLabels(in *v1alpha1.ClusterServiceVersion, labelSets ...labels.Set) (*v1alpha1.ClusterServiceVersion, error) {
	csvLabelSet := labels.Set(in.GetLabels())
	merged := csvLabelSet
	for _, labelSet := range labelSets {
		merged = labels.Merge(merged, labelSet)
	}
	if labels.Equals(csvLabelSet, merged) {
		return in, nil
	}

	o.Log.WithField("labels", merged).Error("Labels updated!")

	out := in.DeepCopy()
	out.SetLabels(merged)
	out, err := o.Client.OperatorsV1alpha1().ClusterServiceVersions(out.GetNamespace()).Update(out)
	return out, err
}

func defaultKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}
