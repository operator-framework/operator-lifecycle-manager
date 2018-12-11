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
	"strings"
	"testing"
	"time"

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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"
	kagg "k8s.io/kube-aggregator/pkg/client/informers/externalversions"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/event"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
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
func NewFakeOperator(clientObjs []runtime.Object, k8sObjs []runtime.Object, extObjs []runtime.Object, regObjs []runtime.Object, resolver install.StrategyResolverInterface, namespaces []string, stopCh <-chan struct{}) (*Operator, []cache.InformerSynced, error) {
	// Create client fakes
	clientFake := fake.NewSimpleClientset(clientObjs...)
	k8sClientFake := k8sfake.NewSimpleClientset(k8sObjs...)
	k8sClientFake.Resources = apiResourcesForObjects(append(extObjs, regObjs...))
	opClientFake := operatorclient.NewClient(k8sClientFake, apiextensionsfake.NewSimpleClientset(extObjs...), apiregistrationfake.NewSimpleClientset(regObjs...))

	eventRecorder, err := event.NewRecorder(opClientFake.KubernetesInterface().CoreV1().Events(metav1.NamespaceAll))
	if err != nil {
		return nil, nil, err
	}

	// Create the new operator
	queueOperator, err := queueinformer.NewOperatorFromClient(opClientFake, logrus.New())
	op := &Operator{
		Operator:  queueOperator,
		client:    clientFake,
		lister:    operatorlister.NewLister(),
		resolver:  resolver,
		csvQueues: make(map[string]workqueue.RateLimitingInterface),
		recorder:  eventRecorder,
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

	for _, ns := range namespaces {
		csvInformer := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(ns)).Operators().V1alpha1().ClusterServiceVersions()
		op.lister.OperatorsV1alpha1().RegisterClusterServiceVersionLister(ns, csvInformer.Lister())
		operatorGroupInformer := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(ns)).Operators().V1alpha2().OperatorGroups()
		op.lister.OperatorsV1alpha2().RegisterOperatorGroupLister(ns, operatorGroupInformer.Lister())
		deploymentInformer := informers.NewSharedInformerFactoryWithOptions(opClientFake.KubernetesInterface(), wakeupInterval, informers.WithNamespace(ns)).Apps().V1().Deployments()
		op.lister.AppsV1().RegisterDeploymentLister(ns, deploymentInformer.Lister())
		informerList = append(informerList, []cache.SharedIndexInformer{csvInformer.Informer(), operatorGroupInformer.Informer(), deploymentInformer.Informer()}...)
	}

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
	name, namespace, replaces string,
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
			Replaces:        replaces,
			InstallStrategy: installStrategy,
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

func apiService(group, version, serviceName, serviceNamespace, deploymentName string, caBundle []byte, availableStatus apiregistrationv1.ConditionStatus) *apiregistrationv1.APIService {
	apiService := &apiregistrationv1.APIService{
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

func crd(name string, version string) *v1beta1.CustomResourceDefinition {
	return &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "group",
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: name + "group",
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

	operatorGroup := &v1alpha2.OperatorGroup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OperatorGroup",
			APIVersion: v1alpha2.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: namespace,
		},
		Spec: v1alpha2.OperatorGroupSpec{},
		Status: v1alpha2.OperatorGroupStatus{
			Namespaces: []string{namespace},
		},
	}

	templateAnnotations := map[string]string{
		operatorGroupTargetsAnnotationKey:   namespace,
		operatorGroupNamespaceAnnotationKey: namespace,
		operatorGroupAnnotationKey:          operatorGroup.GetName(),
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
	type initial struct {
		csvs []runtime.Object
		crds []runtime.Object
		objs []runtime.Object
		apis []runtime.Object
	}
	type expected struct {
		csvStates map[string]csvState
		err       map[string]error
	}
	tests := []struct {
		name     string
		initial  initial
		expected expected
	}{
		{
			name: "SingleCSVNoneToPending/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					),
				},
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
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), nil, apis("a1.corev1.a1Kind")),
				},
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
					csv("csv1",
						namespace,
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					csv("csv1",
						namespace,
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
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{},
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
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
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
			name: "SingleCSVPendingToPending/APIService/Required/Unavailable",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
				apis: []runtime.Object{apiService("a1", "v1", "", "", "", validCAPEM, apiregistrationv1.ConditionFalse)},
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
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
				apis: []runtime.Object{apiService("a1", "v1", "", "", "", validCAPEM, apiregistrationv1.ConditionUnknown)},
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
			name: "SingleCSVPendingToFailed/APIService/Owned/DeploymentNotFound",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("b1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), apis("a1.v1.a1Kind"), nil),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv2",
						namespace,
						"",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.NewTime(time.Now().Add(24*time.Hour)), metav1.NewTime(time.Now())),
					withAPIServices(csv("csv2",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), apis("a1.v1.a1Kind"), nil),
				},
				apis: []runtime.Object{apiService("a1", "v1", "v1-a1", namespace, "", validCAPEM, apiregistrationv1.ConditionTrue)},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", templateAnnotations),
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
					csv("csv1",
						namespace,
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", templateAnnotations),
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
				apis: []runtime.Object{apiService("a1", "v1", "", "", "", validCAPEM, apiregistrationv1.ConditionTrue)},
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					), apis("a1.v1.a1Kind"), nil),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", []byte("a-bad-ca"), apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", expiredCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
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
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", expiredCAPEM, apiregistrationv1.ConditionTrue),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(templateAnnotations, map[string]string{
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
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending, reason: v1alpha1.CSVReasonAPIServiceResourcesNeedReinstall},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToFailed/BadStrategy",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "SingleCSVInstallingToSucceeded",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "SingleCSVSucceededToFailed/CRD",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil),
				},
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
					deployment("csv2-dep1", namespace, "sa", templateAnnotations),
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
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
					deployment("csv2-dep1", namespace, "sa", templateAnnotations),
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
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
					deployment("csv2-dep1", namespace, "sa", templateAnnotations),
					deployment("csv3-dep1", namespace, "sa", templateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", templateAnnotations),
					deployment("csv2-dep1", namespace, "sa", templateAnnotations),
					deployment("csv3-dep1", namespace, "sa", templateAnnotations),
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
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv2-dep1", namespace, "sa", templateAnnotations),
					deployment("csv3-dep1", namespace, "sa", templateAnnotations),
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
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					),
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv2-dep1", namespace, "sa", templateAnnotations),
					deployment("csv3-dep1", namespace, "sa", templateAnnotations),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv2": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			namespaceObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			tt.initial.objs = append(tt.initial.objs, namespaceObj)

			// put csvs in operatorgroup
			csvs := make([]runtime.Object, 0)
			for _, csv := range tt.initial.csvs {
				csvs = append(csvs, withAnnotations(csv, templateAnnotations))
			}
			clientObjs := append(csvs, operatorGroup)

			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			op, hasSyncedFns, err := NewFakeOperator(clientObjs, tt.initial.objs, tt.initial.crds, tt.initial.apis, &install.StrategyResolver{}, []string{namespace}, stopCh)
			require.NoError(t, err)

			// run csv sync for each CSV
			for _, csv := range csvs {
				err := op.syncClusterServiceVersion(csv)
				expectedErr := tt.expected.err[csv.(*v1alpha1.ClusterServiceVersion).Name]
				require.Equal(t, expectedErr, err)

				// wait for informers to sync before continuing
				ok := cache.WaitForCacheSync(stopCh, hasSyncedFns...)
				require.True(t, ok, "wait for cache sync failed")
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
		})
	}
}

func TestSyncOperatorGroups(t *testing.T) {
	nowTime := metav1.Date(2006, time.January, 2, 15, 4, 5, 0, time.FixedZone("MST", -7*3600))
	timeNow = func() metav1.Time { return nowTime }

	operatorNamespace := "operator-ns"
	targetNamespace := "target-ns"
	aLabel := map[string]string{"app": "matchLabel"}

	namespaces := []runtime.Object{
		&corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Namespace",
				APIVersion: "",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: operatorNamespace,
			},
		},
		&corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Namespace",
				APIVersion: "",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:   targetNamespace,
				Labels: aLabel,
			},
		},
	}

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

	crd := crd("c1.fake.api.group", "v1")
	operatorCSV := csv("csv1",
		operatorNamespace,
		"",
		installStrategy("csv1-dep1", permissions, nil),
		[]*v1beta1.CustomResourceDefinition{crd},
		[]*v1beta1.CustomResourceDefinition{},
		v1alpha1.CSVPhaseNone,
	)

	// after state transitions from operatorgroups, this is the operator csv we expect
	operatorCSVFinal := operatorCSV.DeepCopy()
	operatorCSVFinal.Status.Phase = v1alpha1.CSVPhaseSucceeded
	operatorCSVFinal.Status.Message = "install strategy completed with no errors"
	operatorCSVFinal.Status.Reason = v1alpha1.CSVReasonInstallSuccessful
	operatorCSVFinal.Status.LastUpdateTime = timeNow()
	operatorCSVFinal.Status.LastTransitionTime = timeNow()
	operatorCSVFinal.Status.RequirementStatus = []v1alpha1.RequirementStatus{
		{
			Group:   "apiextensions.k8s.io",
			Version: "v1beta1",
			Kind:    "CustomResourceDefinition",
			Name:    crd.GetName(),
			Status:  v1alpha1.RequirementStatusReasonPresent,
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
	annotatedDeployment.Spec.Template.SetAnnotations(map[string]string{"olm.targetNamespaces": targetNamespace + "," + operatorNamespace, "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": operatorNamespace})
	annotatedDeployment.SetLabels(map[string]string{
		"olm.owner":           "csv1",
		"olm.owner.namespace": "operator-ns",
	})

	annotatedGlobalDeployment := ownedDeployment.DeepCopy()
	annotatedGlobalDeployment.Spec.Template.SetAnnotations(map[string]string{"olm.targetNamespaces": "", "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": operatorNamespace})
	annotatedGlobalDeployment.SetLabels(map[string]string{
		"olm.owner":           "csv1",
		"olm.owner.namespace": "operator-ns",
	})

	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: rbacv1.GroupName,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "csv-role",
			Namespace:       operatorNamespace,
			Labels:          ownerutil.OwnerLabel(operatorCSV),
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
			Labels:          ownerutil.OwnerLabel(operatorCSV),
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
		operatorGroup *v1alpha2.OperatorGroup
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
		expectedStatus v1alpha2.OperatorGroupStatus
		final          final
	}{
		{
			name:          "operator group with no matching namespace, no CSVs",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: aLabel,
						},
					},
				},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: operatorNamespace,
						},
					},
				},
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces:  []string{operatorNamespace},
				LastUpdated: timeNow(),
			},
		},
		{
			name:          "operator group with matching namespace, no CSVs",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "matchLabel",
							},
						},
					},
				},

				k8sObjs: namespaces,
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces:  []string{targetNamespace, operatorNamespace},
				LastUpdated: timeNow(),
			},
		},
		{
			name:          "operator group with matching namespace, CSV present, found",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
						Labels:    aLabel,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: aLabel,
						},
					},
				},
				clientObjs: []runtime.Object{operatorCSV},
				k8sObjs:    append(namespaces, ownedDeployment, serviceAccount, role, roleBinding),
				crds:       []runtime.Object{crd},
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces:  []string{targetNamespace, operatorNamespace},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFinal.DeepCopy(), map[string]string{"olm.targetNamespaces": targetNamespace + "," + operatorNamespace, "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": operatorNamespace}),
					annotatedDeployment,
				},
				targetNamespace: {
					withAnnotations(targetCSV.DeepCopy(), map[string]string{"olm.targetNamespaces": targetNamespace + "," + operatorNamespace, "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": operatorNamespace}),
					&rbacv1.Role{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Role",
							APIVersion: rbacv1.GroupName,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv-role",
							Namespace: targetNamespace,
							Labels: map[string]string{
								"olm.owner":           "csv1",
								"olm.owner.namespace": "operator-ns",
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
								"olm.owner":           "csv1",
								"olm.owner.namespace": "operator-ns",
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
			name:          "operator group all namespaces, CSV present, found",
			expectedEqual: true,
			initial: initial{
				operatorGroup: &v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: operatorNamespace,
						Labels:    aLabel,
					},
					Spec: v1alpha2.OperatorGroupSpec{},
				},
				clientObjs: []runtime.Object{operatorCSV},
				k8sObjs: []runtime.Object{
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        operatorNamespace,
							Labels:      aLabel,
							Annotations: map[string]string{"test": "annotation"},
						},
					},
					&corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name:        targetNamespace,
							Labels:      aLabel,
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
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces:  []string{corev1.NamespaceAll},
				LastUpdated: timeNow(),
			},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFinal.DeepCopy(), map[string]string{"olm.targetNamespaces": "", "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": operatorNamespace}),
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
							},
							OwnerReferences: []metav1.OwnerReference{
								ownerutil.NonBlockingOwner(targetCSV),
							},
						},
						Rules: permissions[0].Rules,
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
							Kind:     "ClusterRole",
							Name:     "csv-role",
						},
					},
				},
				targetNamespace: {
					withAnnotations(targetCSV.DeepCopy(), map[string]string{"olm.targetNamespaces": "", "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": operatorNamespace}),
				},
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
			op, hasSyncedFns, err := NewFakeOperator(tt.initial.clientObjs, tt.initial.k8sObjs, tt.initial.crds, tt.initial.apis, &install.StrategyResolver{}, namespaces, stopCh)
			require.NoError(t, err)

			err = op.syncOperatorGroups(tt.initial.operatorGroup)
			require.NoError(t, err)

			// Sync csvs enough to get them back to succeeded state
			for i := 0; i < 6; i++ {
				opGroupCSVs, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(operatorNamespace).List(metav1.ListOptions{})
				require.NoError(t, err)

				for _, obj := range opGroupCSVs.Items {
					ok := cache.WaitForCacheSync(stopCh, hasSyncedFns...)
					require.True(t, ok, "wait for cache sync failed")

					err = op.syncClusterServiceVersion(&obj)
					require.NoError(t, err, "%#v", obj)
				}
			}

			operatorGroup, err := op.GetClient().OperatorsV1alpha2().OperatorGroups(tt.initial.operatorGroup.GetNamespace()).Get(tt.initial.operatorGroup.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, tt.expectedStatus, operatorGroup.Status)

			for namespace, objects := range tt.final.objects {
				for _, object := range objects {
					var err error
					var fetched runtime.Object
					switch o := object.(type) {
					case *appsv1.Deployment:
						fetched, err = op.OpClient.GetDeployment(namespace, o.GetName())
					case *rbacv1.ClusterRole:
						fetched, err = op.OpClient.GetClusterRole(o.GetName())
					case *rbacv1.Role:
						fetched, err = op.OpClient.GetRole(namespace, o.GetName())
					case *rbacv1.ClusterRoleBinding:
						fetched, err = op.OpClient.GetClusterRoleBinding(o.GetName())
					case *rbacv1.RoleBinding:
						fetched, err = op.OpClient.GetRoleBinding(namespace, o.GetName())
					case *v1alpha1.ClusterServiceVersion:
						fetched, err = op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(o.GetName(), metav1.GetOptions{})
					default:
						require.Failf(t, "couldn't find expected object", "%#v", object)
					}
					require.NoError(t, err, "couldn't fetch %s %v", namespace, object)
					require.Equal(t, object, fetched, "%s in %s not equal", object.GetObjectKind().GroupVersionKind().String(), namespace)
				}
			}
		})
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
			in:   csv("name", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
		{
			name: "CSVInCluster/ReplacingNotFound",
			in:   csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv3", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
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
			op, _, err := NewFakeOperator(tt.initial.csvs, nil, nil, nil, &install.StrategyResolver{}, []string{namespace}, stopCh)
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
			in:       csv("name", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
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
			in:       csv("name", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
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
