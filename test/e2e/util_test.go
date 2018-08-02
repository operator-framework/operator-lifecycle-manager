package e2e

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/names"
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
func newKubeClient(t *testing.T) operatorclient.ClientInterface {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	return operatorclient.NewClient(kubeconfigPath)
}

// awaitPods waits for a set of pods to exist in the cluster
func awaitPods(t *testing.T, c operatorclient.ClientInterface, selector string, expectedCount int) (*corev1.PodList, error) {
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
func pollForCustomResource(t *testing.T, c operatorclient.ClientInterface, group string, version string, kind string, name string) error {
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
func waitForAndFetchCustomResource(t *testing.T, c operatorclient.ClientInterface, version string, kind string, name string) (*unstructured.Unstructured, error) {
	t.Logf("Looking for %s %s in %s\n", kind, name, testNamespace)
	var res *unstructured.Unstructured
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		res, err = c.GetCustomResource(v1alpha1.GroupName, version, testNamespace, kind, name)
		if err != nil {
			return false, nil
		}
		return true, nil
	})

	return res, err
}

/// waitForAndFetchCustomResource is same as pollForCustomResource but returns the fetched unstructured resource
func waitForAndFetchChildren(t *testing.T, c operatorclient.ClientInterface, version string, kind string, owner ownerutil.Owner, count int) ([]*unstructured.Unstructured, error) {
	t.Logf("Looking for %d %s in %s\n", count, kind, testNamespace)
	var res []*unstructured.Unstructured
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		crList, err := c.ListCustomResource(v1alpha1.GroupName, version, testNamespace, kind)
		if err != nil {
			t.Log(err)
			return false, nil
		}

		owned := 0
		for _, obj := range crList.Items {
			if ownerutil.IsOwnedBy(obj, owner) {
				owned += 1
				res = append(res, obj)
			}
		}

		// waiting for count number of objects to exist
		if owned != count {
			return false, nil
		}
		return true, nil
	})

	return res, err
}

func cleanupCustomResource(t *testing.T, c operatorclient.ClientInterface, group, kind, name string) cleanupFunc {
	return func() {
		t.Logf("deleting %s %s", kind, name)
		require.NoError(t, c.DeleteCustomResource(v1alpha1.GroupName, group, testNamespace, kind, name))
	}
}

// compareResources compares resource equality then prints a diff for easier debugging
func compareResources(t *testing.T, expected, actual interface{}) {
	if eq := equality.Semantic.DeepEqual(expected, actual); !eq {
		t.Fatalf("Resource does not match expected value: %s",
			diff.ObjectDiff(expected, actual))
	}
}

func createInternalCatalogSource(t *testing.T, c operatorclient.ClientInterface, name, namespace string, manifests []registry.PackageManifest, crds []v1beta1.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*v1alpha1.CatalogSource, error) {
	// Create a config map containing the PackageManifests and CSVs
	configMapName := fmt.Sprintf("%s-configmap", name)
	catalogConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: configMapName,
		},
		Data: map[string]string{},
	}
	catalogConfigMap.SetNamespace(namespace)

	// Add raw manifests
	if manifests != nil {
		manifestsRaw, err := yaml.Marshal(manifests)
		require.NoError(t, err)
		catalogConfigMap.Data[registry.ConfigMapPackageName] = string(manifestsRaw)
	}

	// Add raw CRDs
	if crds != nil {
		crdsRaw, err := yaml.Marshal(crds)
		require.NoError(t, err)
		catalogConfigMap.Data[registry.ConfigMapCRDName] = string(crdsRaw)
	}

	// Add raw CSVs
	if csvs != nil {
		csvsRaw, err := yaml.Marshal(csvs)
		require.NoError(t, err)
		catalogConfigMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)
	}

	_, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Create(catalogConfigMap)
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}

	// Create an internal CatalogSource custom resource pointing to the ConfigMap
	catalogSource := v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Name:       name,
			SourceType: "internal",
			ConfigMap:  configMapName,
		},
	}
	catalogSource.SetNamespace(namespace)

	csUnst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&catalogSource)
	require.NoError(t, err)
	t.Logf("Creating catalog source %s in namespace %s...", name, namespace)
	err = c.CreateCustomResource(&unstructured.Unstructured{Object: csUnst})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}
	t.Logf("Catalog source %s created", name)

	return &catalogSource, nil
}
