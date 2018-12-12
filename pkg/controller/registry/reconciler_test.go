package registry

import (
	"fmt"
	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"testing"
	"time"
)
const (
	registryImageName = "test:image"
)
func reconciler(t *testing.T, k8sObjs []runtime.Object, stopc <-chan struct{}) (*ConfigMapRegistryReconciler, operatorclient.ClientInterface) {
	opClientFake := operatorclient.NewClient(k8sfake.NewSimpleClientset(k8sObjs...), nil, nil)

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

	rec := &ConfigMapRegistryReconciler{
		Image:                registryImageName,
		OpClient:             opClientFake,
		RoleLister:           roleInformer.Lister(),
		RoleBindingLister:    roleBindingInformer.Lister(),
		ServiceAccountLister: serviceAccountInformer.Lister(),
		ServiceLister:        serviceInformer.Lister(),
		PodLister:            podInformer.Lister(),
		ConfigMapLister:      configMapInformer.Lister(),
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
			Group:   name + "group",
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name: "v1",
					Served: true,
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
			Namespace:       "cool-namespace",
			UID:             types.UID("configmap-uid"),
			ResourceVersion: "resource-version",
		},
		Data: data,
	}
}

func TestValidConfigMap(t *testing.T) {
	cm := validConfigMap()
	require.NotNil(t, cm)
	require.Contains(t, cm.Data["customResourceDefinitions"], "fake")
}

func validCatalogSource(configMap *corev1.ConfigMap) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cool-catalog",
			Namespace: "cool-namespace",
			UID:       types.UID("catalog-uid"),
		},
		Spec: v1alpha1.CatalogSourceSpec{
			ConfigMap:  "cool-configmap",
			SourceType: "nope",
		},
		Status: v1alpha1.CatalogSourceStatus{
			ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
				Name: configMap.GetName(),
				Namespace: configMap.GetNamespace(),
				UID: configMap.GetUID(),
				ResourceVersion: configMap.GetResourceVersion(),
			},
		},
	}
}

func objectsForCatalogSource(catsrc *v1alpha1.CatalogSource) []runtime.Object {
	decorated := catalogSourceDecorator{catsrc}
	objs := []runtime.Object{
		decorated.Pod(registryImageName),
		decorated.Service(),
		decorated.ServiceAccount(),
		decorated.Role(),
		decorated.RoleBinding(),
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

func modifyObjName(objs []runtime.Object, kind, newName string) []runtime.Object {
	out := []runtime.Object{}
	for _, o :=range objs {
		if o.GetObjectKind().GroupVersionKind().Kind == kind {
			mo := o.(metav1.Object)
			mo.SetName(newName)
			out = append(out, mo.(runtime.Object))
			continue
		}
		out = append(out, o)
	}
	return out
}

func TestConfigMapRegistryReconciler(t *testing.T) {
	validConfigMap := validConfigMap()
	validCatalogSource := validCatalogSource(validConfigMap)
	outdatedCatalogSource := validCatalogSource.DeepCopy()
	outdatedCatalogSource.Status.ConfigMapResource.ResourceVersion = "old"
	type cluster struct {
		k8sObjs []runtime.Object
	}
	type in struct {
		cluster cluster
		catsrc *v1alpha1.CatalogSource
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
				catsrc: &v1alpha1.CatalogSource{},
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
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadServiceAccount",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), "ServiceAccount", "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadService",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), "Service", "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadPod",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), "Pod", "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadRole",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), "Role", "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
				},
			},
		},
		{
			testName: "ExistingRegistry/BadRoleBinding",
			in: in{
				cluster: cluster{
					k8sObjs: append(modifyObjName(objectsForCatalogSource(validCatalogSource), "RoleBinding", "badName"), validConfigMap),
				},
				catsrc: validCatalogSource,
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
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
					Protocol:         "grpc",
					ServiceName:      "cool-catalog",
					ServiceNamespace: "cool-namespace",
					Port:             "50051",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			stopc := make(chan struct{})
			defer close(stopc)

			rec, client := reconciler(t, tt.in.cluster.k8sObjs, stopc)

			err := rec.EnsureRegistryServer(tt.in.catsrc)

			require.Equal(t, tt.out.err, err)
			require.Equal(t, tt.out.status, tt.in.catsrc.Status.RegistryServiceStatus)

			if tt.out.err != nil {
				return
			}

			// if no error, the reconciler should create the same set of kube objects every time
			decorated := catalogSourceDecorator{tt.in.catsrc}

			pod := decorated.Pod(registryImageName)
			outPod, err := client.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).Get(pod.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, pod, outPod)

			service := decorated.Service()
			outService, err := client.KubernetesInterface().CoreV1().Services(service.GetNamespace()).Get(service.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, service, outService)

			serviceAccount := decorated.ServiceAccount()
			outServiceAccount, err := client.KubernetesInterface().CoreV1().ServiceAccounts(serviceAccount.GetNamespace()).Get(serviceAccount.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, serviceAccount, outServiceAccount)

			role := decorated.Role()
			outRole, err := client.KubernetesInterface().RbacV1().Roles(role.GetNamespace()).Get(role.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, role, outRole)

			roleBinding := decorated.RoleBinding()
			outRoleBinding, err := client.KubernetesInterface().RbacV1().RoleBindings(roleBinding.GetNamespace()).Get(roleBinding.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, roleBinding, outRoleBinding)
		})
	}
}
