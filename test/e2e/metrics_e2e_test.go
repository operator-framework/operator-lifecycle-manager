// +build !bare

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

const (
	// RetryInterval defines the frequency at which we check for updates against the
	// k8s api when waiting for a specific condition to be true.
	RetryInterval = time.Second * 5

	// Timeout defines the amount of time we should spend querying the k8s api
	// when waiting for a specific condition to be true.
	Timeout = time.Minute * 5
)

// TestCSVMetrics tests the metrics endpoint of the OLM pod for CSV metrics.
func TestCSVMetrics(t *testing.T) {
	c := newKubeClient(t)
	crc := newCRClient(t)

	failingCSV := v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: genName("failing-csv-test-"),
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: strategy,
			},
		},
	}

	cleanupCSV, err := createCSV(t, c, crc, failingCSV, testNamespace, false, false)
	require.NoError(t, err)

	_, err = fetchCSV(t, crc, failingCSV.Name, testNamespace, csvFailedChecker)
	require.NoError(t, err)

	rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=olm-operator"), operatorNamespace, "8081")
	if err != nil {
		t.Fatalf("Metrics test failed: %v\n", err)
	}

	// Verify metrics have been emitted for packageserver csv
	require.Contains(t, rawOutput, "csv_abnormal")
	require.Contains(t, rawOutput, "name=\""+failingCSV.Name+"\"")
	require.Contains(t, rawOutput, "phase=\"Failed\"")
	require.Contains(t, rawOutput, "reason=\"UnsupportedOperatorGroup\"")
	require.Contains(t, rawOutput, "version=\"0.0.0\"")
	require.Contains(t, rawOutput, "csv_succeeded")

	cleanupCSV()

	rawOutput, err = getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=olm-operator"), operatorNamespace, "8081")
	if err != nil {
		t.Fatalf("Failed to retrieve metrics from OLM pod because of: %v\n", err)
	}
	require.NotContains(t, rawOutput, "csv_abnormal{name=\""+failingCSV.Name+"\"")
	require.NotContains(t, rawOutput, "csv_succeeded{name=\""+failingCSV.Name+"\"")
}

func TestSubscriptionMetrics(t *testing.T) {
	c := newKubeClient(t)
	crc := newCRClient(t)
	subscriptionName := "metric-subscription"

	_, subscription := createSubscription(t, crc, testNamespace, "metric-subscription", testPackageName, stableChannel, v1alpha1.ApprovalManual)
	err := waitForSubscriptionMetric(t, c, subscriptionName, testPackageName, stableChannel)
	require.NoError(t, err)

	subscription = getSubscription(t, crc, testNamespace, subscriptionName)
	subscription.Spec.Channel = betaChannel
	updateSubscription(t, crc, subscription)
	err = waitForSubscriptionMetric(t, c, subscriptionName, testPackageName, betaChannel)
	require.NoError(t, err)
	err = waitForSubscriptionMetricDeletion(t, c, subscriptionName, testPackageName, stableChannel)
	require.NoError(t, err)

	subscription = getSubscription(t, crc, testNamespace, subscriptionName)
	subscription.Spec.Channel = alphaChannel
	updateSubscription(t, crc, subscription)
	err = waitForSubscriptionMetric(t, c, subscriptionName, testPackageName, alphaChannel)
	require.NoError(t, err)
	err = waitForSubscriptionMetricDeletion(t, c, subscriptionName, testPackageName, stableChannel)
	require.NoError(t, err)
	err = waitForSubscriptionMetricDeletion(t, c, subscriptionName, testPackageName, betaChannel)
	require.NoError(t, err)

	subscription = getSubscription(t, crc, testNamespace, subscriptionName)
	subscriptionCleanup := buildSubscriptionCleanupFunc(t, crc, subscription)
	if subscriptionCleanup != nil {
		subscriptionCleanup()
	}
	err = waitForSubscriptionMetricDeletion(t, c, subscriptionName, testPackageName, alphaChannel)
	require.NoError(t, err)

}

func getPodWithLabel(t *testing.T, client operatorclient.ClientInterface, label string) string {
	listOptions := metav1.ListOptions{LabelSelector: label}
	podList, err := client.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(listOptions)
	if err != nil {
		log.Infof("Error %v\n", err)
		t.Fatalf("Listing pods failed: %v\n", err)
	}
	if len(podList.Items) != 1 {
		t.Fatalf("Expected 1 olm-operator pod, got %v", len(podList.Items))
	}

	podName := podList.Items[0].GetName()
	log.Infof("Looking at pod %v in namespace %v", podName, operatorNamespace)
	return podName

}

func getMetricsFromPod(t *testing.T, client operatorclient.ClientInterface, podName string, namespace string, port string) (string, error) {
	olmPod, err := client.KubernetesInterface().CoreV1().Pods(namespace).Get(podName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(olmPod.Spec.Containers) != 1 {
		t.Fatalf("Expected only 1 container in olm-operator pod, got %v", len(olmPod.Spec.Containers))
	}

	var foundCert bool
	var foundKey bool
	// assuming -tls-cert and -tls-key aren't used anywhere else as a parameter value
	for _, param := range olmPod.Spec.Containers[0].Args {
		if param == "-tls-cert" {
			foundCert = true
		} else if param == "-tls-key" {
			foundKey = true
		}
	}

	var scheme string
	if foundCert && foundKey {
		scheme = "https"
	} else {
		scheme = "http"
	}
	log.Infof("Retrieving metrics using scheme %v\n", scheme)

	rawOutput, err := client.KubernetesInterface().CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("pods").
		SubResource("proxy").
		Name(net.JoinSchemeNamePort(scheme, podName, port)).
		Suffix("metrics").
		Do().Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}

func waitForSubscriptionMetric(t *testing.T, c operatorclient.ClientInterface, name, pkg, channel string) error {
	return wait.PollImmediate(RetryInterval, Timeout, func() (bool, error) {

		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, name)) &&
			strings.Contains(rawOutput, fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, channel)) &&
			strings.Contains(rawOutput, fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, pkg)) {
			return true, nil
		}
		return false, nil
	})
}

func waitForSubscriptionMetricDeletion(t *testing.T, c operatorclient.ClientInterface, name, pkg, channel string) error {
	return wait.PollImmediate(RetryInterval, Timeout, func() (bool, error) {

		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, fmt.Sprintf("%s=\"%s\"", metrics.NAME_LABEL, name)) &&
			strings.Contains(rawOutput, fmt.Sprintf("%s=\"%s\"", metrics.CHANNEL_LABEL, channel)) &&
			strings.Contains(rawOutput, fmt.Sprintf("%s=\"%s\"", metrics.PACKAGE_LABEL, pkg)) {
			return false, nil
		}
		return true, nil
	})
}
