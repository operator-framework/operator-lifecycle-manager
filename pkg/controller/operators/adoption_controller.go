package operators

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// AdoptionReconciler automagically associates Operator components with their respective operator resource.
type AdoptionReconciler struct {
	client.Client

	log     logr.Logger
	mu      sync.RWMutex
	factory decorators.OperatorFactory
}

// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators/status,verbs=update;patch
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager adds the operator reconciler to the given controller manager.
func (r *AdoptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Trigger operator events from the events of their compoenents.
	enqueueSub := &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(r.mapToSubscriptions),
	}

	// Create multiple controllers for resource types that require automatic adoption
	err := ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Subscription{}).
		Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueueSub).
		Complete(reconcile.Func(r.ReconcileSubscription))
	if err != nil {
		return err
	}

	enqueueCSV := &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(r.mapToClusterServiceVersions),
	}
	err = ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.ClusterServiceVersion{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.Service{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.ServiceAccount{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.Secret{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.Role{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.RoleBinding{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.ClusterRole{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.ClusterRoleBinding{}}, enqueueCSV).
		Watches(&source.Kind{Type: &apiextensionsv1.CustomResourceDefinition{}}, enqueueCSV).
		Watches(&source.Kind{Type: &apiregistrationv1.APIService{}}, enqueueCSV).
		Watches(&source.Kind{Type: &operatorsv1alpha1.Subscription{}}, enqueueCSV).
		Watches(&source.Kind{Type: &operatorsv1alpha1.InstallPlan{}}, enqueueCSV).
		Complete(reconcile.Func(r.ReconcileClusterServiceVersion))
	if err != nil {
		return err
	}

	return nil
}

// NewAdoptionReconciler constructs and returns an AdoptionReconciler.
// As a side effect, the given scheme has operator discovery types added to it.
func NewAdoptionReconciler(cli client.Client, log logr.Logger, scheme *runtime.Scheme) (*AdoptionReconciler, error) {
	// Add watched types to scheme.
	if err := AddToScheme(scheme); err != nil {
		return nil, err
	}

	factory, err := decorators.NewSchemedOperatorFactory(scheme)
	if err != nil {
		return nil, err
	}

	return &AdoptionReconciler{
		Client: cli,

		log:     log,
		factory: factory,
	}, nil
}

var fieldOwner = client.FieldOwner("olm")

// ReconcileSubscription labels the CSVs installed by a Subscription as components of an operator named after the subscribed package and install namespace.
func (r *AdoptionReconciler) ReconcileSubscription(req ctrl.Request) (reconcile.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling subscription")

	// Fetch the Subscription from the cache
	ctx := context.TODO()
	in := &operatorsv1alpha1.Subscription{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find Subscription")
		} else {
			log.Error(err, "Error finding Subscription")
		}

		return reconcile.Result{}, nil
	}

	// OLM generated Operators are named after their packages and further qualified by the install namespace
	if in.Spec == nil || in.Spec.Package == "" {
		log.Info("subscription spec missing package, ignoring")
		return reconcile.Result{}, nil
	}

	// Wrap with convenience decorator
	operator, err := r.factory.NewPackageOperator(in.Spec.Package, in.GetNamespace())
	if err != nil {
		log.Error(err, "Could not wrap Operator with convenience decorator")
		return reconcile.Result{}, nil
	}

	// Ensure the subscription is adopted
	var errs []error
	out := in.DeepCopy()
	adopted, err := operator.AdoptComponent(out)
	if err != nil {
		errs = append(errs, err)
	}
	if adopted {
		if err := r.Patch(ctx, out, client.MergeFrom(in)); err != nil {
			log.Error(err, "Error adopting Subscription")
			errs = append(errs, err)
		}
	}

	// Find the Subscription's CSVs and apply the component label if necessary
	adoptCSV := func(name string) error {
		csv := &operatorsv1alpha1.ClusterServiceVersion{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: in.GetNamespace(), Name: name}, csv); err != nil {
			if apierrors.IsNotFound(err) {
				err = nil
			}

			return err
		}
		log.Info("found CSV")

		candidate := csv.DeepCopy()
		adopted, err := operator.AdoptComponent(csv)
		if err != nil {
			return err
		}

		if adopted {
			// Only update the CSV if freshly adopted
			if err := r.Patch(ctx, csv, client.MergeFrom(candidate)); err != nil {
				return err
			}
		}

		return nil
	}

	if name := in.Status.InstalledCSV; name != "" {
		if err := adoptCSV(name); err != nil {
			log.Error(err, "Error adopting installed CSV")
			errs = append(errs, err)
		}
	}

	return reconcile.Result{}, utilerrors.NewAggregate(errs)
}

// ReconcileClusterServiceVersion projects the component labels of a given CSV onto all resources owned by it.
func (r *AdoptionReconciler) ReconcileClusterServiceVersion(req ctrl.Request) (reconcile.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling ClusterServiceVersion")

	// Fetch the CSV from the cache
	ctx := context.TODO()
	in := &operatorsv1alpha1.ClusterServiceVersion{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find ClusterServiceVersion")
		} else {
			log.Error(err, "Error finding ClusterServiceVersion")
		}

		return reconcile.Result{}, nil
	}

	// Adopt all resources owned by the CSV if necessary
	return reconcile.Result{}, r.adoptComponents(ctx, in)
}

func (r *AdoptionReconciler) adoptComponents(ctx context.Context, csv *operatorsv1alpha1.ClusterServiceVersion) error {
	if csv.IsCopied() {
		// For now, skip copied CSVs
		return nil
	}

	var operators []decorators.Operator
	for _, name := range decorators.OperatorNames(csv.GetLabels()) {
		o := &operatorsv1.Operator{}
		o.SetName(name.Name)
		operator, err := r.factory.NewOperator(o)
		if err != nil {
			return err
		}
		operators = append(operators, *operator)
	}

	if len(operators) < 1 {
		// No operators to adopt for
		return nil
	}

	// Label (adopt) prospective components
	var errs []error
	// TODO(njhale): parallelize
	for _, operator := range operators {
		components, err := r.adoptees(ctx, operator, csv)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		for _, component := range components {
			candidate := component.DeepCopyObject()
			adopted, err := operator.AdoptComponent(component)
			if err != nil {
				errs = append(errs, err)
				continue
			}

			if !adopted {
				// Don't update since we didn't adopt
				// This shouldn't occur since we already filtered candidates
				r.log.Error(fmt.Errorf("failed to adopt component candidate"), "candidate not adopted", "candidate", component)
				continue
			}

			// Patch the component to adopt
			if err = r.Patch(ctx, component, client.MergeFrom(candidate)); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *AdoptionReconciler) adoptees(ctx context.Context, operator decorators.Operator, csv *operatorsv1alpha1.ClusterServiceVersion) ([]runtime.Object, error) {
	// Note: We need to figure out how to dynamically add new list types here (or some equivalent) in
	// order to support operators composed of custom resources.
	componentLists := []runtime.Object{
		&appsv1.DeploymentList{},
		&corev1.ServiceList{},
		&corev1.NamespaceList{},
		&corev1.ServiceAccountList{},
		&corev1.SecretList{},
		&corev1.ConfigMapList{},
		&rbacv1.RoleList{},
		&rbacv1.RoleBindingList{},
		&rbacv1.ClusterRoleList{},
		&rbacv1.ClusterRoleBindingList{},
		&apiregistrationv1.APIServiceList{},
		&apiextensionsv1.CustomResourceDefinitionList{},
		&operatorsv1alpha1.SubscriptionList{},
		&operatorsv1alpha1.InstallPlanList{},
		&operatorsv1alpha1.ClusterServiceVersionList{},
	}

	// Only resources that aren't already labelled are adoption candidates
	selector, err := operator.NonComponentSelector()
	if err != nil {
		return nil, err
	}
	opt := client.MatchingLabelsSelector{Selector: selector}
	for _, list := range componentLists {
		if err := r.List(ctx, list, opt); err != nil {
			return nil, err
		}
	}

	var (
		components []runtime.Object
		errs       []error
	)
	for _, candidate := range flatten(componentLists) {
		m, err := meta.Accessor(candidate)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if ownerutil.IsOwnedBy(m, csv) || ownerutil.IsOwnedByLabel(m, csv) {
			components = append(components, candidate)
		}
	}

	// Pick up owned CRDs
	for _, provided := range csv.Spec.CustomResourceDefinitions.Owned {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := r.Get(ctx, types.NamespacedName{Name: provided.Name}, crd); err != nil {
			if !apierrors.IsNotFound(err) {
				// Inform requeue on transient error
				errs = append(errs, err)
			}

			// Skip on transient error or missing CRD
			continue
		}

		if crd == nil || !selector.Matches(labels.Set(crd.GetLabels())) {
			// Skip empty and labelled CRDs
			continue
		}

		components = append(components, crd)
	}

	if err := utilerrors.NewAggregate(errs); err != nil {
		return nil, err
	}

	return components, nil
}

func (r *AdoptionReconciler) mapToSubscriptions(obj handler.MapObject) (requests []reconcile.Request) {
	if obj.Meta == nil {
		return
	}

	// Requeue all Subscriptions in the resource namespace
	// The Subscription reconciler will sort out the important changes
	ctx := context.TODO()
	subs := &operatorsv1alpha1.SubscriptionList{}
	if err := r.List(ctx, subs, client.InNamespace(obj.Meta.GetNamespace())); err != nil {
		r.log.Error(err, "couldn't list subscriptions")
	}

	for _, sub := range subs.Items {
		nsn := types.NamespacedName{Namespace: sub.GetNamespace(), Name: sub.GetName()}
		requests = append(requests, reconcile.Request{NamespacedName: nsn})
	}
	r.log.Info("requeueing subscriptions", "requests", requests)

	return
}

func (r *AdoptionReconciler) mapToClusterServiceVersions(obj handler.MapObject) (requests []reconcile.Request) {
	if obj.Meta == nil {
		return
	}

	// Get all owner CSV from owner labels if cluster scoped
	if obj.Meta.GetNamespace() == metav1.NamespaceAll {
		name, ns, ok := ownerutil.GetOwnerByKindLabel(obj.Meta, operatorsv1alpha1.ClusterServiceVersionKind)
		if ok {
			nsn := types.NamespacedName{Namespace: ns, Name: name}
			requests = append(requests, reconcile.Request{NamespacedName: nsn})
		}

		return
	}

	// Get all owner CSVs from OwnerReferences
	owners := ownerutil.GetOwnersByKind(obj.Meta, operatorsv1alpha1.ClusterServiceVersionKind)
	for _, owner := range owners {
		nsn := types.NamespacedName{Namespace: obj.Meta.GetNamespace(), Name: owner.Name}
		requests = append(requests, reconcile.Request{NamespacedName: nsn})
	}

	// TODO(njhale): Requeue CSVs on CRD changes

	return
}

func flatten(objs []runtime.Object) (flattened []runtime.Object) {
	for _, obj := range objs {
		if nested, err := meta.ExtractList(obj); err == nil {
			flattened = append(flattened, flatten(nested)...)
			continue
		}

		flattened = append(flattened, obj)
	}

	return
}
