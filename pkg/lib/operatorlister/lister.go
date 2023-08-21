package operatorlister

import (
	appsv1 "k8s.io/client-go/listers/apps/v1"
	corev1 "k8s.io/client-go/listers/core/v1"
	rbacv1 "k8s.io/client-go/listers/rbac/v1"
	"k8s.io/client-go/metadata/metadatalister"
	aregv1 "k8s.io/kube-aggregator/pkg/client/listers/apiregistration/v1"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	v2 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v2"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_v1_service_account_lister.go k8s.io/client-go/listers/core/v1.ServiceAccountLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_v1_service_account_namespace_lister.go k8s.io/client-go/listers/core/v1.ServiceAccountNamespaceLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_v1_service_lister.go k8s.io/client-go/listers/core/v1.ServiceLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_v1_service_namespace_lister.go k8s.io/client-go/listers/core/v1.ServiceNamespaceLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_v1_secret_lister.go k8s.io/client-go/listers/core/v1.SecretLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_v1_secret_namespace_lister.go k8s.io/client-go/listers/core/v1.SecretNamespaceLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_rbac_v1_role_lister.go k8s.io/client-go/listers/rbac/v1.RoleLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_rbac_v1_role_namespace_lister.go k8s.io/client-go/listers/rbac/v1.RoleNamespaceLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_rbac_v1_rolebinding_lister.go k8s.io/client-go/listers/rbac/v1.RoleBindingLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_rbac_v1_rolebinding_namespace_lister.go k8s.io/client-go/listers/rbac/v1.RoleBindingNamespaceLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../fakes/client-go/listers/fake_rbac_v1_clusterrolebinding_lister.go k8s.io/client-go/listers/rbac/v1.ClusterRoleBindingLister

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ./operatorlisterfakes/fake_clusterserviceversion_v1alpha1_lister.go ../../api/client/listers/operators/v1alpha1.ClusterServiceVersionLister
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ./operatorlisterfakes/fake_clusterserviceversion_v1alpha1_namespace_lister.go ../../api/client/listers/operators/v1alpha1.ClusterServiceVersionNamespaceLister

// OperatorLister is a union of versioned informer listers
//
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . OperatorLister
type OperatorLister interface {
	AppsV1() AppsV1Lister
	CoreV1() CoreV1Lister
	RbacV1() RbacV1Lister
	APIRegistrationV1() APIRegistrationV1Lister
	APIExtensionsV1() APIExtensionsV1Lister

	OperatorsV1alpha1() OperatorsV1alpha1Lister
	OperatorsV1() OperatorsV1Lister
	OperatorsV2() OperatorsV2Lister
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . AppsV1Lister
type AppsV1Lister interface {
	DeploymentLister() appsv1.DeploymentLister

	RegisterDeploymentLister(namespace string, lister appsv1.DeploymentLister)
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . CoreV1Lister
type CoreV1Lister interface {
	RegisterSecretLister(namespace string, lister corev1.SecretLister)
	RegisterServiceLister(namespace string, lister corev1.ServiceLister)
	RegisterServiceAccountLister(namespace string, lister corev1.ServiceAccountLister)
	RegisterPodLister(namespace string, lister corev1.PodLister)
	RegisterConfigMapLister(namespace string, lister corev1.ConfigMapLister)
	RegisterNamespaceLister(lister corev1.NamespaceLister)

	SecretLister() corev1.SecretLister
	ServiceLister() corev1.ServiceLister
	ServiceAccountLister() corev1.ServiceAccountLister
	NamespaceLister() corev1.NamespaceLister
	PodLister() corev1.PodLister
	ConfigMapLister() corev1.ConfigMapLister
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . RbacV1Lister
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

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . APIRegistrationV1Lister
type APIRegistrationV1Lister interface {
	RegisterAPIServiceLister(lister aregv1.APIServiceLister)

	APIServiceLister() aregv1.APIServiceLister
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . APIExtensionsV1Lister
type APIExtensionsV1Lister interface {
	RegisterCustomResourceDefinitionLister(lister metadatalister.Lister)
	CustomResourceDefinitionLister() metadatalister.Lister
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . OperatorsV1alpha1Lister
type OperatorsV1alpha1Lister interface {
	RegisterClusterServiceVersionLister(namespace string, lister v1alpha1.ClusterServiceVersionLister)
	RegisterCatalogSourceLister(namespace string, lister v1alpha1.CatalogSourceLister)
	RegisterSubscriptionLister(namespace string, lister v1alpha1.SubscriptionLister)
	RegisterInstallPlanLister(namespace string, lister v1alpha1.InstallPlanLister)

	ClusterServiceVersionLister() v1alpha1.ClusterServiceVersionLister
	CatalogSourceLister() v1alpha1.CatalogSourceLister
	SubscriptionLister() v1alpha1.SubscriptionLister
	InstallPlanLister() v1alpha1.InstallPlanLister
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . OperatorsV1Lister
type OperatorsV1Lister interface {
	RegisterOperatorGroupLister(namespace string, lister v1.OperatorGroupLister)

	OperatorGroupLister() v1.OperatorGroupLister
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . OperatorsV2Lister
type OperatorsV2Lister interface {
	RegisterOperatorConditionLister(namespace string, lister v2.OperatorConditionLister)

	OperatorConditionLister() v2.OperatorConditionLister
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
	podLister            *UnionPodLister
	configMapLister      *UnionConfigMapLister
}

func newCoreV1Lister() *coreV1Lister {
	return &coreV1Lister{
		secretLister:         &UnionSecretLister{},
		serviceLister:        &UnionServiceLister{},
		serviceAccountLister: &UnionServiceAccountLister{},
		namespaceLister:      &UnionNamespaceLister{},
		podLister:            &UnionPodLister{},
		configMapLister:      &UnionConfigMapLister{},
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

type apiExtensionsV1Lister struct {
	customResourceDefinitionLister *UnionCustomResourceDefinitionLister
}

func newAPIExtensionsV1Lister() *apiExtensionsV1Lister {
	return &apiExtensionsV1Lister{
		customResourceDefinitionLister: &UnionCustomResourceDefinitionLister{},
	}
}

type operatorsV1alpha1Lister struct {
	clusterServiceVersionLister *UnionClusterServiceVersionLister
	catalogSourceLister         *UnionCatalogSourceLister
	subscriptionLister          *UnionSubscriptionLister
	installPlanLister           *UnionInstallPlanLister
}

func newOperatorsV1alpha1Lister() *operatorsV1alpha1Lister {
	return &operatorsV1alpha1Lister{
		clusterServiceVersionLister: &UnionClusterServiceVersionLister{},
		catalogSourceLister:         &UnionCatalogSourceLister{},
		subscriptionLister:          &UnionSubscriptionLister{},
		installPlanLister:           &UnionInstallPlanLister{},
	}
}

type operatorsV1Lister struct {
	operatorGroupLister *UnionOperatorGroupLister
}

type operatorsV2Lister struct {
	operatorConditionLister *UnionOperatorConditionLister
}

func newOperatorsV1Lister() *operatorsV1Lister {
	return &operatorsV1Lister{
		operatorGroupLister: &UnionOperatorGroupLister{},
	}
}

func newOperatorsV2Lister() *operatorsV2Lister {
	return &operatorsV2Lister{
		operatorConditionLister: &UnionOperatorConditionLister{},
	}
}

// Interface assertion
var _ OperatorLister = &lister{}

type lister struct {
	appsV1Lister            *appsV1Lister
	coreV1Lister            *coreV1Lister
	rbacV1Lister            *rbacV1Lister
	apiRegistrationV1Lister *apiRegistrationV1Lister
	apiExtensionsV1Lister   *apiExtensionsV1Lister
	operatorsV1alpha1Lister *operatorsV1alpha1Lister
	operatorsV1Lister       *operatorsV1Lister
	operatorsV2Lister       *operatorsV2Lister
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

func (l *lister) APIExtensionsV1() APIExtensionsV1Lister {
	return l.apiExtensionsV1Lister
}

func (l *lister) OperatorsV1alpha1() OperatorsV1alpha1Lister {
	return l.operatorsV1alpha1Lister
}

func (l *lister) OperatorsV1() OperatorsV1Lister {
	return l.operatorsV1Lister
}

func (l *lister) OperatorsV2() OperatorsV2Lister {
	return l.operatorsV2Lister
}

func NewLister() OperatorLister {
	// TODO: better initialization
	return &lister{
		appsV1Lister:            newAppsV1Lister(),
		coreV1Lister:            newCoreV1Lister(),
		rbacV1Lister:            newRbacV1Lister(),
		apiRegistrationV1Lister: newAPIRegistrationV1Lister(),
		apiExtensionsV1Lister:   newAPIExtensionsV1Lister(),
		operatorsV1alpha1Lister: newOperatorsV1alpha1Lister(),
		operatorsV1Lister:       newOperatorsV1Lister(),
		operatorsV2Lister:       newOperatorsV2Lister(),
	}
}
