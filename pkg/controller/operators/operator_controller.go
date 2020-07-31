package operators

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
)

var (
	localSchemeBuilder = runtime.NewSchemeBuilder(
		kscheme.AddToScheme,
		apiextensionsv1.AddToScheme,
		apiregistrationv1.AddToScheme,
		operatorsv1alpha1.AddToScheme,
		operatorsv1.AddToScheme,
		operatorsv1.AddToScheme,
	)

	// AddToScheme adds all types necessary for the controller to operate.
	AddToScheme = localSchemeBuilder.AddToScheme
)

// OperatorReconciler reconciles a Operator object.
type OperatorReconciler struct {
	client.Client

	log     logr.Logger
	mu      sync.RWMutex
	factory decorators.OperatorFactory

	// operators contains the names of Operators the OperatorReconciler has observed exist.
	operators map[types.NamespacedName]struct{}
}

// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators/status,verbs=update;patch
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager adds the operator reconciler to the given controller manager.
func (r *OperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Trigger operator events from the events of their compoenents.
	enqueueOperator := &handler.EnqueueRequestsFromMapFunc{
		ToRequests: handler.ToRequestsFunc(r.mapComponentRequests),
	}

	// Note: If we want to support resources composed of custom resources, we need to figure out how
	// to dynamically add resource types to watch.
	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1.Operator{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.ServiceAccount{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.Secret{}}, enqueueOperator).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.Role{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.RoleBinding{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.ClusterRole{}}, enqueueOperator).
		Watches(&source.Kind{Type: &rbacv1.ClusterRoleBinding{}}, enqueueOperator).
		Watches(&source.Kind{Type: &apiextensionsv1.CustomResourceDefinition{}}, enqueueOperator).
		Watches(&source.Kind{Type: &apiregistrationv1.APIService{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.Subscription{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.InstallPlan{}}, enqueueOperator).
		Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueueOperator).
		// TODO(njhale): Add WebhookConfigurations and ConfigMaps
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

		log:       log,
		factory:   factory,
		operators: map[types.NamespacedName]struct{}{},
	}, nil
}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &OperatorReconciler{}

func (r *OperatorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling operator")

	// Fetch the Operator from the cache
	ctx := context.TODO()
	in := &operatorsv1.Operator{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find Operator")
			r.unobserve(req.NamespacedName)
			// TODO(njhale): Recreate operator if we can find any components.
		} else {
			log.Error(err, "Error finding Operator")
		}

		return reconcile.Result{}, nil
	}
	r.observe(req.NamespacedName)

	// Wrap with convenience decorator
	operator, err := r.factory.NewOperator(in)
	if err != nil {
		log.Error(err, "Could not wrap Operator with convenience decorator")
		return reconcile.Result{}, nil
	}

	if err = r.updateComponents(ctx, operator); err != nil {
		log.Error(err, "Could not update components")
		return reconcile.Result{}, nil

	}

	if err := r.Status().Update(ctx, operator.Operator); err != nil {
		log.Error(err, "Could not update Operator status")
		return ctrl.Result{}, err
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
	componentLists := []runtime.Object{
		&appsv1.DeploymentList{},
		&corev1.NamespaceList{},
		&corev1.ServiceAccountList{},
		&corev1.SecretList{},
		&corev1.ConfigMapList{},
		&rbacv1.RoleList{},
		&rbacv1.RoleBindingList{},
		&rbacv1.ClusterRoleList{},
		&rbacv1.ClusterRoleBindingList{},
		&apiextensionsv1.CustomResourceDefinitionList{},
		&apiregistrationv1.APIServiceList{},
		&operatorsv1alpha1.SubscriptionList{},
		&operatorsv1alpha1.InstallPlanList{},
		&operatorsv1alpha1.ClusterServiceVersionList{},
	}

	opt := client.MatchingLabelsSelector{Selector: selector}
	for _, list := range componentLists {
		if err := r.List(ctx, list, opt); err != nil {
			return nil, err
		}
	}

	return componentLists, nil
}

func (r *OperatorReconciler) observed(name types.NamespacedName) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.operators[name]
	return ok
}

func (r *OperatorReconciler) observe(name types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.operators[name] = struct{}{}
}

func (r *OperatorReconciler) unobserve(name types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.operators, name)
}

func (r *OperatorReconciler) mapComponentRequests(obj handler.MapObject) (requests []reconcile.Request) {
	if obj.Meta == nil {
		return
	}

	for _, name := range decorators.OperatorNames(obj.Meta.GetLabels()) {
		// Only enqueue if we can find the operator in our cache
		if r.observed(name) {
			requests = append(requests, reconcile.Request{NamespacedName: name})
			continue
		}

		// Otherwise, best-effort generate a new operator
		// TODO(njhale): Implement verification that the operator-discovery admission webhook accepted this label (JWT or maybe sign a set of fields?)
		operator := &operatorsv1.Operator{}
		operator.SetName(name.Name)
		if err := r.Create(context.Background(), operator); err != nil && !apierrors.IsAlreadyExists(err) {
			r.log.Error(err, "couldn't generate operator", "operator", name, "component", obj.Meta.GetSelfLink())
		}
	}

	return
}
