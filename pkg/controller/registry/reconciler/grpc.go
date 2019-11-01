package reconciler

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const CatalogSourceUpdateKey = "olm.catalogSource.update"

// grpcCatalogSourceDecorator wraps CatalogSource to add additional methods
type grpcCatalogSourceDecorator struct {
	*v1alpha1.CatalogSource
}

func (s *grpcCatalogSourceDecorator) Selector() labels.Selector {
	return labels.SelectorFromValidatedSet(map[string]string{
		CatalogSourceLabelKey: s.GetName(),
	})
}

func (s *grpcCatalogSourceDecorator) UpdateSelector() labels.Selector {
	return labels.SelectorFromValidatedSet(map[string]string{
		CatalogSourceUpdateKey: s.GetName(),
	})
}

func (s *grpcCatalogSourceDecorator) Labels() map[string]string {
	return map[string]string{
		CatalogSourceLabelKey: s.GetName(),
	}
}

func (s *grpcCatalogSourceDecorator) Service() *v1.Service {
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.GetName(),
			Namespace: s.GetNamespace(),
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Name:       "grpc",
					Port:       50051,
					TargetPort: intstr.FromInt(50051),
				},
			},
			Selector: s.Labels(),
		},
	}
	ownerutil.AddOwner(svc, s.CatalogSource, false, false)
	return svc
}

func (s *grpcCatalogSourceDecorator) Pod() *v1.Pod {
	pod := Pod(s.CatalogSource, "registry-server", s.Spec.Image, s.Labels(), 5, 10)
	ownerutil.AddOwner(pod, s.CatalogSource, false, false)
	return pod
}

type GrpcRegistryReconciler struct {
	now      nowFunc
	Lister   operatorlister.OperatorLister
	OpClient operatorclient.ClientInterface
}

var _ RegistryReconciler = &GrpcRegistryReconciler{}

func (c *GrpcRegistryReconciler) currentService(source grpcCatalogSourceDecorator) *v1.Service {
	serviceName := source.Service().GetName()
	service, err := c.Lister.CoreV1().ServiceLister().Services(source.GetNamespace()).Get(serviceName)
	if err != nil {
		logrus.WithField("service", serviceName).Warn("couldn't find service in cache")
		return nil
	}
	return service
}

func (c *GrpcRegistryReconciler) currentPods(source grpcCatalogSourceDecorator) []*v1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(source.Selector())
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Selector()).Warn("multiple pods found for selector")
	}
	return pods
}

func (c *GrpcRegistryReconciler) currentUpdatePods(source grpcCatalogSourceDecorator) []*v1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(source.UpdateSelector())
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Selector()).Warn("multiple pods found for selector")
	}
	return pods
}

func (c *GrpcRegistryReconciler) currentPodsWithCorrectImage(source grpcCatalogSourceDecorator) []*v1.Pod {
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(labels.SelectorFromValidatedSet(source.Labels()))
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	found := []*v1.Pod{}
	for _, p := range pods {
		if p.Spec.Containers[0].Image == source.Spec.Image {
			found = append(found, p)
		}
	}
	return found
}

// EnsureRegistryServer ensures that all components of registry server are up to date.
func (c *GrpcRegistryReconciler) EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error {
	source := grpcCatalogSourceDecorator{catalogSource}

	// if service status is nil, we force create every object to ensure they're created the first time
	overwrite := source.Status.RegistryServiceStatus == nil
	// recreate the pod if no existing pod is serving the latest image
	overwritePod := overwrite || len(c.currentPodsWithCorrectImage(source)) == 0

	//TODO: if any of these error out, we should write a status back (possibly set RegistryServiceStatus to nil so they get recreated)
	if err := c.ensurePod(source, overwritePod); err != nil {
		return errors.Wrapf(err, "error ensuring pod: %s", source.Pod().GetName())
	}
	if err := c.ensureService(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring service: %s", source.Service().GetName())
	}

	if overwritePod {
		now := c.now()
		catalogSource.Status.RegistryServiceStatus = &v1alpha1.RegistryServiceStatus{
			CreatedAt:        now,
			Protocol:         "grpc",
			ServiceName:      source.Service().GetName(),
			ServiceNamespace: source.GetNamespace(),
			Port:             fmt.Sprintf("%d", source.Service().Spec.Ports[0].Port),
		}
	}
	return nil
}

func (c *GrpcRegistryReconciler) ensurePod(source grpcCatalogSourceDecorator, overwrite bool) error {
	// currentLivePods refers to the currently live instances of the catalog source
	currentLivePods := c.currentPods(source)
	if len(currentLivePods) > 0 {
		if !overwrite {
			return nil
		}
		for _, p := range currentLivePods {
			if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(p.GetName(), metav1.NewDeleteOptions(0)); err != nil {
				return errors.Wrapf(err, "error deleting old pod: %s", p.GetName())
			}
		}
	}
	_, err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Create(source.Pod())
	if err != nil {
		return errors.Wrapf(err, "error creating new pod: %s", source.Pod().GetGenerateName())
	}

	//currentUpdatePods refers to test instances of the catalog source, checking if the catalog source has been updated
	currentUpdatePods := c.currentUpdatePods(source)
	if len(currentUpdatePods) > 0 {
		if source.ReadyToUpdate() {
			// delete old update pods
			for _, p := range currentUpdatePods {
				if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(p.GetName(), metav1.NewDeleteOptions(0)); err != nil {
					return errors.Wrapf(err, "error deleting old pod: %s", p.GetName())
				}
			}
			// make new updated catalog source pod
			source.SetLastUpdateTime()
			pod, err := c.createUpdatePod(source)
			if err != nil {
				return errors.Wrapf(err, "error creating update catalog source pod")
			}
			if updatePodByDigest(pod, source) {
				// there exists an update pod with a new digest
				// route traffic to that pod and delete the old catalog source pod
				pod.Labels[CatalogSourceLabelKey] = source.GetName()
				pod.Labels[CatalogSourceUpdateKey] = ""

				for _, p := range currentLivePods {
					if err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Delete(p.GetName(), metav1.NewDeleteOptions(0)); err != nil {
						return errors.Wrapf(err, "error deleting old pod: %s", p.GetName())
					}
				}
			}
		}
	}

	// need to make a new update pod
	if source.ReadyToUpdate() {
		source.SetLastUpdateTime()
		_ , err := c.createUpdatePod(source)
		if err != nil {
			return errors.Wrapf(err, "error creating update catalog source pod")
		}
	}
	return nil
}

func (c *GrpcRegistryReconciler) ensureService(source grpcCatalogSourceDecorator, overwrite bool) error {
	service := source.Service()
	if c.currentService(source) != nil {
		if !overwrite {
			return nil
		}
		if err := c.OpClient.DeleteService(service.GetNamespace(), service.GetName(), metav1.NewDeleteOptions(0)); err != nil {
			return err
		}
	}
	_, err := c.OpClient.CreateService(service)
	return err
}

// createUpdatePod is an internal method that creates a pod using the latest catalog source.
func (c *GrpcRegistryReconciler) createUpdatePod(source grpcCatalogSourceDecorator) (*corev1.Pod, error) {
	// remove label from pod to ensure service does accidentally route traffic to the pod
	p := source.Pod()
	p.Labels[CatalogSourceLabelKey] = ""
	p.Labels[CatalogSourceUpdateKey] = source.Name

	pod, err := c.OpClient.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).Create(p)
	if err != nil {
		logrus.WithField("pod", "catalog-source-pod").Warn("couldn't create new catalog source pod")
		return nil, err
	}

	return pod, nil
}

// checkUpdatePodDigest checks update pod to get Image ID and see if it matches the old version
func updatePodByDigest(pod *corev1.Pod, source grpcCatalogSourceDecorator) bool {
	var newCatalogSourceImage string
	if pod.Status.ContainerStatuses != nil {
		newCatalogSourceImage = pod.Status.ContainerStatuses[0].ImageID
	} else {
		return false
	}

	logrus.WithField("pod", pod.Spec.Containers[0].Image).Info(fmt.Sprintf("found new image digest %s",
		pod.Status.ContainerStatuses[0].ImageID))

	return newCatalogSourceImage == source.Pod().Status.ContainerStatuses[0].ImageID
}

// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
func (c *GrpcRegistryReconciler) CheckRegistryServer(catalogSource *v1alpha1.CatalogSource) (healthy bool, err error) {
	source := grpcCatalogSourceDecorator{catalogSource}

	// Check on registry resources
	// TODO: add gRPC health check
	if len(c.currentPodsWithCorrectImage(source)) < 1 ||
		c.currentService(source) == nil {
		healthy = false
		return
	}

	healthy = true
	return
}
