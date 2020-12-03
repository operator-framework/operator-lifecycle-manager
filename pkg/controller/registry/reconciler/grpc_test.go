package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
)

func validGrpcCatalogSource(image, address string) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "img-catalog",
			Namespace: testNamespace,
			UID:       types.UID("catalog-uid"),
			Labels:    map[string]string{"olm.catalogSource": "img-catalog"},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Image:      image,
			Address:    address,
			SourceType: v1alpha1.SourceTypeGrpc,
		},
	}
}

func grpcCatalogSourceWithSecret(secretName string) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-catalog",
			Namespace: testNamespace,
			UID:       types.UID("catalog-uid"),
			Labels:    map[string]string{"olm.catalogSource": "img-catalog"},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Image:      "private-image",
			Address:    "",
			SourceType: v1alpha1.SourceTypeGrpc,
			Secrets:    []string{secretName},
		},
	}
}

func TestGrpcRegistryReconciler(t *testing.T) {
	now := func() metav1.Time { return metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC) }
	blockOwnerDeletion := true
	isController := true

	type cluster struct {
		k8sObjs []runtime.Object
	}
	type in struct {
		cluster cluster
		catsrc  *v1alpha1.CatalogSource
	}
	type out struct {
		status  *v1alpha1.RegistryServiceStatus
		cluster cluster
		err     error
	}
	tests := []struct {
		testName string
		in       in
		out      out
	}{
		{
			testName: "Grpc/NoExistingRegistry/CreateSuccessful",
			in: in{
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/CreateSuccessful",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("test-img", "")),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/Address/CreateSuccessful",
			in: in{
				cluster: cluster{},
				catsrc:  validGrpcCatalogSource("", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt: now(),
					Protocol:  "grpc",
				},
			},
		},
		{
			testName: "Grpc/AddressAndImage/CreateSuccessful",
			in: in{
				cluster: cluster{},
				catsrc:  validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/ExistingRegistry/BadServiceWithWrongHash",
			in: in{
				cluster: cluster{
					k8sObjs: setLabel(objectsForCatalogSource(validGrpcCatalogSource("test-img", "")), &corev1.Service{}, ServiceHashLabelKey, "wrongHash"),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
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
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img", "")), &corev1.Service{}, "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
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
					k8sObjs: setLabel(objectsForCatalogSource(validGrpcCatalogSource("test-img", "")), &corev1.Pod{}, CatalogSourceLabelKey, ""),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
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
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("old-img", "")),
				},
				catsrc: validGrpcCatalogSource("new-img", ""),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "img-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
			},
		},
		{
			testName: "Grpc/PrivateRegistry/SAHasSecrets",
			in: in{
				cluster: cluster{
					k8sObjs: []runtime.Object{
						&corev1.Secret{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-secret",
								Namespace: testNamespace,
							},
						},
					},
				},
				catsrc: grpcCatalogSourceWithSecret("test-secret"),
			},
			out: out{
				status: &v1alpha1.RegistryServiceStatus{
					CreatedAt:        now(),
					Protocol:         "grpc",
					ServiceName:      "private-catalog",
					ServiceNamespace: testNamespace,
					Port:             "50051",
				},
				cluster: cluster{
					k8sObjs: []runtime.Object{
						&corev1.ServiceAccount{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "private-catalog",
								Namespace: testNamespace,
								OwnerReferences: []metav1.OwnerReference{
									{
										Name:               "private-catalog",
										UID:                types.UID("catalog-uid"),
										Kind:               v1alpha1.CatalogSourceKind,
										APIVersion:         v1alpha1.CatalogSourceCRDAPIVersion,
										BlockOwnerDeletion: &blockOwnerDeletion,
										Controller:         &isController,
									},
								},
							},
							ImagePullSecrets: []corev1.LocalObjectReference{
								{
									Name: "test-secret",
								},
							},
						},
					},
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

			// Check for resource existence
			decorated := grpcCatalogSourceDecorator{tt.in.catsrc}
			pod := decorated.Pod(tt.in.catsrc.GetName())
			service := decorated.Service()
			sa := decorated.ServiceAccount()
			listOptions := metav1.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{CatalogSourceLabelKey: tt.in.catsrc.GetName()}).String()}
			outPods, podErr := client.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).List(context.TODO(), listOptions)
			outService, serviceErr := client.KubernetesInterface().CoreV1().Services(service.GetNamespace()).Get(context.TODO(), service.GetName(), metav1.GetOptions{})
			outsa, saerr := client.KubernetesInterface().CoreV1().ServiceAccounts(sa.GetNamespace()).Get(context.TODO(), sa.GetName(), metav1.GetOptions{})
			switch rec.(type) {
			case *GrpcRegistryReconciler:
				// Should be created by a GrpcRegistryReconciler
				require.NoError(t, podErr)
				require.Len(t, outPods.Items, 1)
				outPod := outPods.Items[0]
				require.Equal(t, pod.GetGenerateName(), outPod.GetGenerateName())
				require.Equal(t, pod.GetLabels(), outPod.GetLabels())
				require.Equal(t, pod.Spec, outPod.Spec)
				require.NoError(t, serviceErr)
				require.Equal(t, service, outService)
				require.NoError(t, saerr)
				if len(tt.in.catsrc.Spec.Secrets) > 0 {
					require.Equal(t, tt.out.cluster.k8sObjs[0], outsa)
				}
			case *GrpcAddressRegistryReconciler:
				// Should not be created by a GrpcAddressRegistryReconciler
				require.NoError(t, podErr)
				require.Len(t, outPods.Items, 0)
				require.NoError(t, err)
				require.True(t, k8serrors.IsNotFound(serviceErr))
			}

		})
	}
}

func TestGrpcRegistryChecker(t *testing.T) {
	type cluster struct {
		k8sObjs []runtime.Object
	}
	type in struct {
		cluster cluster
		catsrc  *v1alpha1.CatalogSource
	}
	type out struct {
		healthy bool
		err     error
	}
	tests := []struct {
		testName string
		in       in
		out      out
	}{
		{
			testName: "Grpc/ExistingRegistry/Image/Healthy",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("test-img", "")),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				healthy: true,
			},
		},
		{
			testName: "Grpc/NoExistingRegistry/Image/NotHealthy",
			in: in{
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/Image/BadService",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img", "")), &corev1.Service{}, "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/Image/BadPod",
			in: in{
				cluster: cluster{
					k8sObjs: setLabel(objectsForCatalogSource(validGrpcCatalogSource("test-img", "")), &corev1.Pod{}, CatalogSourceLabelKey, ""),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/Image/OldPod/NotHealthy",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("old-img", "")),
				},
				catsrc: validGrpcCatalogSource("new-img", ""),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/NoExistingRegistry/Address/Healthy",
			in: in{
				catsrc: validGrpcCatalogSource("", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				healthy: true,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/AddressAndImage/Healthy",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001")),
				},
				catsrc: validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				healthy: true,
			},
		},
		{
			testName: "Grpc/NoExistingRegistry/AddressAndImage/NotHealthy",
			in: in{
				cluster: cluster{},
				catsrc:  validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/AddressAndImage/BadService/NotHealthy",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img", "catalog.svc.cluster.local:50001")), &corev1.Service{}, "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/AddressAndImage/OldPod/NotHealthy",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("old-img", "catalog.svc.cluster.local:50001")),
				},
				catsrc: validGrpcCatalogSource("new-img", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				healthy: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			stopc := make(chan struct{})
			defer close(stopc)

			factory, _ := fakeReconcilerFactory(t, stopc, withK8sObjs(tt.in.cluster.k8sObjs...))
			rec := factory.ReconcilerForSource(tt.in.catsrc)

			healthy, err := rec.CheckRegistryServer(tt.in.catsrc)

			require.Equal(t, tt.out.err, err)
			if tt.out.err != nil {
				return
			}

			require.Equal(t, tt.out.healthy, healthy)

		})
	}
}

func TestGetPodImageID(t *testing.T) {
	var table = []struct {
		description string
		pod         *corev1.Pod
		result      string
	}{
		{
			description: "pod has status: return status",
			pod:         &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{ImageID: "xyz123"}}}},
			result:      "xyz123",
		},
		{
			description: "pod has two containers: return first",
			pod: &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{ImageID: "xyz123"},
				{ImageID: "abc456"},
			}}},
			result: "xyz123",
		},
		{
			description: "pod has no status",
			pod:         &corev1.Pod{Status: corev1.PodStatus{}},
			result:      "",
		},
	}

	for i, tt := range table {
		require.Equal(t, tt.result, imageID(tt.pod), table[i].description)
	}
}

func TestUpdatePodByDigest(t *testing.T) {
	var table = []struct {
		description string
		updatePod   *corev1.Pod
		servingPods []*corev1.Pod
		result      bool
	}{
		{
			description: "pod image ids match: not update from the registry: return false",
			updatePod:   &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{ImageID: "xyz123"}}}},
			servingPods: []*corev1.Pod{{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{ImageID: "xyz123"}}}}},
			result:      false,
		},
		{
			description: "pod image ids do not match: update on the registry: return true",
			updatePod:   &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{ImageID: "abc456"}}}},
			servingPods: []*corev1.Pod{{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{ImageID: "xyz123"}}}}},
			result:      true,
		},
	}

	for i, tt := range table {
		require.Equal(t, tt.result, imageChanged(tt.updatePod, tt.servingPods), table[i].description)
	}
}
