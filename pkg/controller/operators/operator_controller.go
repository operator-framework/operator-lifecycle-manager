package operators

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
		Watches(&appsv1.Deployment{}, enqueueOperator).
		Watches(&corev1.Namespace{}, enqueueOperator).
		Watches(&apiextensionsv1.CustomResourceDefinition{}, enqueueOperator).
		Watches(&apiregistrationv1.APIService{}, enqueueOperator).
		Watches(&operatorsv1alpha1.Subscription{}, enqueueOperator).
		Watches(&operatorsv1alpha1.InstallPlan{}, enqueueOperator).
		Watches(&operatorsv1alpha1.ClusterServiceVersion{}, enqueueOperator).
		Watches(&operatorsv2.OperatorCondition{}, enqueueOperator).
		// Metadata is sufficient to build component refs for
		// GVKs that don't have a .status.conditions field.
		Watches(&corev1.ServiceAccount{}, enqueueOperator, builder.OnlyMetadata).
		Watches(&corev1.Secret{}, enqueueOperator, builder.OnlyMetadata).
		Watches(&corev1.ConfigMap{}, enqueueOperator, builder.OnlyMetadata).
		Watches(&rbacv1.Role{}, enqueueOperator, builder.OnlyMetadata).
		Watches(&rbacv1.RoleBinding{}, enqueueOperator, builder.OnlyMetadata).
		Watches(&rbacv1.ClusterRole{}, enqueueOperator, builder.OnlyMetadata).
		Watches(&rbacv1.ClusterRoleBinding{}, enqueueOperator, builder.OnlyMetadata).
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

		log:     log,
		factory: factory,
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
		if !equality.Semantic.DeepEqual(in.Status, operator.Operator.Status) {
			if err := r.Status().Update(ctx, operator.Operator); err != nil {
				log.Error(err, "Could not update Operator status")
				return ctrl.Result{Requeue: true}, nil
			}
		}
	}

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

func (r *OperatorReconciler) mapComponentRequests(_ context.Context, obj client.Object) []reconcile.Request {
	var requests []reconcile.Request
	if obj == nil {
		return requests
	}

	labels := decorators.OperatorNames(obj.GetLabels())
	for _, name := range labels {
		requests = append(requests, reconcile.Request{NamespacedName: name})
	}

	return requests
}
