package olm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	aextv1beta1 "k8s.io/apiextensions-apiserver/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/pkg/version"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/labeler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

// Fakes

type TestStrategy struct{}

func (t *TestStrategy) GetStrategyName() string {
	return "teststrategy"
}

type TestInstaller struct {
	installErr      error
	checkInstallErr error
}

func NewTestInstaller(installErr error, checkInstallErr error) install.StrategyInstaller {
	return &TestInstaller{
		installErr:      installErr,
		checkInstallErr: checkInstallErr,
	}
}

func (i *TestInstaller) Install(s install.Strategy) error {
	return i.installErr
}

func (i *TestInstaller) CheckInstalled(s install.Strategy) (bool, error) {
	if i.checkInstallErr != nil {
		return false, i.checkInstallErr
	}
	return true, nil
}

func ownerLabelFromCSV(name, namespace string) map[string]string {
	return map[string]string{
		ownerutil.OwnerKey:          name,
		ownerutil.OwnerNamespaceKey: namespace,
		ownerutil.OwnerKind:         v1alpha1.ClusterServiceVersionKind,
	}
}

func apiResourcesForObjects(objs []runtime.Object) []*metav1.APIResourceList {
	apis := []*metav1.APIResourceList{}
	for _, o := range objs {
		switch o.(type) {
		case *v1beta1.CustomResourceDefinition:
			crd := o.(*v1beta1.CustomResourceDefinition)
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: crd.Spec.Group, Version: crd.Spec.Versions[0].Name}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:         crd.GetName(),
						SingularName: crd.Spec.Names.Singular,
						Namespaced:   crd.Spec.Scope == v1beta1.NamespaceScoped,
						Group:        crd.Spec.Group,
						Version:      crd.Spec.Versions[0].Name,
						Kind:         crd.Spec.Names.Kind,
					},
				},
			})
		case *apiregistrationv1.APIService:
			a := o.(*apiregistrationv1.APIService)
			names := strings.Split(a.Name, ".")
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: names[1], Version: a.Spec.Version}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:    names[1],
						Group:   names[1],
						Version: a.Spec.Version,
						Kind:    names[1] + "Kind",
					},
				},
			})
		}
	}
	return apis
}

// NewFakeOperator creates a new operator using fake clients
func NewFakeOperator(clientObjs []runtime.Object, k8sObjs []runtime.Object, extObjs []runtime.Object, regObjs []runtime.Object, strategyResolver install.StrategyResolverInterface, apiReconciler resolver.APIIntersectionReconciler, apiLabeler labeler.Labeler, namespaces []string, stopCh <-chan struct{}) (*Operator, []cache.InformerSynced, error) {
	// Create client fakes
	clientFake := fake.NewSimpleClientset(clientObjs...)
	k8sClientFake := k8sfake.NewSimpleClientset(k8sObjs...)
	k8sClientFake.Resources = apiResourcesForObjects(append(extObjs, regObjs...))
	opClientFake := operatorclient.NewClient(k8sClientFake, apiextensionsfake.NewSimpleClientset(extObjs...), apiregistrationfake.NewSimpleClientset(regObjs...))

	eventRecorder, err := event.NewRecorder(opClientFake.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
	if err != nil {
		return nil, nil, err
	}

	if apiReconciler == nil {
		// Use the default reconciler if one isn't given
		apiReconciler = resolver.APIIntersectionReconcileFunc(resolver.ReconcileAPIIntersection)
	}

	if apiLabeler == nil {
		apiLabeler = labeler.Func(resolver.LabelSetsFor)
	}

	// Create the new operator
	queueOperator, err := queueinformer.NewOperatorFromClient(opClientFake, logrus.StandardLogger())
	op := &Operator{
		Operator:      queueOperator,
		client:        clientFake,
		resolver:      strategyResolver,
		apiReconciler: apiReconciler,
		lister:        operatorlister.NewLister(),
		csvQueueSet:   queueinformer.NewEmptyResourceQueueSet(),
		recorder:      eventRecorder,
		apiLabeler:    apiLabeler,
	}

	wakeupInterval := 5 * time.Minute

	informerFactory := informers.NewSharedInformerFactory(opClientFake.KubernetesInterface(), wakeupInterval)
	roleInformer := informerFactory.Rbac().V1().Roles()
	roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
	clusterRoleInformer := informerFactory.Rbac().V1().ClusterRoles()
	clusterRoleBindingInformer := informerFactory.Rbac().V1().ClusterRoleBindings()
	secretInformer := informerFactory.Core().V1().Secrets()
	serviceInformer := informerFactory.Core().V1().Services()
	serviceAccountInformer := informerFactory.Core().V1().ServiceAccounts()
	namespaceInformer := informerFactory.Core().V1().Namespaces()
	apiServiceInformer := kagg.NewSharedInformerFactory(opClientFake.ApiregistrationV1Interface(), wakeupInterval).Apiregistration().V1().APIServices()
	customResourceDefinitionInformer := aextv1beta1.NewSharedInformerFactory(opClientFake.ApiextensionsV1beta1Interface(), wakeupInterval).Apiextensions().V1beta1().CustomResourceDefinitions()

	// Register informers
	informerList := []cache.SharedIndexInformer{
		roleInformer.Informer(),
		roleBindingInformer.Informer(),
		clusterRoleInformer.Informer(),
		clusterRoleBindingInformer.Informer(),
		secretInformer.Informer(),
		serviceInformer.Informer(),
		serviceAccountInformer.Informer(),
		namespaceInformer.Informer(),
		apiServiceInformer.Informer(),
		customResourceDefinitionInformer.Informer(),
	}

	csvIndexes := map[string]cache.Indexer{}
	for _, ns := range namespaces {
		csvInformer := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(ns)).Operators().V1alpha1().ClusterServiceVersions()
		op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(ns, csvInformer.Lister())
		operatorGroupInformer := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(ns)).Operators().V1().OperatorGroups()
		op.lister.OperatorsV1().RegisterOperatorGroupLister(ns, operatorGroupInformer.Lister())
		deploymentInformer := informers.NewSharedInformerFactoryWithOptions(opClientFake.KubernetesInterface(), wakeupInterval, informers.WithNamespace(ns)).Apps().V1().Deployments()
		op.lister.AppsV1().RegisterDeploymentLister(ns, deploymentInformer.Lister())
		informerList = append(informerList, []cache.SharedIndexInformer{csvInformer.Informer(), operatorGroupInformer.Informer(), deploymentInformer.Informer()}...)
		csvIndexes[ns] = csvInformer.Informer().GetIndexer()
	}
	// Register separate queue for copying csvs
	csvCopyQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "csvCopy")
	csvQueueIndexer := queueinformer.NewQueueIndexer(csvCopyQueue, csvIndexes, op.syncCopyCSV, "csvCopy", logrus.StandardLogger(), metrics.NewMetricsNil())
	op.RegisterQueueIndexer(csvQueueIndexer)
	op.copyQueueIndexer = csvQueueIndexer

	// Register listers
	op.lister.RbacV1().RegisterRoleLister(metav1.NamespaceAll, roleInformer.Lister())
	op.lister.RbacV1().RegisterRoleBindingLister(metav1.NamespaceAll, roleBindingInformer.Lister())
	op.lister.RbacV1().RegisterClusterRoleLister(clusterRoleInformer.Lister())
	op.lister.RbacV1().RegisterClusterRoleBindingLister(clusterRoleBindingInformer.Lister())
	op.lister.CoreV1().RegisterSecretLister(metav1.NamespaceAll, secretInformer.Lister())
	op.lister.CoreV1().RegisterServiceLister(metav1.NamespaceAll, serviceInformer.Lister())
	op.lister.CoreV1().RegisterServiceAccountLister(metav1.NamespaceAll, serviceAccountInformer.Lister())
	op.lister.CoreV1().RegisterNamespaceLister(namespaceInformer.Lister())
	op.lister.APIRegistrationV1().RegisterAPIServiceLister(apiServiceInformer.Lister())
	op.lister.APIExtensionsV1beta1().RegisterCustomResourceDefinitionLister(customResourceDefinitionInformer.Lister())

	var hasSyncedCheckFns []cache.InformerSynced
	for _, informer := range informerList {
		op.RegisterInformer(informer)
		hasSyncedCheckFns = append(hasSyncedCheckFns, informer.HasSynced)
		go informer.Run(stopCh)
	}

	if ok := cache.WaitForCacheSync(stopCh, hasSyncedCheckFns...); !ok {
		return nil, nil, fmt.Errorf("failed to wait for caches to sync")
	}

	return op, hasSyncedCheckFns, nil
}

func (o *Operator) GetClient() versioned.Interface {
	return o.client
}

func buildFakeAPIIntersectionReconcilerThatReturns(result resolver.APIReconciliationResult) *fakes.FakeAPIIntersectionReconciler {
	reconciler := &fakes.FakeAPIIntersectionReconciler{}
	reconciler.ReconcileReturns(result)
	return reconciler
}

// Tests

func deployment(deploymentName, namespace, serviceAccountName string, templateAnnotations map[string]string) *appsv1.Deployment {
	var singleInstance = int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": deploymentName,
				},
			},
			Replicas: &singleInstance,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": deploymentName,
					},
					Annotations: templateAnnotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:  deploymentName + "-c1",
							Image: "nginx:1.7.9",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
						},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:          singleInstance,
			ReadyReplicas:     singleInstance,
			AvailableReplicas: singleInstance,
			UpdatedReplicas:   singleInstance,
		},
	}
}

func serviceAccount(name, namespace string) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	return serviceAccount
}

func service(name, namespace, deploymentName string, targetPort int) *corev1.Service {
	service := &corev1.Service{
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       int32(443),
					TargetPort: intstr.FromInt(targetPort),
				},
			},
			Selector: map[string]string{
				"app": deploymentName,
			},
		},
	}
	service.SetName(name)
	service.SetNamespace(namespace)

	return service
}

func clusterRoleBinding(name, clusterRoleName, serviceAccountName, serviceAccountNamespace string) *rbacv1.ClusterRoleBinding {
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      serviceAccountName,
				Namespace: serviceAccountNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
		},
	}
	clusterRoleBinding.SetName(name)

	return clusterRoleBinding
}

func clusterRole(name string, rules []rbacv1.PolicyRule) *rbacv1.ClusterRole {
	clusterRole := &rbacv1.ClusterRole{
		Rules: rules,
	}
	clusterRole.SetName(name)

	return clusterRole
}

func role(name, namespace string, rules []rbacv1.PolicyRule) *rbacv1.Role {
	role := &rbacv1.Role{
		Rules: rules,
	}
	role.SetName(name)
	role.SetNamespace(namespace)

	return role
}

func roleBinding(name, namespace, roleName, serviceAccountName, serviceAccountNamespace string) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      serviceAccountName,
				Namespace: serviceAccountNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     roleName,
		},
	}
	roleBinding.SetName(name)
	roleBinding.SetNamespace(namespace)

	return roleBinding
}

func tlsSecret(name, namespace string, certPEM, privPEM []byte) *corev1.Secret {
	secret := &corev1.Secret{
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": privPEM,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret.SetName(name)
	secret.SetNamespace(namespace)

	return secret
}

func keyPairToTLSSecret(name, namespace string, kp *certs.KeyPair) *corev1.Secret {
	certPEM, privPEM, err := kp.ToPEM()
	if err != nil {
		panic(err)
	}

	return tlsSecret(name, namespace, certPEM, privPEM)
}

func signedServingPair(notAfter time.Time, ca *certs.KeyPair, hosts []string) *certs.KeyPair {
	servingPair, err := certs.CreateSignedServingPair(notAfter, Organization, ca, hosts)
	if err != nil {
		panic(err)
	}

	return servingPair
}

func withAnnotations(obj runtime.Object, annotations map[string]string) runtime.Object {
	meta, ok := obj.(metav1.Object)
	if !ok {
		panic("could not find metadata on object")
	}
	meta.SetAnnotations(annotations)
	return meta.(runtime.Object)
}

func csvWithAnnotations(csv *v1alpha1.ClusterServiceVersion, annotations map[string]string) *v1alpha1.ClusterServiceVersion {
	return withAnnotations(csv, annotations).(*v1alpha1.ClusterServiceVersion)
}

func withLabels(obj runtime.Object, labels map[string]string) runtime.Object {
	meta, ok := obj.(metav1.Object)
	if !ok {
		panic("could not find metadata on object")
	}
	meta.SetLabels(labels)
	return meta.(runtime.Object)
}

func csvWithLabels(csv *v1alpha1.ClusterServiceVersion, labels map[string]string) *v1alpha1.ClusterServiceVersion {
	return withLabels(csv, labels).(*v1alpha1.ClusterServiceVersion)
}

func addAnnotations(annotations map[string]string, add map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range annotations {
		out[k] = v
	}
	for k, v := range add {
		out[k] = v
	}
	return out
}

func addAnnotation(obj runtime.Object, key string, value string) runtime.Object {
	meta, ok := obj.(metav1.Object)
	if !ok {
		panic("could not find metadata on object")
	}
	return withAnnotations(obj, addAnnotations(meta.GetAnnotations(), map[string]string{key: value}))
}

func csvWithStatusReason(csv *v1alpha1.ClusterServiceVersion, reason v1alpha1.ConditionReason) *v1alpha1.ClusterServiceVersion {
	out := csv.DeepCopy()
	out.Status.Reason = reason
	return csv
}

func installStrategy(deploymentName string, permissions []install.StrategyDeploymentPermissions, clusterPermissions []install.StrategyDeploymentPermissions) v1alpha1.NamedInstallStrategy {
	var singleInstance = int32(1)
	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: deploymentName,
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": deploymentName,
						},
					},
					Replicas: &singleInstance,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": deploymentName,
							},
						},
						Spec: corev1.PodSpec{
							ServiceAccountName: "sa",
							Containers: []corev1.Container{
								{
									Name:  deploymentName + "-c1",
									Image: "nginx:1.7.9",
									Ports: []corev1.ContainerPort{
										{
											ContainerPort: 80,
										},
									},
								},
							},
						},
					},
				},
			},
		},
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
	}
	strategyRaw, err := json.Marshal(strategy)
	if err != nil {
		panic(err)
	}

	return v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}
}

func csv(
	name, namespace, minKubeVersion, replaces string,
	installStrategy v1alpha1.NamedInstallStrategy,
	owned, required []*v1beta1.CustomResourceDefinition,
	phase v1alpha1.ClusterServiceVersionPhase,
) *v1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, crd := range required {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: crd.GetName(), Version: crd.Spec.Versions[0].Name, Kind: crd.Spec.Names.Kind})
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, crd := range owned {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: crd.GetName(), Version: crd.Spec.Versions[0].Name, Kind: crd.Spec.Names.Kind})
	}

	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			MinKubeVersion:  minKubeVersion,
			Replaces:        replaces,
			InstallStrategy: installStrategy,
			InstallModes: []v1alpha1.InstallMode{
				{
					Type:      v1alpha1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeMultiNamespace,
					Supported: true,
				},
				{
					Type:      v1alpha1.InstallModeTypeAllNamespaces,
					Supported: true,
				},
			},
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{
			Phase: phase,
		},
	}
}

func withConditionReason(csv *v1alpha1.ClusterServiceVersion, reason v1alpha1.ConditionReason) *v1alpha1.ClusterServiceVersion {
	csv.Status.Reason = reason
	return csv
}

func withPhase(csv *v1alpha1.ClusterServiceVersion, phase v1alpha1.ClusterServiceVersionPhase, reason v1alpha1.ConditionReason, message string, now metav1.Time) *v1alpha1.ClusterServiceVersion {
	csv.SetPhase(phase, reason, message, now)
	return csv
}

func withCertInfo(csv *v1alpha1.ClusterServiceVersion, rotateAt metav1.Time, lastUpdated metav1.Time) *v1alpha1.ClusterServiceVersion {
	csv.Status.CertsRotateAt = rotateAt
	csv.Status.CertsLastUpdated = lastUpdated
	return csv
}

func withAPIServices(csv *v1alpha1.ClusterServiceVersion, owned, required []v1alpha1.APIServiceDescription) *v1alpha1.ClusterServiceVersion {
	csv.Spec.APIServiceDefinitions = v1alpha1.APIServiceDefinitions{
		Owned:    owned,
		Required: required,
	}
	return csv
}

func withInstallModes(csv *v1alpha1.ClusterServiceVersion, installModes []v1alpha1.InstallMode) *v1alpha1.ClusterServiceVersion {
	csv.Spec.InstallModes = installModes
	return csv

}

func apis(apis ...string) []v1alpha1.APIServiceDescription {
	descs := []v1alpha1.APIServiceDescription{}
	for _, av := range apis {
		split := strings.Split(av, ".")
		descs = append(descs, v1alpha1.APIServiceDescription{
			Group:          split[0],
			Version:        split[1],
			Kind:           split[2],
			DeploymentName: split[0],
		})
	}
	return descs
}

func apiService(group, version, serviceName, serviceNamespace, deploymentName string, caBundle []byte, availableStatus apiregistrationv1.ConditionStatus, ownerLabel map[string]string) *apiregistrationv1.APIService {
	apiService := &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Labels: ownerLabel,
		},
		Spec: apiregistrationv1.APIServiceSpec{
			Group:                group,
			Version:              version,
			GroupPriorityMinimum: int32(2000),
			VersionPriority:      int32(15),
			CABundle:             caBundle,
			Service: &apiregistrationv1.ServiceReference{
				Name:      serviceName,
				Namespace: serviceNamespace,
			},
		},
		Status: apiregistrationv1.APIServiceStatus{
			Conditions: []apiregistrationv1.APIServiceCondition{
				{
					Type:   apiregistrationv1.Available,
					Status: availableStatus,
				},
			},
		},
	}
	apiServiceName := fmt.Sprintf("%s.%s", version, group)
	apiService.SetName(apiServiceName)

	return apiService
}

func crd(name, version, group string) *v1beta1.CustomResourceDefinition {
	return &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "." + group,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: group,
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name:    version,
					Storage: true,
					Served:  true,
				},
			},
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
		Status: v1beta1.CustomResourceDefinitionStatus{
			Conditions: []v1beta1.CustomResourceDefinitionCondition{
				{
					Type:   v1beta1.Established,
					Status: v1beta1.ConditionTrue,
				},
				{
					Type:   v1beta1.NamesAccepted,
					Status: v1beta1.ConditionTrue,
				},
			},
		},
	}
}

func generateCA(notAfter time.Time, organization string) (*certs.KeyPair, error) {
	notBefore := time.Now()

	serial, err := rand.Int(rand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}

	caDetails := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{organization},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	publicKey := &privateKey.PublicKey
	certRaw, err := x509.CreateCertificate(rand.Reader, caDetails, caDetails, publicKey, privateKey)
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(certRaw)
	if err != nil {
		return nil, err
	}

	ca := &certs.KeyPair{
		Cert: cert,
		Priv: privateKey,
	}

	return ca, nil
}

func TestTransitionCSV(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	namespace := "ns"

	apiHash, err := resolver.APIKeyToGVKHash(registry.APIKey{Group: "g1", Version: "v1", Kind: "c1"})
	require.NoError(t, err)

	defaultOperatorGroup := &v1.OperatorGroup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OperatorGroup",
			APIVersion: v1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: namespace,
		},
		Spec: v1.OperatorGroupSpec{},
		Status: v1.OperatorGroupStatus{
			Namespaces: []string{namespace},
		},
	}

	defaultTemplateAnnotations := map[string]string{
		v1.OperatorGroupTargetsAnnotationKey:   namespace,
		v1.OperatorGroupNamespaceAnnotationKey: namespace,
		v1.OperatorGroupAnnotationKey:          defaultOperatorGroup.GetName(),
	}

	// Generate valid and expired CA fixtures
	validCA, err := generateCA(time.Now().Add(10*365*24*time.Hour), Organization)
	require.NoError(t, err)
	validCAPEM, _, err := validCA.ToPEM()
	require.NoError(t, err)
	validCAHash := certs.PEMSHA256(validCAPEM)

	expiredCA, err := generateCA(time.Now(), Organization)
	require.NoError(t, err)
	expiredCAPEM, _, err := expiredCA.ToPEM()
	require.NoError(t, err)
	expiredCAHash := certs.PEMSHA256(expiredCAPEM)

	type csvState struct {
		exists bool
		phase  v1alpha1.ClusterServiceVersionPhase
		reason v1alpha1.ConditionReason
	}
	type operatorConfig struct {
		apiReconciler resolver.APIIntersectionReconciler
		apiLabeler    labeler.Labeler
	}
	type initial struct {
		csvs       []runtime.Object
		clientObjs []runtime.Object
		crds       []runtime.Object
		objs       []runtime.Object
		apis       []runtime.Object
	}
	type expected struct {
		csvStates map[string]csvState
		objs      []runtime.Object
		err       map[string]error
	}
	tests := []struct {
		name     string
		config   operatorConfig
		initial  initial
		expected expected
	}{
		{
			name: "SingleCSVNoneToPending/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "SingleCSVNoneToPending/APIService/Required",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations), nil, apis("a1.corev1.a1Kind")),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "a1Kind.corev1.a1")},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "SingleCSVPendingToFailed/BadStrategy",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "SingleCSVPendingToFailed/BadStrategyPermissions",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]install.StrategyDeploymentPermissions{
								{
									ServiceAccountName: "sa",
									Rules: []rbacv1.PolicyRule{
										{
											Verbs:           []string{"*"},
											Resources:       []string{"*"},
											NonResourceURLs: []string{"/osb"},
										},
									},
								},
							}),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					&corev1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sa",
							Namespace: namespace,
						},
					},
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds:       []runtime.Object{},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Required/Missing",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations), nil, apis("a1.v1.a1Kind")),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Required/Unavailable",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations), nil, apis("a1.v1.a1Kind")),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{apiService("a1", "v1", "", "", "", validCAPEM, apiregistrationv1.ConditionFalse, ownerLabelFromCSV("csv1", namespace))},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Required/Unknown",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations), nil, apis("a1.v1.a1Kind")),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{apiService("a1", "v1", "", "", "", validCAPEM, apiregistrationv1.ConditionUnknown, ownerLabelFromCSV("csv1", namespace))},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Owned/DeploymentNotFound",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("b1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1,a1Kind.v1.a1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "CSVPendingToFailed/CRDOwnerConflict",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonOwnerConflict},
				},
				err: map[string]error{
					"csv2": ErrCRDOwnerConflict,
				},
			},
		},
		{
			name: "CSVPendingToFailed/APIServiceOwnerConflict",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
						apis("a1.v1.a1Kind"), nil), metav1.NewTime(time.Now().Add(24*time.Hour)), metav1.NewTime(time.Now())),
					withAPIServices(csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
						apis("a1.v1.a1Kind"), nil),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "a1Kind.v1.a1")},
				apis:       []runtime.Object{apiService("a1", "v1", "v1-a1", namespace, "", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace))},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonOwnerConflict},
				},
				err: map[string]error{
					"csv2": ErrAPIServiceOwnerConflict,
				},
			},
		},
		{
			name: "SingleCSVFailedToPending/Deployment",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonNeedsReinstall},
				},
			},
		},
		{
			name: "SingleCSVFailedToPending/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonRequirementsNotMet},
				},
			},
		},
		{
			name: "SingleCSVFailedToFailed/BadStrategy",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonInvalidStrategy},
				},
			},
		},
		{
			name: "SingleCSVPendingToInstallReady/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady},
				},
			},
		},
		{
			name: "SingleCSVPendingToInstallReady/APIService/Required",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations), nil, apis("a1.v1.a1Kind")),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{apiService("a1", "v1", "", "", "", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace))},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToInstalling",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstalling},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToInstalling/APIService/Owned",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1,a1Kind.v1.a1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstalling},
				},
			},
		},
		{
			name: "SingleCSVSucceededToPending/APIService/Owned/CertRotation",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonNeedsCertRotation},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/APIService/Owned/BadCAHash/Deployment",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: "a-pretty-bad-hash",
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonAPIServiceResourceIssue},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/APIService/Owned/BadCAHash/Secret",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: "also-a-pretty-bad-hash",
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonAPIServiceResourceIssue},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/APIService/Owned/BadCAHash/DeploymentAndSecret",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: "a-pretty-bad-hash",
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: "also-a-pretty-bad-hash",
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonAPIServiceResourceIssue},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/APIService/Owned/BadCA",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", []byte("a-bad-ca"), apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonAPIServiceResourceIssue},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/APIService/Owned/BadServingCert",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(tlsSecret("v1.a1-cert", namespace, []byte("bad-cert"), []byte("bad-key")), map[string]string{
						OLMCAHashAnnotationKey: validCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonAPIServiceResourceIssue},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/APIService/Owned/ExpiredCA",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", expiredCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: expiredCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), expiredCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: expiredCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonAPIServiceResourceIssue},
				},
			},
		},
		{
			name: "SingleCSVFailedToPending/APIService/Owned/ExpiredCA",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", expiredCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						OLMCAHashAnnotationKey: expiredCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), expiredCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						OLMCAHashAnnotationKey: expiredCAHash,
					}),
					service("v1-a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("v1.a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"v1.a1-cert"},
						},
					}),
					roleBinding("v1.a1-cert", namespace, "v1.a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("v1.a1-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
					clusterRole("system:auth-delegator", []rbacv1.PolicyRule{
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"tokenreviews"},
						},
						{
							Verbs:     []string{"create"},
							APIGroups: []string{"authentication.k8s.io"},
							Resources: []string{"subjectaccessreviews"},
						},
					}),
					clusterRoleBinding("v1.a1-system:auth-delegator", "system:auth-delegator", "sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonAPIServiceResourcesNeedReinstall},
				},
			},
		},
		{
			name: "SingleCSVFailedToPending/InstallModes/Owned/PreviouslyUnsupported",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonUnsupportedOperatorGroup),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonRequirementsUnknown},
				},
			},
		},
		{
			name: "SingleCSVFailedToPending/InstallModes/Owned/PreviouslyNoOperatorGroups",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonNoOperatorGroup),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonRequirementsUnknown},
				},
			},
		},
		{
			name: "SingleCSVFailedToPending/InstallModes/Owned/PreviouslyTooManyOperatorGroups",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonTooManyOperatorGroups),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonRequirementsUnknown},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/InstallModes/Owned/Unsupported",
			initial: initial{
				csvs: []runtime.Object{
					withInstallModes(withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonInstallSuccessful),
						[]v1alpha1.InstallMode{
							{
								Type:      v1alpha1.InstallModeTypeSingleNamespace,
								Supported: false,
							},
						},
					),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis:       []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonUnsupportedOperatorGroup},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/InstallModes/Owned/NoOperatorGroups",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonInstallSuccessful),
				},
				apis: []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonNoOperatorGroup},
				},
				err: map[string]error{
					"csv1": fmt.Errorf("csv in namespace with no operatorgroups"),
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/InstallModes/Owned/TooManyOperatorGroups",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonInstallSuccessful),
				},
				clientObjs: []runtime.Object{
					defaultOperatorGroup,
					&v1.OperatorGroup{
						TypeMeta: metav1.TypeMeta{
							Kind:       "OperatorGroup",
							APIVersion: v1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default-2",
							Namespace: namespace,
						},
						Spec: v1.OperatorGroupSpec{},
						Status: v1.OperatorGroupStatus{
							Namespaces: []string{namespace},
						},
					},
				},
				apis: []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonTooManyOperatorGroups},
				},
				err: map[string]error{
					"csv1": fmt.Errorf("csv created in namespace with multiple operatorgroups, can't pick one automatically"),
				},
			},
		},
		{
			name: "SingleCSVSucceededToSucceeded/OperatorGroupChanged",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonInstallSuccessful),
				},
				clientObjs: []runtime.Object{
					&v1.OperatorGroup{
						TypeMeta: metav1.TypeMeta{
							Kind:       "OperatorGroup",
							APIVersion: v1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "default",
							Namespace: namespace,
						},
						Spec: v1.OperatorGroupSpec{},
						Status: v1.OperatorGroupStatus{
							Namespaces: []string{namespace, "new-namespace"},
						},
					},
				},
				apis: []runtime.Object{},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", defaultTemplateAnnotations),
					serviceAccount("sa", namespace),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded, reason: v1alpha1.CSVReasonInstallSuccessful},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToFailed/BadStrategy",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "SingleCSVInstallingToSucceeded/UnmanagedDeploymentNotAffected",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("extra-dep", namespace, "sa", nil),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
				objs: []runtime.Object{
					deployment("extra-dep", namespace, "sa", nil),
				},
			},
		},
		{
			name: "SingleCSVSucceededToSucceeded/UnmanagedDeploymentInNamespace",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), v1alpha1.CSVReasonInstallSuccessful),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						map[string]string{
							ownerutil.OwnerKey:          "csv1",
							ownerutil.OwnerNamespaceKey: namespace,
							ownerutil.OwnerKind:         "ClusterServiceVersion",
						},
					),
					deployment("extra-dep", namespace, "sa", nil),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
				objs: []runtime.Object{
					deployment("extra-dep", namespace, "sa", nil),
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/CRD",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "CSVSucceededToReplacing",
			initial: initial{
				csvs: []runtime.Object{
					withAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
					"csv2": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "CSVReplacingToDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVDeletedToGone",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleReplacingToDeleted",
			initial: initial{
				// order matters in this test case - we want to apply the latest CSV first to test the GC marking
				csvs: []runtime.Object{
					csvWithLabels(csvWithAnnotations(csv("csv3",
						namespace,
						"0.0.0",
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), labels.Set{
						resolver.APILabelKeyPrefix + apiHash: "provided",
					}),
					csvWithLabels(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations), labels.Set{
						resolver.APILabelKeyPrefix + apiHash: "provided",
					}),
					csvWithLabels(csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations), labels.Set{
						resolver.APILabelKeyPrefix + apiHash: "provided",
					}),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv3",
						namespace,
						"0.0.0",
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone/AfterOneDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv3",
						namespace,
						"0.0.0",
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone/AfterTwoDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv3",
						namespace,
						"0.0.0",
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
					deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv2": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name:   "SingleCSVNoneToFailed/InterOperatorGroupOwnerConflict",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.APIConflict)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonInterOperatorGroupOwnerConflict},
				},
			},
		},
		{
			name:   "SingleCSVNoneToNone/AddAPIs",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.AddAPIs)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseNone},
				},
			},
		},
		{
			name:   "SingleCSVNoneToNone/RemoveAPIs",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.RemoveAPIs)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseNone},
				},
			},
		},
		{
			name:   "SingleCSVNoneToFailed/StaticOperatorGroup/AddAPIs",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.AddAPIs)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{
					func() *v1.OperatorGroup {
						// Make the default OperatorGroup static
						static := defaultOperatorGroup.DeepCopy()
						static.Spec.StaticProvidedAPIs = true
						return static
					}(),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs},
				},
			},
		},
		{
			name:   "SingleCSVNoneToFailed/StaticOperatorGroup/RemoveAPIs",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.RemoveAPIs)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{
					func() *v1.OperatorGroup {
						// Make the default OperatorGroup static
						static := defaultOperatorGroup.DeepCopy()
						static.Spec.StaticProvidedAPIs = true
						return static
					}(),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs},
				},
			},
		},
		{
			name:   "SingleCSVNoneToPending/StaticOperatorGroup/NoAPIConflict",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.NoAPIConflict)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{
					func() *v1.OperatorGroup {
						// Make the default OperatorGroup static
						static := defaultOperatorGroup.DeepCopy()
						static.Spec.StaticProvidedAPIs = true
						return static
					}(),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name:   "SingleCSVFailedToPending/InterOperatorGroupOwnerConflict/NoAPIConflict",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.NoAPIConflict)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csvWithStatusReason(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), v1alpha1.CSVReasonInterOperatorGroupOwnerConflict), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name:   "SingleCSVFailedToPending/StaticOperatorGroup/CannotModifyStaticOperatorGroupProvidedAPIs/NoAPIConflict",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.NoAPIConflict)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csvWithStatusReason(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{
					func() *v1.OperatorGroup {
						// Make the default OperatorGroup static
						static := defaultOperatorGroup.DeepCopy()
						static.Spec.StaticProvidedAPIs = true
						return static
					}(),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name:   "SingleCSVFailedToFailed/InterOperatorGroupOwnerConflict/APIConflict",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.APIConflict)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csvWithStatusReason(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), v1alpha1.CSVReasonInterOperatorGroupOwnerConflict), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonInterOperatorGroupOwnerConflict},
				},
			},
		},
		{
			name:   "SingleCSVFailedToFailed/StaticOperatorGroup/CannotModifyStaticOperatorGroupProvidedAPIs/AddAPIs",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.AddAPIs)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csvWithStatusReason(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{
					func() *v1.OperatorGroup {
						// Make the default OperatorGroup static
						static := defaultOperatorGroup.DeepCopy()
						static.Spec.StaticProvidedAPIs = true
						return static
					}(),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs},
				},
			},
		},
		{
			name:   "SingleCSVFailedToFailed/StaticOperatorGroup/CannotModifyStaticOperatorGroupProvidedAPIs/RemoveAPIs",
			config: operatorConfig{apiReconciler: buildFakeAPIIntersectionReconcilerThatReturns(resolver.RemoveAPIs)},
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csvWithStatusReason(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{
					func() *v1.OperatorGroup {
						// Make the default OperatorGroup static
						static := defaultOperatorGroup.DeepCopy()
						static.Spec.StaticProvidedAPIs = true
						return static
					}(),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed, reason: v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			namespaceObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			tt.initial.objs = append(tt.initial.objs, namespaceObj)
			clientObjs := append(tt.initial.csvs, tt.initial.clientObjs...)

			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			op, _, err := NewFakeOperator(clientObjs, tt.initial.objs, tt.initial.crds, tt.initial.apis, &install.StrategyResolver{}, tt.config.apiReconciler, tt.config.apiLabeler, []string{namespace}, stopCh)
			require.NoError(t, err)

			// run csv sync for each CSV
			for _, csv := range tt.initial.csvs {
				err := op.syncClusterServiceVersion(csv)
				expectedErr := tt.expected.err[csv.(*v1alpha1.ClusterServiceVersion).Name]
				require.Equal(t, expectedErr, err)
			}

			// get csvs in the cluster
			outCSVMap := map[string]*v1alpha1.ClusterServiceVersion{}
			outCSVs, err := op.GetClient().OperatorsV1alpha1().ClusterServiceVersions("ns").List(metav1.ListOptions{})
			require.NoError(t, err)
			for _, csv := range outCSVs.Items {
				outCSVMap[csv.GetName()] = csv.DeepCopy()
			}

			// verify expectations of csvs in cluster
			for csvName, csvState := range tt.expected.csvStates {
				csv, ok := outCSVMap[csvName]
				require.Equal(t, ok, csvState.exists, "%s existence should be %t", csvName, csvState.exists)
				if csvState.exists {
					require.EqualValues(t, string(csvState.phase), string(csv.Status.Phase), "%s had incorrect phase", csvName)
					if csvState.reason != "" {
						require.EqualValues(t, string(csvState.reason), string(csv.Status.Reason), "%s had incorrect condition reason", csvName)
					}
				}
			}

			// Verify other objects
			if tt.expected.objs != nil {
				RequireObjectsInNamespace(t, op.OpClient, op.client, namespace, tt.expected.objs)
			}
		})
	}
}

func TestSyncOperatorGroups(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	nowTime := metav1.Date(2006, time.January, 2, 15, 4, 5, 0, time.FixedZone("MST", -7*3600))
	timeNow = func() metav1.Time { return nowTime }

	operatorNamespace := "operator-ns"
	targetNamespace := "target-ns"

	serviceAccount := serviceAccount("sa", operatorNamespace)

	permissions := []install.StrategyDeploymentPermissions{
		{
			ServiceAccountName: serviceAccount.GetName(),
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get"},
					APIGroups: []string{"my.api.group"},
					Resources: []string{"apis"},
				},
			},
		},
	}

	crd := crd("c1", "v1", "fake.api.group")
	operatorCSV := csvWithLabels(csv("csv1",
		operatorNamespace,
		"0.0.0",
		"",
		installStrategy("csv1-dep1", permissions, nil),
		[]*v1beta1.CustomResourceDefinition{crd},
		[]*v1beta1.CustomResourceDefinition{},
		v1alpha1.CSVPhaseNone,
	), labels.Set{resolver.APILabelKeyPrefix + "9f4c46c37bdff8d0": "provided"})

	serverVersion := version.Get().String()
	// after state transitions from operatorgroups, this is the operator csv we expect
	operatorCSVFinal := operatorCSV.DeepCopy()
	operatorCSVFinal.Status.Phase = v1alpha1.CSVPhaseSucceeded
	operatorCSVFinal.Status.Message = "install strategy completed with no errors"
	operatorCSVFinal.Status.Reason = v1alpha1.CSVReasonInstallSuccessful
	operatorCSVFinal.Status.LastUpdateTime = timeNow()
	operatorCSVFinal.Status.LastTransitionTime = timeNow()
	operatorCSVFinal.Status.RequirementStatus = []v1alpha1.RequirementStatus{
		{
			Group:   "operators.coreos.com",
			Version: "v1alpha1",
			Kind:    "ClusterServiceVersion",
			Name:    "csv1",
			Status:  v1alpha1.RequirementStatusReasonPresent,
			Message: "CSV minKubeVersion (0.0.0) less than server version (" + serverVersion + ")",
		},
		{
			Group:   "apiextensions.k8s.io",
			Version: "v1beta1",
			Kind:    "CustomResourceDefinition",
			Name:    crd.GetName(),
			Status:  v1alpha1.RequirementStatusReasonPresent,
			Message: "CRD is present and Established condition is true",
		},
		{
			Group:   "",
			Version: "v1",
			Kind:    "ServiceAccount",
			Name:    serviceAccount.GetName(),
			Status:  v1alpha1.RequirementStatusReasonPresent,
			Dependents: []v1alpha1.DependentStatus{
				{
					Group:   "rbac.authorization.k8s.io",
					Version: "v1beta1",
					Kind:    "PolicyRule",
					Status:  "Satisfied",
					Message: "namespaced rule:{\"verbs\":[\"get\"],\"apiGroups\":[\"my.api.group\"],\"resources\":[\"apis\"]}",
				},
			},
		},
	}
	operatorCSVFinal.Status.Conditions = []v1alpha1.ClusterServiceVersionCondition{
		{
			Phase:              v1alpha1.CSVPhasePending,
			Reason:             v1alpha1.CSVReasonRequirementsUnknown,
			Message:            "requirements not yet checked",
			LastUpdateTime:     timeNow(),
			LastTransitionTime: timeNow(),
		},
		{
			Phase:              v1alpha1.CSVPhaseInstallReady,
			Reason:             v1alpha1.CSVReasonRequirementsMet,
			Message:            "all requirements found, attempting install",
			LastUpdateTime:     timeNow(),
			LastTransitionTime: timeNow(),
		},
		{
			Phase:              v1alpha1.CSVPhaseInstalling,
			Reason:             v1alpha1.CSVReasonInstallSuccessful,
			Message:            "waiting for install components to report healthy",
			LastUpdateTime:     timeNow(),
			LastTransitionTime: timeNow(),
		},
		{
			Phase:              v1alpha1.CSVPhaseSucceeded,
			Reason:             v1alpha1.CSVReasonInstallSuccessful,
			Message:            "install strategy completed with no errors",
			LastUpdateTime:     timeNow(),
			LastTransitionTime: timeNow(),
		},
	}

	targetCSV := operatorCSVFinal.DeepCopy()
	targetCSV.SetNamespace(targetNamespace)
	targetCSV.Status.Reason = v1alpha1.CSVReasonCopied
	targetCSV.Status.Message = "The operator is running in operator-ns but is managing this namespace"
	targetCSV.Status.LastUpdateTime = timeNow()

	ownerutil.AddNonBlockingOwner(serviceAccount, operatorCSV)

	ownedDeployment := deployment("csv1-dep1", operatorNamespace, serviceAccount.GetName(), nil)
	ownerutil.AddNonBlockingOwner(ownedDeployment, operatorCSV)

	annotatedDeployment := ownedDeployment.DeepCopy()
	annotatedDeployment.Spec.Template.SetAnnotations(map[string]string{v1.OperatorGroupTargetsAnnotationKey: operatorNamespace + "," + targetNamespace, v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace})
	annotatedDeployment.SetLabels(map[string]string{
		"olm.owner":           "csv1",
		"olm.owner.namespace": "operator-ns",
		"olm.owner.kind":      "ClusterServiceVersion",
	})

	annotatedGlobalDeployment := ownedDeployment.DeepCopy()
	annotatedGlobalDeployment.Spec.Template.SetAnnotations(map[string]string{v1.OperatorGroupTargetsAnnotationKey: "", v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace})
	annotatedGlobalDeployment.SetLabels(map[string]string{
		"olm.owner":           "csv1",
		"olm.owner.namespace": "operator-ns",
		"olm.owner.kind":      "ClusterServiceVersion",
	})

	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.GroupName,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "csv-role",
			Namespace:       operatorNamespace,
			Labels:          ownerutil.OwnerLabel(operatorCSV, v1alpha1.ClusterServiceVersionKind),
			OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(operatorCSV)},
		},
		Rules: permissions[0].Rules,
	}

	roleBinding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: rbacv1.GroupName,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "csv-rolebinding",
			Namespace:       operatorNamespace,
			Labels:          ownerutil.OwnerLabel(operatorCSV, v1alpha1.ClusterServiceVersionKind),
			OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(operatorCSV)},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  serviceAccount.GetObjectKind().GroupVersionKind().Group,
				Name:      serviceAccount.GetName(),
				Namespace: serviceAccount.GetNamespace(),
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     role.GetObjectKind().GroupVersionKind().Kind,
			Name:     role.GetName(),
		},
	}

	type initial struct {
		operatorGroup *v1.OperatorGroup
		clientObjs    []runtime.Object
		crds          []runtime.Object
		k8sObjs       []runtime.Object
		apis          []runtime.Object
	}
	type final struct {
		objects map[string][]runtime.Object
	}
	tests := []struct {
		initial        initial
		name           string
		expectedEqual  bool
		expectedStatus v1.OperatorGroupStatus
		final          final
	}{
		{
			name:          "NoMatchingNamespace/NoCSVs",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1.OperatorGroupSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"a": "app-a"},
						},
					},
				},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: operatorNamespace,
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: targetNamespace,
						},
					},
				},
			},
			expectedStatus: v1.OperatorGroupStatus{},
		},
		{
			name:          "MatchingNamespace/NoCSVs",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1.OperatorGroupSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "app-a"},
						},
					},
				},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: operatorNamespace,
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:   targetNamespace,
							Labels: map[string]string{"app": "app-a"},
						},
					},
				},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{targetNamespace},
				LastUpdated: timeNow(),
			},
		},
		{
			name:          "MatchingNamespace/CSVPresent/Found",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1.OperatorGroupSpec{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "app-a"},
						},
					},
				},
				clientObjs: []runtime.Object{operatorCSV},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:   operatorNamespace,
							Labels: map[string]string{"app": "app-a"},
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:   targetNamespace,
							Labels: map[string]string{"app": "app-a"},
						},
					},
					ownedDeployment,
					serviceAccount,
					role,
					roleBinding,
				},
				crds: []runtime.Object{crd},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{operatorNamespace, targetNamespace},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFinal.DeepCopy(), map[string]string{v1.OperatorGroupTargetsAnnotationKey: operatorNamespace + "," + targetNamespace, v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
					annotatedDeployment,
				},
				targetNamespace: {
					withLabels(
						withAnnotations(targetCSV.DeepCopy(), map[string]string{v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
						labels.Merge(targetCSV.GetLabels(), map[string]string{v1alpha1.CopiedLabelKey: operatorNamespace}),
					),
					&rbacv1.Role{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Role",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv-role",
							Namespace: targetNamespace,
							Labels: map[string]string{
								"olm.copiedFrom":      "operator-ns",
								"olm.owner":           "csv1",
								"olm.owner.namespace": "target-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
							OwnerReferences: []metav1.OwnerReference{
								ownerutil.NonBlockingOwner(targetCSV),
							},
						},
						Rules: permissions[0].Rules,
					},
					&rbacv1.RoleBinding{
						TypeMeta: metav1.TypeMeta{
							Kind:       "RoleBinding",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv-rolebinding",
							Namespace: targetNamespace,
							Labels: map[string]string{
								"olm.copiedFrom":      "operator-ns",
								"olm.owner":           "csv1",
								"olm.owner.namespace": "target-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
							OwnerReferences: []metav1.OwnerReference{
								ownerutil.NonBlockingOwner(targetCSV),
							},
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      rbacv1.ServiceAccountKind,
								Name:      serviceAccount.GetName(),
								Namespace: operatorNamespace,
							},
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: rbacv1.GroupName,
							Kind:     role.GroupVersionKind().Kind,
							Name:     "csv-role",
						},
					},
				},
			}},
		},
		{
			name:          "MatchingNamespace/CSVPresent/Found/ExplicitTargetNamespaces",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1.OperatorGroupSpec{
						TargetNamespaces: []string{operatorNamespace, targetNamespace},
					},
				},
				clientObjs: []runtime.Object{operatorCSV},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: operatorNamespace,
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: targetNamespace,
						},
					},
					ownedDeployment,
					serviceAccount,
					role,
					roleBinding,
				},
				crds: []runtime.Object{crd},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{operatorNamespace, targetNamespace},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFinal.DeepCopy(), map[string]string{v1.OperatorGroupTargetsAnnotationKey: operatorNamespace + "," + targetNamespace, v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
					annotatedDeployment,
				},
				targetNamespace: {
					withLabels(
						withAnnotations(targetCSV.DeepCopy(), map[string]string{v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
						labels.Merge(targetCSV.GetLabels(), map[string]string{v1alpha1.CopiedLabelKey: operatorNamespace}),
					),
					&rbacv1.Role{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Role",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv-role",
							Namespace: targetNamespace,
							Labels: map[string]string{
								"olm.copiedFrom":      "operator-ns",
								"olm.owner":           "csv1",
								"olm.owner.namespace": "target-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
							OwnerReferences: []metav1.OwnerReference{
								ownerutil.NonBlockingOwner(targetCSV),
							},
						},
						Rules: permissions[0].Rules,
					},
					&rbacv1.RoleBinding{
						TypeMeta: metav1.TypeMeta{
							Kind:       "RoleBinding",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv-rolebinding",
							Namespace: targetNamespace,
							Labels: map[string]string{
								"olm.copiedFrom":      "operator-ns",
								"olm.owner":           "csv1",
								"olm.owner.namespace": "target-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
							OwnerReferences: []metav1.OwnerReference{
								ownerutil.NonBlockingOwner(targetCSV),
							},
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      rbacv1.ServiceAccountKind,
								Name:      serviceAccount.GetName(),
								Namespace: operatorNamespace,
							},
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: rbacv1.GroupName,
							Kind:     role.GroupVersionKind().Kind,
							Name:     "csv-role",
						},
					},
				},
			}},
		},
		{
			name:          "AllNamespaces/CSVPresent/Found",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
						Labels:    map[string]string{"app": "app-a"},
					},
					Spec: v1.OperatorGroupSpec{},
				},
				clientObjs: []runtime.Object{operatorCSV},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        operatorNamespace,
							Labels:      map[string]string{"app": "app-a"},
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        targetNamespace,
							Labels:      map[string]string{"app": "app-a"},
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					ownedDeployment,
					serviceAccount,
					role,
					roleBinding,
				},
				crds: []runtime.Object{crd},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{corev1.NamespaceAll},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFinal.DeepCopy(), map[string]string{v1.OperatorGroupTargetsAnnotationKey: "", v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
					annotatedGlobalDeployment,
				},
				"": {
					&rbacv1.ClusterRole{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ClusterRole",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "csv-role",
							Labels: map[string]string{
								"olm.owner":           "csv1",
								"olm.owner.namespace": "operator-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
						},
						Rules: append(permissions[0].Rules, rbacv1.PolicyRule{
							Verbs:     ViewVerbs,
							APIGroups: []string{corev1.GroupName},
							Resources: []string{"namespaces"},
						}),
					},
					&rbacv1.ClusterRoleBinding{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ClusterRoleBinding",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "csv-rolebinding",
							Labels: map[string]string{
								"olm.owner":           "csv1",
								"olm.owner.namespace": "operator-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      rbacv1.ServiceAccountKind,
								Name:      serviceAccount.GetName(),
								Namespace: operatorNamespace,
							},
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: rbacv1.GroupName,
							Kind:     "ClusterRole",
							Name:     "csv-role",
						},
					},
				},
				targetNamespace: {
					withLabels(
						withAnnotations(targetCSV.DeepCopy(), map[string]string{v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
						labels.Merge(targetCSV.GetLabels(), map[string]string{v1alpha1.CopiedLabelKey: operatorNamespace}),
					),
				},
			}},
		},
		{
			name:          "AllNamespaces/CSVPresent/Found/PruneMissingProvidedAPI",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
						Labels:    map[string]string{"app": "app-a"},
						Annotations: map[string]string{
							v1.OperatorGroupProvidedAPIsAnnotationKey: "c1.v1.fake.api.group,missing.v1.fake.api.group",
						},
					},
					Spec: v1.OperatorGroupSpec{},
				},
				clientObjs: []runtime.Object{operatorCSV},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        operatorNamespace,
							Labels:      map[string]string{"app": "app-a"},
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        targetNamespace,
							Labels:      map[string]string{"app": "app-a"},
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					ownedDeployment,
					serviceAccount,
					role,
					roleBinding,
				},
				crds: []runtime.Object{crd},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{corev1.NamespaceAll},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFinal.DeepCopy(), map[string]string{v1.OperatorGroupTargetsAnnotationKey: "", v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
					annotatedGlobalDeployment,
					&v1.OperatorGroup{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "operator-group-1",
							Namespace: operatorNamespace,
							Labels:    map[string]string{"app": "app-a"},
							Annotations: map[string]string{
								v1.OperatorGroupProvidedAPIsAnnotationKey: "c1.v1.fake.api.group",
							},
						},
						Spec: v1.OperatorGroupSpec{},
						Status: v1.OperatorGroupStatus{
							Namespaces:  []string{corev1.NamespaceAll},
							LastUpdated: timeNow(),
						},
					},
				},
				"": {
					&rbacv1.ClusterRole{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ClusterRole",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "csv-role",
							Labels: map[string]string{
								"olm.owner":           "csv1",
								"olm.owner.namespace": "operator-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
						},
						Rules: append(permissions[0].Rules, rbacv1.PolicyRule{
							Verbs:     ViewVerbs,
							APIGroups: []string{corev1.GroupName},
							Resources: []string{"namespaces"},
						}),
					},
					&rbacv1.ClusterRoleBinding{
						TypeMeta: metav1.TypeMeta{
							Kind:       "ClusterRoleBinding",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: "csv-rolebinding",
							Labels: map[string]string{
								"olm.owner":           "csv1",
								"olm.owner.namespace": "operator-ns",
								"olm.owner.kind":      "ClusterServiceVersion",
							},
						},
						Subjects: []rbacv1.Subject{
							{
								Kind:      rbacv1.ServiceAccountKind,
								Name:      serviceAccount.GetName(),
								Namespace: operatorNamespace,
							},
						},
						RoleRef: rbacv1.RoleRef{
							APIGroup: rbacv1.GroupName,
							Kind:     "ClusterRole",
							Name:     "csv-role",
						},
					},
				},
				targetNamespace: {
					withLabels(
						withAnnotations(targetCSV.DeepCopy(), map[string]string{v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
						labels.Merge(targetCSV.GetLabels(), map[string]string{v1alpha1.CopiedLabelKey: operatorNamespace}),
					),
				},
			}},
		},
		{
			name:          "AllNamespaces/CSVPresent/Found/PruneMissingProvidedAPI/StaticProvidedAPIs",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
						Labels:    map[string]string{"app": "app-a"},
						Annotations: map[string]string{
							v1.OperatorGroupProvidedAPIsAnnotationKey: "missing.fake.api.group",
						},
					},
					Spec: v1.OperatorGroupSpec{
						StaticProvidedAPIs: true,
					},
				},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        operatorNamespace,
							Labels:      map[string]string{"app": "app-a"},
							Annotations: map[string]string{"test": "annotation"},
						},
					},
				},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{corev1.NamespaceAll},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					&v1.OperatorGroup{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "operator-group-1",
							Namespace: operatorNamespace,
							Labels:    map[string]string{"app": "app-a"},
							Annotations: map[string]string{
								v1.OperatorGroupProvidedAPIsAnnotationKey: "missing.fake.api.group",
							},
						},
						Spec: v1.OperatorGroupSpec{
							StaticProvidedAPIs: true,
						},
						Status: v1.OperatorGroupStatus{
							Namespaces:  []string{corev1.NamespaceAll},
							LastUpdated: timeNow(),
						},
					},
				},
			}},
		},
		{
			name:          "AllNamespaces/CSVPresent/InstallModeNotSupported",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1.OperatorGroupSpec{},
				},
				clientObjs: []runtime.Object{
					withInstallModes(operatorCSV.DeepCopy(), []v1alpha1.InstallMode{
						{
							Type:      v1alpha1.InstallModeTypeAllNamespaces,
							Supported: false,
						},
					}),
				},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        operatorNamespace,
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        targetNamespace,
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					ownedDeployment,
					serviceAccount,
					role,
					roleBinding,
				},
				crds: []runtime.Object{crd},
			},
			expectedStatus: v1.OperatorGroupStatus{
				Namespaces:  []string{corev1.NamespaceAll},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withPhase(
						withInstallModes(
							withAnnotations(operatorCSV.DeepCopy(), map[string]string{
								v1.OperatorGroupTargetsAnnotationKey:   "",
								v1.OperatorGroupAnnotationKey:          "operator-group-1",
								v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace,
							}).(*v1alpha1.ClusterServiceVersion),
							[]v1alpha1.InstallMode{
								{
									Type:      v1alpha1.InstallModeTypeAllNamespaces,
									Supported: false,
								},
							}), v1alpha1.CSVPhaseFailed,
						v1alpha1.CSVReasonUnsupportedOperatorGroup,
						"AllNamespaces InstallModeType not supported, cannot configure to watch all namespaces",
						timeNow()),
				},
				"":              {},
				targetNamespace: {},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespaces := []string{}
			// Pick out Namespaces
			for _, obj := range tt.initial.k8sObjs {
				if ns, ok := obj.(*corev1.Namespace); ok {
					namespaces = append(namespaces, ns.GetName())
				}
			}

			// Append operatorGroup to initialObjs
			tt.initial.clientObjs = append(tt.initial.clientObjs, tt.initial.operatorGroup)

			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			op, _, err := NewFakeOperator(tt.initial.clientObjs, tt.initial.k8sObjs, tt.initial.crds, tt.initial.apis, &install.StrategyResolver{}, nil, nil, namespaces, stopCh)
			require.NoError(t, err)

			err = op.syncOperatorGroups(tt.initial.operatorGroup)
			require.NoError(t, err)

			// Sync csvs enough to get them back to succeeded state
			for i := 0; i < 8; i++ {
				opGroupCSVs, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(operatorNamespace).List(metav1.ListOptions{})
				require.NoError(t, err)

				for _, obj := range opGroupCSVs.Items {

					err = op.syncClusterServiceVersion(&obj)
					require.NoError(t, err, "%#v", obj)

					err = op.syncCopyCSV(&obj)
					require.NoError(t, err, "%#v", obj)
				}
			}

			// Sync again to catch provided API changes
			err = op.syncOperatorGroups(tt.initial.operatorGroup)
			require.NoError(t, err)

			operatorGroup, err := op.GetClient().OperatorsV1().OperatorGroups(tt.initial.operatorGroup.GetNamespace()).Get(tt.initial.operatorGroup.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			sort.Strings(tt.expectedStatus.Namespaces)
			sort.Strings(operatorGroup.Status.Namespaces)
			assert.Equal(t, tt.expectedStatus, operatorGroup.Status)

			for namespace, objects := range tt.final.objects {
				RequireObjectsInNamespace(t, op.OpClient, op.client, namespace, objects)
			}
		})
	}
}

func RequireObjectsInNamespace(t *testing.T, opClient operatorclient.ClientInterface, client versioned.Interface, namespace string, objects []runtime.Object) {
	for _, object := range objects {
		var err error
		var fetched runtime.Object
		switch o := object.(type) {
		case *appsv1.Deployment:
			fetched, err = opClient.GetDeployment(namespace, o.GetName())
		case *rbacv1.ClusterRole:
			fetched, err = opClient.GetClusterRole(o.GetName())
		case *rbacv1.Role:
			fetched, err = opClient.GetRole(namespace, o.GetName())
		case *rbacv1.ClusterRoleBinding:
			fetched, err = opClient.GetClusterRoleBinding(o.GetName())
		case *rbacv1.RoleBinding:
			fetched, err = opClient.GetRoleBinding(namespace, o.GetName())
		case *v1alpha1.ClusterServiceVersion:
			fetched, err = client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(o.GetName(), metav1.GetOptions{})
		case *v1.OperatorGroup:
			fetched, err = client.OperatorsV1().OperatorGroups(namespace).Get(o.GetName(), metav1.GetOptions{})
		default:
			require.Failf(t, "couldn't find expected object", "%#v", object)
		}
		require.NoError(t, err, "couldn't fetch %s %v", namespace, object)
		require.True(t, reflect.DeepEqual(object, fetched), diff.ObjectDiff(object, fetched))
	}
}

func TestIsReplacing(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	namespace := "ns"

	type initial struct {
		csvs []runtime.Object
	}
	tests := []struct {
		name     string
		initial  initial
		in       *v1alpha1.ClusterServiceVersion
		expected *v1alpha1.ClusterServiceVersion
	}{
		{
			name: "QueryErr",
			in:   csv("name", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv2", namespace, "0.0.0", "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
		{
			name: "CSVInCluster/ReplacingNotFound",
			in:   csv("csv2", namespace, "0.0.0", "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv3", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			op, _, err := NewFakeOperator(tt.initial.csvs, nil, nil, nil, &install.StrategyResolver{}, nil, nil, []string{namespace}, stopCh)
			require.NoError(t, err)

			require.Equal(t, tt.expected, op.isReplacing(tt.in))
		})
	}
}

func TestIsBeingReplaced(t *testing.T) {
	namespace := "ns"

	type initial struct {
		csvs map[string]*v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name     string
		initial  initial
		in       *v1alpha1.ClusterServiceVersion
		expected *v1alpha1.ClusterServiceVersion
	}{
		{
			name:     "QueryErr",
			in:       csv("name", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "0.0.0", "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv2", namespace, "0.0.0", "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			op := &Operator{Operator: &queueinformer.Operator{Log: logrus.New()}}

			require.Equal(t, tt.expected, op.isBeingReplaced(tt.in, tt.initial.csvs))
		})
	}
}

func TestCheckReplacement(t *testing.T) {
	namespace := "ns"

	type initial struct {
		csvs map[string]*v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name     string
		initial  initial
		in       *v1alpha1.ClusterServiceVersion
		expected *v1alpha1.ClusterServiceVersion
	}{
		{
			name:     "QueryErr",
			in:       csv("name", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv1", namespace, "0.0.0", "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "0.0.0", "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv2", namespace, "0.0.0", "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			op := &Operator{Operator: &queueinformer.Operator{Log: logrus.New()}}

			require.Equal(t, tt.expected, op.isBeingReplaced(tt.in, tt.initial.csvs))
		})
	}
}

func TestAPIServiceResourceErrorActionable(t *testing.T) {
	tests := []struct {
		name       string
		errs       []error
		actionable bool
	}{
		{
			name:       "Nil/Actionable",
			errs:       nil,
			actionable: true,
		},
		{
			name:       "Empty/Actionable",
			errs:       nil,
			actionable: true,
		},
		{
			name:       "Error/Actionable",
			errs:       []error{fmt.Errorf("err-a")},
			actionable: true,
		},
		{
			name:       "Errors/Actionable",
			errs:       []error{fmt.Errorf("err-a"), fmt.Errorf("err-b")},
			actionable: true,
		},
		{
			name:       "ContainsUnadoptable/NotActionable",
			errs:       []error{fmt.Errorf("err-a"), olmerrors.UnadoptableError{}},
			actionable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := &Operator{}
			aggregate := utilerrors.NewAggregate(tt.errs)
			require.Equal(t, tt.actionable, op.apiServiceResourceErrorActionable(aggregate))
		})
	}

}
