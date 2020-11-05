package reconciler

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	k8slabels "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/labels"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

const (
	registryImageName = "test:image"
	testNamespace     = "testns"
)

type fakeReconcilerConfig struct {
	now                  nowFunc
	k8sObjs              []runtime.Object
	k8sClientOptions     []clientfake.Option
	configMapServerImage string
}

type fakeReconcilerOption func(*fakeReconcilerConfig)

func withNow(now nowFunc) fakeReconcilerOption {
	return func(config *fakeReconcilerConfig) {
		config.now = now
	}
}

func withK8sObjs(k8sObjs ...runtime.Object) fakeReconcilerOption {
	return func(config *fakeReconcilerConfig) {
		config.k8sObjs = k8sObjs
	}
}

func withK8sClientOptions(options ...clientfake.Option) fakeReconcilerOption {
	return func(config *fakeReconcilerConfig) {
		config.k8sClientOptions = options
	}
}

func withConfigMapServerImage(configMapServerImage string) fakeReconcilerOption {
	return func(config *fakeReconcilerConfig) {
		config.configMapServerImage = configMapServerImage
	}
}

func fakeReconcilerFactory(t *testing.T, stopc <-chan struct{}, options ...fakeReconcilerOption) (RegistryReconcilerFactory, operatorclient.ClientInterface) {
	config := &fakeReconcilerConfig{
		now:                  metav1.Now,
		configMapServerImage: registryImageName,
	}

	// Apply all config options
	for _, option := range options {
		option(config)
	}

	opClientFake := operatorclient.NewClient(clientfake.NewReactionForwardingClientsetDecorator(config.k8sObjs, config.k8sClientOptions...), nil, nil)

	// Creates registry pods in response to configmaps
	informerFactory := informers.NewSharedInformerFactory(opClientFake.KubernetesInterface(), 5*time.Second)
	roleInformer := informerFactory.Rbac().V1().Roles()
	roleBindingInformer := informerFactory.Rbac().V1().RoleBindings()
	serviceAccountInformer := informerFactory.Core().V1().ServiceAccounts()
	serviceInformer := informerFactory.Core().V1().Services()
	podInformer := informerFactory.Core().V1().Pods()
	configMapInformer := informerFactory.Core().V1().ConfigMaps()

	registryInformers := []cache.SharedIndexInformer{
		roleInformer.Informer(),
		roleBindingInformer.Informer(),
		serviceAccountInformer.Informer(),
		serviceInformer.Informer(),
		podInformer.Informer(),
		configMapInformer.Informer(),
	}

	lister := operatorlister.NewLister()
	lister.RbacV1().RegisterRoleLister(testNamespace, roleInformer.Lister())
	lister.RbacV1().RegisterRoleBindingLister(testNamespace, roleBindingInformer.Lister())
	lister.CoreV1().RegisterServiceAccountLister(testNamespace, serviceAccountInformer.Lister())
	lister.CoreV1().RegisterServiceLister(testNamespace, serviceInformer.Lister())
	lister.CoreV1().RegisterPodLister(testNamespace, podInformer.Lister())
	lister.CoreV1().RegisterConfigMapLister(testNamespace, configMapInformer.Lister())

	rec := &registryReconcilerFactory{
		now:                  config.now,
		OpClient:             opClientFake,
		Lister:               lister,
		ConfigMapServerImage: config.configMapServerImage,
	}

	var hasSyncedCheckFns []cache.InformerSynced
	for _, informer := range registryInformers {
		hasSyncedCheckFns = append(hasSyncedCheckFns, informer.HasSynced)
		go informer.Run(stopc)
	}

	require.True(t, cache.WaitForCacheSync(stopc, hasSyncedCheckFns...), "caches failed to sync")

	return rec, opClientFake
}

func crd(name string) v1beta1.CustomResourceDefinition {
	return v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: name + "group",
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
				},
			},
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

func validConfigMap() *corev1.ConfigMap {
	data := make(map[string]string)
	dataYaml, _ := yaml.Marshal([]v1beta1.CustomResourceDefinition{crd("fake-crd")})
	data["customResourceDefinitions"] = string(dataYaml)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "cool-configmap",
			Namespace:       testNamespace,
			UID:             types.UID("configmap-uid"),
			ResourceVersion: "resource-version",
		},
		Data: data,
	}
}

func TestValidConfigMap(t *testing.T) {
	cm := validConfigMap()
	require.NotNil(t, cm)
	require.Contains(t, cm.Data[registry.ConfigMapCRDName], "fake")
}

func validConfigMapCatalogSource(configMap *corev1.ConfigMap) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cool-catalog",
			Namespace: testNamespace,
			UID:       types.UID("catalog-uid"),
			Labels:    map[string]string{"olm.catalogSource": "cool-catalog"},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			ConfigMap:  "cool-configmap",
			SourceType: v1alpha1.SourceTypeConfigmap,
		},
		Status: v1alpha1.CatalogSourceStatus{
			ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
				Name:            configMap.GetName(),
				Namespace:       configMap.GetNamespace(),
				UID:             configMap.GetUID(),
				ResourceVersion: configMap.GetResourceVersion(),
			},
		},
	}
}

func objectsForCatalogSource(catsrc *v1alpha1.CatalogSource) []runtime.Object {
	var objs []runtime.Object
	switch catsrc.Spec.SourceType {
	case v1alpha1.SourceTypeInternal, v1alpha1.SourceTypeConfigmap:
		decorated := configMapCatalogSourceDecorator{catsrc}
		objs = clientfake.AddSimpleGeneratedNames(
			clientfake.AddSimpleGeneratedName(decorated.Pod(registryImageName)),
			decorated.Service(),
			decorated.ServiceAccount(),
			decorated.Role(),
			decorated.RoleBinding(),
		)
	case v1alpha1.SourceTypeGrpc:
		if catsrc.Spec.Image != "" {
			decorated := grpcCatalogSourceDecorator{catsrc}
			objs = clientfake.AddSimpleGeneratedNames(
				decorated.Pod(),
				decorated.Service(),
			)
		}
	}

	blockOwnerDeletion := false
	isController := false
	for _, o := range objs {
		mo := o.(metav1.Object)
		mo.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion:         "operators.coreos.com/v1alpha1",
			Kind:               "CatalogSource",
			Name:               catsrc.GetName(),
			UID:                catsrc.GetUID(),
			BlockOwnerDeletion: &blockOwnerDeletion,
			Controller:         &isController,
		}})
	}
	return objs
}

func modifyObjName(objs []runtime.Object, kind runtime.Object, newName string) []runtime.Object {
	out := []runtime.Object{}
	t := reflect.TypeOf(kind)
	for _, obj := range objs {
		o := obj.DeepCopyObject()
		if reflect.TypeOf(o) == t {
			if accessor, err := meta.Accessor(o); err == nil {
				accessor.SetName(newName)
			}
		}
		out = append(out, o)
	}
	return out
}

func setLabel(objs []runtime.Object, kind runtime.Object, label, value string) []runtime.Object {
	out := []runtime.Object{}
	t := reflect.TypeOf(kind)
	for _, obj := range objs {
		o := obj.DeepCopyObject()
		if reflect.TypeOf(o) == t {
			if accessor, err := meta.Accessor(o); err == nil {
				k8slabels.AddLabel(accessor.GetLabels(), label, value)
			}
		}
		out = append(out, o)
	}
	return out
}

func TestConfigMapRegistryReconciler(t *testing.T) {
	now := func() metav1.Time { return metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC) }

	validConfigMap := validConfigMap()
	validCatalogSource := validConfigMapCatalogSource(validConfigMap)
	outdatedCatalogSource := validCatalogSource.DeepCopy()
	outdatedCatalogSource.Status.ConfigMapResource.ResourceVersion = "old"
	type cluster struct {
		k8sObjs []runtime.Object
	}
	type in struct {
		cluster cluster
		catsrc  *v1alpha1.CatalogSource
	}
	type out struct {
		status *v1alpha1.RegistryServiceStatus
		err    error
	}
	tests := []struct {
		testName string
		in       in
		out      out
	}{
		{
			testName: "NoConfigMap",
			in: in{
				cluster: cluster{},
				catsrc: &v1alpha1.CatalogSource{
					Spec: v1alpha1.CatalogSourceSpec{
						SourceType: v1alpha1.SourceTypeConfigmap,
					},
				},
			},
			out: out{
				err: fmt.Errorf("unable to get configmap / from cache"),
			},
		},
		{
			testName: "NoExistingRegistry/CreateSuccessful",
			in: in{
				cluster: cluster{
					k8sObjs: []runtime.Object{validConfigMap},
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadServiceAccount",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), &corev1.ServiceAccount{}, "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadService",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), &corev1.Service{}, "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadServiceWithWrongHash",
			in: in{
				cluster: cluster{
					k8sObjs: append(setLabel(objectsForCatalogSource(validCatalogSource), &corev1.Service{}, ServiceHashLabelKey, "wrongHash"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadPod",
			in: in{
				cluster: cluster{
					k8sObjs: append(setLabel(objectsForCatalogSource(validCatalogSource), &corev1.Pod{}, CatalogSourceLabelKey, "badValue"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadRole",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), &rbacv1.Role{}, "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadRoleBinding",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), &rbacv1.RoleBinding{}, "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/OldPod",
			in: in{
				cluster: cluster{
					k8sObjs: append(objectsForCatalogSource(validCatalogSource), validConfigMap),
				},
				catsrc: outdatedCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			stopc := make(chan struct{})
			defer close(stopc)

			factory, client := fakeReconcilerFactory(t, stopc, withNow(now), withK8sObjs(tt.in.cluster.k8sObjs...), withK8sClientOptions(clientfake.WithNameGeneration(t)))
			rec := factory.ReconcilerForSource(tt.in.catsrc)

			err := rec.EnsureRegistryServer(tt.in.catsrc)

			require.Equal(t, tt.out.err, err)
			require.Equal(t, tt.out.status, tt.in.catsrc.Status.RegistryServiceStatus)

			if tt.out.err != nil {
				return
			}

			// if no error, the reconciler should create the same set of kube objects every time
			decorated := configMapCatalogSourceDecorator{tt.in.catsrc}

			pod := decorated.Pod(registryImageName)
			listOptions := metav1.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{CatalogSourceLabelKey: tt.in.catsrc.GetName()}).String()}
			outPods, err := client.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).List(context.TODO(), listOptions)
			require.NoError(t, err)
			require.Len(t, outPods.Items, 1)
			outPod := outPods.Items[0]
			require.Equal(t, pod.GetGenerateName(), outPod.GetGenerateName())
			require.Equal(t, pod.GetLabels(), outPod.GetLabels())
			require.Equal(t, pod.Spec, outPod.Spec)

			service := decorated.Service()
			outService, err := client.KubernetesInterface().CoreV1().Services(service.GetNamespace()).Get(context.TODO(), service.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, service, outService)

			serviceAccount := decorated.ServiceAccount()
			outServiceAccount, err := client.KubernetesInterface().CoreV1().ServiceAccounts(serviceAccount.GetNamespace()).Get(context.TODO(), serviceAccount.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, serviceAccount, outServiceAccount)

			role := decorated.Role()
			outRole, err := client.KubernetesInterface().RbacV1().Roles(role.GetNamespace()).Get(context.TODO(), role.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, role, outRole)

			roleBinding := decorated.RoleBinding()
			outRoleBinding, err := client.KubernetesInterface().RbacV1().RoleBindings(roleBinding.GetNamespace()).Get(context.TODO(), roleBinding.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, roleBinding, outRoleBinding)
		})
	}
}
