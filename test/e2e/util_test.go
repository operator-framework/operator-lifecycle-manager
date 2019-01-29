package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	"github.com/operator-framework/operator-registry/pkg/api/grpc_health_v1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extScheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage/names"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	pmclient "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client"
	pmversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute

	olmConfigMap = "olm-operators"
	// sync name with scripts/install_local.sh
	packageServerCSV = "packageserver.v1.0.0"
)

var (
	cleaner *namespaceCleaner
	genName = names.SimpleNameGenerator.GenerateName

	persistentCatalogNames               = []string{olmConfigMap}
	nonPersistentCatalogsFieldSelector   = createFieldNotEqualSelector("metadata.name", persistentCatalogNames...)
	persistentConfigMapNames             = []string{olmConfigMap}
	nonPersistentConfigMapsFieldSelector = createFieldNotEqualSelector("metadata.name", persistentConfigMapNames...)
	persistentCSVNames                   = []string{packageServerCSV}
	nonPersistentCSVFieldSelector        = createFieldNotEqualSelector("metadata.name", persistentCSVNames...)
)

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
	if kubeConfigPath == nil {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate OLM serviceaccount
	// TODO: thread logger from test
	return operatorclient.NewClientFromConfig(*kubeConfigPath, logrus.New())
}

func newCRClient(t *testing.T) versioned.Interface {
	if kubeConfigPath == nil {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate OLM serviceaccount
	crclient, err := client.NewClient(*kubeConfigPath)
	require.NoError(t, err)
	return crclient
}

func newPMClient(t *testing.T) pmversioned.Interface {
	if kubeConfigPath == nil {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate OLM serviceaccount
	pmc, err := pmclient.NewClient(*kubeConfigPath)
	require.NoError(t, err)
	return pmc
}

// podsCheckFunc describes a function that true if the given PodList meets some criteria; false otherwise.
type podsCheckFunc func(pods *corev1.PodList) bool

// unionPodsCheck returns a podsCheckFunc that represents the union of the given podsCheckFuncs.
func unionPodsCheck(checks ...podsCheckFunc) podsCheckFunc {
	return func(pods *corev1.PodList) bool {
		for _, check := range checks {
			if !check(pods) {
				return false
			}
		}

		return true
	}
}

// podCount returns a podsCheckFunc that returns true if a PodList is of length count; false otherwise.
func podCount(count int) podsCheckFunc {
	return func(pods *corev1.PodList) bool {
		return len(pods.Items) == count
	}
}

// podsReady returns true if all of the pods in the given PodList have a ready condition with ConditionStatus "True"; false otherwise.
func podsReady(pods *corev1.PodList) bool {
	for _, pod := range pods.Items {
		if !podReady(&pod) {
			return false
		}
	}

	return true
}

// podCheckFunc describes a function that returns true if the given Pod meets some criteria; false otherwise.
type podCheckFunc func(pod *corev1.Pod) bool

// hasPodIP returns true if the given Pod has a PodIP.
func hasPodIP(pod *corev1.Pod) bool {
	return pod.Status.PodIP != ""
}

// podReady returns true if the given Pod has a ready condition with ConditionStatus "True"; false otherwise.
func podReady(pod *corev1.Pod) bool {
	var status corev1.ConditionStatus
	for _, condition := range pod.Status.Conditions {
		if condition.Type != corev1.PodReady {
			// Ignore all condition other than PodReady
			continue
		}

		// Found PodReady condition
		status = condition.Status
		break
	}

	return status == corev1.ConditionTrue
}

// awaitPods waits for a set of pods to exist in the cluster
// TODO(alecmerdler): Rewrite using generic function from `watch_test.go`
func awaitPods(t *testing.T, c operatorclient.ClientInterface, namespace, selector string, checkPods podsCheckFunc) (*corev1.PodList, error) {
	fetchedPodList, err := c.KubernetesInterface().CoreV1().Pods(namespace).List(metav1.ListOptions{
		LabelSelector: selector,
	})
	require.NoError(t, err)
	if checkPods(fetchedPodList) {
		return fetchedPodList, err
	}

	watcher, err := c.KubernetesInterface().CoreV1().Pods(namespace).Watch(metav1.ListOptions{
		LabelSelector:   selector,
		ResourceVersion: fetchedPodList.GetResourceVersion(),
	})
	require.NoError(t, err)

	events := watcher.ResultChan()
	for {
		podNames := []string{}
		for _, pod := range fetchedPodList.Items {
			podNames = append(podNames, pod.GetName())
		}
		if checkPods(fetchedPodList) {
			return fetchedPodList, nil
		}

		select {
		case evt := <-events:
			pod := evt.Object.(*corev1.Pod)
			if evt.Type == watch.Added {
				fetchedPodList.Items = append(fetchedPodList.Items, *pod)
			} else if evt.Type == watch.Modified {
				for i, existingPod := range fetchedPodList.Items {
					if pod.GetUID() == existingPod.GetUID() {
						fetchedPodList.Items[i] = *pod
					}
				}
			} else if evt.Type == watch.Deleted {
				for i, existingPod := range fetchedPodList.Items {
					if pod.GetUID() == existingPod.GetUID() {
						fetchedPodList.Items = append(fetchedPodList.Items[:i], fetchedPodList.Items[i+1:]...)
					}
				}
			}
		case <-time.After(pollDuration):
			return nil, fmt.Errorf("timed out waiting for pods matching selector %s to match given conditions", selector)
		}
	}
}

func checkAnnotations(t *testing.T, obj metav1.ObjectMeta, expected map[string]string) bool {
	t.Logf("Checking if annotations match %v", expected)
	t.Logf("Current annotations: %v", obj.GetAnnotations())

	if len(obj.GetAnnotations()) != len(expected) {
		return false
	}
	for key, value := range expected {
		if v, ok := obj.GetAnnotations()[key]; !ok || v != value {
			return false
		}
	}
	t.Logf("Annotations match")
	return true
}

type checkResourceFunc func() error

func waitForEmptyList(checkList func() (int, error)) error {
	var err error
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		count, err := checkList()
		if err != nil {
			return false, err
		}
		if count == 0 {
			return true, nil
		}
		return false, nil
	})

	return err
}

// This check is disabled for most test runs, but can be enabled for verifying pod health if the e2e tests are running
// in the same kubernetes cluster as the registry pods (currently this only happens with e2e-local-docker)
var checkPodHealth = false

func registryPodHealthy(address string) bool {
	if !checkPodHealth {
		return true
	}

	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		fmt.Printf("error connecting: %s\n", err.Error())
		return false
	}
	health := grpc_health_v1.NewHealthClient(conn)
	res, err := health.Check(context.TODO(), &grpc_health_v1.HealthCheckRequest{Service: "Registry"})
	if err != nil {
		fmt.Printf("error connecting: %s\n", err.Error())
		return false
	}
	if res.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		fmt.Printf("not healthy: %s\n", res.Status.String())
		return false
	}
	return true
}

func catalogSourceRegistryPodSynced(catalog *v1alpha1.CatalogSource) bool {
	if !catalog.Status.LastSync.IsZero() && catalog.Status.RegistryServiceStatus != nil {
		fmt.Printf("catalog %s pod with address %s\n", catalog.GetName(), catalog.Status.RegistryServiceStatus.Address())
		return registryPodHealthy(catalog.Status.RegistryServiceStatus.Address())
	}
	fmt.Println("waiting for catalog pod to be available")
	return false
}

func catalogSourceListerWatcher(crc versioned.Interface, ns string) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return crc.OperatorsV1alpha1().CatalogSources(ns).List(options)
		},
		WatchFunc: crc.OperatorsV1alpha1().CatalogSources(ns).Watch,
	}
}

func subscriptionListerWatcher(crc versioned.Interface, ns string) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return crc.OperatorsV1alpha1().Subscriptions(ns).List(options)
		},
		WatchFunc: crc.OperatorsV1alpha1().Subscriptions(ns).Watch,
	}
}

func installPlanListerWatcher(crc versioned.Interface, ns string) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return crc.OperatorsV1alpha1().InstallPlans(ns).List(options)
		},
		WatchFunc: crc.OperatorsV1alpha1().InstallPlans(ns).Watch,
	}
}

func clusterRoleListerWatcher(c operatorclient.ClientInterface) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return c.KubernetesInterface().RbacV1().ClusterRoles().List(options)
		},
		WatchFunc: c.KubernetesInterface().RbacV1().ClusterRoles().Watch,
	}
}

func operatorGroupListerWatcher(crc versioned.Interface, ns string) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return crc.OperatorsV1().OperatorGroups(ns).List(options)
		},
		WatchFunc: crc.OperatorsV1().OperatorGroups(ns).Watch,
	}
}

func csvListerWatcher(crc versioned.Interface) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).List(options)
		},
		WatchFunc: crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Watch,
	}
}

func deploymentListerWatcher(c operatorclient.ClientInterface) cache.ListerWatcher {
	return &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			return c.KubernetesInterface().Apps().Deployments(testNamespace).List(options)
		},
		WatchFunc: c.KubernetesInterface().Apps().Deployments(testNamespace).Watch,
	}
}

// TODO(alecmerdler): Rewrite using generic function from `watch_test.go`
func fetchCatalogSource(t *testing.T, crc versioned.Interface, name, namespace string, check func(*v1alpha1.CatalogSource) bool) (*v1alpha1.CatalogSource, error) {
	fetchedList, err := crc.OperatorsV1alpha1().CatalogSources(namespace).List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	require.NoError(t, err)

	if len(fetchedList.Items) == 1 && check(&fetchedList.Items[0]) {
		return &fetchedList.Items[0], err
	}

	watcher, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Watch(metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: fetchedList.GetResourceVersion(),
	})
	require.NoError(t, err)

	events := watcher.ResultChan()
	for {
		select {
		case evt := <-events:
			if evt.Type == watch.Added || evt.Type == watch.Modified {
				item := evt.Object.(*v1alpha1.CatalogSource)
				if check(item) {
					return item, err
				}
			}
		case <-time.After(pollDuration):
			return nil, fmt.Errorf("timed out waiting for CatalogSource")
		}
	}
}

// TODO(alecmerdler): Rewrite using generic function from `watch_test.go`
func fetchSubscription(t *testing.T, crc versioned.Interface, namespace, name string, check subscriptionStateChecker) (*v1alpha1.Subscription, error) {
	fetchedList, err := crc.OperatorsV1alpha1().Subscriptions(namespace).List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	require.NoError(t, err)

	log := func(s string) {
		t.Logf("%s: %s", time.Now().Format("15:04:05.9999"), s)
	}

	if len(fetchedList.Items) == 1 && check(&fetchedList.Items[0]) {
		return &fetchedList.Items[0], err
	}

	watcher, err := crc.OperatorsV1alpha1().Subscriptions(namespace).Watch(metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: fetchedList.GetResourceVersion(),
	})
	require.NoError(t, err)

	events := watcher.ResultChan()
	for {
		select {
		case evt := <-events:
			if evt.Type == watch.Added || evt.Type == watch.Modified {
				item := evt.Object.(*v1alpha1.Subscription)
				log(fmt.Sprintf("%s (%s): %s", item.Status.State, item.Status.CurrentCSV, item.Status.InstallPlanRef))
				if check(item) {
					return item, err
				}
			}
		case <-time.After(pollDuration):
			log(fmt.Sprintf("never got correct status"))

			return nil, fmt.Errorf("timed out waiting for Subscription")
		}
	}
}

// TODO(alecmerdler): Rewrite using generic function from `watch_test.go`
func fetchInstallPlan(t *testing.T, crc versioned.Interface, name string, checkPhase checkInstallPlanFunc) (*v1alpha1.InstallPlan, error) {
	fetchedList, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	require.NoError(t, err)

	if len(fetchedList.Items) == 1 && checkPhase(&fetchedList.Items[0]) {
		return &fetchedList.Items[0], err
	}

	watcher, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).Watch(metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: fetchedList.GetResourceVersion(),
	})
	require.NoError(t, err)

	events := watcher.ResultChan()
	for {
		select {
		case evt := <-events:
			if evt.Type == watch.Added || evt.Type == watch.Modified {
				item := evt.Object.(*v1alpha1.InstallPlan)
				if checkPhase(item) {
					return item, err
				}
			}
		case <-time.After(pollDuration):
			return nil, fmt.Errorf("timed out waiting for InstallPlan")
		}
	}
}

// TODO(alecmerdler): Rewrite using generic function from `watch_test.go`
func fetchCSV(t *testing.T, c versioned.Interface, name, namespace string, check csvConditionChecker) (*v1alpha1.ClusterServiceVersion, error) {
	fetchedList, err := c.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	require.NoError(t, err)

	if len(fetchedList.Items) == 1 && check(&fetchedList.Items[0]) {
		return &fetchedList.Items[0], nil
	}

	watcher, err := c.OperatorsV1alpha1().ClusterServiceVersions(namespace).Watch(metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: fetchedList.GetResourceVersion(),
	})
	require.NoError(t, err)

	events := watcher.ResultChan()
	for {
		select {
		case evt := <-events:
			if evt.Type == watch.Added || evt.Type == watch.Modified {
				csv := evt.Object.(*v1alpha1.ClusterServiceVersion)
				t.Logf("%s (%s): %s", csv.Status.Phase, csv.Status.Reason, csv.Status.Message)
				if check(csv) {
					return csv, nil
				}
			}
		case <-time.After(pollDuration):
			return nil, fmt.Errorf("timed out waiting for ClusterServiceVersion")
		}
	}
}

type deploymentChecker func(dep *appsv1.Deployment) bool

// TODO(alecmerdler): Rewrite using generic function from `watch_test.go`
func fetchDeployment(t *testing.T, c operatorclient.ClientInterface, name, namespace string, checker deploymentChecker) (*appsv1.Deployment, error) {
	fetchedList, err := c.KubernetesInterface().AppsV1().Deployments(namespace).List(metav1.ListOptions{FieldSelector: "metadata.name=" + name})
	require.NoError(t, err)

	if len(fetchedList.Items) == 1 && checker(&fetchedList.Items[0]) {
		return &fetchedList.Items[0], nil
	}

	watcher, err := c.KubernetesInterface().AppsV1().Deployments(namespace).Watch(metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: fetchedList.ResourceVersion,
	})
	require.NoError(t, err)

	events := watcher.ResultChan()
	for {
		select {
		case evt := <-events:
			if evt.Type == watch.Added || evt.Type == watch.Modified {
				dep := evt.Object.(*appsv1.Deployment)
				if checker(dep) {
					return dep, nil
				}
			}
		case <-time.After(pollDuration):
			return nil, fmt.Errorf("timed out waiting for Deployment")
		}
	}
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

func cleanupOLM(t *testing.T, namespace string) {
	var immediate int64 = 0
	crc := newCRClient(t)
	c := newKubeClient(t)

	// Cleanup non persistent OLM CRs
	t.Log("cleaning up any remaining non persistent resources...")
	deleteOptions := &metav1.DeleteOptions{GracePeriodSeconds: &immediate}
	listOptions := metav1.ListOptions{}
	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).DeleteCollection(deleteOptions, metav1.ListOptions{FieldSelector: nonPersistentCSVFieldSelector}))
	require.NoError(t, crc.OperatorsV1alpha1().InstallPlans(namespace).DeleteCollection(deleteOptions, listOptions))
	require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(namespace).DeleteCollection(deleteOptions, listOptions))
	require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(namespace).DeleteCollection(deleteOptions, metav1.ListOptions{FieldSelector: nonPersistentCatalogsFieldSelector}))

	// error: the server does not allow this method on the requested resource
	// Cleanup non persistent configmaps
	require.NoError(t, c.KubernetesInterface().CoreV1().Pods(namespace).DeleteCollection(deleteOptions, metav1.ListOptions{}))

	var err error
	// TODO(alecmerdler): Convert to `.Watch()`
	err = waitForEmptyList(func() (int, error) {
		res, err := crc.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(metav1.ListOptions{FieldSelector: nonPersistentCSVFieldSelector})
		t.Logf("%d %s remaining", len(res.Items), "csvs")
		return len(res.Items), err
	})
	require.NoError(t, err)

	err = waitForEmptyList(func() (int, error) {
		res, err := crc.OperatorsV1alpha1().InstallPlans(namespace).List(metav1.ListOptions{})
		t.Logf("%d %s remaining", len(res.Items), "installplans")
		return len(res.Items), err
	})
	require.NoError(t, err)

	err = waitForEmptyList(func() (int, error) {
		res, err := crc.OperatorsV1alpha1().Subscriptions(namespace).List(metav1.ListOptions{})
		t.Logf("%d %s remaining", len(res.Items), "subs")
		return len(res.Items), err
	})
	require.NoError(t, err)

	err = waitForEmptyList(func() (int, error) {
		res, err := crc.OperatorsV1alpha1().CatalogSources(namespace).List(metav1.ListOptions{FieldSelector: nonPersistentCatalogsFieldSelector})
		t.Logf("%d %s remaining", len(res.Items), "catalogs")
		return len(res.Items), err
	})
	require.NoError(t, err)
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

func buildServiceAccountCleanupFunc(t *testing.T, c operatorclient.ClientInterface, namespace string, serviceAccount *corev1.ServiceAccount) cleanupFunc {
	return func() {
		t.Logf("Deleting service account %s...", serviceAccount.GetName())
		require.NoError(t, c.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Delete(serviceAccount.GetName(), &metav1.DeleteOptions{}))
	}
}

func createInternalCatalogSource(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, name, namespace string, manifests []registry.PackageManifest, crds []apiextensions.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*v1alpha1.CatalogSource, cleanupFunc) {
	configMap, configMapCleanup := createConfigMapForCatalogData(t, c, name, namespace, manifests, crds, csvs)

	// Create an internal CatalogSource custom resource pointing to the ConfigMap
	catalogSource := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: "internal",
			ConfigMap:  configMap.GetName(),
		},
	}
	catalogSource.SetNamespace(namespace)

	t.Logf("Creating catalog source %s in namespace %s...", name, namespace)
	catalogSource, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Create(catalogSource)
	if err != nil && !errors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}
	t.Logf("Catalog source %s created", name)

	cleanupInternalCatalogSource := func() {
		configMapCleanup()
		buildCatalogSourceCleanupFunc(t, crc, namespace, catalogSource)()
	}
	return catalogSource, cleanupInternalCatalogSource
}

func createConfigMapForCatalogData(t *testing.T, c operatorclient.ClientInterface, name, namespace string, manifests []registry.PackageManifest, crds []apiextensions.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*corev1.ConfigMap, cleanupFunc) {
	// Create a config map containing the PackageManifests and CSVs
	configMapName := fmt.Sprintf("%s-configmap", name)
	catalogConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
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
	var crdsRaw []byte
	if crds != nil {
		crdStrings := []string{}
		for _, crd := range crds {
			crdStrings = append(crdStrings, serializeCRD(t, crd))
		}
		var err error
		crdsRaw, err = yaml.Marshal(crdStrings)
		require.NoError(t, err)
	}
	catalogConfigMap.Data[registry.ConfigMapCRDName] = strings.Replace(string(crdsRaw), "- |\n  ", "- ", -1)

	// Add raw CSVs
	if csvs != nil {
		csvsRaw, err := yaml.Marshal(csvs)
		require.NoError(t, err)
		catalogConfigMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)
	}

	createdConfigMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Create(catalogConfigMap)
	if err != nil && !errors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}
	return createdConfigMap, buildConfigMapCleanupFunc(t, c, namespace, createdConfigMap)
}

func serializeCRD(t *testing.T, crd apiextensions.CustomResourceDefinition) string {
	scheme := runtime.NewScheme()
	extScheme.AddToScheme(scheme)
	k8sscheme.AddToScheme(scheme)
	err := v1beta1.AddToScheme(scheme)
	require.NoError(t, err)

	out := &v1beta1.CustomResourceDefinition{}
	err = scheme.Convert(&crd, out, nil)
	require.NoError(t, err)
	out.TypeMeta = metav1.TypeMeta{
		Kind:       "CustomResourceDefinition",
		APIVersion: "apiextensions.k8s.io/v1beta1",
	}

	// set up object serializer
	serializer := k8sjson.NewYAMLSerializer(k8sjson.DefaultMetaFactory, scheme, scheme)

	// create an object manifest
	var manifest bytes.Buffer
	err = serializer.Encode(out, &manifest)
	require.NoError(t, err)
	return manifest.String()
}

func serializeObject(obj interface{}) string {
	bytes, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return string(bytes)
}
