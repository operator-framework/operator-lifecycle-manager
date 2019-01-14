package reconciler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

func grpcReconciler(t *testing.T, k8sObjs []runtime.Object, stopc <-chan struct{}) (*GrpcRegistryReconciler, operatorclient.ClientInterface) {
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

	lister := operatorlister.NewLister()
	lister.RbacV1().RegisterRoleLister(testNamespace, roleInformer.Lister())
	lister.RbacV1().RegisterRoleBindingLister(testNamespace, roleBindingInformer.Lister())
	lister.CoreV1().RegisterServiceAccountLister(testNamespace, serviceAccountInformer.Lister())
	lister.CoreV1().RegisterServiceLister(testNamespace, serviceInformer.Lister())
	lister.CoreV1().RegisterPodLister(testNamespace, podInformer.Lister())
	lister.CoreV1().RegisterConfigMapLister(testNamespace, configMapInformer.Lister())

	rec := &GrpcRegistryReconciler{
		OpClient: opClientFake,
		Lister:   lister,
	}

	var hasSyncedCheckFns []cache.InformerSynced
	for _, informer := range registryInformers {
		hasSyncedCheckFns = append(hasSyncedCheckFns, informer.HasSynced)
		go informer.Run(stopc)
	}

	require.True(t, cache.WaitForCacheSync(stopc, hasSyncedCheckFns...), "caches failed to sync")

	return rec, opClientFake
}

func validGrpcCatalogSource(image string) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "img-catalog",
			Namespace: testNamespace,
			UID:       types.UID("catalog-uid"),
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Image:      image,
			SourceType: v1alpha1.SourceTypeGrpc,
		},
	}
}

func TestGrpcRegistryReconciler(t *testing.T) {
	nowTime := metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC)
	timeNow = func() metav1.Time { return nowTime }

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
			testName: "Grpc/NoExistingRegistry/CreateSuccessful",
			in: in{
				catsrc: validGrpcCatalogSource("test-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/BadServiceAccount",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img")), "ServiceAccount", "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/BadService",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img")), "Service", "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/BadPod",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img")), "Pod", "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/BadRole",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img")), "Role", "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/BadRoleBinding",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img")), "RoleBinding", "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/OldPod",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("old-img")),
				},
				catsrc: validGrpcCatalogSource("new-img"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        timeNow(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
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

			rec, client := grpcReconciler(t, tt.in.cluster.k8sObjs, stopc)

			err := rec.EnsureRegistryServer(tt.in.catsrc)

			require.Equal(t, tt.out.err, err)
			require.Equal(t, tt.out.status, tt.in.catsrc.Status.RegistryServiceStatus)

			if tt.out.err != nil {
				return
			}

			// if no error, the reconciler should create the same set of kube objects every time
			decorated := grpcCatalogSourceDecorator{tt.in.catsrc}

			pod := decorated.Pod()
			outPod, err := client.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).Get(pod.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, pod, outPod)

			service := decorated.Service()
			outService, err := client.KubernetesInterface().CoreV1().Services(service.GetNamespace()).Get(service.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, service, outService)
		})
	}
}
