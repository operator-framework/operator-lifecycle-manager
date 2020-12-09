package operators

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// OperatorConditionGeneratorReconciler reconciles a ClusterServiceVersion object and creates an OperatorCondition.
type OperatorConditionGeneratorReconciler struct {
	Client client.Client
	log    logr.Logger
}

// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorconditions,verbs=get;list;update;patch;delete
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorconditions/status,verbs=update;patch

// SetupWithManager adds the OperatorCondition Reconciler reconciler to the given controller manager.
func (r *OperatorConditionGeneratorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	handler := &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &operatorsv1alpha1.ClusterServiceVersion{},
	}
	p := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if _, ok := e.Meta.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if _, ok := e.Meta.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if _, ok := e.MetaOld.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			if _, ok := e.Meta.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.ClusterServiceVersion{}, builder.WithPredicates(p)).
		Watches(&source.Kind{Type: &operatorsv1.OperatorCondition{}}, handler).
		Complete(r)
}

// NewOperatorConditionGeneratorReconciler constructs and returns an OperatorConditionGeneratorReconciler.
// As a side effect, the given scheme has operator discovery types added to it.
func NewOperatorConditionGeneratorReconciler(cli client.Client, log logr.Logger, scheme *runtime.Scheme) (*OperatorConditionGeneratorReconciler, error) {
	// Add watched types to scheme.
	if err := AddToScheme(scheme); err != nil {
		return nil, err
	}

	return &OperatorConditionGeneratorReconciler{
		Client: cli,
		log:    log,
	}, nil
}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &OperatorConditionGeneratorReconciler{}

func (r *OperatorConditionGeneratorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)

	in := &operatorsv1alpha1.ClusterServiceVersion{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, in)
	if err != nil {
		log.V(1).Error(err, "Unable to find ClusterServiceVersion")
		return ctrl.Result{}, err
	}

	operatorCondition := &operatorsv1.OperatorCondition{
		ObjectMeta: metav1.ObjectMeta{
			// For now, only generate an OperatorCondition with the same name as the csv.
			Name:      in.GetName(),
			Namespace: in.GetNamespace(),
		},
		Spec: operatorsv1.OperatorConditionSpec{
			ServiceAccounts: getServiceAccountNames(*in),
			Deployments:     getDeploymentNames(*in),
		},
	}
	ownerutil.AddOwner(operatorCondition, in, false, true)

	err = r.ensureOperatorCondition(*operatorCondition)
	if err != nil {
		log.V(1).Error(err, "Error ensuring  ClusterServiceVersion")
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

func getServiceAccountNames(csv operatorsv1alpha1.ClusterServiceVersion) []string {
	result := []string{}
	for _, clusterPermissions := range csv.Spec.InstallStrategy.StrategySpec.ClusterPermissions {
		if clusterPermissions.ServiceAccountName != "" {
			result = append(result, clusterPermissions.ServiceAccountName)
		}
	}

	for _, permissions := range csv.Spec.InstallStrategy.StrategySpec.Permissions {
		if permissions.ServiceAccountName != "" {
			result = append(result, permissions.ServiceAccountName)
		}
	}

	if len(result) == 0 {
		result = []string{"default"}
	}

	return result
}

func getDeploymentNames(csv operatorsv1alpha1.ClusterServiceVersion) []string {
	result := []string{}
	for _, deploymentSpec := range csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
		if deploymentSpec.Name != "" {
			result = append(result, deploymentSpec.Name)
		}
	}

	return result
}

func (r *OperatorConditionGeneratorReconciler) ensureOperatorCondition(operatorCondition operatorsv1.OperatorCondition) error {
	existingOperatorCondition := &operatorsv1.OperatorCondition{}
	err := r.Client.Get(context.TODO(), client.ObjectKey{Name: operatorCondition.GetName(), Namespace: operatorCondition.GetNamespace()}, existingOperatorCondition)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return err
		}
		return r.Client.Create(context.TODO(), &operatorCondition)
	}

	if reflect.DeepEqual(operatorCondition.OwnerReferences, existingOperatorCondition.OwnerReferences) &&
		reflect.DeepEqual(operatorCondition.Spec.Deployments, existingOperatorCondition.Spec.Deployments) &&
		reflect.DeepEqual(operatorCondition.Spec.ServiceAccounts, existingOperatorCondition.Spec.ServiceAccounts) {
		r.log.V(5).Info("Existing OperatorCondition does not need to be updated")
		return nil
	}
	r.log.V(5).Info("Existing OperatorCondition needs to be updated")
	existingOperatorCondition.OwnerReferences = operatorCondition.OwnerReferences
	existingOperatorCondition.Spec.Deployments = operatorCondition.Spec.Deployments
	existingOperatorCondition.Spec.ServiceAccounts = operatorCondition.Spec.ServiceAccounts
	return r.Client.Update(context.TODO(), existingOperatorCondition)
}
