//go:generate go run github.com/golang/mock/mockgen -source client.go -destination operatorclientmocks/mock_client.go -package operatorclientmocks
package operatorclient

import (
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistration "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

type ClientInterface interface {
	KubernetesInterface() kubernetes.Interface
	ApiextensionsInterface() apiextensions.Interface
	ApiregistrationV1Interface() apiregistration.Interface
	APIServiceClient
	CustomResourceClient
	SecretClient
	ServiceClient
	ServiceAccountClient
	RoleClient
	RoleBindingClient
	ClusterRoleBindingClient
	ClusterRoleClient
	DeploymentClient
	ConfigMapClient
}

// CustomResourceClient contains methods for the Custom Resource.
type CustomResourceClient interface {
	GetCustomResource(apiGroup, version, namespace, resourceKind, resourceName string) (*unstructured.Unstructured, error)
	GetCustomResourceRaw(apiGroup, version, namespace, resourceKind, resourceName string) ([]byte, error)
	CreateCustomResource(item *unstructured.Unstructured) error
	CreateCustomResourceRaw(apiGroup, version, namespace, kind string, data []byte) error
	CreateCustomResourceRawIfNotFound(apiGroup, version, namespace, kind, name string, data []byte) (bool, error)
	UpdateCustomResource(item *unstructured.Unstructured) error
	UpdateCustomResourceRaw(apiGroup, version, namespace, resourceKind, resourceName string, data []byte) error
	CreateOrUpdateCustomeResourceRaw(apiGroup, version, namespace, resourceKind, resourceName string, data []byte) error
	DeleteCustomResource(apiGroup, version, namespace, resourceKind, resourceName string) error
	AtomicModifyCustomResource(apiGroup, version, namespace, resourceKind, resourceName string, f CustomResourceModifier, data interface{}) error
	ListCustomResource(apiGroup, version, namespace, resourceKind string) (*CustomResourceList, error)
}

// APIServiceClient contains methods for manipulating APIServiceBindings.
type APIServiceClient interface {
	CreateAPIService(*apiregistrationv1.APIService) (*apiregistrationv1.APIService, error)
	GetAPIService(name string) (*apiregistrationv1.APIService, error)
	UpdateAPIService(modified *apiregistrationv1.APIService) (*apiregistrationv1.APIService, error)
	DeleteAPIService(name string, options *metav1.DeleteOptions) error
}

// SecretClient contains methods for manipulating Secrets
type SecretClient interface {
	CreateSecret(*v1.Secret) (*v1.Secret, error)
	GetSecret(namespace, name string) (*v1.Secret, error)
	UpdateSecret(modified *v1.Secret) (*v1.Secret, error)
	DeleteSecret(namespace, name string, options *metav1.DeleteOptions) error
}

// ServiceClient contains methods for manipulating Services
type ServiceClient interface {
	CreateService(*v1.Service) (*v1.Service, error)
	GetService(namespace, name string) (*v1.Service, error)
	UpdateService(modified *v1.Service) (*v1.Service, error)
	DeleteService(namespace, name string, options *metav1.DeleteOptions) error
}

// ServiceAccountClient contains methods for manipulating ServiceAccounts.
type ServiceAccountClient interface {
	CreateServiceAccount(*v1.ServiceAccount) (*v1.ServiceAccount, error)
	GetServiceAccount(namespace, name string) (*v1.ServiceAccount, error)
	UpdateServiceAccount(modified *v1.ServiceAccount) (*v1.ServiceAccount, error)
	DeleteServiceAccount(namespace, name string, options *metav1.DeleteOptions) error
}

// RoleClient contains methods for manipulating Roles.
type RoleClient interface {
	CreateRole(*rbacv1.Role) (*rbacv1.Role, error)
	GetRole(namespace, name string) (*rbacv1.Role, error)
	UpdateRole(modified *rbacv1.Role) (*rbacv1.Role, error)
	DeleteRole(namespace, name string, options *metav1.DeleteOptions) error
}

// RoleBindingClient contains methods for manipulating RoleBindings.
type RoleBindingClient interface {
	CreateRoleBinding(*rbacv1.RoleBinding) (*rbacv1.RoleBinding, error)
	GetRoleBinding(namespace, name string) (*rbacv1.RoleBinding, error)
	UpdateRoleBinding(modified *rbacv1.RoleBinding) (*rbacv1.RoleBinding, error)
	DeleteRoleBinding(namespace, name string, options *metav1.DeleteOptions) error
}

// ClusterRoleClient contains methods for manipulating ClusterRoleBindings.
type ClusterRoleClient interface {
	CreateClusterRole(*rbacv1.ClusterRole) (*rbacv1.ClusterRole, error)
	GetClusterRole(name string) (*rbacv1.ClusterRole, error)
	UpdateClusterRole(modified *rbacv1.ClusterRole) (*rbacv1.ClusterRole, error)
	DeleteClusterRole(name string, options *metav1.DeleteOptions) error
}

// ClusterRoleBindingClient contains methods for manipulating ClusterRoleBindings.
type ClusterRoleBindingClient interface {
	CreateClusterRoleBinding(*rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error)
	GetClusterRoleBinding(name string) (*rbacv1.ClusterRoleBinding, error)
	UpdateClusterRoleBinding(modified *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error)
	DeleteClusterRoleBinding(name string, options *metav1.DeleteOptions) error
}

// DeploymentClient contains methods for the Deployment resource.
type DeploymentClient interface {
	GetDeployment(namespace, name string) (*appsv1.Deployment, error)
	CreateDeployment(*appsv1.Deployment) (*appsv1.Deployment, error)
	DeleteDeployment(namespace, name string, options *metav1.DeleteOptions) error
	UpdateDeployment(*appsv1.Deployment) (*appsv1.Deployment, bool, error)
	PatchDeployment(*appsv1.Deployment, *appsv1.Deployment) (*appsv1.Deployment, bool, error)
	RollingUpdateDeployment(*appsv1.Deployment) (*appsv1.Deployment, bool, error)
	RollingPatchDeployment(*appsv1.Deployment, *appsv1.Deployment) (*appsv1.Deployment, bool, error)
	RollingUpdateDeploymentMigrations(namespace, name string, f UpdateFunction) (*appsv1.Deployment, bool, error)
	RollingPatchDeploymentMigrations(namespace, name string, f PatchFunction) (*appsv1.Deployment, bool, error)
	CreateOrRollingUpdateDeployment(*appsv1.Deployment) (*appsv1.Deployment, bool, error)
	ListDeploymentsWithLabels(namespace string, labels labels.Set) (*appsv1.DeploymentList, error)
}

// ConfigMapClient contains methods for the ConfigMap resource
type ConfigMapClient interface {
	CreateConfigMap(*v1.ConfigMap) (*v1.ConfigMap, error)
	GetConfigMap(namespace, name string) (*v1.ConfigMap, error)
	UpdateConfigMap(modified *v1.ConfigMap) (*v1.ConfigMap, error)
	DeleteConfigMap(namespace, name string, options *metav1.DeleteOptions) error
}

// Interface assertion.
var _ ClientInterface = &Client{}

// Client is a kubernetes client that can talk to the API server.
type Client struct {
	kubernetes.Interface
	extInterface apiextensions.Interface
	regInterface apiregistration.Interface
}

func NewClientFromRestConfig(config *rest.Config) (client ClientInterface, err error) {
	kubernetes, err := kubernetes.NewForConfig(config)
	if err != nil {
		return
	}

	apiextensions, err := apiextensions.NewForConfig(config)
	if err != nil {
		return
	}

	apiregistration, err := apiregistration.NewForConfig(config)
	if err != nil {
		return
	}

	client = &Client{
		kubernetes,
		apiextensions,
		apiregistration,
	}

	return
}

// NewClient creates a kubernetes client
func NewClient(k8sClient kubernetes.Interface, extclient apiextensions.Interface, regclient apiregistration.Interface) ClientInterface {
	return &Client{k8sClient, extclient, regclient}
}

// KubernetesInterface returns the Kubernetes interface.
func (c *Client) KubernetesInterface() kubernetes.Interface {
	return c.Interface
}

// ApiextensionsInterface returns the API extension interface.
func (c *Client) ApiextensionsInterface() apiextensions.Interface {
	return c.extInterface
}

// ApiregistrationV1Interface returns the API registration (aggregated apiserver) interface
func (c *Client) ApiregistrationV1Interface() apiregistration.Interface {
	return c.regInterface
}
