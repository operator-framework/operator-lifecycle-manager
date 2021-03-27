package olm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/pkg/version"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/certs"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
	csvutility "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/csv"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/labeler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/scoped"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

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

func addDepSpecHashLabel(labels map[string]string, strat v1alpha1.NamedInstallStrategy) map[string]string {
	labels[install.DeploymentSpecHashLabelKey] = install.HashDeploymentSpec(strat.StrategySpec.DeploymentSpecs[0].Spec)
	return labels
}

func apiResourcesForObjects(objs []runtime.Object) []*metav1.APIResourceList {
	apis := []*metav1.APIResourceList{}
	for _, o := range objs {
		switch o.(type) {
		case *apiextensionsv1.CustomResourceDefinition:
			crd := o.(*apiextensionsv1.CustomResourceDefinition)
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: crd.Spec.Group, Version: crd.Spec.Versions[0].Name}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:         crd.GetName(),
						SingularName: crd.Spec.Names.Singular,
						Namespaced:   crd.Spec.Scope == apiextensionsv1.NamespaceScoped,
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

// fakeOperatorConfig is the configuration for a fake operator.
type fakeOperatorConfig struct {
	*operatorConfig

	recorder          record.EventRecorder
	namespaces        []string
	fakeClientOptions []clientfake.Option
	clientObjs        []runtime.Object
	k8sObjs           []runtime.Object
	extObjs           []runtime.Object
	regObjs           []runtime.Object
}

// fakeOperatorOption applies an option to the given fake operator configuration.
type fakeOperatorOption func(*fakeOperatorConfig)

func withResyncPeriod(period time.Duration) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.resyncPeriod = queueinformer.ResyncWithJitter(period, 0.1)
	}
}

func withOperatorNamespace(namespace string) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.operatorNamespace = namespace
	}
}

func withWatchedNamespaces(namespaces ...string) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.watchedNamespaces = namespaces
	}
}

func withLogger(logger *logrus.Logger) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.logger = logger
	}
}

func withClock(clock utilclock.Clock) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.clock = clock
	}
}

func withStrategyResolver(strategyResolver install.StrategyResolverInterface) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.strategyResolver = strategyResolver
	}
}

func withAPIReconciler(apiReconciler resolver.APIIntersectionReconciler) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		if apiReconciler != nil {
			config.apiReconciler = apiReconciler
		}
	}
}

func withAPILabeler(apiLabeler labeler.Labeler) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		if apiLabeler != nil {
			config.apiLabeler = apiLabeler
		}
	}
}

func withEventRecorder(rec record.EventRecorder) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.recorder = rec
	}
}

func withNamespaces(namespaces ...string) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.namespaces = namespaces
	}
}

func withFakeClientOptions(options ...clientfake.Option) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.fakeClientOptions = options
	}
}

func withClientObjs(clientObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.clientObjs = clientObjs
	}
}

func withK8sObjs(k8sObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.k8sObjs = k8sObjs
	}
}

func withExtObjs(extObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.extObjs = extObjs
	}
}

func withRegObjs(regObjs ...runtime.Object) fakeOperatorOption {
	return func(config *fakeOperatorConfig) {
		config.regObjs = regObjs
	}
}

// NewFakeOperator creates and starts a new operator using fake clients.
func NewFakeOperator(ctx context.Context, options ...fakeOperatorOption) (*Operator, error) {
	// Apply options to default config
	config := &fakeOperatorConfig{
		operatorConfig: &operatorConfig{
			resyncPeriod:      queueinformer.ResyncWithJitter(5*time.Minute, 0.1),
			operatorNamespace: "default",
			watchedNamespaces: []string{metav1.NamespaceAll},
			clock:             &utilclock.RealClock{},
			logger:            logrus.New(),
			strategyResolver:  &install.StrategyResolver{},
			apiReconciler:     resolver.APIIntersectionReconcileFunc(resolver.ReconcileAPIIntersection),
			apiLabeler:        labeler.Func(resolver.LabelSetsFor),
			restConfig:        &rest.Config{},
		},
		recorder: &record.FakeRecorder{},
		// default expected namespaces
		namespaces: []string{"default", "kube-system", "kube-public"},
	}
	for _, option := range options {
		option(config)
	}

	// Create client fakes
	config.externalClient = fake.NewReactionForwardingClientsetDecorator(config.clientObjs, config.fakeClientOptions...)
	// TODO: Using the ReactionForwardingClientsetDecorator for k8s objects causes issues with adding Resources for discovery.
	// For now, directly use a SimpleClientset instead.
	k8sClientFake := k8sfake.NewSimpleClientset(config.k8sObjs...)
	k8sClientFake.Resources = apiResourcesForObjects(append(config.extObjs, config.regObjs...))
	config.operatorClient = operatorclient.NewClient(k8sClientFake, apiextensionsfake.NewSimpleClientset(config.extObjs...), apiregistrationfake.NewSimpleClientset(config.regObjs...))
	config.configClient = configfake.NewSimpleClientset()

	for _, ns := range config.namespaces {
		_, err := config.operatorClient.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
		// Ignore already-exists errors
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return nil, err
		}
	}

	op, err := newOperatorWithConfig(ctx, config.operatorConfig)
	if err != nil {
		return nil, err
	}
	op.recorder = config.recorder

	scheme := runtime.NewScheme()
	if err := k8sscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}

	op.csvSetGenerator = csvutility.NewSetGenerator(config.logger, op.lister)
	op.csvReplaceFinder = csvutility.NewReplaceFinder(config.logger, config.externalClient)
	op.serviceAccountSyncer = scoped.NewUserDefinedServiceAccountSyncer(config.logger, scheme, config.operatorClient, op.client)

	// Only start the operator's informers (no reconciliation)
	op.RunInformers(ctx)

	if ok := cache.WaitForCacheSync(ctx.Done(), op.HasSynced); !ok {
		return nil, fmt.Errorf("failed to wait for caches to sync")
	}

	return op, nil
}

func buildFakeAPIIntersectionReconcilerThatReturns(result resolver.APIReconciliationResult) *fakes.FakeAPIIntersectionReconciler {
	reconciler := &fakes.FakeAPIIntersectionReconciler{}
	reconciler.ReconcileReturns(result)
	return reconciler
}

func deployment(deploymentName, namespace, serviceAccountName string, templateAnnotations map[string]string) *appsv1.Deployment {
	var (
		singleInstance       = int32(1)
		revisionHistoryLimit = int32(1)
	)
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
			RevisionHistoryLimit: &revisionHistoryLimit,
			Replicas:             &singleInstance,
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
	servingPair, err := certs.CreateSignedServingPair(notAfter, install.Organization, ca, hosts)
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

func withUID(obj runtime.Object, uid types.UID) runtime.Object {
	meta, ok := obj.(metav1.Object)
	if !ok {
		panic("could not find metadata on object")
	}
	meta.SetUID(uid)
	return meta.(runtime.Object)
}

func csvWithUID(csv *v1alpha1.ClusterServiceVersion, uid types.UID) *v1alpha1.ClusterServiceVersion {
	return withUID(csv, uid).(*v1alpha1.ClusterServiceVersion)
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

func installStrategy(deploymentName string, permissions []v1alpha1.StrategyDeploymentPermissions, clusterPermissions []v1alpha1.StrategyDeploymentPermissions) v1alpha1.NamedInstallStrategy {
	var singleInstance = int32(1)
	strategy := v1alpha1.StrategyDetailsDeployment{
		DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
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

	return v1alpha1.NamedInstallStrategy{
		StrategyName: v1alpha1.InstallStrategyNameDeployment,
		StrategySpec: strategy,
	}
}

func apiServiceInstallStrategy(deploymentName string, cahash string, permissions []v1alpha1.StrategyDeploymentPermissions, clusterPermissions []v1alpha1.StrategyDeploymentPermissions) v1alpha1.NamedInstallStrategy {
	strategy := installStrategy(deploymentName, permissions, clusterPermissions)

	strategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Annotations = map[string]string{install.OLMCAHashAnnotationKey: cahash}

	strategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Spec.Volumes = []corev1.Volume{{
		Name: "apiservice-cert",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "v1.a1-cert",
				Items: []corev1.KeyToPath{
					{
						Key:  "tls.crt",
						Path: "apiserver.crt",
					},
					{
						Key:  "tls.key",
						Path: "apiserver.key",
					},
				},
			},
		},
	}}
	strategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
		Name:      "apiservice-cert",
		MountPath: "/apiserver.local.config/certificates",
	}}
	return strategy
}

func withTemplateAnnotations(strategy v1alpha1.NamedInstallStrategy, annotations map[string]string) v1alpha1.NamedInstallStrategy {
	strategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Annotations = annotations
	return strategy
}

func csv(
	name, namespace, minKubeVersion, replaces string,
	installStrategy v1alpha1.NamedInstallStrategy,
	owned, required []*apiextensionsv1.CustomResourceDefinition,
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
	csv.SetPhase(phase, reason, message, &now)
	return csv
}

func withCertInfo(csv *v1alpha1.ClusterServiceVersion, rotateAt metav1.Time, lastUpdated metav1.Time) *v1alpha1.ClusterServiceVersion {
	csv.Status.CertsRotateAt = &rotateAt
	csv.Status.CertsLastUpdated = &lastUpdated
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

func crd(name, version, group string) *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "." + group,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: group,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    version,
					Storage: true,
					Served:  true,
				},
			},
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
		Status: apiextensionsv1.CustomResourceDefinitionStatus{
			Conditions: []apiextensionsv1.CustomResourceDefinitionCondition{
				{
					Type:   apiextensionsv1.Established,
					Status: apiextensionsv1.ConditionTrue,
				},
				{
					Type:   apiextensionsv1.NamesAccepted,
					Status: apiextensionsv1.ConditionTrue,
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
			Organization: []string{install.Organization},
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

	apiHash, err := resolver.APIKeyToGVKHash(opregistry.APIKey{Group: "g1", Version: "v1", Kind: "c1"})
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
	validCA, err := generateCA(time.Now().Add(10*365*24*time.Hour), install.Organization)
	require.NoError(t, err)
	validCAPEM, _, err := validCA.ToPEM()
	require.NoError(t, err)
	validCAHash := certs.PEMSHA256(validCAPEM)

	expiredCA, err := generateCA(time.Now(), install.Organization)
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
			name: "SingleCSVPendingToFailed/BadStrategyPermissions",
			initial: initial{
				csvs: []runtime.Object{
					csvWithUID(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]v1alpha1.StrategyDeploymentPermissions{
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations), types.UID("csv-uid")),
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
							OwnerReferences: []metav1.OwnerReference{
								{
									Kind: v1alpha1.ClusterServiceVersionKind,
									UID:  "csv-uid",
								},
							},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv2-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "c1.v1.g1")},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
					),
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
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
						apis("a1.v1.a1Kind"), nil), metav1.NewTime(time.Now().Add(24*time.Hour)), metav1.NewTime(time.Now())),
					withAPIServices(csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"",
						installStrategy("a1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), defaultTemplateAnnotations),
						apis("a1.v1.a1Kind"), nil),
				},
				clientObjs: []runtime.Object{addAnnotation(defaultOperatorGroup, v1.OperatorGroupProvidedAPIsAnnotationKey, "a1Kind.v1.a1")},
				apis:       []runtime.Object{apiService("a1", "v1", "a1-service", namespace, "", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace))},
				objs: []runtime.Object{
					withLabels(
						deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
							install.OLMCAHashAnnotationKey: validCAHash,
						})),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(apiServiceInstallStrategy("a1", validCAHash, nil, nil), addAnnotations(defaultTemplateAnnotations, map[string]string{
							install.OLMCAHashAnnotationKey: validCAHash,
						}))),
					),
					withAnnotations(keyPairToTLSSecret("a1-service-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"a1-service.ns", "a1-service.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
					}),
					service("a1", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("a1-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"a1-service-cert"},
						},
					}),
					roleBinding("a1-service-cert", namespace, "a1-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("a1-service-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
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
					clusterRoleBinding("a1-service-system:auth-delegator", "system:auth-delegator", "sa", namespace),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
			name: "SingleCSVPendingToInstallReady/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "a1-service", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("a1-service-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"a1-service.ns", "a1-service.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
					}),
					service("a1-service", namespace, "a1", 80),
					serviceAccount("sa", namespace),
					role("a1-service-cert", namespace, []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"secrets"},
							ResourceNames: []string{"a1-service-cert"},
						},
					}),
					roleBinding("a1-service-cert", namespace, "a1-service-cert", "sa", namespace),
					role("extension-apiserver-authentication-reader", "kube-system", []rbacv1.PolicyRule{
						{
							Verbs:         []string{"get"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"extension-apiserver-authentication"},
						},
					}),
					roleBinding("a1-service-auth-reader", "kube-system", "extension-apiserver-authentication-reader", "sa", namespace),
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
					clusterRoleBinding("a1-service-system:auth-delegator", "system:auth-delegator", "sa", namespace),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: "a-pretty-bad-hash",
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: "also-a-pretty-bad-hash",
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: "a-pretty-bad-hash",
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: "also-a-pretty-bad-hash",
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", []byte("a-bad-ca"), apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), validCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", validCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
					})),
					withAnnotations(tlsSecret("v1.a1-cert", namespace, []byte("bad-cert"), []byte("bad-key")), map[string]string{
						install.OLMCAHashAnnotationKey: validCAHash,
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", expiredCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: expiredCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), expiredCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: expiredCAHash,
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseFailed,
					), defaultTemplateAnnotations), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				apis: []runtime.Object{
					apiService("a1", "v1", "v1-a1", namespace, "a1", expiredCAPEM, apiregistrationv1.ConditionTrue, ownerLabelFromCSV("csv1", namespace)),
				},
				objs: []runtime.Object{
					deployment("a1", namespace, "sa", addAnnotations(defaultTemplateAnnotations, map[string]string{
						install.OLMCAHashAnnotationKey: expiredCAHash,
					})),
					withAnnotations(keyPairToTLSSecret("v1.a1-cert", namespace, signedServingPair(time.Now().Add(24*time.Hour), expiredCA, []string{"v1-a1.ns", "v1-a1.ns.svc"})), map[string]string{
						install.OLMCAHashAnnotationKey: expiredCAHash,
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
			name: "SingleCSVInstallingToSucceeded/UnmanagedDeploymentNotAffected",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
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
			name: "SingleCSVInstallingToInstallReady",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds:       []runtime.Object{},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						map[string]string{install.DeploymentSpecHashLabelKey: "BadHash"},
					),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady, reason: "InstallWaiting"},
				},
			},
		},
		{
			name: "SingleCSVInstallingToInstallReadyDueToAnnotations",
			initial: initial{
				csvs: []runtime.Object{
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds:       []runtime.Object{},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace, "sa", map[string]string{}),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady, reason: ""},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						addDepSpecHashLabel(map[string]string{
							ownerutil.OwnerKey:          "csv1",
							ownerutil.OwnerNamespaceKey: namespace,
							ownerutil.OwnerKind:         "ClusterServiceVersion",
						}, withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
			name: "SingleCSVSucceededToPending/DeploymentSpecChanged",
			initial: initial{
				csvs: []runtime.Object{
					withConditionReason(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), addAnnotations(defaultTemplateAnnotations, map[string]string{"new": "annotation"})), v1alpha1.CSVReasonInstallSuccessful),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(map[string]string{
							ownerutil.OwnerKey:          "csv1",
							ownerutil.OwnerNamespaceKey: namespace,
							ownerutil.OwnerKind:         "ClusterServiceVersion",
						}, withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
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
			name: "CSVSucceededToReplacing",
			initial: initial{
				csvs: []runtime.Object{
					withAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv2", namespace), withTemplateAnnotations(installStrategy("csv2-dep1", nil, nil), defaultTemplateAnnotations)),
					),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv2", namespace), withTemplateAnnotations(installStrategy("csv2-dep1", nil, nil), defaultTemplateAnnotations)),
					),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations), labels.Set{
						resolver.APILabelKeyPrefix + apiHash: "provided",
					}),
					csvWithLabels(csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations), labels.Set{
						resolver.APILabelKeyPrefix + apiHash: "provided",
					}),
					csvWithLabels(csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv2", namespace), withTemplateAnnotations(installStrategy("csv2-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv3", namespace), withTemplateAnnotations(installStrategy("csv3-dep1", nil, nil), defaultTemplateAnnotations)),
					),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv2",
						namespace,
						"0.0.0",
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv1", namespace), withTemplateAnnotations(installStrategy("csv1-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv2", namespace), withTemplateAnnotations(installStrategy("csv2-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv3", namespace), withTemplateAnnotations(installStrategy("csv3-dep1", nil, nil), defaultTemplateAnnotations)),
					),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv3",
						namespace,
						"0.0.0",
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv2", namespace), withTemplateAnnotations(installStrategy("csv2-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv3", namespace), withTemplateAnnotations(installStrategy("csv3-dep1", nil, nil), defaultTemplateAnnotations)),
					),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					), defaultTemplateAnnotations),
					csvWithAnnotations(csv("csv3",
						namespace,
						"0.0.0",
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), defaultTemplateAnnotations),
				},
				clientObjs: []runtime.Object{defaultOperatorGroup},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				objs: []runtime.Object{
					withLabels(
						deployment("csv2-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv2", namespace), withTemplateAnnotations(installStrategy("csv2-dep1", nil, nil), defaultTemplateAnnotations)),
					),
					withLabels(
						deployment("csv3-dep1", namespace, "sa", defaultTemplateAnnotations),
						addDepSpecHashLabel(ownerLabelFromCSV("csv3", namespace), withTemplateAnnotations(installStrategy("csv3-dep1", nil, nil), defaultTemplateAnnotations)),
					),
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
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
			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(
				ctx,
				withNamespaces(namespace, "kube-system"),
				withClientObjs(append(tt.initial.csvs, tt.initial.clientObjs...)...),
				withK8sObjs(tt.initial.objs...),
				withExtObjs(tt.initial.crds...),
				withRegObjs(tt.initial.apis...),
				withOperatorNamespace(namespace),
				withAPIReconciler(tt.config.apiReconciler),
				withAPILabeler(tt.config.apiLabeler),
			)
			require.NoError(t, err)

			// run csv sync for each CSV
			for _, csv := range tt.initial.csvs {
				err := op.syncClusterServiceVersion(csv)
				expectedErr := tt.expected.err[csv.(*v1alpha1.ClusterServiceVersion).Name]
				require.Equal(t, expectedErr, err)
			}

			// get csvs in the cluster
			outCSVMap := map[string]*v1alpha1.ClusterServiceVersion{}
			outCSVs, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(context.TODO(), metav1.ListOptions{})
			require.NoError(t, err)
			for _, csv := range outCSVs.Items {
				outCSVMap[csv.GetName()] = csv.DeepCopy()
			}

			// verify expectations of csvs in cluster
			for csvName, csvState := range tt.expected.csvStates {
				csv, ok := outCSVMap[csvName]
				require.Equal(t, ok, csvState.exists, "%s existence should be %t", csvName, csvState.exists)
				if csvState.exists {
					if csvState.reason != "" {
						require.EqualValues(t, string(csvState.reason), string(csv.Status.Reason), "%s had incorrect condition reason - %v", csvName, csv)
					}
				}
			}

			// Verify other objects
			if tt.expected.objs != nil {
				RequireObjectsInNamespace(t, op.opClient, op.client, namespace, tt.expected.objs)
			}
		})
	}
}

func TestWebhookCABundleRetrieval(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	namespace := "ns"
	missingCAError := fmt.Errorf("Unable to find ca")
	caBundle := []byte("Foo")

	type initial struct {
		csvs []runtime.Object
		crds []runtime.Object
		objs []runtime.Object
		desc v1alpha1.WebhookDescription
	}
	type expected struct {
		caBundle []byte
		err      error
	}
	tests := []struct {
		name     string
		initial  initial
		expected expected
	}{
		{
			name: "MissingCAResource",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]v1alpha1.StrategyDeploymentPermissions{},
						),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					),
				},
				desc: v1alpha1.WebhookDescription{
					GenerateName: "webhook",
					Type:         v1alpha1.ValidatingAdmissionWebhook,
				},
			},
			expected: expected{
				caBundle: nil,
				err:      missingCAError,
			},
		},
		{
			name: "RetrieveCAFromConversionWebhook",
			initial: initial{
				csvs: []runtime.Object{
					csvWithConversionWebhook(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]v1alpha1.StrategyDeploymentPermissions{},
						),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), "csv1-dep1", []string{"c1.g1"}),
				},
				crds: []runtime.Object{
					crdWithConversionWebhook(crd("c1", "v1", "g1"), caBundle),
				},
				desc: v1alpha1.WebhookDescription{
					GenerateName:   "webhook",
					Type:           v1alpha1.ConversionWebhook,
					ConversionCRDs: []string{"c1.g1"},
				},
			},
			expected: expected{
				caBundle: caBundle,
				err:      nil,
			},
		},
		{
			name: "FailToRetrieveCAFromConversionWebhook",
			initial: initial{
				csvs: []runtime.Object{
					csvWithConversionWebhook(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]v1alpha1.StrategyDeploymentPermissions{},
						),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), "csv1-dep1", []string{"c1.g1"}),
				},
				crds: []runtime.Object{
					crd("c1", "v1", "g1"),
				},
				desc: v1alpha1.WebhookDescription{
					GenerateName:   "webhook",
					Type:           v1alpha1.ConversionWebhook,
					ConversionCRDs: []string{"c1.g1"},
				},
			},
			expected: expected{
				caBundle: nil,
				err:      missingCAError,
			},
		},
		{
			name: "RetrieveFromValidatingAdmissionWebhook",
			initial: initial{
				csvs: []runtime.Object{
					csvWithValidatingAdmissionWebhook(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]v1alpha1.StrategyDeploymentPermissions{},
						),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), "csv1-dep1", []string{"c1.g1"}),
				},
				objs: []runtime.Object{
					&admissionregistrationv1.ValidatingWebhookConfiguration{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "webhook",
							Namespace: namespace,
							Labels: map[string]string{
								"olm.owner":                             "csv1",
								"olm.owner.namespace":                   namespace,
								"olm.owner.kind":                        v1alpha1.ClusterServiceVersionKind,
								"olm.webhook-description-generate-name": "webhook",
							},
						},
						Webhooks: []admissionregistrationv1.ValidatingWebhook{
							{
								Name: "Webhook",
								ClientConfig: admissionregistrationv1.WebhookClientConfig{
									CABundle: caBundle,
								},
							},
						},
					},
				},
				desc: v1alpha1.WebhookDescription{
					GenerateName: "webhook",
					Type:         v1alpha1.ValidatingAdmissionWebhook,
				},
			},
			expected: expected{
				caBundle: caBundle,
				err:      nil,
			},
		},
		{
			name: "RetrieveFromMutatingAdmissionWebhook",
			initial: initial{
				csvs: []runtime.Object{
					csvWithMutatingAdmissionWebhook(csv("csv1",
						namespace,
						"0.0.0",
						"",
						installStrategy("csv1-dep1",
							nil,
							[]v1alpha1.StrategyDeploymentPermissions{},
						),
						[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
						[]*apiextensionsv1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					), "csv1-dep1", []string{"c1.g1"}),
				},
				objs: []runtime.Object{
					&admissionregistrationv1.MutatingWebhookConfiguration{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "webhook",
							Namespace: namespace,
							Labels: map[string]string{
								"olm.owner":                             "csv1",
								"olm.owner.namespace":                   namespace,
								"olm.owner.kind":                        v1alpha1.ClusterServiceVersionKind,
								"olm.webhook-description-generate-name": "webhook",
							},
						},
						Webhooks: []admissionregistrationv1.MutatingWebhook{
							{
								Name: "Webhook",
								ClientConfig: admissionregistrationv1.WebhookClientConfig{
									CABundle: caBundle,
								},
							},
						},
					},
				},
				desc: v1alpha1.WebhookDescription{
					GenerateName: "webhook",
					Type:         v1alpha1.MutatingAdmissionWebhook,
				},
			},
			expected: expected{
				caBundle: caBundle,
				err:      nil,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(
				ctx,
				withNamespaces(namespace, "kube-system"),
				withClientObjs(tt.initial.csvs...),
				withK8sObjs(tt.initial.objs...),
				withExtObjs(tt.initial.crds...),
				withOperatorNamespace(namespace),
			)
			require.NoError(t, err)

			// run csv sync for each CSV
			for _, csv := range tt.initial.csvs {
				caBundle, err := op.getWebhookCABundle(csv.(*v1alpha1.ClusterServiceVersion), &tt.initial.desc)
				require.Equal(t, tt.expected.err, err)
				require.Equal(t, tt.expected.caBundle, caBundle)
			}
		})
	}
}

// TestUpdates verifies that a set of expected phase transitions occur when multiple CSVs are present
// and that they do not depend on sync order or event order
func TestUpdates(t *testing.T) {
	// A - replacedby -> B - replacedby -> C
	namespace := "ns"
	defaultOperatorGroup := &v1.OperatorGroup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OperatorGroup",
			APIVersion: v1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: namespace,
		},
		Spec: v1.OperatorGroupSpec{
			TargetNamespaces: []string{namespace},
		},
		Status: v1.OperatorGroupStatus{
			Namespaces: []string{namespace},
		},
	}
	defaultTemplateAnnotations := map[string]string{
		v1.OperatorGroupTargetsAnnotationKey:   namespace,
		v1.OperatorGroupNamespaceAnnotationKey: namespace,
		v1.OperatorGroupAnnotationKey:          defaultOperatorGroup.GetName(),
	}
	runningOperator := []runtime.Object{
		withLabels(
			deployment("csv1-dep1", namespace, "sa", defaultTemplateAnnotations),
			map[string]string{
				ownerutil.OwnerKey:          "csv1",
				ownerutil.OwnerNamespaceKey: namespace,
				ownerutil.OwnerKind:         "ClusterServiceVersion",
			},
		),
	}

	deleted := v1alpha1.ClusterServiceVersionPhase("deleted")
	deploymentName := "csv1-dep1"
	crd := crd("c1", "v1", "g1")
	a := csv("csvA",
		namespace,
		"0.0.0",
		"",
		installStrategy(deploymentName, nil, nil),
		[]*apiextensionsv1.CustomResourceDefinition{crd},
		[]*apiextensionsv1.CustomResourceDefinition{},
		v1alpha1.CSVPhaseNone)
	b := csv("csvB",
		namespace,
		"0.0.0",
		"csvA",
		installStrategy(deploymentName, nil, nil),
		[]*apiextensionsv1.CustomResourceDefinition{crd},
		[]*apiextensionsv1.CustomResourceDefinition{},
		v1alpha1.CSVPhaseNone)
	c := csv("csvC",
		namespace,
		"0.0.0",
		"csvB",
		installStrategy(deploymentName, nil, nil),
		[]*apiextensionsv1.CustomResourceDefinition{crd},
		[]*apiextensionsv1.CustomResourceDefinition{},
		v1alpha1.CSVPhaseNone)

	simulateSuccessfulRollout := func(csv *v1alpha1.ClusterServiceVersion, client operatorclient.ClientInterface) {
		// get the deployment, which should exist
		dep, err := client.GetDeployment(namespace, deploymentName)
		require.NoError(t, err)

		// force it healthy
		dep.Status.Replicas = 1
		dep.Status.UpdatedReplicas = 1
		dep.Status.AvailableReplicas = 1
		_, err = client.KubernetesInterface().AppsV1().Deployments(namespace).UpdateStatus(context.TODO(), dep, metav1.UpdateOptions{})
		require.NoError(t, err)
	}

	// when csv A is in phase, X, expect B and C to be in state Y
	type csvPhaseKey struct {
		name  string
		phase v1alpha1.ClusterServiceVersionPhase
	}
	type expectation struct {
		whenIn   csvPhaseKey
		shouldBe map[string]v1alpha1.ClusterServiceVersionPhase
	}
	// for a given CSV and phase, set the expected phases of the other CSVs
	expected := []expectation{
		{
			whenIn: csvPhaseKey{name: a.GetName(), phase: v1alpha1.CSVPhaseNone},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				b.GetName(): v1alpha1.CSVPhaseNone,
				c.GetName(): v1alpha1.CSVPhaseNone,
			},
		},
		{
			whenIn: csvPhaseKey{name: a.GetName(), phase: v1alpha1.CSVPhasePending},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				b.GetName(): v1alpha1.CSVPhasePending,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: a.GetName(), phase: v1alpha1.CSVPhaseInstallReady},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				b.GetName(): v1alpha1.CSVPhasePending,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: a.GetName(), phase: v1alpha1.CSVPhaseInstalling},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				b.GetName(): v1alpha1.CSVPhasePending,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: a.GetName(), phase: v1alpha1.CSVPhaseSucceeded},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				b.GetName(): v1alpha1.CSVPhasePending,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: b.GetName(), phase: v1alpha1.CSVPhaseInstallReady},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				a.GetName(): v1alpha1.CSVPhaseReplacing,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: b.GetName(), phase: v1alpha1.CSVPhaseInstalling},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				a.GetName(): v1alpha1.CSVPhaseReplacing,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: b.GetName(), phase: v1alpha1.CSVPhaseSucceeded},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				a.GetName(): v1alpha1.CSVPhaseDeleting,
				c.GetName(): v1alpha1.CSVPhasePending,
			},
		},
		{
			whenIn: csvPhaseKey{name: c.GetName(), phase: v1alpha1.CSVPhaseInstallReady},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				a.GetName(): deleted,
				b.GetName(): v1alpha1.CSVPhaseReplacing,
			},
		},
		{
			whenIn: csvPhaseKey{name: c.GetName(), phase: v1alpha1.CSVPhaseInstalling},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				a.GetName(): deleted,
				b.GetName(): v1alpha1.CSVPhaseReplacing,
			},
		},
		{
			whenIn: csvPhaseKey{name: c.GetName(), phase: v1alpha1.CSVPhaseSucceeded},
			shouldBe: map[string]v1alpha1.ClusterServiceVersionPhase{
				a.GetName(): deleted,
				b.GetName(): deleted,
			},
		},
	}
	tests := []struct {
		name string
		in   []*v1alpha1.ClusterServiceVersion
	}{
		{
			name: "abc",
			in:   []*v1alpha1.ClusterServiceVersion{a, b, c},
		},
		{
			name: "acb",
			in:   []*v1alpha1.ClusterServiceVersion{a, c, b},
		},
		{
			name: "bac",
			in:   []*v1alpha1.ClusterServiceVersion{b, a, c},
		},
		{
			name: "bca",
			in:   []*v1alpha1.ClusterServiceVersion{b, c, a},
		},
		{
			name: "cba",
			in:   []*v1alpha1.ClusterServiceVersion{c, b, a},
		},
		{
			name: "cab",
			in:   []*v1alpha1.ClusterServiceVersion{c, a, b},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Setup fake operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(
				ctx,
				withExtObjs(crd),
				withClientObjs(defaultOperatorGroup),
				withK8sObjs(runningOperator...),
				withNamespaces(namespace),
			)
			require.NoError(t, err)

			// helper to get the latest view of a set of CSVs from the set - we only expect no errors if not deleted
			fetchLatestCSVs := func(csvsToSync map[string]*v1alpha1.ClusterServiceVersion, deleted map[string]struct{}) (out map[string]*v1alpha1.ClusterServiceVersion) {
				out = map[string]*v1alpha1.ClusterServiceVersion{}
				for name := range csvsToSync {
					fetched, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
					if _, ok := deleted[name]; !ok {
						require.NoError(t, err)
						out[name] = fetched
					}
				}
				return out
			}

			// helper to sync a set of csvs, in order, and return the latest view from the cluster
			syncCSVs := func(csvsToSync map[string]*v1alpha1.ClusterServiceVersion, deleted map[string]struct{}) (out map[string]*v1alpha1.ClusterServiceVersion) {
				for name, csv := range csvsToSync {
					_ = op.syncClusterServiceVersion(csv)
					if _, ok := deleted[name]; !ok {
						require.NoError(t, err)
					}
				}
				return fetchLatestCSVs(csvsToSync, deleted)
			}

			// helper, given a set of expectations, pull out which entries we expect to have been deleted from the cluster
			deletedCSVs := func(shouldBe map[string]v1alpha1.ClusterServiceVersionPhase) map[string]struct{} {
				out := map[string]struct{}{}
				for name, phase := range shouldBe {
					if phase != deleted {
						continue
					}
					out[name] = struct{}{}
				}
				return out
			}

			// Create input CSV set
			csvsToSync := map[string]*v1alpha1.ClusterServiceVersion{}
			for _, csv := range tt.in {
				_, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(context.TODO(), csv, metav1.CreateOptions{})
				require.NoError(t, err)
				csvsToSync[csv.GetName()] = csv
			}

			for _, e := range expected {
				// get the latest view from the cluster
				csvsToSync = fetchLatestCSVs(csvsToSync, deletedCSVs(e.shouldBe))

				// sync the current csv until it's reached the expected status
				current := csvsToSync[e.whenIn.name]

				if current.Status.Phase == v1alpha1.CSVPhaseInstalling {
					simulateSuccessfulRollout(current, op.opClient)
				}
				for current.Status.Phase != e.whenIn.phase {
					fmt.Printf("waiting for (when) %s to be %s\n", e.whenIn.name, e.whenIn.phase)
					csvsToSync = syncCSVs(csvsToSync, deletedCSVs(e.shouldBe))
					current = csvsToSync[e.whenIn.name]
				}

				// sync the other csvs until they're in the expected status
				for name, phase := range e.shouldBe {
					if phase == deleted {
						// todo verify deleted
						continue
					}
					other := csvsToSync[name]
					for other.Status.Phase != phase {
						fmt.Printf("waiting for %s to be %s\n", name, phase)
						_ = op.syncClusterServiceVersion(other)
						other, err = op.client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
						require.NoError(t, err)
					}
					csvsToSync[name] = other
				}

				for name, phase := range e.shouldBe {
					if phase == deleted {
						continue
					}
					require.Equal(t, phase, csvsToSync[name].Status.Phase)
				}
			}
		})
	}
}

func TestSyncOperatorGroups(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)
	clockFake := utilclock.NewFakeClock(time.Date(2006, time.January, 2, 15, 4, 5, 0, time.FixedZone("MST", -7*3600)))
	now := metav1.NewTime(clockFake.Now().UTC())

	operatorNamespace := "operator-ns"
	targetNamespace := "target-ns"

	serviceAccount := serviceAccount("sa", operatorNamespace)

	permissions := []v1alpha1.StrategyDeploymentPermissions{
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
	deploymentName := "csv1-dep1"
	crd := crd("c1", "v1", "fake.api.group")
	operatorCSV := csvWithLabels(csv("csv1",
		operatorNamespace,
		"0.0.0",
		"",
		installStrategy(deploymentName, permissions, nil),
		[]*apiextensionsv1.CustomResourceDefinition{crd},
		[]*apiextensionsv1.CustomResourceDefinition{},
		v1alpha1.CSVPhaseNone,
	), labels.Set{resolver.APILabelKeyPrefix + "9f4c46c37bdff8d0": "provided"})

	operatorCSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs[0].Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{corev1.EnvVar{
		Name:  "OPERATOR_CONDITION_NAME",
		Value: operatorCSV.GetName(),
	}}

	serverVersion := version.Get().String()
	// after state transitions from operatorgroups, this is the operator csv we expect
	operatorCSVFinal := operatorCSV.DeepCopy()
	operatorCSVFinal.Status.Phase = v1alpha1.CSVPhaseSucceeded
	operatorCSVFinal.Status.Message = "install strategy completed with no errors"
	operatorCSVFinal.Status.Reason = v1alpha1.CSVReasonInstallSuccessful
	operatorCSVFinal.Status.LastUpdateTime = &now
	operatorCSVFinal.Status.LastTransitionTime = &now
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
			Version: "v1",
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
					Version: "v1",
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
			LastUpdateTime:     &now,
			LastTransitionTime: &now,
		},
		{
			Phase:              v1alpha1.CSVPhaseInstallReady,
			Reason:             v1alpha1.CSVReasonRequirementsMet,
			Message:            "all requirements found, attempting install",
			LastUpdateTime:     &now,
			LastTransitionTime: &now,
		},
		{
			Phase:              v1alpha1.CSVPhaseInstalling,
			Reason:             v1alpha1.CSVReasonInstallSuccessful,
			Message:            "waiting for install components to report healthy",
			LastUpdateTime:     &now,
			LastTransitionTime: &now,
		},
		{
			Phase:              v1alpha1.CSVPhaseSucceeded,
			Reason:             v1alpha1.CSVReasonInstallSuccessful,
			Message:            "install strategy completed with no errors",
			LastUpdateTime:     &now,
			LastTransitionTime: &now,
		},
	}

	// Failed CSV due to operatorgroup namespace selector doesn't any existing namespaces
	operatorCSVFailedNoTargetNS := operatorCSV.DeepCopy()
	operatorCSVFailedNoTargetNS.Status.Phase = v1alpha1.CSVPhaseFailed
	operatorCSVFailedNoTargetNS.Status.Message = "no targetNamespaces are matched operatorgroups namespace selection"
	operatorCSVFailedNoTargetNS.Status.Reason = v1alpha1.CSVReasonNoTargetNamespaces
	operatorCSVFailedNoTargetNS.Status.LastUpdateTime = &now
	operatorCSVFailedNoTargetNS.Status.LastTransitionTime = &now
	operatorCSVFailedNoTargetNS.Status.Conditions = []v1alpha1.ClusterServiceVersionCondition{
		{
			Phase:              v1alpha1.CSVPhaseFailed,
			Reason:             v1alpha1.CSVReasonNoTargetNamespaces,
			Message:            "no targetNamespaces are matched operatorgroups namespace selection",
			LastUpdateTime:     &now,
			LastTransitionTime: &now,
		},
	}

	targetCSV := operatorCSVFinal.DeepCopy()
	targetCSV.SetNamespace(targetNamespace)
	targetCSV.Status.Reason = v1alpha1.CSVReasonCopied
	targetCSV.Status.Message = "The operator is running in operator-ns but is managing this namespace"
	targetCSV.Status.LastUpdateTime = &now

	ownerutil.AddNonBlockingOwner(serviceAccount, operatorCSV)

	ownedDeployment := deployment(deploymentName, operatorNamespace, serviceAccount.GetName(), nil)
	ownedDeployment.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: "OPERATOR_CONDITION_NAME", Value: "csv1"}}
	ownerutil.AddNonBlockingOwner(ownedDeployment, operatorCSV)
	deploymentSpec := installStrategy(deploymentName, permissions, nil).StrategySpec.DeploymentSpecs[0].Spec
	ownedDeployment.SetLabels(map[string]string{
		install.DeploymentSpecHashLabelKey: install.HashDeploymentSpec(deploymentSpec),
	})

	annotatedDeployment := ownedDeployment.DeepCopy()
	annotatedDeployment.Spec.Template.SetAnnotations(map[string]string{v1.OperatorGroupTargetsAnnotationKey: operatorNamespace + "," + targetNamespace, v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace})
	annotatedDeployment.SetLabels(map[string]string{
		"olm.owner":                        "csv1",
		"olm.owner.namespace":              "operator-ns",
		"olm.owner.kind":                   "ClusterServiceVersion",
		install.DeploymentSpecHashLabelKey: install.HashDeploymentSpec(annotatedDeployment.Spec),
	})

	annotatedGlobalDeployment := ownedDeployment.DeepCopy()
	annotatedGlobalDeployment.Spec.Template.SetAnnotations(map[string]string{v1.OperatorGroupTargetsAnnotationKey: "", v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace})
	annotatedGlobalDeployment.SetLabels(map[string]string{
		"olm.owner":                        "csv1",
		"olm.owner.namespace":              "operator-ns",
		"olm.owner.kind":                   "ClusterServiceVersion",
		install.DeploymentSpecHashLabelKey: install.HashDeploymentSpec(annotatedGlobalDeployment.Spec),
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
		initial         initial
		name            string
		expectedEqual   bool
		expectedStatus  v1.OperatorGroupStatus
		final           final
		ignoreCopyError bool
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
			name:          "NoMatchingNamespace/CSVPresent",
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
			expectedStatus: v1.OperatorGroupStatus{},
			final: final{objects: map[string][]runtime.Object{
				operatorNamespace: {
					withAnnotations(operatorCSVFailedNoTargetNS.DeepCopy(), map[string]string{v1.OperatorGroupAnnotationKey: "operator-group-1", v1.OperatorGroupNamespaceAnnotationKey: operatorNamespace}),
				},
			}},
			ignoreCopyError: true,
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
				LastUpdated: &now,
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
				LastUpdated: &now,
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
							ResourceVersion: "0",
							Name:            "csv-role",
							Namespace:       targetNamespace,
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
							ResourceVersion: "0",
							Name:            "csv-rolebinding",
							Namespace:       targetNamespace,
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
				LastUpdated: &now,
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
							ResourceVersion: "0",
							Name:            "csv-role",
							Namespace:       targetNamespace,
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
							ResourceVersion: "0",
							Name:            "csv-rolebinding",
							Namespace:       targetNamespace,
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
				LastUpdated: &now,
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
				LastUpdated: &now,
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
							LastUpdated: &now,
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
				LastUpdated: &now,
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
						now),
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

			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(
				ctx,
				withClock(clockFake),
				withNamespaces(namespaces...),
				withOperatorNamespace(operatorNamespace),
				withClientObjs(tt.initial.clientObjs...),
				withK8sObjs(tt.initial.k8sObjs...),
				withExtObjs(tt.initial.crds...),
				withRegObjs(tt.initial.apis...),
			)
			require.NoError(t, err)

			simulateSuccessfulRollout := func(csv *v1alpha1.ClusterServiceVersion, client operatorclient.ClientInterface) {
				// get the deployment, which should exist
				dep, err := client.GetDeployment(tt.initial.operatorGroup.GetNamespace(), deploymentName)
				require.NoError(t, err)

				// force it healthy
				dep.Status.Replicas = 1
				dep.Status.UpdatedReplicas = 1
				dep.Status.AvailableReplicas = 1
				_, err = client.KubernetesInterface().AppsV1().Deployments(tt.initial.operatorGroup.GetNamespace()).UpdateStatus(context.TODO(), dep, metav1.UpdateOptions{})
				require.NoError(t, err)
			}

			err = op.syncOperatorGroups(tt.initial.operatorGroup)
			require.NoError(t, err)

			// wait on operator group updated status to be in the cache as it is required for later CSV operations
			err = wait.PollImmediate(1*time.Millisecond, 5*time.Second, func() (bool, error) {
				operatorGroup, err := op.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(tt.initial.operatorGroup.GetNamespace()).Get(tt.initial.operatorGroup.GetName())
				if err != nil {
					return false, err
				}
				sort.Strings(tt.expectedStatus.Namespaces)
				sort.Strings(operatorGroup.Status.Namespaces)
				if !reflect.DeepEqual(tt.expectedStatus, operatorGroup.Status) {
					return false, err
				}
				return true, nil
			})
			require.NoError(t, err)

			// this must be done twice to have annotateCSVs run in syncOperatorGroups
			// and to catch provided API changes
			err = op.syncOperatorGroups(tt.initial.operatorGroup)
			require.NoError(t, err)

			// Sync csvs enough to get them back to succeeded state
			for i := 0; i < 16; i++ {
				opGroupCSVs, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(operatorNamespace).List(context.TODO(), metav1.ListOptions{})
				require.NoError(t, err)

				for i, obj := range opGroupCSVs.Items {
					if obj.Status.Phase == v1alpha1.CSVPhaseInstalling {
						simulateSuccessfulRollout(&obj, op.opClient)
					}
					err = op.syncClusterServiceVersion(&obj)
					require.NoError(t, err, "%#v", obj)

					err = op.syncCopyCSV(&obj)
					if !tt.ignoreCopyError {
						require.NoError(t, err, "%#v", obj)
					}

					if i == 0 {
						err = wait.PollImmediate(1*time.Millisecond, 10*time.Second, func() (bool, error) {
							for namespace, objects := range tt.final.objects {
								if err := RequireObjectsInCache(t, op.lister, namespace, objects, false); err != nil {
									return false, nil
								}
							}
							return true, nil
						})
						require.NoError(t, err)
					}

					if i == 16 {
						err = wait.PollImmediate(1*time.Millisecond, 10*time.Second, func() (bool, error) {
							for namespace, objects := range tt.final.objects {
								if err := RequireObjectsInCache(t, op.lister, namespace, objects, true); err != nil {
									return false, nil
								}
							}
							return true, nil
						})
						require.NoError(t, err)
					}
				}
			}

			operatorGroup, err := op.client.OperatorsV1().OperatorGroups(tt.initial.operatorGroup.GetNamespace()).Get(context.TODO(), tt.initial.operatorGroup.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			sort.Strings(tt.expectedStatus.Namespaces)
			sort.Strings(operatorGroup.Status.Namespaces)
			assert.Equal(t, tt.expectedStatus, operatorGroup.Status)

			for namespace, objects := range tt.final.objects {
				RequireObjectsInNamespace(t, op.opClient, op.client, namespace, objects)
			}
		})
	}
}

func RequireObjectsInCache(t *testing.T, lister operatorlister.OperatorLister, namespace string, objects []runtime.Object, doCompare bool) error {
	for _, object := range objects {
		var err error
		var fetched runtime.Object
		switch o := object.(type) {
		case *appsv1.Deployment:
			fetched, err = lister.AppsV1().DeploymentLister().Deployments(namespace).Get(o.GetName())
		case *rbacv1.ClusterRole:
			fetched, err = lister.RbacV1().ClusterRoleLister().Get(o.GetName())
		case *rbacv1.Role:
			fetched, err = lister.RbacV1().RoleLister().Roles(namespace).Get(o.GetName())
		case *rbacv1.ClusterRoleBinding:
			fetched, err = lister.RbacV1().ClusterRoleBindingLister().Get(o.GetName())
		case *rbacv1.RoleBinding:
			fetched, err = lister.RbacV1().RoleBindingLister().RoleBindings(namespace).Get(o.GetName())
		case *v1alpha1.ClusterServiceVersion:
			fetched, err = lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(namespace).Get(o.GetName())
		case *v1.OperatorGroup:
			fetched, err = lister.OperatorsV1().OperatorGroupLister().OperatorGroups(namespace).Get(o.GetName())
		default:
			require.Failf(t, "couldn't find expected object", "%#v", object)
		}
		if err != nil {
			return fmt.Errorf("namespace: %v, error: %v", namespace, err)
		}
		if doCompare {
			if !reflect.DeepEqual(object, fetched) {
				return fmt.Errorf("expected object didn't match %v: %s", object, cmp.Diff(object, fetched))
			}
		}
	}
	return nil
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
			fetched, err = client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), o.GetName(), metav1.GetOptions{})
			// This protects against small timing issues in sync tests
			// We generally don't care about the conditions (state history in this case, unlike many kube resources)
			// and this will still check that the final state is correct
			object.(*v1alpha1.ClusterServiceVersion).Status.Conditions = nil
			fetched.(*v1alpha1.ClusterServiceVersion).Status.Conditions = nil
		case *v1.OperatorGroup:
			fetched, err = client.OperatorsV1().OperatorGroups(namespace).Get(context.TODO(), o.GetName(), metav1.GetOptions{})
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
			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(ctx, withNamespaces(namespace), withClientObjs(tt.initial.csvs...))
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
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(ctx, withNamespaces(namespace))
			require.NoError(t, err)

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
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()
			op, err := NewFakeOperator(ctx, withNamespaces(namespace))
			require.NoError(t, err)
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

func crdWithConversionWebhook(crd *apiextensionsv1.CustomResourceDefinition, caBundle []byte) *apiextensionsv1.CustomResourceDefinition {
	crd.Spec.Conversion = &apiextensionsv1.CustomResourceConversion{
		Strategy: "Webhook",
		Webhook: &apiextensionsv1.WebhookConversion{
			ConversionReviewVersions: []string{"v1beta1"},
			ClientConfig: &apiextensionsv1.WebhookClientConfig{
				CABundle: caBundle,
			},
		},
	}
	return crd
}

func csvWithConversionWebhook(csv *v1alpha1.ClusterServiceVersion, deploymentName string, conversionCRDs []string) *v1alpha1.ClusterServiceVersion {
	return csvWithWebhook(csv, deploymentName, conversionCRDs, v1alpha1.ConversionWebhook)
}

func csvWithValidatingAdmissionWebhook(csv *v1alpha1.ClusterServiceVersion, deploymentName string, conversionCRDs []string) *v1alpha1.ClusterServiceVersion {
	return csvWithWebhook(csv, deploymentName, conversionCRDs, v1alpha1.ValidatingAdmissionWebhook)
}

func csvWithMutatingAdmissionWebhook(csv *v1alpha1.ClusterServiceVersion, deploymentName string, conversionCRDs []string) *v1alpha1.ClusterServiceVersion {
	return csvWithWebhook(csv, deploymentName, conversionCRDs, v1alpha1.MutatingAdmissionWebhook)
}

func csvWithWebhook(csv *v1alpha1.ClusterServiceVersion, deploymentName string, conversionCRDs []string, webhookType v1alpha1.WebhookAdmissionType) *v1alpha1.ClusterServiceVersion {
	sideEffectNone := admissionregistrationv1.SideEffectClassNone
	targetPort := intstr.FromInt(443)
	csv.Spec.WebhookDefinitions = []v1alpha1.WebhookDescription{
		{
			Type:                    webhookType,
			DeploymentName:          deploymentName,
			ContainerPort:           443,
			TargetPort:              &targetPort,
			SideEffects:             &sideEffectNone,
			ConversionCRDs:          conversionCRDs,
			AdmissionReviewVersions: []string{"v1beta1"},
		},
	}
	return csv
}

func TestGetReplacementChain(t *testing.T) {
	CSV := func(name, replaces string) *v1alpha1.ClusterServiceVersion {
		return &v1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: v1alpha1.ClusterServiceVersionSpec{
				Replaces: replaces,
			},
		}
	}

	for _, tc := range []struct {
		Name     string
		From     *v1alpha1.ClusterServiceVersion
		All      map[string]*v1alpha1.ClusterServiceVersion
		Expected []string
	}{
		{
			Name: "csv replaces itself",
			From: CSV("itself", "itself"),
			All: map[string]*v1alpha1.ClusterServiceVersion{
				"itself": CSV("itself", "itself"),
			},
			Expected: []string{"itself"},
		},
		{
			Name: "two csvs replace each other",
			From: CSV("a", "b"),
			All: map[string]*v1alpha1.ClusterServiceVersion{
				"a": CSV("a", "b"),
				"b": CSV("b", "a"),
			},
			Expected: []string{"a", "b"},
		},
		{
			Name: "starting from head of chain without cycles",
			From: CSV("a", "b"),
			All: map[string]*v1alpha1.ClusterServiceVersion{
				"a": CSV("a", "b"),
				"b": CSV("b", "c"),
				"c": CSV("c", ""),
			},
			Expected: []string{"a", "b", "c"},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assert := assert.New(t)
			var actual []string
			for name := range (&Operator{}).getReplacementChain(tc.From, tc.All) {
				actual = append(actual, name)
			}
			assert.ElementsMatch(tc.Expected, actual)
		})
	}
}
