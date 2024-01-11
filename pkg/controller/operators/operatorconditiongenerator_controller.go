package operators

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
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
	handler := handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &operatorsv1alpha1.ClusterServiceVersion{}, handler.OnlyControllerOwner())
	p := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			if _, ok := e.Object.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if _, ok := e.Object.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if _, ok := e.ObjectOld.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			if _, ok := e.Object.GetLabels()[operatorsv1alpha1.CopiedLabelKey]; ok {
				return false
			}
			return true
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.ClusterServiceVersion{}, builder.WithPredicates(p)).
		Watches(&operatorsv2.OperatorCondition{}, handler).
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

func (r *OperatorConditionGeneratorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req).V(1)
	metrics.EmitOperatorConditionGeneratorReconcile(req.Namespace, req.Name)

	in := &operatorsv1alpha1.ClusterServiceVersion{}
	if err := r.Client.Get(ctx, req.NamespacedName, in); err != nil {
		log.Info("Unable to find ClusterServiceVersion")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err, ok := r.processFinalizer(ctx, in); !ok {
		return ctrl.Result{}, err
	}

	operatorCondition := &operatorsv2.OperatorCondition{
		ObjectMeta: metav1.ObjectMeta{
			// For now, only generate an OperatorCondition with the same name as the csv.
			Name:      in.GetName(),
			Namespace: in.GetNamespace(),
		},
		Spec: operatorsv2.OperatorConditionSpec{
			ServiceAccounts: getServiceAccountNames(*in),
			Deployments:     getDeploymentNames(*in),
		},
	}
	ownerutil.AddOwner(operatorCondition, in, false, true)

	if err := r.ensureOperatorCondition(*operatorCondition); err != nil {
		log.Info("Error ensuring  OperatorCondition")
		return ctrl.Result{}, err
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

func (r *OperatorConditionGeneratorReconciler) ensureOperatorCondition(operatorCondition operatorsv2.OperatorCondition) error {
	existingOperatorCondition := &operatorsv2.OperatorCondition{}
	err := r.Client.Get(context.TODO(), client.ObjectKey{Name: operatorCondition.GetName(), Namespace: operatorCondition.GetNamespace()}, existingOperatorCondition)
	if err != nil {
		if !apierrors.IsNotFound(err) {
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

// Return values, err, ok; ok == true: continue Reconcile, ok == false: exit Reconcile
func (r *OperatorConditionGeneratorReconciler) processFinalizer(ctx context.Context, csv *operatorsv1alpha1.ClusterServiceVersion) (error, bool) {
	myFinalizerName := "operators.coreos.com/csv-cleanup"
	log := r.log.WithValues("name", csv.GetName()).WithValues("namespace", csv.GetNamespace())

	if csv.ObjectMeta.DeletionTimestamp.IsZero() {
		// CSV is not being deleted, add finalizer if not present
		if !controllerutil.ContainsFinalizer(csv, myFinalizerName) {
			patch := csv.DeepCopy()
			controllerutil.AddFinalizer(patch, myFinalizerName)
			if err := r.Client.Patch(ctx, patch, client.MergeFrom(csv)); err != nil {
				log.Error(err, "Adding finalizer")
				return err, false
			}
		}
		return nil, true
	}

	if !controllerutil.ContainsFinalizer(csv, myFinalizerName) {
		// Finalizer has been removed; stop reconciliation as the CSV is being deleted
		return nil, false
	}

	// CSV is being deleted and the finalizer still present; do any clean up
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	listOptions := client.ListOptions{
		LabelSelector: ownerSelector,
	}
	deleteOptions := client.DeleteAllOfOptions{
		ListOptions: listOptions,
	}
	// Look for resources owned by this CSV, and delete them.
	log.WithValues("selector", ownerSelector).Info("Cleaning up resources after CSV deletion")
	var errs []error

	err := r.Client.DeleteAllOf(ctx, &rbacv1.ClusterRoleBinding{}, &deleteOptions)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Deleting ClusterRoleBindings on CSV delete")
		errs = append(errs, err)
	}

	err = r.Client.DeleteAllOf(ctx, &rbacv1.ClusterRole{}, &deleteOptions)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Deleting ClusterRoles on CSV delete")
		errs = append(errs, err)
	}

	err = r.Client.DeleteAllOf(ctx, &admissionregistrationv1.MutatingWebhookConfiguration{}, &deleteOptions)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Deleting MutatingWebhookConfigurations on CSV delete")
		errs = append(errs, err)
	}

	err = r.Client.DeleteAllOf(ctx, &admissionregistrationv1.ValidatingWebhookConfiguration{}, &deleteOptions)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Deleting ValidatingWebhookConfigurations on CSV delete")
		errs = append(errs, err)
	}

	// Make sure things are deleted
	crbList := &rbacv1.ClusterRoleBindingList{}
	err = r.Client.List(ctx, crbList, &listOptions)
	if err != nil {
		errs = append(errs, err)
	} else if len(crbList.Items) != 0 {
		errs = append(errs, fmt.Errorf("waiting for ClusterRoleBindings to delete"))
	}

	crList := &rbacv1.ClusterRoleList{}
	err = r.Client.List(ctx, crList, &listOptions)
	if err != nil {
		errs = append(errs, err)
	} else if len(crList.Items) != 0 {
		errs = append(errs, fmt.Errorf("waiting for ClusterRoles to delete"))
	}

	mwcList := &admissionregistrationv1.MutatingWebhookConfigurationList{}
	err = r.Client.List(ctx, mwcList, &listOptions)
	if err != nil {
		errs = append(errs, err)
	} else if len(mwcList.Items) != 0 {
		errs = append(errs, fmt.Errorf("waiting for MutatingWebhookConfigurations to delete"))
	}

	vwcList := &admissionregistrationv1.ValidatingWebhookConfigurationList{}
	err = r.Client.List(ctx, vwcList, &listOptions)
	if err != nil {
		errs = append(errs, err)
	} else if len(vwcList.Items) != 0 {
		errs = append(errs, fmt.Errorf("waiting for ValidatingWebhookConfigurations to delete"))
	}

	// Return any errors
	if err := utilerrors.NewAggregate(errs); err != nil {
		return err, false
	}

	// If no errors, remove our finalizer from the CSV and update
	patch := csv.DeepCopy()
	controllerutil.RemoveFinalizer(patch, myFinalizerName)
	if err := r.Client.Patch(ctx, patch, client.MergeFrom(csv)); err != nil {
		log.Error(err, "Removing finalizer")
		return err, false
	}

	// Stop reconciliation as the csv is being deleted
	return nil, false
}
