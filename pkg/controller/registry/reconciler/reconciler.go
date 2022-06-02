//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_reconciler_factory.go . RegistryReconcilerFactory
package reconciler

import (
	"fmt"
	"hash/fnv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

type nowFunc func() metav1.Time

const (
	// CatalogSourceLabelKey is the key for a label containing a CatalogSource name.
	CatalogSourceLabelKey string = "olm.catalogSource"
	// CatalogPriorityClassKey is the key of an annotation in default catalogsources
	CatalogPriorityClassKey string = "operatorframework.io/priorityclass"
	// PodHashLabelKey is the key of a label for podspec hash information
	PodHashLabelKey = "olm.pod-spec-hash"
	//ClusterAutoscalingAnnotation is the annotation that enables the cluster autoscaler to evict catalog pods
	ClusterAutoscalingAnnotationKey string = "cluster-autoscaler.kubernetes.io/safe-to-evict"
)

// RegistryEnsurer describes methods for ensuring a registry exists.
type RegistryEnsurer interface {
	// EnsureRegistryServer ensures a registry server exists for the given CatalogSource.
	EnsureRegistryServer(catalogSource *operatorsv1alpha1.CatalogSource) error
}

// RegistryChecker describes methods for checking a registry.
type RegistryChecker interface {
	// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
	CheckRegistryServer(catalogSource *operatorsv1alpha1.CatalogSource) (healthy bool, err error)
}

// RegistryReconciler knows how to reconcile a registry.
type RegistryReconciler interface {
	RegistryChecker
	RegistryEnsurer
}

// RegistryReconcilerFactory describes factory methods for RegistryReconcilers.
type RegistryReconcilerFactory interface {
	ReconcilerForSource(source *operatorsv1alpha1.CatalogSource) RegistryReconciler
}

// RegistryReconcilerFactory is a factory for RegistryReconcilers.
type registryReconcilerFactory struct {
	now                  nowFunc
	Lister               operatorlister.OperatorLister
	OpClient             operatorclient.ClientInterface
	ConfigMapServerImage string
	SSAClient            *controllerclient.ServerSideApplier
}

// ReconcilerForSource returns a RegistryReconciler based on the configuration of the given CatalogSource.
func (r *registryReconcilerFactory) ReconcilerForSource(source *operatorsv1alpha1.CatalogSource) RegistryReconciler {
	// TODO: add memoization by source type
	switch source.Spec.SourceType {
	case operatorsv1alpha1.SourceTypeInternal, operatorsv1alpha1.SourceTypeConfigmap:
		return &ConfigMapRegistryReconciler{
			now:      r.now,
			Lister:   r.Lister,
			OpClient: r.OpClient,
			Image:    r.ConfigMapServerImage,
		}
	case operatorsv1alpha1.SourceTypeGrpc:
		if source.Spec.Image != "" {
			return &GrpcRegistryReconciler{
				now:       r.now,
				Lister:    r.Lister,
				OpClient:  r.OpClient,
				SSAClient: r.SSAClient,
			}
		} else if source.Spec.Address != "" {
			return &GrpcAddressRegistryReconciler{
				now: r.now,
			}
		}
	}
	return nil
}

// NewRegistryReconcilerFactory returns an initialized RegistryReconcilerFactory.
func NewRegistryReconcilerFactory(lister operatorlister.OperatorLister, opClient operatorclient.ClientInterface, configMapServerImage string, now nowFunc, ssaClient *controllerclient.ServerSideApplier) RegistryReconcilerFactory {
	return &registryReconcilerFactory{
		now:                  now,
		Lister:               lister,
		OpClient:             opClient,
		ConfigMapServerImage: configMapServerImage,
		SSAClient:            ssaClient,
	}
}

func Pod(source *operatorsv1alpha1.CatalogSource, name string, image string, saName string, labels map[string]string, annotations map[string]string, readinessDelay int32, livenessDelay int32) *corev1.Pod {
	// Ensure the catalog image is always pulled if the image is not based on a digest, measured by whether an "@" is included.
	// See https://github.com/docker/distribution/blob/master/reference/reference.go for more info.
	// This means recreating non-digest based catalog pods will result in the latest version of the catalog content being delivered on-cluster.
	var pullPolicy corev1.PullPolicy
	if strings.Contains(image, "@") {
		pullPolicy = corev1.PullIfNotPresent
	} else {
		pullPolicy = corev1.PullAlways
	}

	readOnlyRootFilesystem := false

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: source.GetName() + "-",
			Namespace:    source.GetNamespace(),
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  name,
					Image: image,
					Ports: []corev1.ContainerPort{
						{
							Name:          "grpc",
							ContainerPort: 50051,
						},
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: readinessDelay,
						TimeoutSeconds:      5,
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: livenessDelay,
						TimeoutSeconds:      5,
					},
					StartupProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						FailureThreshold: 15,
						PeriodSeconds:    10,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: &readOnlyRootFilesystem,
					},
					ImagePullPolicy:          pullPolicy,
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
				},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/os": "linux",
			},
			ServiceAccountName: saName,
		},
	}

	// Override scheduling options if specified
	if source.Spec.GrpcPodConfig != nil {
		grpcPodConfig := source.Spec.GrpcPodConfig

		// Override node selector
		if grpcPodConfig.NodeSelector != nil {
			pod.Spec.NodeSelector = make(map[string]string, len(grpcPodConfig.NodeSelector))
			for key, value := range grpcPodConfig.NodeSelector {
				pod.Spec.NodeSelector[key] = value
			}
		}

		// Override priority class name
		if grpcPodConfig.PriorityClassName != nil {
			pod.Spec.PriorityClassName = *grpcPodConfig.PriorityClassName
		}

		// Override tolerations
		if grpcPodConfig.Tolerations != nil {
			pod.Spec.Tolerations = make([]corev1.Toleration, len(grpcPodConfig.Tolerations))
			for index, toleration := range grpcPodConfig.Tolerations {
				pod.Spec.Tolerations[index] = *toleration.DeepCopy()
			}
		}
	}

	// Set priorityclass if its annotation exists
	if prio, ok := annotations[CatalogPriorityClassKey]; ok && prio != "" {
		pod.Spec.PriorityClassName = prio
	}

	// Add PodSpec hash
	// This hash info will be used to detect PodSpec changes
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[PodHashLabelKey] = hashPodSpec(pod.Spec)
	pod.SetLabels(labels)

	// add eviction annotation to enable the cluster autoscaler to evict the pod in order to drain the node
	// since catalog pods are not backed by a controller, they cannot be evicted by default
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[ClusterAutoscalingAnnotationKey] = "true"
	pod.SetAnnotations(annotations)

	return pod
}

// hashPodSpec calculates a hash given a copy of the pod spec
func hashPodSpec(spec corev1.PodSpec) string {
	hasher := fnv.New32a()
	hashutil.DeepHashObject(hasher, &spec)
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}
