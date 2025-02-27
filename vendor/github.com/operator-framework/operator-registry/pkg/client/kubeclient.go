package client

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func NewKubeClient(kubeconfig string, logger *logrus.Logger) (*kubernetes.Clientset, error) {
	var config *rest.Config

	if overrideConfig := os.Getenv(clientcmd.RecommendedConfigPathEnvVar); overrideConfig != "" {
		kubeconfig = overrideConfig
	}

	var err error
	if kubeconfig != "" {
		logger.Infof("Loading kube client config from path %q", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		logger.Infof("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		// nolint:stylecheck
		err = fmt.Errorf("Cannot load config for REST client: %v", err)
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	return clientset, err
}
