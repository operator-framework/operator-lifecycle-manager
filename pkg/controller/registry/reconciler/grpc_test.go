package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
)

func validGrpcCatalogSource(image, address string) *v1alpha1.CatalogSource {
	return &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "img-catalog",
			Namespace: testNamespace,
			UID:       "catalog-uid",
			Labels:    map[string]string{"olm.catalogSource": "img-catalog"},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Image:      image,
			Address:    address,
			SourceType: v1alpha1.SourceTypeGrpc,
		},
	}
}

func grpcCatalogSourceWithSecret(secretNames []string) *v1alpha1.CatalogSource {
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
			Secrets:    secretNames,
		},
	}
}
func grpcCatalogSourceWithStatus(status v1alpha1.CatalogSourceStatus) *v1alpha1.CatalogSource {
	catsrc := validGrpcCatalogSource("image", "")
	catsrc.Status = status
	return catsrc
}

func grpcCatalogSourceWithAnnotations(annotations map[string]string) *v1alpha1.CatalogSource {
	catsrc := validGrpcCatalogSource("image", "")
	catsrc.ObjectMeta.Annotations = annotations
	return catsrc
}

func grpcCatalogSourceWithName(name string) *v1alpha1.CatalogSource {
	catsrc := validGrpcCatalogSource("image", "")
	catsrc.SetName(name)
	catsrc.ObjectMeta.Labels["olm.catalogSource"] = name
	return catsrc
}

func withPodDeletedButNotRemoved(objs []runtime.Object) []runtime.Object {
	var out []runtime.Object
	for _, obj := range objs {
		o := obj.DeepCopyObject()
		if pod, ok := obj.(*corev1.Pod); ok {
			pod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
				Type:   corev1.DisruptionTarget,
				Reason: "DeletionByTaintManager",
				Status: corev1.ConditionTrue,
			})
			o = pod
		}
		out = append(out, o)
	}
	return out
}
func TestGrpcRegistryReconciler(t *testing.T) {
	now := func() metav1.Time { return metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC) }
	blockOwnerDeletion := true
	isController := true

	// We expect the empty string secret name should not be set
	// on the service account
	testSecrets := []string{"test-secret", ""}

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
				cluster: cluster{
					k8sObjs: baseClusterState(),
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
			testName: "Grpc/NoExistingRegistry/CreateSuccessful/CatalogSourceWithPeriodInNameCreatesValidServiceName",
			in: in{
				cluster: cluster{
					k8sObjs: baseClusterState(),
				},
				catsrc: grpcCatalogSourceWithName("img.catalog"),
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
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")),
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
				cluster: cluster{
					k8sObjs: baseClusterState(),
				},
				catsrc: validGrpcCatalogSource("", "catalog.svc.cluster.local:50001"),
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
				cluster: cluster{
					k8sObjs: baseClusterState(),
				},
				catsrc: validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001"),
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
					k8sObjs: setLabel(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &corev1.Service{}, ServiceHashLabelKey, "wrongHash"),
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
			testName: "Grpc/ExistingRegistry/BadNetworkPolicies",
			in: in{
				cluster: cluster{
					k8sObjs: setLabel(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &networkingv1.NetworkPolicy{}, CatalogSourceLabelKey, "wrongValue"),
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
					k8sObjs: modifyObjName(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &corev1.Service{}, "badName"),
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
					k8sObjs: setLabel(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &corev1.Pod{}, CatalogSourceLabelKey, ""),
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
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("old-img", "")),
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
						defaultNamespace(),
						&corev1.Secret{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-secret",
								Namespace: testNamespace,
							},
						},
					},
				},
				catsrc: grpcCatalogSourceWithSecret(testSecrets),
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
								Labels:    map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue},
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
		{
			testName: "Grpc/NoExistingRegistry/CreateWithAnnotations",
			in: in{
				cluster: cluster{
					k8sObjs: baseClusterState(),
				},
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"annotation1": "value1",
					"annotation2": "value2",
				}),
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
			testName: "Grpc/ExistingRegistry/UpdateInvalidRegistryServiceStatus",
			in: in{
				cluster: cluster{
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("image", "")),
				},
				catsrc: grpcCatalogSourceWithStatus(v1alpha1.CatalogSourceStatus{
					RegistryServiceStatus: &v1alpha1.RegistryServiceStatus{
						CreatedAt: now(),
						Protocol:  "grpc",
					},
				}),
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
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			stopc := make(chan struct{})
			defer close(stopc)

			factory, client := fakeReconcilerFactory(t, stopc, withNow(now), withK8sObjs(tt.in.cluster.k8sObjs...), withK8sClientOptions(clientfake.WithNameGeneration(t)))
			rec := factory.ReconcilerForSource(tt.in.catsrc)

			err := rec.EnsureRegistryServer(logrus.NewEntry(logrus.New()), tt.in.catsrc)

			require.Equal(t, tt.out.err, err)
			require.Equal(t, tt.out.status, tt.in.catsrc.Status.RegistryServiceStatus)

			if tt.out.err != nil {
				return
			}

			// Check for resource existence
			decorated := grpcCatalogSourceDecorator{CatalogSource: tt.in.catsrc, createPodAsUser: runAsUser}
			grpcServerNetworkPolicy := decorated.GRPCServerNetworkPolicy()
			unpackBundlesNetworkPolicy := decorated.UnpackBundlesNetworkPolicy()
			sa := decorated.ServiceAccount()
			pod, err := decorated.Pod(sa, defaultPodSecurityConfig)
			if err != nil {
				t.Fatal(err)
			}
			service, err := decorated.Service()
			if err != nil {
				t.Fatal(err)
			}
			listOptions := metav1.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{CatalogSourceLabelKey: tt.in.catsrc.GetName()}).String()}
			outGRPCNetworkPolicy, grpcNPErr := client.KubernetesInterface().NetworkingV1().NetworkPolicies(grpcServerNetworkPolicy.GetNamespace()).Get(context.TODO(), grpcServerNetworkPolicy.GetName(), metav1.GetOptions{})
			outUnpackBundlesNetworkPolicy, ubNPErr := client.KubernetesInterface().NetworkingV1().NetworkPolicies(unpackBundlesNetworkPolicy.GetNamespace()).Get(context.TODO(), unpackBundlesNetworkPolicy.GetName(), metav1.GetOptions{})
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
				require.Equal(t, pod.GetAnnotations(), outPod.GetAnnotations())
				require.Equal(t, pod.Spec, outPod.Spec)
				require.NoError(t, grpcNPErr)
				require.NoError(t, ubNPErr)
				require.Equal(t, grpcServerNetworkPolicy, outGRPCNetworkPolicy)
				require.Equal(t, unpackBundlesNetworkPolicy, outUnpackBundlesNetworkPolicy)
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
				require.True(t, apierrors.IsNotFound(grpcNPErr))
				require.True(t, apierrors.IsNotFound(ubNPErr))
				require.True(t, apierrors.IsNotFound(serviceErr))
			}
		})
	}
}

func TestRegistryPodPriorityClass(t *testing.T) {
	now := func() metav1.Time { return metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC) }

	type cluster struct {
		k8sObjs []runtime.Object
	}
	type in struct {
		cluster cluster
		catsrc  *v1alpha1.CatalogSource
	}
	tests := []struct {
		testName      string
		in            in
		priorityclass string
	}{
		{
			testName: "Grpc/WithValidPriorityClassAnnotation",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"operatorframework.io/priorityclass": "system-cluster-critical",
				}),
			},
			priorityclass: "system-cluster-critical",
		},
		{
			testName: "Grpc/WithInvalidPriorityClassAnnotation",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"operatorframework.io/priorityclass": "",
				}),
			},
			priorityclass: "",
		},
		{
			testName: "Grpc/WithNoPriorityClassAnnotation",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"annotationkey": "annotationvalue",
				}),
			},
			priorityclass: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			stopc := make(chan struct{})
			defer close(stopc)

			// a defaultNamespace resource must be present so that the reconciler can determine the
			// security context configuration for the underlying pod
			clusterState := append(tt.in.cluster.k8sObjs, defaultNamespace())

			factory, client := fakeReconcilerFactory(t, stopc, withNow(now), withK8sObjs(clusterState...), withK8sClientOptions(clientfake.WithNameGeneration(t)))
			rec := factory.ReconcilerForSource(tt.in.catsrc)

			err := rec.EnsureRegistryServer(logrus.NewEntry(logrus.New()), tt.in.catsrc)
			require.NoError(t, err)

			// Check for resource existence
			decorated := grpcCatalogSourceDecorator{CatalogSource: tt.in.catsrc, createPodAsUser: runAsUser}
			pod, err := decorated.Pod(serviceAccount(tt.in.catsrc.Namespace, tt.in.catsrc.Name), defaultPodSecurityConfig)
			require.NoError(t, err)
			listOptions := metav1.ListOptions{LabelSelector: labels.SelectorFromSet(labels.Set{CatalogSourceLabelKey: tt.in.catsrc.GetName()}).String()}
			outPods, podErr := client.KubernetesInterface().CoreV1().Pods(pod.GetNamespace()).List(context.TODO(), listOptions)
			require.NoError(t, podErr)
			require.Len(t, outPods.Items, 1)
			outPod := outPods.Items[0]
			require.Equal(t, tt.priorityclass, outPod.Spec.PriorityClassName)
			require.Equal(t, pod.GetLabels()[PodHashLabelKey], outPod.GetLabels()[PodHashLabelKey])
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
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")),
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
			testName: "Grpc/ExistingRegistry/Image/BadNetworkPolicies",
			in: in{
				cluster: cluster{
					k8sObjs: setLabel(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &networkingv1.NetworkPolicy{}, CatalogSourceLabelKey, "wrongValue"),
				},
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
					k8sObjs: modifyObjName(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &corev1.Service{}, "badName"),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/Image/BadServiceAccount",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &corev1.ServiceAccount{}, "badName"),
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
					k8sObjs: setLabel(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "")), &corev1.Pod{}, CatalogSourceLabelKey, ""),
				},
				catsrc: validGrpcCatalogSource("test-img", ""),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/Image/DeadPod",
			in: in{
				cluster: cluster{
					k8sObjs: withPodDeletedButNotRemoved(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", ""))),
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
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("old-img", "")),
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
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001")),
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
				catsrc: validGrpcCatalogSource("img-catalog", "catalog.svc.cluster.local:50001"),
			},
			out: out{
				healthy: false,
			},
		},
		{
			testName: "Grpc/ExistingRegistry/AddressAndImage/BadService/NotHealthy",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(t, validGrpcCatalogSource("test-img", "catalog.svc.cluster.local:50001")), &corev1.Service{}, "badName"),
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
					k8sObjs: objectsForCatalogSource(t, validGrpcCatalogSource("old-img", "catalog.svc.cluster.local:50001")),
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

			healthy, err := rec.CheckRegistryServer(logrus.NewEntry(logrus.New()), tt.in.catsrc)

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
			description: "default pod has status: return status",
			pod:         &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{ImageID: "xyz123"}}}},
			result:      "xyz123",
		},
		{
			description: "extractConfig pod has status: return status",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{
					{ImageID: "xyz123"},
					{ImageID: "abc456"},
				},
				ContainerStatuses: []corev1.ContainerStatus{
					{ImageID: "xyz123"},
				},
			}},
			result: "abc456",
		},
		{
			description: "pod has unexpected container config",
			pod: &corev1.Pod{Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{ImageID: "xyz123"},
				{ImageID: "abc456"},
			}}},
			result: "",
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
		require.Equal(t, tt.result, imageChanged(logrus.NewEntry(logrus.New()), tt.updatePod, tt.servingPods), table[i].description)
	}
}
