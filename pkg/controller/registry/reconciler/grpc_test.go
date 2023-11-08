package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			testName: "Grpc/NoExistingRegistry/NoExistingServiceAccount/CreateError",
			in: in{
				catsrc:  validGrpcCatalogSource("test-img", ""),
				cluster: cluster{},
			},
			out: out{
				err: errors.Wrapf(fmt.Errorf("serviceAccount testns/img-catalog has not been issued a pull secret"), "error ensuring service account pull secret: %s", "img-catalog"),
			},
		},
		{
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithoutPullSecret/CreateError",
			in: in{
				catsrc: validGrpcCatalogSource("test-img", ""),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(validGrpcCatalogSource("test-img", ""), withoutServiceAccountImagePullSecrets()),
				},
			},
			out: out{
				err: errors.Wrapf(fmt.Errorf("serviceAccount testns/img-catalog has not been issued a pull secret"), "error ensuring service account pull secret: %s", "img-catalog"),
			},
		},
		{
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithPullSecret/CreateSuccessful",
			in: in{
				catsrc: validGrpcCatalogSource("test-img", ""),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(validGrpcCatalogSource("test-img", "")),
				},
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
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithPullSecret/CreateSuccessful/CatalogSourceWithPeriodInNameCreatesValidServiceName",
			in: in{
				catsrc: grpcCatalogSourceWithName("img.catalog"),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(grpcCatalogSourceWithName("img.catalog")),
				},
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
			testName: "Grpc/AddressAndImage/ExistingServiceAccountWithPullSecret/CreateSuccessful",
			in: in{
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(grpcCatalogSourceWithName("img-catalog")),
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
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithPullSecret/CreateWithAnnotations",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"annotation1": "value1",
					"annotation2": "value2",
				}),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(grpcCatalogSourceWithAnnotations(map[string]string{
						"annotation1": "value1",
						"annotation2": "value2",
					})),
				},
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
					k8sObjs: objectsForCatalogSource(validGrpcCatalogSource("image", "")),
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

			err := rec.EnsureRegistryServer(tt.in.catsrc)

			if tt.out.err == nil {
				require.Equal(t, tt.out.err, err)
			} else {
				require.Equal(t, tt.out.err.Error(), err.Error())
			}
			require.Equal(t, tt.out.status, tt.in.catsrc.Status.RegistryServiceStatus)

			if tt.out.err != nil {
				return
			}

			// Check for resource existence
			decorated := grpcCatalogSourceDecorator{CatalogSource: tt.in.catsrc, createPodAsUser: runAsUser}
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
				if diff := cmp.Diff(outPods.Items, []corev1.Pod{*pod}); diff != "" {
					fmt.Printf("incorrect pods: %s\n", diff)
				}
				require.Len(t, outPods.Items, 1)
				outPod := outPods.Items[0]
				require.Equal(t, pod.GetGenerateName(), outPod.GetGenerateName())
				require.Equal(t, pod.GetLabels(), outPod.GetLabels())
				require.Equal(t, pod.GetAnnotations(), outPod.GetAnnotations())
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
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithPullSecret/WithValidPriorityClassAnnotation",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"operatorframework.io/priorityclass": "system-cluster-critical",
				}),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(grpcCatalogSourceWithAnnotations(map[string]string{
						"operatorframework.io/priorityclass": "system-cluster-critical",
					})),
				},
			},
			priorityclass: "system-cluster-critical",
		},
		{
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithPullSecret/WithInvalidPriorityClassAnnotation",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"operatorframework.io/priorityclass": "",
				}),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(grpcCatalogSourceWithAnnotations(map[string]string{
						"operatorframework.io/priorityclass": "",
					})),
				},
			},
			priorityclass: "",
		},
		{
			testName: "Grpc/NoExistingRegistry/ExistingServiceAccountWithPullSecret/WithNoPriorityClassAnnotation",
			in: in{
				catsrc: grpcCatalogSourceWithAnnotations(map[string]string{
					"annotationkey": "annotationvalue",
				}),
				cluster: cluster{
					k8sObjs: serviceAccountForCatalogSource(grpcCatalogSourceWithAnnotations(map[string]string{
						"annotationkey": "annotationvalue",
					})),
				},
			},
			priorityclass: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			stopc := make(chan struct{})
			defer close(stopc)

			factory, client := fakeReconcilerFactory(t, stopc, withNow(now), withK8sObjs(tt.in.cluster.k8sObjs...), withK8sClientOptions(clientfake.WithNameGeneration(t)))
			rec := factory.ReconcilerForSource(tt.in.catsrc)

			err := rec.EnsureRegistryServer(tt.in.catsrc)
			require.NoError(t, err)

			// Check for resource existence
			decorated := grpcCatalogSourceDecorator{CatalogSource: tt.in.catsrc, createPodAsUser: runAsUser}
			pod := decorated.Pod(tt.in.catsrc.GetName())
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
			testName: "Grpc/ExistingRegistry/Image/BadServiceAccount",
			in: in{
				cluster: cluster{
					k8sObjs: modifyObjName(objectsForCatalogSource(validGrpcCatalogSource("test-img", "")), &corev1.ServiceAccount{}, "badName"),
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
