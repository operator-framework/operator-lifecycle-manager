package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/operator-framework/operator-registry/pkg/client"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

func grpcServiceHealthCheck(service *corev1.Service) (bool, error) {
	if service == nil {
		return false, fmt.Errorf("no grpc service found")
	}

	serviceAddress := fmt.Sprintf("%s:50051", service.Spec.ClusterIP)
	return grpcHealthCheck(serviceAddress)
}

func grpcHealthCheck(address string) (bool, error) {
	logrus.Printf("[DEBUG] creating service for address: %s", address)

	registryClient, err := client.NewClient(address)
	if err != nil {
		logrus.Printf("[DEBUG] could not create client %s", err)
		return false, fmt.Errorf("unable to reache service")
	}

	healthy, err := registryClient.HealthCheck(context.Background(), time.Second*10)
	if err != nil {
		logrus.Printf("grpc service healthcheck failed: %s", err)
		err = fmt.Errorf("registry service health check failed")
	}
	return healthy, err
}
