//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_reconciler_factory.go . RegistryReconcilerFactory
package reconciler

import (
	"fmt"
	"path/filepath"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/image"
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
	EnsureRegistryServer(logger *logrus.Entry, catalogSource *operatorsv1alpha1.CatalogSource) error
}

// RegistryChecker describes methods for checking a registry.
type RegistryChecker interface {
	// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
	CheckRegistryServer(logger *logrus.Entry, catalogSource *operatorsv1alpha1.CatalogSource) (healthy bool, err error)
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
	createPodAsUser      int64
	opmImage             string
	utilImage            string
}

// ReconcilerForSource returns a RegistryReconciler based on the configuration of the given CatalogSource.
func (r *registryReconcilerFactory) ReconcilerForSource(source *operatorsv1alpha1.CatalogSource) RegistryReconciler {
	// TODO: add memoization by source type
	switch source.Spec.SourceType {
	case operatorsv1alpha1.SourceTypeInternal, operatorsv1alpha1.SourceTypeConfigmap:
		return &ConfigMapRegistryReconciler{
			now:             r.now,
			Lister:          r.Lister,
			OpClient:        r.OpClient,
			Image:           r.ConfigMapServerImage,
			createPodAsUser: r.createPodAsUser,
		}
	case operatorsv1alpha1.SourceTypeGrpc:
		if source.Spec.Image != "" {
			return &GrpcRegistryReconciler{
				now:             r.now,
				Lister:          r.Lister,
				OpClient:        r.OpClient,
				SSAClient:       r.SSAClient,
				createPodAsUser: r.createPodAsUser,
				opmImage:        r.opmImage,
				utilImage:       r.utilImage,
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
func NewRegistryReconcilerFactory(lister operatorlister.OperatorLister, opClient operatorclient.ClientInterface, configMapServerImage string, now nowFunc, ssaClient *controllerclient.ServerSideApplier, createPodAsUser int64, opmImage, utilImage string) RegistryReconcilerFactory {
	return &registryReconcilerFactory{
		now:                  now,
		Lister:               lister,
		OpClient:             opClient,
		ConfigMapServerImage: configMapServerImage,
		SSAClient:            ssaClient,
		createPodAsUser:      createPodAsUser,
		opmImage:             opmImage,
		utilImage:            utilImage,
	}
}

func Pod(source *operatorsv1alpha1.CatalogSource, name, opmImg, utilImage, img string, serviceAccount *corev1.ServiceAccount, labels, annotations map[string]string, readinessDelay, livenessDelay int32, runAsUser int64) (*corev1.Pod, error) {
	// make a copy of the labels and annotations to avoid mutating the input parameters
	podLabels := make(map[string]string)
	podAnnotations := make(map[string]string)

	for key, value := range labels {
		podLabels[key] = value
	}
	podLabels[install.OLMManagedLabelKey] = install.OLMManagedLabelValue

	for key, value := range annotations {
		podAnnotations[key] = value
	}

	// Default case for nil serviceAccount
	var saName string
	var saImagePullSecrets []corev1.LocalObjectReference
	// If the serviceAccount is not nil, set the fields that should appear on the pod
	if serviceAccount != nil {
		saName = serviceAccount.GetName()
		saImagePullSecrets = serviceAccount.ImagePullSecrets
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: source.GetName() + "-",
			Namespace:    source.GetNamespace(),
			Labels:       podLabels,
			Annotations:  podAnnotations,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  name,
					Image: img,
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
						FailureThreshold: 10,
						PeriodSeconds:    10,
						TimeoutSeconds:   5,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					SecurityContext: &corev1.SecurityContext{
						ReadOnlyRootFilesystem: ptr.To(bool(false)),
					},
					ImagePullPolicy:          image.InferImagePullPolicy(img),
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
				},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/os": "linux",
			},
			ServiceAccountName: saName,
			// If this field is not set, there that the is a chance that pod will be created without the imagePullSecret
			// defined by the serviceAccount
			ImagePullSecrets: saImagePullSecrets,
		},
	}

	if source.Spec.GrpcPodConfig != nil && source.Spec.GrpcPodConfig.SecurityContextConfig == operatorsv1alpha1.Restricted {
		addSecurityContext(pod, runAsUser)
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

		// Override affinity
		if grpcPodConfig.Affinity != nil {
			pod.Spec.Affinity = grpcPodConfig.Affinity.DeepCopy()
		}

		// Add memory targets
		if grpcPodConfig.MemoryTarget != nil {
			pod.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = *grpcPodConfig.MemoryTarget

			if pod.Spec.Containers[0].Resources.Limits == nil {
				pod.Spec.Containers[0].Resources.Limits = map[corev1.ResourceName]resource.Quantity{}
			}

			grpcPodConfig.MemoryTarget.Format = resource.BinarySI
			pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, corev1.EnvVar{
				Name:  "GOMEMLIMIT",
				Value: grpcPodConfig.MemoryTarget.String() + "B", // k8s resources use Mi, GOMEMLIMIT wants MiB
			})
		}

		// Reconfigure pod to extract content
		if grpcPodConfig.ExtractContent != nil {
			pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
				Name: "utilities",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			}, corev1.Volume{
				Name: "catalog-content",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			})
			const utilitiesPath = "/utilities"
			utilitiesVolumeMount := corev1.VolumeMount{
				Name:      "utilities",
				MountPath: utilitiesPath,
			}
			const catalogPath = "/extracted-catalog"
			contentVolumeMount := corev1.VolumeMount{
				Name:      "catalog-content",
				MountPath: catalogPath,
			}
			pod.Spec.InitContainers = append(pod.Spec.InitContainers, corev1.Container{
				Name:                     "extract-utilities",
				Image:                    utilImage,
				Command:                  []string{"cp"},
				Args:                     []string{"/bin/copy-content", fmt.Sprintf("%s/copy-content", utilitiesPath)},
				VolumeMounts:             []corev1.VolumeMount{utilitiesVolumeMount},
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			}, corev1.Container{
				Name:            "extract-content",
				Image:           img,
				ImagePullPolicy: image.InferImagePullPolicy(img),
				Command:         []string{utilitiesPath + "/copy-content"},
				Args: []string{
					"--catalog.from=" + grpcPodConfig.ExtractContent.CatalogDir,
					"--catalog.to=" + fmt.Sprintf("%s/catalog", catalogPath),
					"--cache.from=" + grpcPodConfig.ExtractContent.CacheDir,
					"--cache.to=" + fmt.Sprintf("%s/cache", catalogPath),
				},
				VolumeMounts:             []corev1.VolumeMount{utilitiesVolumeMount, contentVolumeMount},
				TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
			})

			pod.Spec.Containers[0].Image = opmImg
			pod.Spec.Containers[0].Command = []string{"/bin/opm"}
			pod.Spec.Containers[0].Args = []string{
				"serve",
				filepath.Join(catalogPath, "catalog"),
				"--cache-dir=" + filepath.Join(catalogPath, "cache"),
			}
			pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, contentVolumeMount)
		}
	}

	// Set priorityclass if its annotation exists
	if prio, ok := podAnnotations[CatalogPriorityClassKey]; ok && prio != "" {
		pod.Spec.PriorityClassName = prio
	}

	// Add PodSpec hash
	// This hash info will be used to detect PodSpec changes
	hash, err := hashutil.DeepHashObject(&pod.Spec)
	if err != nil {
		return nil, err
	}
	podLabels[PodHashLabelKey] = hash

	// add eviction annotation to enable the cluster autoscaler to evict the pod in order to drain the node
	// since catalog pods are not backed by a controller, they cannot be evicted by default
	podAnnotations[ClusterAutoscalingAnnotationKey] = "true"

	return pod, nil
}

func addSecurityContext(pod *corev1.Pod, runAsUser int64) {
	pod.Spec.Containers[0].SecurityContext.AllowPrivilegeEscalation = ptr.To(bool(false))
	pod.Spec.Containers[0].SecurityContext.Capabilities = &corev1.Capabilities{
		Drop: []corev1.Capability{"ALL"},
	}
	pod.Spec.SecurityContext = &corev1.PodSecurityContext{
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
	if runAsUser > 0 {
		pod.Spec.SecurityContext.RunAsUser = &runAsUser
		pod.Spec.SecurityContext.RunAsNonRoot = ptr.To(bool(true))
	}
}
