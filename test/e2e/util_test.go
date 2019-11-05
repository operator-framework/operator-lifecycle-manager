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
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage/names"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"k8s.io/component-base/featuregate"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/features"
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
	// TODO: impersonate ALM serviceaccount
	// TODO: thread logger from test
	return operatorclient.NewClientFromConfig(*kubeConfigPath, logrus.New())
}

func newCRClient(t *testing.T) versioned.Interface {
	if kubeConfigPath == nil {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	crclient, err := client.NewClient(*kubeConfigPath)
	require.NoError(t, err)
	return crclient
}

func newPMClient(t *testing.T) pmversioned.Interface {
	if kubeConfigPath == nil {
		t.Log("using in-cluster config")
	}
	// TODO: impersonate ALM serviceaccount
	pmc, err := pmclient.NewClient(*kubeConfigPath)
	require.NoError(t, err)
	return pmc
}

// awaitPods waits for a set of pods to exist in the cluster
func awaitPods(t *testing.T, c operatorclient.ClientInterface, namespace, selector string, checkPods podsCheckFunc) (*corev1.PodList, error) {
	var fetchedPodList *corev1.PodList
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedPodList, err = c.KubernetesInterface().CoreV1().Pods(namespace).List(metav1.ListOptions{
			LabelSelector: selector,
		})

		if err != nil {
			return false, err
		}

		t.Logf("Waiting for pods matching selector %s to match given conditions", selector)

		return checkPods(fetchedPodList), nil
	})

	require.NoError(t, err)
	return fetchedPodList, err
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

func awaitPod(t *testing.T, c operatorclient.ClientInterface, namespace, name string, checkPod podCheckFunc) *corev1.Pod {
	var pod *corev1.Pod
	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		p, err := c.KubernetesInterface().CoreV1().Pods(namespace).Get(name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pod = p
		return checkPod(pod), nil
	})
	require.NoError(t, err)

	return pod
}

func awaitAnnotations(t *testing.T, query func() (metav1.ObjectMeta, error), expected map[string]string) error {
	var err error
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		t.Logf("Waiting for annotations to match %v", expected)
		obj, err := query()
		if err != nil && !apierrors.IsNotFound(err) {
			return false, err
		}
		t.Logf("current annotations: %v", obj.GetAnnotations())

		if len(obj.GetAnnotations()) != len(expected) {
			return false, nil
		}

		for key, value := range expected {
			if v, ok := obj.GetAnnotations()[key]; !ok || v != value {
				return false, nil
			}
		}

		t.Logf("Annotations match")
		return true, nil
	})

	return err
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
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})

	return err
}

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

type catalogSourceCheckFunc func(*v1alpha1.CatalogSource) bool

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
	registry := catalog.Status.RegistryServiceStatus
	connState := catalog.Status.GRPCConnectionState
	if registry != nil && connState != nil && !connState.LastConnectTime.IsZero() {
		fmt.Printf("catalog %s pod with address %s\n", catalog.GetName(), registry.Address())
		return registryPodHealthy(registry.Address())
	}
	fmt.Printf("waiting for catalog pod %v to be available (for sync)\n", catalog.GetName())
	return false
}

func fetchCatalogSource(t *testing.T, crc versioned.Interface, name, namespace string, check catalogSourceCheckFunc) (*v1alpha1.CatalogSource, error) {
	var fetched *v1alpha1.CatalogSource
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = crc.OperatorsV1alpha1().CatalogSources(namespace).Get(name, metav1.GetOptions{})
		if err != nil || fetched == nil {
			fmt.Println(err)
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
	if err != nil && !apierrors.IsAlreadyExists(err) {
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
	if err != nil && !apierrors.IsAlreadyExists(err) {
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

func createCR(c operatorclient.ClientInterface, item *unstructured.Unstructured, apiGroup, version, namespace, resourceKind, resourceName string) (cleanupFunc, error) {
	err := c.CreateCustomResource(item)
	if err != nil {
		return nil, err
	}
	return buildCRCleanupFunc(c, apiGroup, version, namespace, resourceKind, resourceName), nil
}

func buildCRCleanupFunc(c operatorclient.ClientInterface, apiGroup, version, namespace, resourceKind, resourceName string) cleanupFunc {
	return func() {
		err := c.DeleteCustomResource(apiGroup, version, namespace, resourceKind, resourceName)
		if err != nil {
			fmt.Println(err)
		}

		waitForDelete(func() error {
			_, err := c.GetCustomResource(apiGroup, version, namespace, resourceKind, resourceName)
			return err
		})
	}
}

// predicateFunc is a predicate for watch events.
type predicateFunc func(t *testing.T, event watch.Event) (met bool)

// awaitPredicates waits for all predicates to be met by events of a watch in the order given.
func awaitPredicates(ctx context.Context, t *testing.T, w watch.Interface, fns ...predicateFunc) {
	if len(fns) < 1 {
		panic("no predicates given to await")
	}

	i := 0
	for i < len(fns) {
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
			return
		case event, ok := <-w.ResultChan():
			if !ok {
				return
			}

			if fns[i](t, event) {
				i++
			}
		}
	}
}

// filteredPredicate filters events to the given predicate by event type to the given types.
// When no event types are given as arguments, all event types are passed through.
func filteredPredicate(fn predicateFunc, eventTypes ...watch.EventType) predicateFunc {
	return func(t *testing.T, event watch.Event) bool {
		valid := true
		for _, eventType := range eventTypes {
			if valid = eventType == event.Type; valid {
				break
			}
		}

		if !valid {
			return false
		}

		return fn(t, event)
	}
}

func deploymentPredicate(fn func(*appsv1.Deployment) bool) predicateFunc {
	return func(t *testing.T, event watch.Event) bool {
		deployment, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			panic(fmt.Sprintf("unexpected event object type %T in deployment", event.Object))
		}

		return fn(deployment)
	}
}

var deploymentAvailable = filteredPredicate(deploymentPredicate(func(deployment *appsv1.Deployment) bool {
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}), watch.Added, watch.Modified)

func deploymentReplicas(replicas int32) predicateFunc {
	return filteredPredicate(deploymentPredicate(func(deployment *appsv1.Deployment) bool {
		return deployment.Status.Replicas == replicas
	}), watch.Added, watch.Modified)
}

// togglev2alpha1 toggles the v2alpha1 feature gate on or off.
func togglev2alpha1(t *testing.T, c operatorclient.ClientInterface) error {
	// Set the feature flag on OLM's deployment
	deployment, err := getOperatorDeployment(c, operatorNamespace, labels.Set{"app": "olm-operator"})
	if err != nil {
		return err
	}

	return toggleFeatureGates(t, c, deployment, features.OperatorLifecycleManagerV2)
}

// toggleFeatureGates toggles the given feature gates on or off based on their current setting in the olm-operator's deployment.
func toggleFeatureGates(t *testing.T, c operatorclient.ClientInterface, deployment *appsv1.Deployment, toToggle ...featuregate.Feature) error {
	var (
		containers     = deployment.Spec.Template.Spec.Containers
		containerIndex = -1
		argIndex       = -1
		prefix         = "--feature-gates="
		gateVals       string
	)

	// Find the container and argument indices for the feature gate option
	for i, container := range containers {
		if container.Name != "olm-operator" {
			continue
		}
		containerIndex = i

		for j, arg := range container.Args {
			if gateVals = strings.TrimPrefix(arg, prefix); arg == gateVals {
				continue
			}
			argIndex = j

			break
		}

		break
	}

	if containerIndex < 0 {
		// This should never happen since Deployments must have at least one container
		return fmt.Errorf("olm-operator deployment has no containers")
	}

	gate := features.Gate.DeepCopy()
	if argIndex >= 0 {
		// Collect existing gate values
		if err := gate.Set(gateVals); err != nil {
			return err
		}
	}

	// Toggle gates
	toggled := map[string]bool{}
	for _, feature := range toToggle {
		toggled[string(feature)] = !gate.Enabled(feature)
	}

	if err := gate.SetFromMap(toggled); err != nil {
		return err
	}

	gateArg := fmt.Sprintf("%s%s", prefix, gate)
	if argIndex >= 0 {
		// Overwrite existing gate options
		containers[containerIndex].Args[argIndex] = gateArg
	} else {
		// No existing gate options, add one
		containers[containerIndex].Args = append(containers[containerIndex].Args, gateArg)
	}

	w, err := c.KubernetesInterface().AppsV1().Deployments(deployment.GetNamespace()).Watch(metav1.ListOptions{})
	if err != nil {
		return err
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		_, err := c.KubernetesInterface().AppsV1().Deployments(deployment.GetNamespace()).Update(deployment)
		return err
	}); err != nil {
		return err
	}

	deadline, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	awaitPredicates(deadline, t, w, deploymentReplicas(2), deploymentAvailable, deploymentReplicas(1))

	return err
}

const (
	cvoNamespace      = "openshift-cluster-version"
	cvoDeploymentName = "cluster-version-operator"
)

func toggleCVO(t *testing.T, c operatorclient.ClientInterface) error {
	scale, err := c.KubernetesInterface().AppsV1().Deployments(cvoNamespace).GetScale(cvoDeploymentName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CVO is not enabled
			err = nil
		}

		return err
	}

	if scale.Spec.Replicas > 0 {
		scale.Spec.Replicas = 0
	} else {
		scale.Spec.Replicas = 1
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		_, err := c.KubernetesInterface().AppsV1().Deployments(cvoNamespace).UpdateScale(cvoDeploymentName, scale)
		return err
	}); err != nil {
		return err
	}

	return nil
}
