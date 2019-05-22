package reconciler

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

type GrpcRegistryPodSelectorReconciler struct {
	Lister   operatorlister.OperatorLister
	OpClient operatorclient.ClientInterface
}

var _ RegistryReconciler = &GrpcRegistryPodSelectorReconciler{}

// grpcSelectorCatalogSourceDecorator wraps CatalogSource to add additional methods
type grpcSelectorCatalogSourceDecorator struct {
	*v1alpha1.CatalogSource
}

func (s *grpcSelectorCatalogSourceDecorator) Service() *corev1.Service {
	selector, err := metav1.LabelSelectorAsMap(s.Spec.PodSelector)
	if err != nil {
		logrus.WithError(err).Warn("invalid pod selector")
		return nil
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.GetName(),
			Namespace: s.GetNamespace(),
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "grpc",
					Port:       GrpcPort,
					TargetPort: intstr.FromInt(GrpcPort),
				},
			},
			Selector: selector,
		},
	}
	ownerutil.AddOwner(svc, s.CatalogSource, false, false)
	return svc
}

func (c *GrpcRegistryPodSelectorReconciler) currentService(source grpcSelectorCatalogSourceDecorator) *corev1.Service {
	service := source.Service()
	if service == nil {
		logrus.Warn("invalid service")
		return nil
	}
	serviceName := service.GetName()
	service, err := c.Lister.CoreV1().ServiceLister().Services(source.GetNamespace()).Get(serviceName)
	if err != nil {
		logrus.WithField("service", serviceName).Warn("couldn't find service in cache")
		return nil
	}
	return service
}

func (c *GrpcRegistryPodSelectorReconciler) currentPods(source grpcSelectorCatalogSourceDecorator) []*corev1.Pod {
	selector, err := metav1.LabelSelectorAsSelector(source.Spec.PodSelector)
	if err != nil {
		logrus.WithError(err).Warn("invalid pod selector")
		return nil
	}
	pods, err := c.Lister.CoreV1().PodLister().Pods(source.GetNamespace()).List(selector)
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}
	if len(pods) > 1 {
		logrus.WithField("selector", source.Spec.PodSelector).Warn("multiple pods found for selector")
	}
	return pods
}

// EnsureRegistryServer ensures that all components of registry server are up to date.
func (c *GrpcRegistryPodSelectorReconciler) EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error {
	source := grpcSelectorCatalogSourceDecorator{catalogSource}

	// if service status is nil, we force create every object to ensure they're created the first time
	overwrite := source.Status.RegistryServiceStatus == nil

	if err := c.ensureService(source, overwrite); err != nil {
		return errors.Wrapf(err, "error ensuring service: %s", source.Service().GetName())
	}

	if overwrite {
		catalogSource.Status.RegistryServiceStatus = &v1alpha1.RegistryServiceStatus{
			CreatedAt:        timeNow(),
			Protocol:         "grpc",
			ServiceName:      source.Service().GetName(),
			ServiceNamespace: source.GetNamespace(),
			Port:             fmt.Sprintf("%d", source.Service().Spec.Ports[0].Port),
		}
		catalogSource.Status.LastSync = timeNow()
	}
	return nil
}

func (c *GrpcRegistryPodSelectorReconciler) ensureService(source grpcSelectorCatalogSourceDecorator, overwrite bool) error {
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

// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
func (c *GrpcRegistryPodSelectorReconciler) CheckRegistryServer(catalogSource *v1alpha1.CatalogSource) (healthy bool, err error) {
	source := grpcSelectorCatalogSourceDecorator{catalogSource}

	// Check on registry resources
	// TODO: add gRPC health check
	if len(c.currentPods(source)) < 1 ||
		c.currentService(source) == nil {
		healthy = false
		return
	}

	healthy = true
	return
}
