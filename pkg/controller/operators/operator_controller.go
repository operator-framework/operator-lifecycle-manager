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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

var (
	localSchemeBuilder = runtime.NewSchemeBuilder(
		kscheme.AddToScheme,
		apiextensionsv1.AddToScheme,
		apiregistrationv1.AddToScheme,
		operatorsv1alpha1.AddToScheme,
		operatorsv1.AddToScheme,
		operatorsv2.AddToScheme,
	)

	// AddToScheme adds all types necessary for the controller to operate.
	AddToScheme = localSchemeBuilder.AddToScheme
)

// OperatorReconciler reconciles a Operator object.
type OperatorReconciler struct {
	client.Client

	log     logr.Logger
	factory decorators.OperatorFactory

	// last observed resourceVersion for known Operators
	lastResourceVersion map[types.NamespacedName]string
	mu                  sync.RWMutex
}

// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators/status,verbs=update;patch
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager adds the operator reconciler to the given controller manager.
func (r *OperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Trigger operator events from the events of their compoenents.
	enqueueOperator := handler.EnqueueRequestsFromMapFunc(r.mapComponentRequests)
	// Note: If we want to support resources composed of custom resources, we need to figure out how
	// to dynamically add resource types to watch.
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1.Operator{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, enqueueOperator).
		Watches(&source.Kind{Type: &apiextensionsv1.CustomResourceDefinition{}}, enqueueOperator).
		Watches(&source.Kind{Type: &apiregistrationv1.APIService{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.Subscription{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.InstallPlan{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv2.OperatorCondition{}}, enqueueOperator).
		// Metadata is sufficient to build component refs for
		// GVKs that don't have a .status.conditions field.
		Watches(&source.Kind{Type: &corev1.ServiceAccount{}}, enqueueOperator, builder.OnlyMetadata).
		Watches(&source.Kind{Type: &corev1.Secret{}}, enqueueOperator, builder.OnlyMetadata).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, enqueueOperator, builder.OnlyMetadata).
		Watches(&source.Kind{Type: &rbacv1.Role{}}, enqueueOperator, builder.OnlyMetadata).
		Watches(&source.Kind{Type: &rbacv1.RoleBinding{}}, enqueueOperator, builder.OnlyMetadata).
		Watches(&source.Kind{Type: &rbacv1.ClusterRole{}}, enqueueOperator, builder.OnlyMetadata).
		Watches(&source.Kind{Type: &rbacv1.ClusterRoleBinding{}}, enqueueOperator, builder.OnlyMetadata).
		// TODO(njhale): Add WebhookConfigurations
		Complete(r)
}

// NewOperatorReconciler constructs and returns an OperatorReconciler.
// As a side effect, the given scheme has operator discovery types added to it.
func NewOperatorReconciler(cli client.Client, log logr.Logger, scheme *runtime.Scheme) (*OperatorReconciler, error) {
	// Add watched types to scheme.
	if err := AddToScheme(scheme); err != nil {
		return nil, err
	}

	factory, err := decorators.NewSchemedOperatorFactory(scheme)
	if err != nil {
		return nil, err
	}

	return &OperatorReconciler{
		Client: cli,

		log:                 log,
		factory:             factory,
		lastResourceVersion: map[types.NamespacedName]string{},
	}, nil
}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &OperatorReconciler{}

func (r *OperatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling operator")
	metrics.EmitOperatorReconcile(req.Namespace, req.Name)

	// Get the Operator
	create := false
	name := req.NamespacedName.Name
	in := &operatorsv1.Operator{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			// If the Operator instance is not found, we're likely reconciling because
			// of a DELETE event. Only recreate the Operator if any of its components
			// still exist.
			if exists, err := r.hasExistingComponents(ctx, name); err != nil || !exists {
				return reconcile.Result{}, err
			}
			create = true
			in.SetName(name)
		} else {
			log.Error(err, "Error requesting Operator")
			return reconcile.Result{Requeue: true}, nil
		}
	}

	rv, ok := r.getLastResourceVersion(req.NamespacedName)
	if !create && ok && rv == in.ResourceVersion {
		log.V(1).Info("Operator is already up-to-date")
		return reconcile.Result{}, nil
	}

	// Set the cached resource version to 0 so we can handle
	// the race with requests enqueuing via mapComponentRequests
	r.setLastResourceVersion(req.NamespacedName, "0")

	// Wrap with convenience decorator
	operator, err := r.factory.NewOperator(in)
	if err != nil {
		log.Error(err, "Could not wrap Operator with convenience decorator")
		return reconcile.Result{Requeue: true}, nil
	}

	if err = r.updateComponents(ctx, operator); err != nil {
		log.Error(err, "Could not update components")
		return reconcile.Result{Requeue: true}, nil
	}

	if create {
		if err := r.Create(context.Background(), operator.Operator); err != nil && !apierrors.IsAlreadyExists(err) {
			r.log.Error(err, "Could not create Operator", "operator", name)
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if err := r.Status().Update(ctx, operator.Operator); err != nil {
			log.Error(err, "Could not update Operator status")
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Only set the resource version if it already exists.
	// If it does not exist, it means mapComponentRequests was called
	// while we were reconciling and we need to reconcile again
	r.setLastResourceVersionIfExists(req.NamespacedName, operator.GetResourceVersion())

	return ctrl.Result{}, nil
}

func (r *OperatorReconciler) updateComponents(ctx context.Context, operator *decorators.Operator) error {
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

func (r *OperatorReconciler) listComponents(ctx context.Context, selector labels.Selector) ([]runtime.Object, error) {
	// Note: We need to figure out how to dynamically add new list types here (or some equivalent) in
	// order to support operators composed of custom resources.
	componentLists := componentLists()

	opt := client.MatchingLabelsSelector{Selector: selector}
	for _, list := range componentLists {
		cList, ok := list.(client.ObjectList)
		if !ok {
			return nil, fmt.Errorf("unable to typecast runtime.Object to client.ObjectList")
		}
		if err := r.List(ctx, cList, opt); err != nil {
			return nil, err
		}
	}

	return componentLists, nil
}

func (r *OperatorReconciler) hasExistingComponents(ctx context.Context, name string) (bool, error) {
	op := &operatorsv1.Operator{}
	op.SetName(name)
	operator := decorators.Operator{Operator: op}

	selector, err := operator.ComponentSelector()
	if err != nil {
		return false, err
	}

	components, err := r.listComponents(ctx, selector)
	if err != nil {
		return false, err
	}

	for _, list := range components {
		items, err := meta.ExtractList(list)
		if err != nil {
			return false, fmt.Errorf("unable to extract list from runtime.Object")
		}
		if len(items) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (r *OperatorReconciler) getLastResourceVersion(name types.NamespacedName) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rv, ok := r.lastResourceVersion[name]
	return rv, ok
}

func (r *OperatorReconciler) setLastResourceVersion(name types.NamespacedName, rv string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastResourceVersion[name] = rv
}

func (r *OperatorReconciler) setLastResourceVersionIfExists(name types.NamespacedName, rv string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.lastResourceVersion[name]; ok {
		r.lastResourceVersion[name] = rv
	}
}

func (r *OperatorReconciler) unsetLastResourceVersion(name types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.lastResourceVersion, name)
}

func (r *OperatorReconciler) mapComponentRequests(obj client.Object) []reconcile.Request {
	var requests []reconcile.Request
	if obj == nil {
		return requests
	}

	labels := decorators.OperatorNames(obj.GetLabels())
	for _, name := range labels {
		// unset the last recorded resource version so the Operator will reconcile
		r.unsetLastResourceVersion(name)
		requests = append(requests, reconcile.Request{NamespacedName: name})
	}

	return requests
}
