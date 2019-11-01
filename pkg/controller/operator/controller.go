package operator

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	operatorsv2alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v2alpha1"
)

var (
	localSchemeBuilder = runtime.NewSchemeBuilder(
		kscheme.AddToScheme,
		apiextensionsv1beta1.AddToScheme,
		apiregistrationv1.AddToScheme,
		operatorsv1alpha1.AddToScheme,
		operatorsv1.AddToScheme,
		operatorsv2alpha1.AddToScheme,
	)
	AddToScheme = localSchemeBuilder.AddToScheme
)

// AddController adds the operator controller's reconcilers to the given manager.
func AddController(mgr ctrl.Manager, log logr.Logger) error {
	if err := AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	rec := newReconciler(mgr.GetClient(), log.WithName("operator-controller"))
	enqueueOperator := &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(rec.mapComponentRequests),
	}

	// Note: If we want to support resources composed of custom resources, we need to figure out how
	// to dynamically add resource types to watch.
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv2alpha1.Operator{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.ServiceAccount{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.Secret{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.Role{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.RoleBinding{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.ClusterRole{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.ClusterRoleBinding{}}, enqueueOperator).
		Watches(&source.Kind{Type: &apiextensionsv1beta1.CustomResourceDefinition{}}, enqueueOperator). // TODO: Bump to apiextensionsv1.CustomResourceDefinition
		Watches(&source.Kind{Type: &apiregistrationv1.APIService{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.Subscription{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.InstallPlan{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueueOperator).
		Complete(rec)
}

type reconciler struct {
	client.Client

	log logr.Logger
	mux sync.RWMutex
	// operators contains the names of Operators the reconciler has observed exist.
	operators map[types.NamespacedName]struct{}
}

func newReconciler(cli client.Client, log logr.Logger) *reconciler {
	return &reconciler{
		Client: cli,

		log:       log,
		operators: map[types.NamespacedName]struct{}{},
	}
}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &reconciler{}

func (r *reconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling operator")

	// Fetch the Operator from the cache
	ctx := context.TODO()
	in := &operatorsv2alpha1.Operator{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find Operator")
			r.unobserve(req.NamespacedName)
		} else {
			log.Error(err, "Error finding Operator")
		}

		return reconcile.Result{}, nil
	}
	r.observe(req.NamespacedName)

	// Wrap with convenience decorator
	operator, err := NewOperator(in)
	if err != nil {
		log.Error(err, "Could not wrap Operator with convenience decorator")
		return reconcile.Result{}, nil
	}

	if err = r.updateComponents(ctx, operator); err != nil {
		log.Error(err, "Could not update components")
		return reconcile.Result{}, nil

	}

	if err := r.Update(ctx, operator.Operator); err != nil {
		log.Error(err, "Could not update Operator status")
		return ctrl.Result{}, err
	}

	if err := r.Get(ctx, req.NamespacedName, operator.Operator); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *reconciler) updateComponents(ctx context.Context, operator *Operator) error {
	selector, err := operator.ComponentSelector()
	if err != nil {
		return err
	}

	components, err := r.listComponents(ctx, selector)
	if err != nil {
		return err
	}

	return operator.SetComponents(components...)
}

func (r *reconciler) listComponents(ctx context.Context, selector labels.Selector) ([]runtime.Object, error) {
	// Note: We need to figure out how to dynamically add new list types here (or some equivalent) in
	// order to support operators composed of custom resources.
	opt := client.MatchingLabelsSelector{Selector: selector}
	componentLists := []runtime.Object{
		&appsv1.DeploymentList{},
		&corev1.NamespaceList{},
		&corev1.ServiceAccountList{},
		&corev1.SecretList{},
		&rbacv1.RoleList{},
		&rbacv1.RoleBindingList{},
		&rbacv1.ClusterRoleList{},
		&rbacv1.ClusterRoleBindingList{},
		&apiextensionsv1beta1.CustomResourceDefinitionList{}, // TODO: Bump to apiextensionsv1.CustomResourceDefinitionList
		&apiregistrationv1.APIServiceList{},
		&operatorsv1alpha1.SubscriptionList{},
		&operatorsv1alpha1.InstallPlanList{},
		&operatorsv1alpha1.ClusterServiceVersionList{},
		// TODO: Add operatorsv2alpha1.OperatorList
	}

	for _, list := range componentLists {
		if err := r.List(ctx, list, opt); err != nil {
			return nil, err
		}
	}

	return componentLists, nil
}

func (r *reconciler) observed(name types.NamespacedName) bool {
	r.mux.RLock()
	defer r.mux.RUnlock()
	_, ok := r.operators[name]
	return ok
}

func (r *reconciler) observe(name types.NamespacedName) {
	r.mux.Lock()
	defer r.mux.Unlock()
	r.operators[name] = struct{}{}
}

func (r *reconciler) unobserve(name types.NamespacedName) {
	r.mux.Lock()
	defer r.mux.Unlock()
	delete(r.operators, name)
}

func (r *reconciler) mapComponentRequests(obj handler.MapObject) (requests []reconcile.Request) {
	if obj.Meta == nil {
		return
	}

	for _, name := range OperatorNames(obj.Meta.GetLabels()) {
		// Only enqueue if we can find the operator in our cache
		if !r.observed(name) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: name})
	}

	return
}
