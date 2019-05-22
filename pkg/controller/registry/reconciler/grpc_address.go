package reconciler

import (
	"net"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

type GrpcAddressRegistryReconciler struct {
	Lister          operatorlister.OperatorLister
	GlobalNamespace string
}

var _ RegistryEnsurer = &GrpcAddressRegistryReconciler{}
var _ RegistryChecker = &GrpcAddressRegistryReconciler{}
var _ RegistryReconciler = &GrpcAddressRegistryReconciler{}

// EnsureRegistryServer ensures a registry server exists for the given CatalogSource.
func (g *GrpcAddressRegistryReconciler) EnsureRegistryServer(catalogSource *v1alpha1.CatalogSource) error {
	catalogSource.Status.RegistryServiceStatus = &v1alpha1.RegistryServiceStatus{
		CreatedAt: timeNow(),
		Protocol:  "grpc",
	}

	return nil
}

func (c *GrpcAddressRegistryReconciler) currentPodsWithCorrectNamespace(source *v1alpha1.CatalogSource) []*corev1.Pod {
	selector, err := metav1.LabelSelectorAsSelector(source.Spec.PodSelector)
	if err != nil {
		logrus.WithError(err).Warn("invalid pod selector")
		return nil
	}

	pods, err := c.Lister.CoreV1().PodLister().Pods(metav1.NamespaceAll).List(selector)
	if err != nil {
		logrus.WithError(err).Warn("couldn't find pod in cache")
		return nil
	}

	correctNamespace := func(p *corev1.Pod) bool {
		return p.GetNamespace() == c.GlobalNamespace || p.GetNamespace() == source.GetNamespace()
	}
	host, _, err := net.SplitHostPort(source.Spec.Address)
	if err != nil {
		logrus.WithError(err).Warn("invalid address - must be ip:port")
		return nil
	}
	matchingAddress := func(p *corev1.Pod) bool {
		return host == p.Status.HostIP || host == p.Status.PodIP
	}
	found := []*corev1.Pod{}
	for _, p := range pods {
		if correctNamespace(p) && matchingAddress(p) {
			found = append(found, p)
		}
	}
	return found
}

// CheckRegistryServer returns true if the given CatalogSource is considered healthy; false otherwise.
func (c *GrpcAddressRegistryReconciler) CheckRegistryServer(catalogSource *v1alpha1.CatalogSource) (healthy bool, err error) {
	// Check on registry resources
	// TODO: add gRPC health check
	if len(c.currentPodsWithCorrectNamespace(catalogSource)) < 1 {
		healthy = false
		return
	}

	healthy = true
	return
}
