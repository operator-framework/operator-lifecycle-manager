//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_reconciler_factory.go . RegistryReconcilerFactory
package reconciler

import (
	"strings"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type nowFunc func() metav1.Time

const (
	// CatalogSourceLabelKey is the key for a label containing a CatalogSource name.
	CatalogSourceLabelKey string = "olm.catalogSource"
)

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
	SSAClient            *controllerclient.ServerSideApplier
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

func Pod(source *v1alpha1.CatalogSource, name string, image string, saName string, labels map[string]string, annotations map[string]string, readinessDelay int32, livenessDelay int32) *v1.Pod {
	// Ensure the catalog image is always pulled if the image is not based on a digest, measured by whether an "@" is included.
	// See https://github.com/docker/distribution/blob/master/reference/reference.go for more info.
	// This means recreating non-digest based catalog pods will result in the latest version of the catalog content being delivered on-cluster.
	var pullPolicy v1.PullPolicy
	if strings.Contains(image, "@") {
		pullPolicy = v1.PullIfNotPresent
	} else {
		pullPolicy = v1.PullAlways
	}

	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: source.GetName() + "-",
			Namespace:    source.GetNamespace(),
			Labels:       labels,
			Annotations:  annotations,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  name,
					Image: image,
					Ports: []v1.ContainerPort{
						{
							Name:          "grpc",
							ContainerPort: 50051,
						},
					},
					ReadinessProbe: &v1.Probe{
						Handler: v1.Handler{
							Exec: &v1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: readinessDelay,
						TimeoutSeconds:      5,
					},
					LivenessProbe: &v1.Probe{
						Handler: v1.Handler{
							Exec: &v1.ExecAction{
								Command: []string{"grpc_health_probe", "-addr=:50051"},
							},
						},
						InitialDelaySeconds: livenessDelay,
					},
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU:    resource.MustParse("10m"),
							v1.ResourceMemory: resource.MustParse("50Mi"),
						},
					},
					ImagePullPolicy:          pullPolicy,
					TerminationMessagePolicy: v1.TerminationMessageFallbackToLogsOnError,
				},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/os": "linux",
			},
			ServiceAccountName: saName,
		},
	}
	return pod
}
