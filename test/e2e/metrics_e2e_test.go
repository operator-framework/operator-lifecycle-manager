// +build !bare

package e2e

import (
	"strings"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

const (
	// RetryInterval defines the frequency at which we check for updates against the
	// k8s api when waiting for a specific condition to be true.
	RetryInterval = time.Second * 5

	// Timeout defines the amount of time we should spend querying the k8s api
	// when waiting for a specific condition to be true.
	Timeout = time.Minute * 5
)

// TestCSVMetrics tests the metrics endpoint of the OLM pod for metrics emitted by CSVs.
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

	subscriptionCleanup, subscription := createSubscription(t, crc, testNamespace, "metric-subscription", testPackageName, stableChannel, v1alpha1.ApprovalManual)

	err := wait.PollImmediate(RetryInterval, Timeout, func() (done bool, err error) {
		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, "name=\"metric-subscription\"") &&
			strings.Contains(rawOutput, "channel=\""+stableChannel+"\"") &&
			strings.Contains(rawOutput, "package=\""+testPackageName+"\"") {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	updatedSubscription, err := crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Get(subscription.GetName(), metav1.GetOptions{})
	require.NoError(t, err)

	updatedSubscription.Spec.Channel = betaChannel
	updateSubscription(t, crc, updatedSubscription)

	err = wait.PollImmediate(RetryInterval, Timeout, func() (done bool, err error) {
		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, "name=\"metric-subscription\"") &&
			strings.Contains(rawOutput, "channel=\""+stableChannel+"\"") &&
			strings.Contains(rawOutput, "package=\""+testPackageName+"\"") {
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)

	err = wait.PollImmediate(RetryInterval, Timeout, func() (done bool, err error) {
		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, "name=\"metric-subscription\"") &&
			strings.Contains(rawOutput, "channel=\""+betaChannel+"\"") &&
			strings.Contains(rawOutput, "package=\""+testPackageName+"\"") {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	updatedSubscription, err = crc.OperatorsV1alpha1().Subscriptions(subscription.GetNamespace()).Get(subscription.GetName(), metav1.GetOptions{})
	require.NoError(t, err)

	updatedSubscription.Spec.Channel = alphaChannel
	updateSubscription(t, crc, updatedSubscription)

	err = wait.PollImmediate(RetryInterval, Timeout, func() (done bool, err error) {
		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, "name=\"metric-subscription\"") &&
			strings.Contains(rawOutput, "channel=\""+betaChannel+"\"") &&
			strings.Contains(rawOutput, "package=\""+testPackageName+"\"") {
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)

	err = wait.PollImmediate(RetryInterval, Timeout, func() (done bool, err error) {
		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, "name=\"metric-subscription\"") &&
			strings.Contains(rawOutput, "channel=\""+alphaChannel+"\"") &&
			strings.Contains(rawOutput, "package=\""+testPackageName+"\"") {
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	if subscriptionCleanup != nil {
		subscriptionCleanup()
	}
	err = wait.PollImmediate(RetryInterval, Timeout, func() (done bool, err error) {
		rawOutput, err := getMetricsFromPod(t, c, getPodWithLabel(t, c, "app=catalog-operator"), operatorNamespace, "8081")
		if err != nil {
			return false, err
		}
		if strings.Contains(rawOutput, "subscription_sync_total") &&
			strings.Contains(rawOutput, "name=metric-subscription") &&
			strings.Contains(rawOutput, "channel=\""+alphaChannel+"\"") &&
			strings.Contains(rawOutput, "package=\""+testPackageName+"\"") {
			return false, nil
		}
		return true, nil
	})
	require.NoError(t, err)
}

func getPodWithLabel(t *testing.T, client operatorclient.ClientInterface, label string) *corev1.Pod {
	listOptions := metav1.ListOptions{LabelSelector: label}
	podList, err := client.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(listOptions)
	if err != nil {
		log.Infof("Error %v\n", err)
		t.Fatalf("Listing pods failed: %v\n", err)
	}
	if len(podList.Items) != 1 {
		t.Fatalf("Expected 1 olm-operator pod, got %v", len(podList.Items))
	}
	return &podList.Items[0]
}

func getMetricsFromPod(t *testing.T, client operatorclient.ClientInterface, pod *corev1.Pod, namespace string, port string) (string, error) {
	olmPod, err := client.KubernetesInterface().CoreV1().Pods(namespace).Get(pod.GetName(), metav1.GetOptions{})
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
		Name(net.JoinSchemeNamePort(scheme, pod.GetName(), port)).
		Suffix("metrics").
		Do().Raw()
	if err != nil {
		return "", err
	}
	return string(rawOutput), nil
}
