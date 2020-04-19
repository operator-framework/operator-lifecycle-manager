package catalog

import (
	"context"
	"fmt"

	errorwrap "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func newStepEnsurer(kubeClient operatorclient.ClientInterface, crClient versioned.Interface, dynamicClient dynamic.Interface) *StepEnsurer {
	return &StepEnsurer{
		kubeClient:    kubeClient,
		crClient:      crClient,
		dynamicClient: dynamicClient,
	}
}

// StepEnsurer ensures that resource(s) specified in install plan exist in cluster.
type StepEnsurer struct {
	kubeClient    operatorclient.ClientInterface
	crClient      versioned.Interface
	dynamicClient dynamic.Interface
}

// EnsureClusterServiceVersion writes the specified ClusterServiceVersion
// object to the cluster.
func (o *StepEnsurer) EnsureClusterServiceVersion(csv *v1alpha1.ClusterServiceVersion) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.crClient.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Create(context.TODO(), csv, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating csv %s", csv.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureSubscription writes the specified Subscription object to the cluster.
func (o *StepEnsurer) EnsureSubscription(subscription *v1alpha1.Subscription) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.crClient.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Create(context.TODO(), subscription, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating subscription %s", subscription.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureSecret copies the secret from the OLM namespace and writes a new one
// to the namespace requested.
func (o *StepEnsurer) EnsureSecret(operatorNamespace, planNamespace, name string) (status v1alpha1.StepStatus, err error) {
	secret, getError := o.kubeClient.KubernetesInterface().CoreV1().Secrets(operatorNamespace).Get(context.TODO(), name, metav1.GetOptions{})
	if getError != nil {
		if k8serrors.IsNotFound(getError) {
			err = fmt.Errorf("secret %s does not exist - %v", name, getError)
			return
		}

		err = errorwrap.Wrapf(getError, "error getting pull secret from olm namespace %s", secret.GetName())
		return
	}

	// Set the namespace to the InstallPlan's namespace and attempt to
	// create a new secret.
	secret.SetNamespace(planNamespace)

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name,
			Namespace: planNamespace,
		},
		Data: secret.Data,
		Type: secret.Type,
	}

	if _, createError := o.kubeClient.KubernetesInterface().CoreV1().Secrets(planNamespace).Create(context.TODO(), newSecret, metav1.CreateOptions{}); createError != nil {
		if k8serrors.IsAlreadyExists(createError) {
			status = v1alpha1.StepStatusPresent
			return
		}

		err = fmt.Errorf("error creating secret %s - %v", secret.Name, createError)
		return
	}

	status = v1alpha1.StepStatusCreated
	return
}

// EnsureBundleSecret creates user-specified secrets from the bundle. Called when StepResource.Secret is true
func (o *StepEnsurer) EnsureBundleSecret(namespace string, secret *corev1.Secret) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().CoreV1().Secrets(namespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error updating secret: %s", secret.GetName())
		return
	}

	secret.SetNamespace(namespace)
	// NOTE: any annotations/changes applied to the secret are lost
	if _, updateErr := o.kubeClient.UpdateSecret(secret); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating secret: %s", secret.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureServiceAccount writes the specified ServiceAccount object to the cluster.
func (o *StepEnsurer) EnsureServiceAccount(namespace string, sa *corev1.ServiceAccount) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Create(context.TODO(), sa, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating service account: %s", sa.GetName())
		return
	}

	// Carrying secrets through the service account update.
	preSa, getErr := o.kubeClient.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Get(context.TODO(),
		sa.Name,
		metav1.GetOptions{})
	if getErr != nil {
		err = errorwrap.Wrapf(getErr, "error getting older version of service account: %s", sa.GetName())
		return
	}
	sa.Secrets = preSa.Secrets

	sa.SetNamespace(namespace)

	// Use DeepDerivative to check if new SA is the same as the old SA. If no field is changed, we skip the update call.
	if !apiequality.Semantic.DeepDerivative(sa, preSa) {
		if _, updateErr := o.kubeClient.UpdateServiceAccount(sa); updateErr != nil {
			err = errorwrap.Wrapf(updateErr, "error updating service account: %s", sa.GetName())
			return
		}
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureService writes the specified Service object to the cluster.
func (o *StepEnsurer) EnsureService(namespace string, service *corev1.Service) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().CoreV1().Services(namespace).Create(context.TODO(), service, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error updating service: %s", service.GetName())
		return
	}

	service.SetNamespace(namespace)
	if _, updateErr := o.kubeClient.UpdateService(service); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating service: %s", service.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureClusterRole writes the specified ClusterRole object to the cluster.
func (o *StepEnsurer) EnsureClusterRole(cr *rbacv1.ClusterRole, step *v1alpha1.Step) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().RbacV1().ClusterRoles().Create(context.TODO(), cr, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating clusterrole %s", cr.GetName())
		return
	}

	// We're updating, point owner to the newest csv
	if cr.ObjectMeta.Labels == nil {
		cr.ObjectMeta.Labels = map[string]string{}
	}
	cr.ObjectMeta.Labels[ownerutil.OwnerKey] = step.Resolving
	if _, updateErr := o.kubeClient.UpdateClusterRole(cr); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating clusterrole %s", cr.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureClusterRoleBinding writes the specified ClusterRoleBinding object to the cluster.
func (o *StepEnsurer) EnsureClusterRoleBinding(crb *rbacv1.ClusterRoleBinding, step *v1alpha1.Step) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().RbacV1().ClusterRoleBindings().Create(context.TODO(), crb, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating clusterrolebinding %s", crb.GetName())
		return
	}

	// if we're updating, point owner to the newest csv
	if crb.ObjectMeta.Labels == nil {
		crb.ObjectMeta.Labels = map[string]string{}
	}
	crb.ObjectMeta.Labels[ownerutil.OwnerKey] = step.Resolving
	if _, updateErr := o.kubeClient.UpdateClusterRoleBinding(crb); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating clusterrolebinding %s", crb.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureRole writes the specified Role object to the cluster.
func (o *StepEnsurer) EnsureRole(namespace string, role *rbacv1.Role) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().RbacV1().Roles(namespace).Create(context.TODO(), role, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating role %s", role.GetName())
		return
	}

	// If it already existed, mark the step as Present.
	role.SetNamespace(namespace)
	if _, updateErr := o.kubeClient.UpdateRole(role); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating role %s", role.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureRoleBinding writes the specified RoleBinding object to the cluster.
func (o *StepEnsurer) EnsureRoleBinding(namespace string, rb *rbacv1.RoleBinding) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().RbacV1().RoleBindings(namespace).Create(context.TODO(), rb, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating rolebinding %s", rb.GetName())
		return
	}

	rb.SetNamespace(namespace)
	if _, updateErr := o.kubeClient.UpdateRoleBinding(rb); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating rolebinding %s", rb.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureUnstructuredObject writes the unspecified resource object to the cluster.
func (o *StepEnsurer) EnsureUnstructuredObject(client dynamic.ResourceInterface, obj *unstructured.Unstructured) (status v1alpha1.StepStatus, err error) {
	_, createErr := client.Create(context.TODO(), obj, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error creating unstructured object %s", obj.GetName())
		return
	}

	original, getError := client.Get(context.TODO(), obj.GetName(), metav1.GetOptions{})
	if getError != nil {
		err = errorwrap.Wrapf(getError, "error getting unstructured object %s", obj.GetName())
		return
	}

	// Set the objects resource version
	obj.SetResourceVersion(original.GetResourceVersion())

	_, updateError := client.Update(context.TODO(), obj, metav1.UpdateOptions{})
	if updateError != nil {
		err = errorwrap.Wrapf(updateError, "error updating unstructured object %s", obj.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}

// EnsureConfigMap writes the specified ConfigMap object to the cluster.
func (o *StepEnsurer) EnsureConfigMap(namespace string, configmap *corev1.ConfigMap) (status v1alpha1.StepStatus, err error) {
	_, createErr := o.kubeClient.KubernetesInterface().CoreV1().ConfigMaps(namespace).Create(context.TODO(), configmap, metav1.CreateOptions{})
	if createErr == nil {
		status = v1alpha1.StepStatusCreated
		return
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		err = errorwrap.Wrapf(createErr, "error updating configmap: %s", configmap.GetName())
		return
	}

	configmap.SetNamespace(namespace)
	if _, updateErr := o.kubeClient.UpdateConfigMap(configmap); updateErr != nil {
		err = errorwrap.Wrapf(updateErr, "error updating configmap: %s", configmap.GetName())
		return
	}

	status = v1alpha1.StepStatusPresent
	return
}
