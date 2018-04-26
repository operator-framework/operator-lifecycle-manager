package e2e

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/coreos/alm/pkg/api/apis"
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute
)

var testNamespace = metav1.NamespaceDefault
var genName = names.SimpleNameGenerator.GenerateName

func init() {
	e2eNamespace := os.Getenv("NAMESPACE")
	if e2eNamespace != "" {
		testNamespace = e2eNamespace
	}
	flag.Set("logtostderr", "true")
	flag.Parse()
}

// newKubeClient configures a client to talk to the cluster defined by KUBECONFIG
func newKubeClient(t *testing.T) opClient.Interface {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	return opClient.NewClient(kubeconfigPath)
}

// awaitPods waits for a set of pods to exist in the cluster
func awaitPods(t *testing.T, c opClient.Interface, selector string, expectedCount int) (*corev1.PodList, error) {
	var fetchedPodList *corev1.PodList
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedPodList, err = c.KubernetesInterface().CoreV1().Pods(testNamespace).List(metav1.ListOptions{
			LabelSelector: selector,
		})

		if err != nil {
			return false, err
		}

		t.Logf("Waiting for %d nodes matching %s selector, %d present", expectedCount, selector, len(fetchedPodList.Items))

		if len(fetchedPodList.Items) < expectedCount {
			return false, nil
		}

		return true, nil
	})

	require.NoError(t, err)
	return fetchedPodList, err
}

// pollForCustomResource waits for a CR to exist in the cluster, returning an error if we fail to retrieve the CR after its been created
func pollForCustomResource(t *testing.T, c opClient.Interface, group string, version string, kind string, name string) error {
	t.Logf("Looking for %s %s in %s\n", kind, name, testNamespace)

	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := c.GetCustomResource(group, version, testNamespace, kind, name)
		if err != nil {
			if sErr := err.(*errors.StatusError); sErr.Status().Reason == metav1.StatusReasonNotFound {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})

	return err
}

/// waitForAndFetchCustomResource is same as pollForCustomResource but returns the fetched unstructured resource
func waitForAndFetchCustomResource(t *testing.T, c opClient.Interface, version string, kind string, name string) (*unstructured.Unstructured, error) {
	t.Logf("Looking for %s %s in %s\n", kind, name, testNamespace)
	var res *unstructured.Unstructured
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		res, err = c.GetCustomResource(apis.GroupName, version, testNamespace, kind, name)
		if err != nil {
			return false, nil
		}
		return true, nil
	})

	return res, err
}
func cleanupCustomResource(c opClient.Interface, group, kind, name string) cleanupFunc {
	return func() {
		err := c.DeleteCustomResource(apis.GroupName, group, testNamespace, kind, name)
		if err != nil {
			fmt.Printf("ERROR cleaning up - DeleteCustomResource(%s, %s, %s, %s, %s) err=%v\n",
				apis.GroupName, group, testNamespace, kind, name, err)
		}
	}
}
