package openshift

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
)

const (
	MaxOpenShiftVersionProperty = "olm.maxOpenShiftVersion"
)

type ClusterOperator struct {
	*configv1.ClusterOperator

	TotalSyncs, SuccessfulSyncs int
}

func NewClusterOperator(name string) *ClusterOperator {
	co := &ClusterOperator{ClusterOperator: &configv1.ClusterOperator{}}
	co.SetName(name)
	return co
}

func (c *ClusterOperator) GetOperatorVersion() string {
	for _, v := range c.Status.Versions {
		if v.Name == "operator" {
			return v.Version
		}
	}

	return ""
}

func (c *ClusterOperator) SetVersions(versions []configv1.OperandVersion) {
	// Note: DeepEquals cares about element order, so we may end up updating more often than we need to.
	// This is good enough for now, but if it becomes an issue we should check that the elements match instead.
	if reflect.DeepEqual(c.Status.Versions, versions) {
		return
	}
	c.Status.Versions = versions

	return
}

func (c *ClusterOperator) GetCondition(conditionType configv1.ClusterStatusConditionType) *configv1.ClusterOperatorStatusCondition {
	for _, cond := range c.Status.Conditions {
		if cond.Type == conditionType {
			return &cond
		}
	}

	return nil
}

func (c *ClusterOperator) SetCondition(condition *configv1.ClusterOperatorStatusCondition) {
	// Filter dups
	conditions := []configv1.ClusterOperatorStatusCondition{}
	for _, c := range c.Status.Conditions {
		if c.Type != condition.Type {
			conditions = append(conditions, c)
		}
	}

	conditions = append(conditions, *condition)
}

type Mutator interface {
	Mutate(context.Context, *ClusterOperator) error
}

type MutateFunc func(context.Context, *ClusterOperator) error

func (m MutateFunc) Mutate(ctx context.Context, co *ClusterOperator) error {
	return m(ctx, co)
}

type SerialMutations []Mutator

func (s SerialMutations) Mutate(ctx context.Context, co *ClusterOperator) error {
	var errs []error
	for _, m := range s {
		if err := m.Mutate(ctx, co); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

type ReconcilerConfig struct {
	Client       client.Client
	Log          logr.Logger
	RequeueDelay time.Duration
	TweakBuilder func(*builder.Builder) *builder.Builder

	Name           string
	Namespace      string
	SyncCh         <-chan error
	SyncThreshold  int
	TargetVersions []configv1.OperandVersion

	Mutator Mutator
}

type ReconcilerOption func(*ReconcilerConfig)

func (c *ReconcilerConfig) apply(opts []ReconcilerOption) {
	for _, opt := range opts {
		opt(c)
	}
}

func (c *ReconcilerConfig) complete() error {
	// TODO
	if c.Name == "" {
		return fmt.Errorf("No ClusterOperator name specified")
	}
	if len(c.TargetVersions) < 1 {
		c.TargetVersions = []configv1.OperandVersion{
			{
				Name:    "operator",
				Version: os.Getenv("RELEASE_VERSION"),
			},
			{
				Name:    "operator-lifecycle-manager",
				Version: olmversion.OLMVersion,
			},
		}
	}

	return nil
}

func WatchNamespace(namespace string) predicate.Funcs {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetNamespace() == namespace
	})
}

func WatchName(name string) predicate.Funcs {
	return predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetName() == name
	})
}

func (c *ReconcilerConfig) mapClusterOperator(_ client.Object) []reconcile.Request {
	// Enqueue the cluster operator
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: c.Name}},
	}
}

func WithSyncChannel(syncCh <-chan error) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.SyncCh = syncCh
	}
}

func WithOLMOperator() ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Name = "operator-lifecycle-manager"

		var mutations SerialMutations
		if config.Mutator != nil {
			mutations = append(mutations, config.Mutator)
		}

		mutations = append(mutations, MutateFunc(func(ctx context.Context, co *ClusterOperator) error {
			refs, err := olmOperatorRelatedObjects(ctx, config.Client, config.Namespace)
			if len(refs) > 0 {
				// Set any refs we found, regardless of any errors encountered (best effort)
				co.Status.RelatedObjects = refs
			}

			return err
		}))
		config.Mutator = mutations

		enqueue := handler.EnqueueRequestsFromMapFunc(config.mapClusterOperator)
		predicates := builder.WithPredicates(WatchNamespace(config.Namespace))
		config.TweakBuilder = func(bldr *builder.Builder) *builder.Builder {
			return bldr.Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueue, predicates)
		}
	}
}

func WithCatalogOperator() ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Name = "operator-lifecycle-manager-catalog"

		var mutations SerialMutations
		if config.Mutator != nil {
			mutations = append(mutations, config.Mutator)
		}

		mutations = append(mutations, MutateFunc(func(ctx context.Context, co *ClusterOperator) error {
			refs, err := catalogOperatorRelatedObjects(ctx, config.Client, config.Namespace)
			if len(refs) > 0 {
				// Set any refs we found, regardless of any errors encountered (best effort)
				co.Status.RelatedObjects = refs
			}

			return err
		}))
		config.Mutator = mutations

		enqueue := handler.EnqueueRequestsFromMapFunc(config.mapClusterOperator)
		predicates := builder.WithPredicates(WatchNamespace(config.Namespace))
		config.TweakBuilder = func(bldr *builder.Builder) *builder.Builder {
			return bldr.Watches(&source.Kind{Type: &operatorsv1alpha1.Subscription{}}, enqueue, predicates).
				Watches(&source.Kind{Type: &operatorsv1alpha1.InstallPlan{}}, enqueue, predicates)
		}
	}
}

type ClusterOperatorReconciler struct {
	config *ReconcilerConfig

	// Surface commonly used config fields
	client       client.Client
	log          logr.Logger
	delayRequeue reconcile.Result
	mutate       MutateFunc
	now          func() metav1.Time

	co *ClusterOperator
}

func NewClusterOperatorReconciler(opts ...ReconcilerOption) (*ClusterOperatorReconciler, error) {
	config := new(ReconcilerConfig)
	config.apply(opts)
	if err := config.complete(); err != nil {
		return nil, err
	}

	r := &ClusterOperatorReconciler{
		config:       config,
		client:       config.Client,
		log:          config.Log,
		delayRequeue: reconcile.Result{RequeueAfter: config.RequeueDelay},
		co:           NewClusterOperator(config.Name),
	}

	var mutations SerialMutations
	if config.Mutator != nil {
		mutations = append(mutations, config.Mutator)
	}
	mutations = append(mutations,
		r.setVersion,
		r.setProgressing,
		r.setAvailable,
		r.setDegraded,
		r.setUpgradeable,
	)
	r.mutate = mutations.Mutate

	return &ClusterOperatorReconciler{
		config:       config,
		client:       config.Client,
		log:          config.Log,
		delayRequeue: reconcile.Result{RequeueAfter: config.RequeueDelay},
		mutate:       config.Mutator.Mutate,

		co: NewClusterOperator(config.Name),
	}, nil
}

func (r *ClusterOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO: Factor out of setup function
	events := make(chan event.GenericEvent)
	go func() {
		// FIXME: Handle nil channel
		for err := range r.config.SyncCh {
			// FIXME: Concurrency issues?
			r.co.TotalSyncs++
			if err == nil {
				r.co.SuccessfulSyncs++
			}

			events <- event.GenericEvent{Object: r.co}
		}
	}()

	bldr := ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(WatchName(r.config.Name))).
		Watches(&source.Channel{Source: events}, &handler.EnqueueRequestForObject{})

	return r.config.TweakBuilder(bldr).Complete(r)
}

func (r *ClusterOperatorReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithValues("request", req)
	noRequeue := reconcile.Result{}
	if req.NamespacedName.Name != r.co.GetName() {
		// Throw away requests to reconcile all ClusterOperators but our own
		// These should already be filtered by controller-runtime by this point
		return noRequeue, nil
	}

	// Get the ClusterOperator
	in := r.co.DeepCopy()
	if err := r.client.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			// The ClusterOperator is missing, let's create it
			// TODO: use CreateOrUpdate instead?
			err = r.client.Create(ctx, r.co.ClusterOperator)
		}

		// Transient error or successful creation, requeue
		return r.delayRequeue, err
	}

	r.co.ClusterOperator = in.DeepCopy()

	if err := r.mutate(ctx, r.co); err != nil {
		// TODO: requeue for transient errors
		log.Error(err, "Error mutating ClusterOperator")
	}

	if !reflect.DeepEqual(r.co.Status, in.Status) {
		// Status change detected, update
		if err := r.client.Status().Update(ctx, r.co); err != nil {
			// Transitive error, requeue
			return r.delayRequeue, err
		}
	}

	return noRequeue, nil
}

func conditionsEqual(a, b *configv1.ClusterOperatorStatusCondition) bool {
	if a == b {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	return a.Type == b.Type && a.Status == b.Status && a.Message == b.Message && a.Reason == b.Reason
}

func (r *ClusterOperatorReconciler) setVersions(_ context.Context, co *ClusterOperator) error {
	// If we've successfully synced, we know our operator is working properly, so we can update the version
	if co.SuccessfulSyncs > 0 && !reflect.DeepEqual(co.Status.Versions, r.config.TargetVersions) {
		co.Status.Versions = r.config.TargetVersions
	}

	return nil
}

func (r *ClusterOperatorReconciler) setProgressing(_ context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorProgressing,
		LastTransitionTime: r.now(),
	}

	// FIXME: Don't hardcode status messages.
	if co.SuccessfulSyncs > 0 && reflect.DeepEqual(co.Status.Versions, r.config.TargetVersions) {
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
		LastTransitionTime: r.now(),
	}

	if co.SuccessfulSyncs > 0 && reflect.DeepEqual(co.Status.Versions, r.config.TargetVersions) {
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
		LastTransitionTime: r.now(),
	}

	if co.SuccessfulSyncs > 0 && reflect.DeepEqual(co.Status.Versions, r.config.TargetVersions) {
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

func (r *ClusterOperatorReconciler) setUpgradeable(ctx context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorUpgradeable,
		LastTransitionTime: r.now(),
	}

	if co.SuccessfulSyncs > 0 && reflect.DeepEqual(co.Status.Versions, r.config.TargetVersions) {
		desired.Status = configv1.ConditionTrue
	} else {
		desired.Status = configv1.ConditionFalse
		desired.Message = "Waiting for updates to take effect"
	}

	var err error
	if desired.Status == configv1.ConditionTrue {
		// Block upgrade on incompatible OLM-managed operators if necessary
		err = blockUpgrade(ctx, co)
	}

	current := co.GetCondition(configv1.OperatorUpgradeable)
	if conditionsEqual(current, desired) { // Comparison ignores lastUpdated
		return err
	}

	co.SetCondition(desired)

	return err
}

const (
	IncompatibleOperatorsInstalled = "IncompatibleOperatorsInstalled"
)

func (r *ClusterOperatorReconciler) blockUpgrade(ctx context.Context, co *ClusterOperator) error {
	desired := &configv1.ClusterOperatorStatusCondition{
		Type:               configv1.OperatorUpgradeable,
		LastTransitionTime: r.now(),
	}

	incompatible, err := r.incompatibleOperators(ctx)
	if err != nil {
		return err
	}

	if len(incompatible) > 0 {
		desired.Status = configv1.ConditionFalse
		desired.Reason = IncompatibleOperatorsInstalled
		// TODO: Truncate message to field length
		desired.Message = incompatible.String()
	} else {
		desired.Status = configv1.ConditionTrue
	}

	current := co.GetCondition(configv1.OperatorUpgradeable)
	if current.Status == configv1.ConditionFalse && current.Reason != IncompatibleOperatorsInstalled {
		// Don't stomp more important concurrent status from OLM
		return nil
	}
	if conditionsEqual(current, desired) { // Comparison ignores lastUpdated
		return nil
	}

	co.SetCondition(desired)

	return nil
}

func (r *ClusterOperatorReconciler) incompatibleOperators(ctx context.Context) (skews, error) {
	// TODO
	return nil, nil
}

type skews []skew

func (s skews) String() string {
	// TODO: Use a string builder.
	str := "The following operators block upgrades: "
	for i, sk := range s {
		if i < len(s)-1 {
			str += sk.String()
		}
	}

	return str
}

type skew struct {
	namespace           string
	name                string
	maxOpenShiftVersion string
}

func (s skew) String() string {
	return fmt.Sprintf("Operator %s in namespace %s is not compatible with OpenShift versions greater than %s", s.name, s.namespace, s.maxOpenShiftVersion)
}

func olmOperatorRelatedObjects(ctx context.Context, cli client.Client, namespace string) ([]configv1.ObjectReference, error) {
	csvList := &operatorsv1alpha1.ClusterServiceVersionList{}
	if err := cli.List(ctx, csvList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	// TODO: Is there a better way to filter (server-side maybe)?
	var refs []configv1.ObjectReference
	for _, csv := range csvList.Items {
		if csv.IsCopied() {
			// Filter out copied CSVs
			continue
		}

		// TODO: Generalize ObjectReference generation
		refs = append(refs, configv1.ObjectReference{
			Group:     operatorsv1alpha1.GroupName,
			Resource:  "clusterserviceversions",
			Namespace: csv.GetNamespace(),
			Name:      csv.GetName(),
		})
	}

	return refs, nil
}

func catalogOperatorRelatedObjects(ctx context.Context, cli client.Client, namespace string) ([]configv1.ObjectReference, error) {
	var errs []error
	subList := &operatorsv1alpha1.SubscriptionList{}
	if err := cli.List(ctx, subList, client.InNamespace(namespace)); err != nil {
		errs = append(errs, err)
	}

	// TODO: Is there a better way to filter (server-side maybe)?
	var refs []configv1.ObjectReference
	for _, sub := range subList.Items {
		// TODO: Generalize ObjectReference generation
		refs = append(refs, configv1.ObjectReference{
			Group:     operatorsv1alpha1.GroupName,
			Resource:  "subscriptions",
			Namespace: sub.GetNamespace(),
			Name:      sub.GetName(),
		})
	}

	ipList := &operatorsv1alpha1.InstallPlanList{}
	if err := cli.List(ctx, ipList, client.InNamespace(namespace)); err != nil {
		errs = append(errs, err)
	}

	for _, ip := range ipList.Items {
		// TODO: Generalize ObjectReference generation
		refs = append(refs, configv1.ObjectReference{
			Group:     operatorsv1alpha1.GroupName,
			Resource:  "installplans",
			Namespace: ip.GetNamespace(),
			Name:      ip.GetName(),
		})
	}

	return refs, nil
}
