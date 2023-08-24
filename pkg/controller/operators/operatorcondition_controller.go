package operators

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv2 "github.com/operator-framework/api/pkg/operators/v2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

const (
	OperatorConditionEnvVarKey = "OPERATOR_CONDITION_NAME"
)

// OperatorConditionReconciler reconciles an OperatorCondition object.
type OperatorConditionReconciler struct {
	client.Client
	log logr.Logger
}

// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorconditions,verbs=get;list;update;patch;delete
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorconditions/status,verbs=update;patch

// SetupWithManager adds the OperatorCondition Reconciler reconciler to the given controller manager.
func (r *OperatorConditionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	deploymentHandler := handler.EnqueueRequestsFromMapFunc(r.mapToOperatorCondition)
	handler := handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &operatorsv2.OperatorCondition{}, handler.OnlyControllerOwner())

	return ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv2.OperatorCondition{}).
		Watches(&rbacv1.Role{}, handler).
		Watches(&rbacv1.RoleBinding{}, handler).
		Watches(&appsv1.Deployment{}, deploymentHandler).
		Complete(r)
}

func (r *OperatorConditionReconciler) mapToOperatorCondition(_ context.Context, obj client.Object) (requests []reconcile.Request) {
	if obj == nil {
		return nil
	}

	owner := ownerutil.GetOwnerByKind(obj, operatorsv1alpha1.ClusterServiceVersionKind)
	if owner == nil {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      owner.Name,
				Namespace: obj.GetNamespace(),
			},
		},
	}
}

// NewOperatorConditionReconciler constructs and returns an OperatorConditionReconciler.
// As a side effect, the given scheme has operator discovery types added to it.
func NewOperatorConditionReconciler(cli client.Client, log logr.Logger, scheme *runtime.Scheme) (*OperatorConditionReconciler, error) {
	// Add watched types to scheme.
	if err := AddToScheme(scheme); err != nil {
		return nil, err
	}

	return &OperatorConditionReconciler{
		Client: cli,
		log:    log,
	}, nil
}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &OperatorConditionReconciler{}

func (r *OperatorConditionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req).V(1)
	log.Info("reconciling")
	metrics.EmitOperatorConditionReconcile(req.Namespace, req.Name)

	operatorCondition := &operatorsv2.OperatorCondition{}
	if err := r.Client.Get(ctx, req.NamespacedName, operatorCondition); err != nil {
		log.Info("Unable to find OperatorCondition")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.ensureOperatorConditionRole(operatorCondition); err != nil {
		log.Info("Error ensuring OperatorCondition Role")
		return ctrl.Result{}, err
	}

	if err := r.ensureOperatorConditionRoleBinding(operatorCondition); err != nil {
		log.Info("Error ensuring OperatorCondition RoleBinding")
		return ctrl.Result{}, err
	}

	if err := r.ensureDeploymentEnvVars(operatorCondition); err != nil {
		log.Info("Error ensuring OperatorCondition Deployment EnvVars")
		return ctrl.Result{}, err
	}

	if err := r.syncOperatorConditionStatus(operatorCondition); err != nil {
		log.Info("Error syncing OperatorCondition Status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *OperatorConditionReconciler) ensureOperatorConditionRole(operatorCondition *operatorsv2.OperatorCondition) error {
	r.log.V(4).Info("Ensuring the Role for the OperatorCondition")
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorCondition.GetName(),
			Namespace: operatorCondition.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get", "update", "patch"},
				APIGroups:     []string{"operators.coreos.com"},
				Resources:     []string{"operatorconditions"},
				ResourceNames: []string{operatorCondition.GetName()},
			},
		},
	}
	ownerutil.AddOwner(role, operatorCondition, false, true)

	existingRole := &rbacv1.Role{}
	err := r.Client.Get(context.TODO(), client.ObjectKey{Name: role.GetName(), Namespace: role.GetNamespace()}, existingRole)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return r.Client.Create(context.TODO(), role)
	}

	if ownerutil.IsOwnedBy(existingRole, operatorCondition) &&
		reflect.DeepEqual(role.Rules, existingRole.Rules) {
		r.log.V(5).Info("Existing Role does not need to be updated")
		return nil
	}
	r.log.V(5).Info("Existing Role needs to be updated")

	existingRole.OwnerReferences = role.OwnerReferences
	existingRole.Rules = role.Rules
	return r.Client.Update(context.TODO(), existingRole)
}

func (r *OperatorConditionReconciler) ensureOperatorConditionRoleBinding(operatorCondition *operatorsv2.OperatorCondition) error {
	r.log.V(4).Info("Ensuring the RoleBinding for the OperatorCondition")
	subjects := []rbacv1.Subject{}
	for _, serviceAccount := range operatorCondition.Spec.ServiceAccounts {
		subjects = append(subjects, rbacv1.Subject{
			Kind:     rbacv1.ServiceAccountKind,
			Name:     serviceAccount,
			APIGroup: "",
		})
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorCondition.GetName(),
			Namespace: operatorCondition.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
			},
		},
		Subjects: subjects,
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			Name:     operatorCondition.GetName(),
			APIGroup: "rbac.authorization.k8s.io",
		},
	}
	ownerutil.AddOwner(roleBinding, operatorCondition, false, true)

	existingRoleBinding := &rbacv1.RoleBinding{}
	err := r.Client.Get(context.TODO(), client.ObjectKey{Name: roleBinding.GetName(), Namespace: roleBinding.GetNamespace()}, existingRoleBinding)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return r.Client.Create(context.TODO(), roleBinding)
	}

	if ownerutil.IsOwnedBy(existingRoleBinding, operatorCondition) &&
		existingRoleBinding.RoleRef == roleBinding.RoleRef &&
		reflect.DeepEqual(roleBinding.Subjects, existingRoleBinding.Subjects) {
		r.log.V(5).Info("Existing RoleBinding does not need to be updated")
		return nil
	}

	r.log.V(5).Info("Existing RoleBinding needs to be updated")
	existingRoleBinding.OwnerReferences = roleBinding.OwnerReferences
	existingRoleBinding.Subjects = roleBinding.Subjects
	existingRoleBinding.RoleRef = roleBinding.RoleRef

	return r.Client.Update(context.TODO(), existingRoleBinding)
}

func (r *OperatorConditionReconciler) ensureDeploymentEnvVars(operatorCondition *operatorsv2.OperatorCondition) error {
	r.log.V(4).Info("Ensuring that deployments have the OPERATOR_CONDITION_NAME variable")
	for _, deploymentName := range operatorCondition.Spec.Deployments {
		deployment := &appsv1.Deployment{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: deploymentName, Namespace: operatorCondition.GetNamespace()}, deployment)
		if err != nil {
			return err
		}

		// Check the deployment is owned by a CSV with the same name as the OperatorCondition.
		deploymentOwner := ownerutil.GetOwnerByKind(deployment, operatorsv1alpha1.ClusterServiceVersionKind)
		if deploymentOwner == nil || deploymentOwner.Name != operatorCondition.GetName() {
			continue
		}

		deploymentNeedsUpdate := false
		for i := range deployment.Spec.Template.Spec.Containers {
			envVars, containedEnvVar := ensureEnvVarIsPresent(deployment.Spec.Template.Spec.Containers[i].Env, corev1.EnvVar{Name: OperatorConditionEnvVarKey, Value: operatorCondition.GetName()})
			if !containedEnvVar {
				deploymentNeedsUpdate = true
			}
			deployment.Spec.Template.Spec.Containers[i].Env = envVars
		}
		if !deploymentNeedsUpdate {
			r.log.V(5).Info("Existing deployment does not need to be updated")
			continue
		}
		r.log.V(5).Info("Existing deployment needs to be updated")
		err = r.Client.Update(context.TODO(), deployment)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *OperatorConditionReconciler) syncOperatorConditionStatus(operatorCondition *operatorsv2.OperatorCondition) error {
	r.log.V(4).Info("Sync operatorcondition status")
	currentGen := operatorCondition.ObjectMeta.GetGeneration()
	changed := false
	for _, cond := range operatorCondition.Spec.Conditions {
		if c := meta.FindStatusCondition(operatorCondition.Status.Conditions, cond.Type); c != nil {
			if cond.Status == c.Status && c.ObservedGeneration == currentGen {
				continue
			}
		}
		cond.ObservedGeneration = currentGen
		meta.SetStatusCondition(&operatorCondition.Status.Conditions, cond)
		changed = true
	}

	if changed {
		return r.Client.Status().Update(context.TODO(), operatorCondition)
	}
	return nil
}

func ensureEnvVarIsPresent(envVars []corev1.EnvVar, envVar corev1.EnvVar) ([]corev1.EnvVar, bool) {
	for i, each := range envVars {
		if each.Name == envVar.Name {
			if each.Value == envVar.Value {
				return envVars, true
			}
			envVars[i].Value = envVar.Value
			return envVars, false
		}
	}
	return append(envVars, envVar), false
}
