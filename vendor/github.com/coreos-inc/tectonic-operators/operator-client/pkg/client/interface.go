package client

import (
	appsv1beta2 "k8s.io/api/apps/v1beta2"
	"k8s.io/api/core/v1"
	extensionsv1beta1 "k8s.io/api/extensions/v1beta1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/coreos-inc/tectonic-operators/operator-client/pkg/types"
)

// Interface is the top level generic interface for the
// operator client.
type Interface interface {
	KubernetesInterface() kubernetes.Interface
	ApiextensionsV1beta1Interface() apiextensions.Interface
	RunLeaderElection(LeaderElectionConfig)
	ImpersonatedClientForServiceAccount(serviceAccountName string, namespace string) (Interface, error)

	CustomResourceClient
	CustomResourceDefinitionClient
	MigrationStatusClient

	AppVersionClient
	ClusterRoleClient
	ClusterRoleBindingClient
	ConfigMapClient
	DaemonSetClient
	DeploymentClient
	IngressClient
	NodeClient
	PodClient
	PodDisruptionBudgetClient
	ServiceClient
	ServiceAccountClient
}

// AppVersionClient contains methods for the AppVersion resource.
type AppVersionClient interface {
	CreateAppVersion(*types.AppVersion) (*types.AppVersion, error)
	AtomicUpdateAppVersion(name string, fn types.AppVersionModifier) (*types.AppVersion, error)
	UpdateAppVersion(*types.AppVersion) (*types.AppVersion, error)
	GetAppVersion(name string) (*types.AppVersion, error)
	ListAppVersionsWithLabels(labels string) (*types.AppVersionList, error)
	DeleteAppVersion(name string) error

	SetFailureStatus(name string, failureStatus *types.FailureStatus) error
	SetTaskStatuses(name string, ts []types.TaskStatus) error
	UpdateTaskStatus(name string, ts types.TaskStatus) error
}

// ConfigMapClient contains methods for the ConfigMap resource.
type ConfigMapClient interface {
	CreateConfigMap(namespace string, cm *v1.ConfigMap) (*v1.ConfigMap, error)
	AtomicUpdateConfigMap(namespace, name string, fn types.ConfigMapModifier) (*v1.ConfigMap, error)
	UpdateConfigMap(*v1.ConfigMap) (*v1.ConfigMap, error)
	GetConfigMap(namespace, name string) (*v1.ConfigMap, error)
	ListConfigMapsWithLabels(namespace string, labels labels.Set) (*v1.ConfigMapList, error)
	DeleteConfigMap(namespace, name string, options *metav1.DeleteOptions) error
}

// MigrationStatusClient contains methods for the MigrationStatus resource.
type MigrationStatusClient interface {
	GetMigrationStatus(name string) (*types.MigrationStatus, error)
	CreateMigrationStatus(*types.MigrationStatus) (*types.MigrationStatus, error)
	UpdateMigrationStatus(*types.MigrationStatus) (*types.MigrationStatus, error)
}

// DaemonSetClient contains methods for the DaemonSet resource.
type DaemonSetClient interface {
	CreateDaemonSet(*appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, error)
	GetDaemonSet(namespace, name string) (*appsv1beta2.DaemonSet, error)
	DeleteDaemonSet(namespace, name string, options *metav1.DeleteOptions) error
	UpdateDaemonSet(*appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error)
	PatchDaemonSet(*appsv1beta2.DaemonSet, *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error)
	RollingUpdateDaemonSet(*appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error)
	RollingPatchDaemonSet(*appsv1beta2.DaemonSet, *appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error)
	RollingUpdateDaemonSetMigrations(namespace, name string, f UpdateFunction, opts UpdateOpts) (*appsv1beta2.DaemonSet, bool, error)
	RollingPatchDaemonSetMigrations(namespace, name string, f PatchFunction, opts UpdateOpts) (*appsv1beta2.DaemonSet, bool, error)
	CreateOrRollingUpdateDaemonSet(*appsv1beta2.DaemonSet) (*appsv1beta2.DaemonSet, bool, error)
	NumberOfDesiredPodsForDaemonSet(*appsv1beta2.DaemonSet) (int, error)
	ListDaemonSetsWithLabels(namespace string, labels labels.Set) (*appsv1beta2.DaemonSetList, error)
}

// PodClient contains methods for the Pod resource.
type PodClient interface {
	DeletePod(namespace, name string) error
	ListPodsWithLabels(namespace string, labels labels.Set) (*v1.PodList, error)
}

// DeploymentClient contains methods for the Deployment resource.
type DeploymentClient interface {
	GetDeployment(namespace, name string) (*appsv1beta2.Deployment, error)
	CreateDeployment(*appsv1beta2.Deployment) (*appsv1beta2.Deployment, error)
	DeleteDeployment(namespace, name string, options *metav1.DeleteOptions) error
	UpdateDeployment(*appsv1beta2.Deployment) (*appsv1beta2.Deployment, bool, error)
	PatchDeployment(*appsv1beta2.Deployment, *appsv1beta2.Deployment) (*appsv1beta2.Deployment, bool, error)
	RollingUpdateDeployment(*appsv1beta2.Deployment) (*appsv1beta2.Deployment, bool, error)
	RollingPatchDeployment(*appsv1beta2.Deployment, *appsv1beta2.Deployment) (*appsv1beta2.Deployment, bool, error)
	RollingUpdateDeploymentMigrations(namespace, name string, f UpdateFunction, opts UpdateOpts) (*appsv1beta2.Deployment, bool, error)
	RollingPatchDeploymentMigrations(namespace, name string, f PatchFunction, opts UpdateOpts) (*appsv1beta2.Deployment, bool, error)
	CreateOrRollingUpdateDeployment(*appsv1beta2.Deployment) (*appsv1beta2.Deployment, bool, error)
	ListDeploymentsWithLabels(namespace string, labels labels.Set) (*appsv1beta2.DeploymentList, error)
}

// ServiceClient contains methods for the Service resource.
type ServiceClient interface {
	GetService(namespace, name string) (*v1.Service, error)
	CreateService(*v1.Service) (*v1.Service, error)
	DeleteService(namespace, name string, options *metav1.DeleteOptions) error
	UpdateService(*v1.Service) (*v1.Service, bool, error)
	PatchService(*v1.Service, *v1.Service) (*v1.Service, bool, error)
	UpdateServiceMigrations(namespace, name string, f UpdateFunction, opts UpdateOpts) (*v1.Service, bool, error)
	PatchServiceMigrations(namespace, name string, f PatchFunction, opts UpdateOpts) (*v1.Service, bool, error)
}

// NodeClient contains methods for the Node resource.
type NodeClient interface {
	ListNodes(metav1.ListOptions) (*v1.NodeList, error)
	GetNode(name string) (*v1.Node, error)
	UpdateNode(*v1.Node) (*v1.Node, error)
	AtomicUpdateNode(name string, f types.NodeModifier) (*v1.Node, error)
	DrainNode(*v1.Node) error
	OptimisticDrainNode(*v1.Node) error
	UnCordonNode(*v1.Node) (*v1.Node, error)
	CordonNode(*v1.Node) (*v1.Node, error)
}

// CustomResourceDefinitionClient contains methods for the Custom Resource Definition.
type CustomResourceDefinitionClient interface {
	GetCustomResourceDefinition(name string) (*v1beta1ext.CustomResourceDefinition, error)
	CreateCustomResourceDefinition(crd *v1beta1ext.CustomResourceDefinition) error
	UpdateCustomResourceDefinition(modified *v1beta1ext.CustomResourceDefinition) error
	DeleteCustomResourceDefinition(name string, options *metav1.DeleteOptions) error
	EnsureCustomResourceDefinition(crd *v1beta1ext.CustomResourceDefinition) error
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

// IngressClient contains methods for manipulating Ingress.
type IngressClient interface {
	CreateIngress(*extensionsv1beta1.Ingress) (*extensionsv1beta1.Ingress, error)
	GetIngress(namespace, name string) (*extensionsv1beta1.Ingress, error)
	UpdateIngress(original, modified *extensionsv1beta1.Ingress) (*extensionsv1beta1.Ingress, bool, error)
	DeleteIngress(namespace, name string, options *metav1.DeleteOptions) error
}

// ServiceAccountClient contains methods for manipulating ServiceAccount.
type ServiceAccountClient interface {
	CreateServiceAccount(*v1.ServiceAccount) (*v1.ServiceAccount, error)
	GetServiceAccount(namespace, name string) (*v1.ServiceAccount, error)
	UpdateServiceAccount(modified *v1.ServiceAccount) (*v1.ServiceAccount, error)
	DeleteServiceAccount(namespace, name string, options *metav1.DeleteOptions) error
}

// ClusterRoleBindingClient contains methods for manipulating ClusterRoleBinding.
type ClusterRoleBindingClient interface {
	CreateClusterRoleBinding(*rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error)
	GetClusterRoleBinding(name string) (*rbacv1.ClusterRoleBinding, error)
	UpdateClusterRoleBinding(modified *rbacv1.ClusterRoleBinding) (*rbacv1.ClusterRoleBinding, error)
	DeleteClusterRoleBinding(name string, options *metav1.DeleteOptions) error
}

// ClusterRoleClient contains methods for manipulating ClusterRole.
type ClusterRoleClient interface {
	CreateClusterRole(*rbacv1.ClusterRole) (*rbacv1.ClusterRole, error)
	GetClusterRole(name string) (*rbacv1.ClusterRole, error)
	UpdateClusterRole(modified *rbacv1.ClusterRole) (*rbacv1.ClusterRole, error)
	DeleteClusterRole(name string, options *metav1.DeleteOptions) error
}

// PodDisruptionBudgetClient contains methods for manipulating PodDisruptionBudget.
type PodDisruptionBudgetClient interface {
	CreatePodDisruptionBudget(*policyv1beta1.PodDisruptionBudget) (*policyv1beta1.PodDisruptionBudget, error)
	GetPodDisruptionBudget(namespace, name string) (*policyv1beta1.PodDisruptionBudget, error)
	UpdatePodDisruptionBudget(*policyv1beta1.PodDisruptionBudget) (*policyv1beta1.PodDisruptionBudget, error)
	DeletePodDisruptionBudget(namespace, name string, options *metav1.DeleteOptions) error
}
