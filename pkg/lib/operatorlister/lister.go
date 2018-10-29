package operatorlister

import (
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

// Interface assertion
var _ OperatorLister = &lister{}

type lister struct {
	appsV1Lister            *appsV1Lister
	coreV1Lister            *coreV1Lister
	rbacV1Lister            *rbacV1Lister
	apiRegistrationV1Lister *apiRegistrationV1Lister
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

func NewLister() OperatorLister {
	// TODO: better initialization
	return &lister{
		appsV1Lister:            newAppsV1Lister(),
		coreV1Lister:            newCoreV1Lister(),
		rbacV1Lister:            newRbacV1Lister(),
		apiRegistrationV1Lister: newAPIRegistrationV1Lister(),
	}
}
