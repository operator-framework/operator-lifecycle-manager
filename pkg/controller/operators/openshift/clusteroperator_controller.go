package openshift

import (
	"context"
	"fmt"
	"reflect"

	configv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
)

var (
	localSchemeBuilder = runtime.NewSchemeBuilder(
		configv1.AddToScheme,
		operatorsv1alpha1.AddToScheme,
	)

	// AddToScheme adds all types necessary for the controller to operate.
	AddToScheme = localSchemeBuilder.AddToScheme
)

type ClusterOperatorReconciler struct {
	*ReconcilerConfig

	delayRequeue reconcile.Result
	mutator      Mutator
	syncTracker  *SyncTracker
	co           *ClusterOperator
}

func NewClusterOperatorReconciler(opts ...ReconcilerOption) (*ClusterOperatorReconciler, error) {
	config := new(ReconcilerConfig)
	config.apply(opts)
	if err := config.complete(); err != nil {
		return nil, err
	}

	co := NewClusterOperator(config.Name)
	r := &ClusterOperatorReconciler{
		ReconcilerConfig: config,
		delayRequeue:     reconcile.Result{RequeueAfter: config.RequeueDelay},
		co:               co,
		syncTracker:      NewSyncTracker(config.SyncCh, co.DeepCopy()),
	}

	var mutations SerialMutations
	if config.Mutator != nil {
		mutations = append(mutations, config.Mutator)
	}
	mutations = append(mutations,
		MutateFunc(r.setVersions),
		MutateFunc(r.setProgressing),
		MutateFunc(r.setAvailable),
		MutateFunc(r.setDegraded),
		MutateFunc(r.setUpgradeable),
	)
	r.mutator = mutations

	return r, nil
}

func (r *ClusterOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.Add(r.syncTracker); err != nil {
		return fmt.Errorf("failed to add %T to manager: %s", r.syncTracker, err)
	}

	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(watchName(&r.Name))).
		Watches(&source.Channel{Source: r.syncTracker.Events()}, &handler.EnqueueRequestForObject{})

	return r.TweakBuilder(bldr).Complete(r)
}

func (r *ClusterOperatorReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.Log.WithValues("request", req)
	noRequeue := reconcile.Result{}
	if req.NamespacedName.Name != r.co.GetName() {
		// Throw away requests to reconcile all ClusterOperators but our own
		// These should already be filtered by controller-runtime by this point
		return noRequeue, nil
	}

	// Get the ClusterOperator
	in := &configv1.ClusterOperator{}
	if err := r.Client.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			// The ClusterOperator is missing, let's create it
			stripObject(r.co.ClusterOperator)
			err = r.Client.Create(ctx, r.co.ClusterOperator)
		}

		// Transient error or successful creation, requeue
		return r.delayRequeue, err
	}

	r.co.ClusterOperator = in.DeepCopy()

	var errs []error
	res := reconcile.Result{}
	if err := r.mutate(ctx, r.co); err != nil {
		// Transitive error, requeue
		log.Error(err, "Error mutating ClusterOperator")
		errs = append(errs, err)
		res = r.delayRequeue
	}

	if !reflect.DeepEqual(r.co.Status, in.Status) {
		// Status change detected, update
		if err := r.Client.Status().Update(ctx, r.co.ClusterOperator); err != nil {
			// Transitive error, requeue
			errs = append(errs, err)
			res = r.delayRequeue
		}
	}

	return res, utilerrors.NewAggregate(errs)
}

func (r *ClusterOperatorReconciler) mutate(ctx context.Context, co *ClusterOperator) error {
	return r.mutator.Mutate(ctx, co)
}

func (r *ClusterOperatorReconciler) setVersions(_ context.Context, co *ClusterOperator) error {
	// If we've successfully synced, we know our operator is working properly, so we can update the version
	if r.syncTracker.SuccessfulSyncs() > 0 && !versionsMatch(co.Status.Versions, r.TargetVersions) {
		co.Status.Versions = r.TargetVersions
	}

	return nil
}

func (r *ClusterOperatorReconciler) setProgressing(_ context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		LastTransitionTime: r.Now(),
	}

	if r.syncTracker.SuccessfulSyncs() > 0 && versionsMatch(co.Status.Versions, r.TargetVersions) {
		desired.Status = configv1.ConditionFalse
		desired.Message = fmt.Sprintf("Deployed %s", olmversion.OLMVersion)
	} else {
		desired.Status = configv1.ConditionTrue
		desired.Message = fmt.Sprintf("Waiting to see update %s succeed", olmversion.OLMVersion)
	}

	current := co.GetCondition(configv1.OperatorProgressing)
	if conditionsEqual(current, desired) { // Comparison ignores lastUpdated
		return nil
	}

	co.SetCondition(desired)

	return nil
}

func (r *ClusterOperatorReconciler) setAvailable(_ context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorAvailable,
		LastTransitionTime: r.Now(),
	}

	if r.syncTracker.SuccessfulSyncs() > 0 && versionsMatch(co.Status.Versions, r.TargetVersions) {
		desired.Status = configv1.ConditionTrue
	} else {
		desired.Status = configv1.ConditionFalse
	}

	current := co.GetCondition(configv1.OperatorAvailable)
	if conditionsEqual(current, desired) { // Comparison ignores lastUpdated
		return nil
	}

	co.SetCondition(desired)

	return nil
}

func (r *ClusterOperatorReconciler) setDegraded(_ context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorDegraded,
		LastTransitionTime: r.Now(),
	}

	if r.syncTracker.SuccessfulSyncs() > 0 && versionsMatch(co.Status.Versions, r.TargetVersions) {
		desired.Status = configv1.ConditionFalse
	} else {
		desired.Status = configv1.ConditionTrue
		desired.Message = "Waiting for updates to take effect"
	}

	current := co.GetCondition(configv1.OperatorDegraded)
	if conditionsEqual(current, desired) { // Comparison ignores lastUpdated
		return nil
	}

	co.SetCondition(desired)

	return nil
}

const (
	IncompatibleOperatorsInstalled = "IncompatibleOperatorsInstalled"
)

func (r *ClusterOperatorReconciler) setUpgradeable(ctx context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorUpgradeable,
		Status:             configv1.ConditionTrue,
		LastTransitionTime: r.Now(),
	}

	// Set upgradeable=false if (either/or):
	// 1. OLM currently upgrading (takes priorty in the status message)
	// 2. Operators currently installed that are incompatible with the next OCP minor version
	if r.syncTracker.SuccessfulSyncs() < 1 || !versionsMatch(co.Status.Versions, r.TargetVersions) {
		// OLM is still upgrading
		desired.Status = configv1.ConditionFalse
		desired.Message = "Waiting for updates to take effect"
	} else {
		incompatible, err := incompatibleOperators(ctx, r.Client)
		if err != nil {
			return err
		}

		if len(incompatible) > 0 {
			// Some incompatible operator is installed
			desired.Status = configv1.ConditionFalse
			desired.Reason = IncompatibleOperatorsInstalled
			desired.Message = incompatible.String() // TODO: Truncate message to field length
		}
	}

	current := co.GetCondition(configv1.OperatorUpgradeable)
	if conditionsEqual(current, desired) { // Comparison ignores lastUpdated
		return nil
	}

	co.SetCondition(desired)

	return nil
}
