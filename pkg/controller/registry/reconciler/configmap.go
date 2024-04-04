//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_reconciler.go . RegistryReconciler
package reconciler

import (
	"context"
	"fmt"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// configMapCatalogSourceDecorator wraps CatalogSource to add additional methods
type configMapCatalogSourceDecorator struct {
	*v1alpha1.CatalogSource
	Reconciler *ConfigMapRegistryReconciler
	runAsUser  int64
}

const (
	// ConfigMapServerPostfix is a postfix appended to the names of resources generated for a ConfigMap server.
	ConfigMapServerPostfix string = "-configmap-server"
)

func (s *configMapCatalogSourceDecorator) serviceAccountName() string {
	return s.GetName() + ConfigMapServerPostfix
}

func (s *configMapCatalogSourceDecorator) roleName() string {
	return s.GetName() + "-configmap-reader"
}

func (s *configMapCatalogSourceDecorator) Selector() map[string]string {
	return map[string]string{
		CatalogSourceLabelKey: s.GetName(),
	}
}

const (
	// ConfigMapRVLabelKey is the key for a label used to track the resource version of a related ConfigMap.
	ConfigMapRVLabelKey string = "olm.configMapResourceVersion"
)

func (s *configMapCatalogSourceDecorator) Labels() map[string]string {
	labels := map[string]string{
		CatalogSourceLabelKey:      s.GetName(),
		install.OLMManagedLabelKey: install.OLMManagedLabelValue,
	}
	if s.Spec.SourceType == v1alpha1.SourceTypeInternal || s.Spec.SourceType == v1alpha1.SourceTypeConfigmap {
		labels[ConfigMapRVLabelKey] = s.Status.ConfigMapResource.ResourceVersion
	}
	return labels
}

func (s *configMapCatalogSourceDecorator) Annotations() map[string]string {
	// TODO: Maybe something better than just a copy of all annotations would be to have a specific 'podMetadata' section in the CatalogSource?
	return s.GetAnnotations()
}

func (s *configMapCatalogSourceDecorator) ConfigMapChanges(configMap *corev1.ConfigMap) bool {
	if s.Status.ConfigMapResource == nil {
		return true
	}
	if s.Status.ConfigMapResource.ResourceVersion == configMap.GetResourceVersion() {
		return false
	}
	return true
}

func (s *configMapCatalogSourceDecorator) Service() (*corev1.Service, error) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.GetName(),
			Namespace: s.GetNamespace(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "grpc",
					Port:       50051,
					TargetPort: intstr.FromInt(50051),
				},
			},
			Selector: s.Selector(),
		},
	}

	labels := map[string]string{
		install.OLMManagedLabelKey: install.OLMManagedLabelValue,
	}
	hash, err := hashutil.DeepHashObject(&svc.Spec)
	if err != nil {
		return nil, err
	}
	labels[ServiceHashLabelKey] = hash
	svc.SetLabels(labels)
	ownerutil.AddOwner(svc, s.CatalogSource, false, false)
	return svc, nil
}

func (s *configMapCatalogSourceDecorator) getNamespaceSecurityContextConfig() (operatorsv1alpha1.SecurityConfig, error) {
	namespace := s.GetNamespace()
	if config, ok := s.Reconciler.namespacePSAConfigCache[namespace]; ok {
		return config, nil
	}
	// Retrieve the client from the reconciler
	client := s.Reconciler.OpClient

	ns, err := client.KubernetesInterface().CoreV1().Namespaces().Get(context.TODO(), s.GetNamespace(), metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("error fetching namespace: %v", err)
	}
	// 'pod-security.kubernetes.io/enforce' is the label used for enforcing namespace level security,
	// and 'restricted' is the value indicating a restricted security policy.
	if val, exists := ns.Labels["pod-security.kubernetes.io/enforce"]; exists && val == "restricted" {
		return operatorsv1alpha1.Restricted, nil
	}

	return operatorsv1alpha1.Legacy, nil
}
func (s *configMapCatalogSourceDecorator) Pod(image string) (*corev1.Pod, error) {
	securityContextConfig, err := s.getNamespaceSecurityContextConfig()
	if err != nil {
		return nil, err
	}
	pod, err := Pod(s.CatalogSource, "configmap-registry-server", "", "", image, nil, s.Labels(), s.Annotations(), 5, 5, s.runAsUser, securityContextConfig)
	if err != nil {
		return nil, err
	}
	pod.Spec.ServiceAccountName = s.GetName() + ConfigMapServerPostfix
	pod.Spec.Containers[0].Command = []string{"configmap-server", "-c", s.Spec.ConfigMap, "-n", s.GetNamespace()}
	ownerutil.AddOwner(pod, s.CatalogSource, false, true)
	return pod, nil
}

func (s *configMapCatalogSourceDecorator) ServiceAccount() *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.serviceAccountName(),
			Namespace: s.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
			},
		},
	}
	ownerutil.AddOwner(sa, s.CatalogSource, false, false)
	return sa
}

func (s *configMapCatalogSourceDecorator) Role() *rbacv1.Role {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.roleName(),
			Namespace: s.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				ResourceNames: []string{s.Spec.ConfigMap},
			},
		},
	}
	ownerutil.AddOwner(role, s.CatalogSource, false, false)
	return role
}

func (s *configMapCatalogSourceDecorator) RoleBinding() *rbacv1.RoleBinding {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.GetName() + "-server-configmap-reader",
			Namespace: s.GetNamespace(),
			Labels: map[string]string{
				install.OLMManagedLabelKey: install.OLMManagedLabelValue,
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      s.serviceAccountName(),
				Namespace: s.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     s.roleName(),
		},
	}
	ownerutil.AddOwner(rb, s.CatalogSource, false, false)
	return rb
}

type ConfigMapRegistryReconciler struct {
	now                     nowFunc
	Lister                  operatorlister.OperatorLister
	OpClient                operatorclient.ClientInterface
	Image                   string
	createPodAsUser         int64
	namespacePSAConfigCache map[string]operatorsv1alpha1.SecurityConfig
}

var _ RegistryEnsurer = &ConfigMapRegistryReconciler{}
var _ RegistryChecker = &ConfigMapRegistryReconciler{}
var _ RegistryReconciler = &ConfigMapRegistryReconciler{}

func (c *ConfigMapRegistryReconciler) currentService(source configMapCatalogSourceDecorator) (*corev1.Service, error) {
	protoService, err := source.Service()
	if err != nil {
		return nil, err
	}
	serviceName := protoService.GetName()
	service, err := c.Lister.CoreV1().ServiceLister().Services(source.GetNamespace()).Get(serviceName)
	if err != nil {
		logrus.WithField("service", serviceName).Debug("couldn't find service in cache")
		return nil, nil
	}
	return service, nil
}

func (c *ConfigMapRegistryReconciler) currentServiceAccount(source configMapCatalogSourceDecorator) *corev1.ServiceAccount {
	serviceAccountName := source.ServiceAccount().GetName()
	serviceAccount, err := c.Lister.CoreV1().ServiceAccountLister().ServiceAccounts(source.GetNamespace()).Get(serviceAccountName)
	if err != nil {
		logrus.WithField("serviceAccouint", serviceAccountName).WithError(err).Debug("couldn't find service account in cache")
		return nil
	}
	return serviceAccount
}

func (c *ConfigMapRegistryReconciler) currentRole(source configMapCatalogSourceDecorator) *rbacv1.Role {
	roleName := source.Role().GetName()
	role, err := c.Lister.RbacV1().RoleLister().Roles(source.GetNamespace()).Get(roleName)
	if err != nil {
		logrus.WithField("role", roleName).WithError(err).Debug("couldn't find role in cache")
		return nil
	}
	return role
}

func (c *ConfigMapRegistryReconciler) currentRoleBinding(source configMapCatalogSourceDecorator) *rbacv1.RoleBinding {
	roleBindingName := source.RoleBinding().GetName()
	roleBinding, err := c.Lister.RbacV1().RoleBindingLister().RoleBindings(source.GetNamespace()).Get(roleBindingName)
	if err != nil {
		logrus.WithField("roleBinding", roleBindingName).WithError(err).Debug("couldn't find role binding in cache")
		return nil
	}
	return roleBinding
}

func (c *ConfigMapRegistryReconciler) currentPods(source configMapCatalogSourceDecorator, image string) ([]*corev1.Pod, error) {
	protoPod, err := source.Pod(image)
	if err != nil {
		return nil, err
	}
	podName := protoPod.GetName()
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(labels.SelectorFromSet(source.Selector()))
	if err != nil {
		logrus.WithField("pod", podName).WithError(err).Debug("couldn't find pod in cache")
		return nil, nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Selector()).Debug("multiple pods found for selector")
	}
	return pods, nil
}

func (c *ConfigMapRegistryReconciler) currentPodsWithCorrectResourceVersion(source configMapCatalogSourceDecorator, image string) ([]*corev1.Pod, error) {
	protoPod, err := source.Pod(image)
	if err != nil {
		return nil, err
	}
	podName := protoPod.GetName()
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(labels.SelectorFromValidatedSet(source.Labels()))
	if err != nil {
		logrus.WithField("pod", podName).WithError(err).Debug("couldn't find pod in cache")
		return nil, nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Labels()).Debug("multiple pods found for selector")
	}
	return pods, nil
}

// EnsureRegistryServer ensures that all components of registry server are up to date.
func (c *ConfigMapRegistryReconciler) EnsureRegistryServer(logger *logrus.Entry, catalogSource *v1alpha1.CatalogSource) error {
	if c.namespacePSAConfigCache == nil {
		c.namespacePSAConfigCache = make(map[string]operatorsv1alpha1.SecurityConfig)
	}
	source := configMapCatalogSourceDecorator{catalogSource, c, c.createPodAsUser}

	image := c.Image
	if source.Spec.SourceType == "grpc" {
		image = source.Spec.Image
	}
	if image == "" {
		return fmt.Errorf("no image for registry")
	}

	// if service status is nil, we force create every object to ensure they're created the first time
	overwrite := source.Status.RegistryServiceStatus == nil
	overwritePod := overwrite

	if source.Spec.SourceType == v1alpha1.SourceTypeConfigmap || source.Spec.SourceType == v1alpha1.SourceTypeInternal {
		// fetch configmap first, exit early if we can't find it
		// we use the live client here instead of a lister since our listers are scoped to objects with the olm.managed label,
		// and this configmap is a user-provided input to the catalog source and will not have that label
		configMap, err := c.OpClient.KubernetesInterface().CoreV1().ConfigMaps(source.GetNamespace()).Get(context.TODO(), source.Spec.ConfigMap, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("unable to find configmap %s/%s: %w", source.GetNamespace(), source.Spec.ConfigMap, err)
		}

		if source.ConfigMapChanges(configMap) {
			catalogSource.Status.ConfigMapResource = &v1alpha1.ConfigMapResourceReference{
				Name:            configMap.GetName(),
				Namespace:       configMap.GetNamespace(),
				UID:             configMap.GetUID(),
				ResourceVersion: configMap.GetResourceVersion(),
				LastUpdateTime:  c.now(),
			}

			// recreate the pod if there are configmap changes; this causes the db to be rebuilt
			overwritePod = true
		}

		// recreate the pod if no existing pod is serving the latest image
		current, err := c.currentPodsWithCorrectResourceVersion(source, image)
		if err != nil {
			return err
		}
		if len(current) == 0 {
			overwritePod = true
		}
	}

	//TODO: if any of these error out, we should write a status back (possibly set RegistryServiceStatus to nil so they get recreated)
	if err := c.ensureServiceAccount(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring service account: %s", source.serviceAccountName())
	}
	if err := c.ensureRole(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring role: %s", source.roleName())
	}
	if err := c.ensureRoleBinding(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring rolebinding: %s", source.RoleBinding().GetName())
	}
	pod, err := source.Pod(image)
	if err != nil {
		return err
	}
	if err := c.ensurePod(source, overwritePod); err != nil {
		return errors.Wrapf(err, "error ensuring pod: %s", pod.GetName())
	}
	service, err := source.Service()
	if err != nil {
		return err
	}
	if err := c.ensureService(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring service: %s", service.GetName())
	}

	if overwritePod {
		now := c.now()
		catalogSource.Status.RegistryServiceStatus = &v1alpha1.RegistryServiceStatus{
			CreatedAt:        now,
			Protocol:         "grpc",
			ServiceName:      service.GetName(),
			ServiceNamespace: source.GetNamespace(),
			Port:             fmt.Sprintf("%d", service.Spec.Ports[0].Port),
		}
	}
	return nil
}

func (c *ConfigMapRegistryReconciler) ensureServiceAccount(source configMapCatalogSourceDecorator, overwrite bool) error {
	serviceAccount := source.ServiceAccount()
	if c.currentServiceAccount(source) != nil {
		if !overwrite {
			return nil
		}
		if err := c.OpClient.DeleteServiceAccount(serviceAccount.GetNamespace(), serviceAccount.GetName(), metav1.NewDeleteOptions(0)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	_, err := c.OpClient.CreateServiceAccount(serviceAccount)
	return err
}

func (c *ConfigMapRegistryReconciler) ensureRole(source configMapCatalogSourceDecorator, overwrite bool) error {
	role := source.Role()
	if c.currentRole(source) != nil {
		if !overwrite {
			return nil
		}
		if err := c.OpClient.DeleteRole(role.GetNamespace(), role.GetName(), metav1.NewDeleteOptions(0)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	_, err := c.OpClient.CreateRole(role)
	return err
}

func (c *ConfigMapRegistryReconciler) ensureRoleBinding(source configMapCatalogSourceDecorator, overwrite bool) error {
	roleBinding := source.RoleBinding()
	if c.currentRoleBinding(source) != nil {
		if !overwrite {
			return nil
		}
		if err := c.OpClient.DeleteRoleBinding(roleBinding.GetNamespace(), roleBinding.GetName(), metav1.NewDeleteOptions(0)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	_, err := c.OpClient.CreateRoleBinding(roleBinding)
	return err
}

func (c *ConfigMapRegistryReconciler) ensurePod(source configMapCatalogSourceDecorator, overwrite bool) error {
	pod, err := source.Pod(c.Image)
	if err != nil {
		return err
	}
	currentPods, err := c.currentPods(source, c.Image)
	if err != nil {
		return err
	}
	if len(currentPods) > 0 {
		if !overwrite {
			return nil
		}
		for _, p := range currentPods {
			if err := c.OpClient.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).Delete(context.TODO(), p.GetName(), *metav1.NewDeleteOptions(1)); err != nil && !apierrors.IsNotFound(err) {
				return errors.Wrapf(err, "error deleting old pod: %s", p.GetName())
			}
		}
	}
	_, err = c.OpClient.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	return errors.Wrapf(err, "error creating new pod: %s", pod.GetGenerateName())
}

func (c *ConfigMapRegistryReconciler) ensureService(source configMapCatalogSourceDecorator, overwrite bool) error {
	service, err := source.Service()
	if err != nil {
		return err
	}
	svc, err := c.currentService(source)
	if err != nil {
		return err
	}
	if svc != nil {
		if !overwrite && ServiceHashMatch(svc, service) {
			return nil
		}
		if err := c.OpClient.DeleteService(service.GetNamespace(), service.GetName(), metav1.NewDeleteOptions(0)); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	_, err = c.OpClient.CreateService(service)
	return err
}

// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
func (c *ConfigMapRegistryReconciler) CheckRegistryServer(logger *logrus.Entry, catalogSource *v1alpha1.CatalogSource) (healthy bool, err error) {
	source := configMapCatalogSourceDecorator{catalogSource, c, c.createPodAsUser}

	image := c.Image
	if source.Spec.SourceType == "grpc" {
		image = source.Spec.Image
	}
	if image == "" {
		err = fmt.Errorf("no image for registry")
		return
	}

	if source.Spec.SourceType == v1alpha1.SourceTypeConfigmap || source.Spec.SourceType == v1alpha1.SourceTypeInternal {
		// we use the live client here instead of a lister since our listers are scoped to objects with the olm.managed label,
		// and this configmap is a user-provided input to the catalog source and will not have that label
		configMap, err := c.OpClient.KubernetesInterface().CoreV1().ConfigMaps(source.GetNamespace()).Get(context.TODO(), source.Spec.ConfigMap, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("unable to find configmap %s/%s: %w", source.GetNamespace(), source.Spec.ConfigMap, err)
		}

		if source.ConfigMapChanges(configMap) {
			return false, nil
		}

		// recreate the pod if no existing pod is serving the latest image
		current, err := c.currentPodsWithCorrectResourceVersion(source, image)
		if err != nil {
			return false, err
		}
		if len(current) == 0 {
			return false, nil
		}
	}

	// Check on registry resources
	// TODO: more complex checks for resources
	// TODO: add gRPC health check
	service, err := c.currentService(source)
	if err != nil {
		return false, err
	}
	pods, err := c.currentPods(source, c.Image)
	if err != nil {
		return false, err
	}
	if c.currentServiceAccount(source) == nil ||
		c.currentRole(source) == nil ||
		c.currentRoleBinding(source) == nil ||
		service == nil ||
		len(pods) < 1 {
		healthy = false
		return
	}

	healthy = true
	return
}
