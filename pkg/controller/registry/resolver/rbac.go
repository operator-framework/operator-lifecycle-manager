package resolver

import (
	"fmt"
	"hash/fnv"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const maxNameLength = 63

func generateName(base string, o interface{}) string {
	hasher := fnv.New32a()
	hashutil.DeepHashObject(hasher, o)
	hash := utilrand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
	if len(base)+len(hash) > maxNameLength {
		base = base[:maxNameLength - len(hash) - 1]
	}

	return fmt.Sprintf("%s-%s", base, hash)
}

type OperatorPermissions struct {
	ServiceAccount      *corev1.ServiceAccount
	Roles               []*rbacv1.Role
	RoleBindings        []*rbacv1.RoleBinding
	ClusterRoles        []*rbacv1.ClusterRole
	ClusterRoleBindings []*rbacv1.ClusterRoleBinding
}

func NewOperatorPermissions(serviceAccount *corev1.ServiceAccount) *OperatorPermissions {
	return &OperatorPermissions{
		ServiceAccount:      serviceAccount,
		Roles:               []*rbacv1.Role{},
		RoleBindings:        []*rbacv1.RoleBinding{},
		ClusterRoles:        []*rbacv1.ClusterRole{},
		ClusterRoleBindings: []*rbacv1.ClusterRoleBinding{},
	}
}

func (o *OperatorPermissions) AddRole(role *rbacv1.Role) {
	o.Roles = append(o.Roles, role)
}

func (o *OperatorPermissions) AddRoleBinding(roleBinding *rbacv1.RoleBinding) {
	o.RoleBindings = append(o.RoleBindings, roleBinding)
}

func (o *OperatorPermissions) AddClusterRole(clusterRole *rbacv1.ClusterRole) {
	o.ClusterRoles = append(o.ClusterRoles, clusterRole)
}

func (o *OperatorPermissions) AddClusterRoleBinding(clusterRoleBinding *rbacv1.ClusterRoleBinding) {
	o.ClusterRoleBindings = append(o.ClusterRoleBindings, clusterRoleBinding)
}

func RBACForClusterServiceVersion(csv *v1alpha1.ClusterServiceVersion) (map[string]*OperatorPermissions, error) {
	permissions := map[string]*OperatorPermissions{}

	// Use a StrategyResolver to get the strategy details
	strategyResolver := install.StrategyResolver{}
	strategy, err := strategyResolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return nil, err
	}

	// Assume the strategy is for a deployment
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return nil, fmt.Errorf("could not assert strategy implementation as deployment for CSV %s", csv.GetName())
	}

	// Resolve Permissions
	for _, permission := range strategyDetailsDeployment.Permissions {
		// Create ServiceAccount if necessary
		if _, ok := permissions[permission.ServiceAccountName]; !ok {
			serviceAccount := &corev1.ServiceAccount{}
			serviceAccount.SetNamespace(csv.GetNamespace())
			serviceAccount.SetName(permission.ServiceAccountName)
			ownerutil.AddNonBlockingOwner(serviceAccount, csv)

			permissions[permission.ServiceAccountName] = NewOperatorPermissions(serviceAccount)
		}

		// Create Role
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:            generateName(fmt.Sprintf("%s-%s", csv.GetName(), permission.ServiceAccountName), []interface{}{csv.GetName(), permission}),
				Namespace:       csv.GetNamespace(),
				OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(csv)},
				Labels:          ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind),
			},
			Rules: permission.Rules,
		}
		permissions[permission.ServiceAccountName].AddRole(role)

		// Create RoleBinding
		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:            role.GetName(),
				Namespace:       csv.GetNamespace(),
				OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(csv)},
				Labels:          ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind),
			},
			RoleRef: rbacv1.RoleRef{
				Kind:     "Role",
				Name:     role.GetName(),
				APIGroup: rbacv1.GroupName},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      permission.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			}},
		}
		permissions[permission.ServiceAccountName].AddRoleBinding(roleBinding)
	}

	// Resolve ClusterPermissions as StepResources
	for _, permission := range strategyDetailsDeployment.ClusterPermissions {
		// Create ServiceAccount if necessary
		if _, ok := permissions[permission.ServiceAccountName]; !ok {
			serviceAccount := &corev1.ServiceAccount{}
			ownerutil.AddOwner(serviceAccount, csv, false, false)
			serviceAccount.SetName(permission.ServiceAccountName)

			permissions[permission.ServiceAccountName] = NewOperatorPermissions(serviceAccount)
		}

		// Create ClusterRole
		role := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:   generateName(csv.GetName(), []interface{}{csv.GetName(), csv.GetNamespace(), permission}),
				Labels: ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind),
			},
			Rules: permission.Rules,
		}
		permissions[permission.ServiceAccountName].AddClusterRole(role)

		// Create ClusterRoleBinding
		roleBinding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      role.GetName(),
				Namespace: csv.GetNamespace(),
				Labels:    ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind),
			},
			RoleRef: rbacv1.RoleRef{
				Kind:     "ClusterRole",
				Name:     role.GetName(),
				APIGroup: rbacv1.GroupName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      permission.ServiceAccountName,
				Namespace: csv.GetNamespace(),
			}},
		}
		permissions[permission.ServiceAccountName].AddClusterRoleBinding(roleBinding)
	}
	return permissions, nil
}
