//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_reconciler_factory.go . RegistryReconcilerFactory
package reconciler

import (
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

type nowFunc func() metav1.Time

const (
	// CatalogSourceLabelKey is the key for a label containing a CatalogSource name.
	CatalogSourceLabelKey = "olm.catalogSource"

	// CopyIndexStatusLocation indicates where in a copy container the status can be found
	CopyIndexStatusLocation = "/status.json"

	// DataMount is the location the db will be copied to
	DataMount = "/mounted/index.db"

	DataMountPath = "/mounted"

)

// CopyIndexResult contains information about the index data from copying it
type CopyIndexResult struct {
	Digest string `json:"digest"`
}

// RegistryEnsurer describes methods for ensuring a registry exists.
type RegistryEnsurer interface {
	// EnsureRegistryServer ensures a registry server exists for the given CatalogSource.
	EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error
}

// RegistryChecker describes methods for checking a registry.
type RegistryChecker interface {
	// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
	CheckRegistryServer(catalogSource *v1alpha1.CatalogSource) (healthy bool, err error)
}

// RegistryReconciler knows how to reconcile a registry.
type RegistryReconciler interface {
	RegistryChecker
	RegistryEnsurer
}

// RegistryReconcilerFactory describes factory methods for RegistryReconcilers.
type RegistryReconcilerFactory interface {
	ReconcilerForSource(source *v1alpha1.CatalogSource) RegistryReconciler
}

// RegistryReconcilerFactory is a factory for RegistryReconcilers.
type registryReconcilerFactory struct {
	now                  nowFunc
	Lister               operatorlister.OperatorLister
	OpClient             operatorclient.ClientInterface
	ConfigMapServerImage string
	UtilImage            string
}

// ReconcilerForSource returns a RegistryReconciler based on the configuration of the given CatalogSource.
func (r *registryReconcilerFactory) ReconcilerForSource(source *v1alpha1.CatalogSource) RegistryReconciler {
	// TODO: add memoization by source type
	switch source.Spec.SourceType {
	case v1alpha1.SourceTypeInternal, v1alpha1.SourceTypeConfigmap:
		return &ConfigMapRegistryReconciler{
			now:      r.now,
			Lister:   r.Lister,
			OpClient: r.OpClient,
			Image:    r.ConfigMapServerImage,
		}
	case v1alpha1.SourceTypeGrpc:
		if source.Spec.Image != "" {
			return &GrpcRegistryReconciler{
				now:       r.now,
				Lister:    r.Lister,
				OpClient:  r.OpClient,
				utilImage: r.UtilImage,
				binImage:  r.ConfigMapServerImage,
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
func NewRegistryReconcilerFactory(lister operatorlister.OperatorLister, opClient operatorclient.ClientInterface, configMapServerImage, utilImage string, now nowFunc) RegistryReconcilerFactory {
	return &registryReconcilerFactory{
		now:                  now,
		Lister:               lister,
		OpClient:             opClient,
		ConfigMapServerImage: configMapServerImage,
		UtilImage:            utilImage,
	}
}

func Pod(source *v1alpha1.CatalogSource, name, image, utilImage, binImage string, labels map[string]string, readinessDelay int32, livenessDelay int32) *corev1.Pod {
	// ensure catalog image is pulled always if catalog polling is configured
	var pullPolicy corev1.PullPolicy
	if source.Spec.UpdateStrategy != nil {
		pullPolicy = corev1.PullAlways
	} else {
		pullPolicy = corev1.PullIfNotPresent
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: source.GetName() + "-",
			Namespace:    source.GetNamespace(),
			Labels:       labels,
		},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{
					Name:    "util",
					Image:   utilImage,
					Command: []string{"/bin/cp", "-Rv", "/bin/cpb", "/util/cpb"}, // Copy tooling for the index container to use
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "util",
							MountPath: "/util",
						},
					},
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
				},
				{
					Name:    "pull",
					Image:   image,
					Command: []string{"/util/cpb", "index", DataMount}, // Copy index content to its mount
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data",
							MountPath: DataMountPath,
						},
						{
							Name:      "util",
							MountPath: "/util",
						},
					},
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
					TerminationMessagePath: CopyIndexStatusLocation,
				},
			},
			Containers: []corev1.Container{
				{
					Name:    name,
					Image:   binImage,
					Command: []string{"/bin/opm", "registry", "serve", "-d", DataMount},
					Ports: []corev1.ContainerPort{
						{
							Name:          "grpc",
							ContainerPort: 50051,
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: readinessDelay,
						TimeoutSeconds:      5,
					},
					LivenessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							Exec: &corev1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: livenessDelay,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					ImagePullPolicy: pullPolicy,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "data",
							MountPath: DataMountPath,
						},
					},
					TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "data", // Used to share index content
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "util", // Used to share utils
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/os": "linux",
			},
		},
	}
	return pod
}
