package e2e

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
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
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/names"
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute

	etcdVersion            = "3.2.13"
	prometheusVersion      = "v1.7.0"
	expectedEtcdNodes      = 3
	expectedPrometheusSize = 3
	ocsConfigMap           = "ocs"
)

var (
	cleaner       *namespaceCleaner
	testNamespace = metav1.NamespaceDefault
	genName       = names.SimpleNameGenerator.GenerateName

	persistentCatalogNames               = []string{ocsConfigMap}
	nonPersistentCatalogsFieldSelector   = createFieldNotEqualSelector("metadata.name", persistentCatalogNames...)
	persistentConfigMapNames             = []string{ocsConfigMap}
	nonPersistentConfigMapsFieldSelector = createFieldNotEqualSelector("metadata.name", persistentConfigMapNames...)
	persistentDeploymentNames            = []string{"alm-operator", "catalog-operator"}
	nonPersistentDeploymentFieldSelector = createFieldNotEqualSelector("metadata.name", persistentDeploymentNames...)
)

func init() {
	e2eNamespace := os.Getenv("NAMESPACE")
	if e2eNamespace != "" {
		testNamespace = e2eNamespace
	}
	flag.Set("logtostderr", "true")
	flag.Parse()
	cleaner = newNamespaceCleaner(testNamespace)
}

type namespaceCleaner struct {
	namespace      string
	skipCleanupOLM bool
}

func newNamespaceCleaner(namespace string) *namespaceCleaner {
	return &namespaceCleaner{
		namespace:      namespace,
		skipCleanupOLM: false,
	}
}

// notifyOnFailure checks if a test has failed or cleanup is true before cleaning a namespace
func (c *namespaceCleaner) NotifyTestComplete(t *testing.T, cleanup bool) {
	if t.Failed() {
		c.skipCleanupOLM = true
	}

	if c.skipCleanupOLM || !cleanup {
		t.Log("skipping cleanup")
		return
	}

	cleanupOLM(t, c.namespace)
}

// newKubeClient configures a client to talk to the cluster defined by KUBECONFIG
func newKubeClient(t *testing.T) operatorclient.ClientInterface {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	return operatorclient.NewClientFromConfig(kubeconfigPath)
}

func newCRClient(t *testing.T) versioned.Interface {
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	crclient, err := client.NewClient(kubeconfigPath)
	require.NoError(t, err)
	return crclient
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

type checkResourceFunc func() error

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

type catalogSourceCheckFunc func(*v1alpha1.CatalogSource) bool

func catalogSourceSynced(catalog *v1alpha1.CatalogSource) bool {
	if !catalog.Status.LastSync.IsZero() {
		return true
	}
	return false
}

func fetchCatalogSource(t *testing.T, crc versioned.Interface, name, namespace string, check catalogSourceCheckFunc) (*v1alpha1.CatalogSource, error) {
	var fetched *v1alpha1.CatalogSource
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = crc.OperatorsV1alpha1().CatalogSources(namespace).Get(name, metav1.GetOptions{})
		if err != nil || fetched == nil {
			return false, err
		}
		return check(fetched), nil
	})

	return fetched, err
}

func createFieldNotEqualSelector(field string, names ...string) string {
	var builder strings.Builder
	for i, name := range names {
		builder.WriteString(field)
		builder.WriteString("!=")
		builder.WriteString(name)
		if i < len(names)-1 {
			builder.WriteString(",")
		}
	}

	return builder.String()
}

func logDeleteWaitingMessage(t *testing.T, objects []metav1.Object) {
	for i, object := range objects {
		t.Logf("waiting for %s (%d/%d) %s to be deleted...", object.GetSelfLink(), i+1, len(objects), object.GetName())
	}
}

func cleanupOLM(t *testing.T, namespace string) {
	crc := newCRClient(t)
	c := newKubeClient(t)

	// Cleanup non persistent OLM CRs
	t.Log("Cleaning up any remaining non persistent resources...")
	deleteOptions := &metav1.DeleteOptions{}
	listOptions := metav1.ListOptions{}
	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).DeleteCollection(deleteOptions, listOptions))
	require.NoError(t, crc.OperatorsV1alpha1().InstallPlans(namespace).DeleteCollection(deleteOptions, listOptions))
	require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(namespace).DeleteCollection(deleteOptions, listOptions))
	require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(namespace).DeleteCollection(deleteOptions, metav1.ListOptions{FieldSelector: nonPersistentCatalogsFieldSelector}))

	// Cleanup non persistent configmaps
	require.NoError(t, c.KubernetesInterface().CoreV1().ConfigMaps(namespace).DeleteCollection(deleteOptions, metav1.ListOptions{FieldSelector: nonPersistentConfigMapsFieldSelector}))

	// Cleanup non persistent deployments
	require.NoError(t, c.KubernetesInterface().AppsV1().Deployments(namespace).DeleteCollection(deleteOptions, metav1.ListOptions{FieldSelector: nonPersistentDeploymentFieldSelector}))

	// Wait for deletion
	listOptions.IncludeUninitialized = true
	require.NoError(t, wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		// Count remaining resources
		remaining := 0
		csvs, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(listOptions)
		if err != nil {
			return false, err
		}
		for i, csv := range csvs.Items {
			t.Logf("waiting for csv (%d/%d) %s to be deleted...", i+1, len(csvs.Items), csv.GetName())
		}
		remaining += len(csvs.Items)

		installPlans, err := crc.OperatorsV1alpha1().InstallPlans(namespace).List(listOptions)
		if err != nil {
			return false, err
		}
		for i, installPlan := range installPlans.Items {
			t.Logf("waiting for installplan (%d/%d) %s to be deleted...", i+1, len(installPlans.Items), installPlan.GetName())
		}
		remaining += len(installPlans.Items)

		subscriptions, err := crc.OperatorsV1alpha1().Subscriptions(namespace).List(listOptions)
		if err != nil {
			return false, err
		}
		for i, subscription := range subscriptions.Items {
			t.Logf("waiting for subscription (%d/%d) %s to be deleted...", i+1, len(subscriptions.Items), subscription.GetName())
		}
		remaining += len(subscriptions.Items)

		catalogSources, err := crc.OperatorsV1alpha1().CatalogSources(namespace).List(metav1.ListOptions{FieldSelector: nonPersistentConfigMapsFieldSelector, IncludeUninitialized: true})
		if err != nil {
			return false, err
		}
		for i, catalogSource := range catalogSources.Items {
			t.Logf("waiting for catalogsource (%d/%d) %s to be deleted...", i+1, len(catalogSources.Items), catalogSource.GetName())
		}
		remaining += len(catalogSources.Items)

		configMaps, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).List(metav1.ListOptions{FieldSelector: nonPersistentConfigMapsFieldSelector, IncludeUninitialized: true})
		if err != nil {
			return false, err
		}
		for i, configMap := range configMaps.Items {
			t.Logf("waiting for configmap (%d/%d) %s to be deleted...", i+1, len(configMaps.Items), configMap.GetName())
		}
		remaining += len(configMaps.Items)

		deployments, err := c.KubernetesInterface().AppsV1().Deployments(namespace).List(metav1.ListOptions{FieldSelector: nonPersistentDeploymentFieldSelector, IncludeUninitialized: true})
		if err != nil {
			return false, err
		}
		for i, deployment := range deployments.Items {
			t.Logf("waiting for deployment (%d/%d) %s to be deleted...", i+1, len(deployments.Items), deployment.GetName())
		}
		remaining += len(deployments.Items)
		t.Logf("Remaining resources: %d", remaining)

		return remaining == 0, nil
	}))
}

func buildCatalogSourceCleanupFunc(t *testing.T, crc versioned.Interface, namespace string, catalogSource *v1alpha1.CatalogSource) cleanupFunc {
	return func() {
		t.Logf("Deleting catalog source %s...", catalogSource.GetName())
		require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(namespace).Delete(catalogSource.GetName(), &metav1.DeleteOptions{}))
	}
}

func buildConfigMapCleanupFunc(t *testing.T, c operatorclient.ClientInterface, namespace string, configMap *corev1.ConfigMap) cleanupFunc {
	return func() {
		t.Logf("Deleting config map %s...", configMap.GetName())
		require.NoError(t, c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Delete(configMap.GetName(), &metav1.DeleteOptions{}))
	}
}

func createInternalCatalogSource(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, name, namespace string, manifests []registry.PackageManifest, crds []v1beta1.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*v1alpha1.CatalogSource, cleanupFunc, error) {
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
		return nil, nil, err
	}

	// Create an internal CatalogSource custom resource pointing to the ConfigMap
	catalogSource := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: "internal",
			ConfigMap:  configMapName,
		},
	}
	catalogSource.SetNamespace(namespace)

	t.Logf("Creating catalog source %s in namespace %s...", name, namespace)
	catalogSource, err = crc.OperatorsV1alpha1().CatalogSources(namespace).Create(catalogSource)
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, nil, err
	}
	t.Logf("Catalog source %s created", name)

	cleanupInternalCatalogSource := func() {
		buildConfigMapCleanupFunc(t, c, namespace, catalogConfigMap)()
		buildCatalogSourceCleanupFunc(t, crc, namespace, catalogSource)()
	}
	return catalogSource, cleanupInternalCatalogSource, nil
}
