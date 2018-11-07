package operatorlister

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha2"
	appsv1 "k8s.io/client-go/listers/apps/v1"
	corev1 "k8s.io/client-go/listers/core/v1"
	rbacv1 "k8s.io/client-go/listers/rbac/v1"
	aregv1 "k8s.io/kube-aggregator/pkg/client/listers/apiregistration/v1"
)

// OperatorLister is a union of versioned informer listers
type OperatorLister interface {
	AppsV1() AppsV1Lister
	CoreV1() CoreV1Lister
	RbacV1() RbacV1Lister
	APIRegistrationV1() APIRegistrationV1Lister

	OperatorsV1alpha1() OperatorsV1alpha1Lister
	OperatorsV1alpha2() OperatorsV1alpha2Lister
}

type AppsV1Lister interface {
	DeploymentLister() appsv1.DeploymentLister

	RegisterDeploymentLister(namespace string, lister appsv1.DeploymentLister)
}

type CoreV1Lister interface {
	RegisterSecretLister(namespace string, lister corev1.SecretLister)
	RegisterServiceLister(namespace string, lister corev1.ServiceLister)
	RegisterServiceAccountLister(namespace string, lister corev1.ServiceAccountLister)
	RegisterNamespaceLister(lister corev1.NamespaceLister)

	SecretLister() corev1.SecretLister
	ServiceLister() corev1.ServiceLister
	ServiceAccountLister() corev1.ServiceAccountLister
	NamespaceLister() corev1.NamespaceLister
}

type RbacV1Lister interface {
	RegisterClusterRoleLister(lister rbacv1.ClusterRoleLister)
	RegisterClusterRoleBindingLister(lister rbacv1.ClusterRoleBindingLister)
	RegisterRoleLister(namespace string, lister rbacv1.RoleLister)
	RegisterRoleBindingLister(namespace string, lister rbacv1.RoleBindingLister)

	ClusterRoleLister() rbacv1.ClusterRoleLister
	ClusterRoleBindingLister() rbacv1.ClusterRoleBindingLister
	RoleLister() rbacv1.RoleLister
	RoleBindingLister() rbacv1.RoleBindingLister
}

type APIRegistrationV1Lister interface {
	RegisterAPIServiceLister(lister aregv1.APIServiceLister)

	APIServiceLister() aregv1.APIServiceLister
}

type OperatorsV1alpha1Lister interface {
	RegisterClusterServiceVersionLister(namespace string, lister v1alpha1.ClusterServiceVersionLister)

	ClusterServiceVersionLister() v1alpha1.ClusterServiceVersionLister
}

type OperatorsV1alpha2Lister interface {
	RegisterOperatorGroupLister(namespace string, lister v1alpha2.OperatorGroupLister)

	OperatorGroupLister() v1alpha2.OperatorGroupLister
}

type appsV1Lister struct {
	deploymentLister *UnionDeploymentLister
}

func newAppsV1Lister() *appsV1Lister {
	return &appsV1Lister{
		deploymentLister: &UnionDeploymentLister{},
	}
}

type coreV1Lister struct {
	secretLister         *UnionSecretLister
	serviceLister        *UnionServiceLister
	serviceAccountLister *UnionServiceAccountLister
	namespaceLister      *UnionNamespaceLister
}

func newCoreV1Lister() *coreV1Lister {
	return &coreV1Lister{
		secretLister:         &UnionSecretLister{},
		serviceLister:        &UnionServiceLister{},
		serviceAccountLister: &UnionServiceAccountLister{},
		namespaceLister:      &UnionNamespaceLister{},
	}
}

type rbacV1Lister struct {
	roleLister               *UnionRoleLister
	roleBindingLister        *UnionRoleBindingLister
	clusterRoleLister        *UnionClusterRoleLister
	clusterRoleBindingLister *UnionClusterRoleBindingLister
}

func newRbacV1Lister() *rbacV1Lister {
	return &rbacV1Lister{
		roleLister:               &UnionRoleLister{},
		roleBindingLister:        &UnionRoleBindingLister{},
		clusterRoleLister:        &UnionClusterRoleLister{},
		clusterRoleBindingLister: &UnionClusterRoleBindingLister{},
	}
}

type apiRegistrationV1Lister struct {
	apiServiceLister *UnionAPIServiceLister
}

func newAPIRegistrationV1Lister() *apiRegistrationV1Lister {
	return &apiRegistrationV1Lister{
		apiServiceLister: &UnionAPIServiceLister{},
	}
}

type operatorsV1alpha1Lister struct {
	clusterServiceVersionLister *UnionClusterServiceVersionLister
}

func newOperatorsV1alpha1Lister() *operatorsV1alpha1Lister {
	return &operatorsV1alpha1Lister{
		clusterServiceVersionLister: &UnionClusterServiceVersionLister{},
	}
}

type operatorsV1alpha2Lister struct {
	operatorGroupLister *UnionOperatorGroupLister
}

func newOperatorsV1alpha2Lister() *operatorsV1alpha2Lister {
	return &operatorsV1alpha2Lister{
		operatorGroupLister: &UnionOperatorGroupLister{},
	}
}

// Interface assertion
var _ OperatorLister = &lister{}

type lister struct {
	appsV1Lister            *appsV1Lister
	coreV1Lister            *coreV1Lister
	rbacV1Lister            *rbacV1Lister
	apiRegistrationV1Lister *apiRegistrationV1Lister

	operatorsV1alpha1Lister *operatorsV1alpha1Lister
	operatorsV1alpha2Lister *operatorsV1alpha2Lister
}

func (l *lister) AppsV1() AppsV1Lister {
	return l.appsV1Lister
}

func (l *lister) CoreV1() CoreV1Lister {
	return l.coreV1Lister
}

func (l *lister) RbacV1() RbacV1Lister {
	return l.rbacV1Lister
}

func (l *lister) APIRegistrationV1() APIRegistrationV1Lister {
	return l.apiRegistrationV1Lister
}

func (l *lister) OperatorsV1alpha1() OperatorsV1alpha1Lister {
	return l.operatorsV1alpha1Lister
}

func (l *lister) OperatorsV1alpha2() OperatorsV1alpha2Lister {
	return l.operatorsV1alpha2Lister
}

func NewLister() OperatorLister {
	// TODO: better initialization
	return &lister{
		appsV1Lister:            newAppsV1Lister(),
		coreV1Lister:            newCoreV1Lister(),
		rbacV1Lister:            newRbacV1Lister(),
		apiRegistrationV1Lister: newAPIRegistrationV1Lister(),

		operatorsV1alpha1Lister: newOperatorsV1alpha1Lister(),
		operatorsV1alpha2Lister: newOperatorsV1alpha2Lister(),
	}
}
