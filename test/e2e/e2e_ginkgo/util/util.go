package util

import (
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/names"
	e2e "k8s.io/kubernetes/test/e2e/framework"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/sirupsen/logrus"
)

var (
	Cleaner                    *namespaceCleaner
	GenName                          = names.SimpleNameGenerator.GenerateName
	immediateDeleteGracePeriod int64 = 0
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute
)

type namespaceCleaner struct {
	namespace      string
	skipCleanupOLM bool
}

type checkResourceFunc func() error

// This function returns a namespaceCleaner object for the given namespace
func NewNamespaceCleaner(namespace string) *namespaceCleaner {
	return &namespaceCleaner{
		namespace:      namespace,
		skipCleanupOLM: false,
	}
}

// This function returns a newly created Kube client
func NewKubeClient(kubeConfigPath string) operatorclient.ClientInterface {
	if kubeConfigPath == "" {
		e2e.Logf("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	// TODO: thread logger from test
	return operatorclient.NewClientFromConfig(kubeConfigPath, logrus.New())
}

// This function returns a newly created client that interacts with OLM resources
func NewCRClient(kubeConfigPath string) versioned.Interface {
	if kubeConfigPath == "" {
		e2e.Logf("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	crclient, err := client.NewClient(kubeConfigPath)
	if err != nil {
		e2e.Failf("failed to create a new client, error: %v", err)
	}
	return crclient
}

// This function polls and validates whether a given resource is deleted without errors
func waitForDelete(checkResource checkResourceFunc) error {
	var err error
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		err := checkResource()
		if errors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})

	return err
}
