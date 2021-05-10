package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extScheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/dynamic"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	controllerclient "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/controller-runtime/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	pmversioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/operator-framework/operator-registry/pkg/api/grpc_health_v1"
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute

	olmConfigMap = "olm-operators" // No-longer used, how long do we keep this around?

	// sync name with scripts/install_local.sh
	packageServerCSV = "packageserver.v1.0.0"
)

var (
	genName = names.SimpleNameGenerator.GenerateName
)

// newKubeClient configures a client to talk to the cluster defined by KUBECONFIG
func newKubeClient() operatorclient.ClientInterface {
	return ctx.Ctx().KubeClient()
}

func newCRClient() versioned.Interface {
	return ctx.Ctx().OperatorClient()
}

func newDynamicClient(t GinkgoTInterface, config *rest.Config) dynamic.Interface {
	return ctx.Ctx().DynamicClient()
}

func newPMClient() pmversioned.Interface {
	return ctx.Ctx().PackageClient()
}

// awaitPods waits for a set of pods to exist in the cluster
func awaitPods(t GinkgoTInterface, c operatorclient.ClientInterface, namespace, selector string, checkPods podsCheckFunc) (*corev1.PodList, error) {
	var fetchedPodList *corev1.PodList
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetchedPodList, err = c.KubernetesInterface().CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
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

func awaitPodsWithInterval(t GinkgoTInterface, c operatorclient.ClientInterface, namespace, selector string, interval time.Duration,
	duration time.Duration, checkPods podsCheckFunc) (*corev1.PodList, error) {
	var fetchedPodList *corev1.PodList
	var err error

	err = wait.Poll(interval, duration, func() (bool, error) {
		fetchedPodList, err = c.KubernetesInterface().CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{
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

func awaitPod(t GinkgoTInterface, c operatorclient.ClientInterface, namespace, name string, checkPod podCheckFunc) *corev1.Pod {
	var pod *corev1.Pod
	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		p, err := c.KubernetesInterface().CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		pod = p
		return checkPod(pod), nil
	})
	require.NoError(t, err)

	return pod
}

func awaitAnnotations(t GinkgoTInterface, query func() (metav1.ObjectMeta, error), expected map[string]string) error {
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
func compareResources(t GinkgoTInterface, expected, actual interface{}) {
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

func waitForGVR(dynamicClient dynamic.Interface, gvr schema.GroupVersionResource, name, namespace string) error {
	return wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		_, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
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
	if registry != nil && connState != nil && !connState.LastConnectTime.IsZero() && connState.LastObservedState == "READY" {
		fmt.Printf("catalog %s pod with address %s\n", catalog.GetName(), registry.Address())
		return registryPodHealthy(registry.Address())
	}
	state := "NO_CONNECTION"
	if connState != nil {
		state = connState.LastObservedState
	}
	fmt.Printf("waiting for catalog pod %v to be available (for sync) - %s\n", catalog.GetName(), state)
	return false
}

func fetchCatalogSourceOnStatus(crc versioned.Interface, name, namespace string, check catalogSourceCheckFunc) (*v1alpha1.CatalogSource, error) {
	var fetched *v1alpha1.CatalogSource
	var err error

	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		fetched, err = crc.OperatorsV1alpha1().CatalogSources(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil || fetched == nil {
			fmt.Println(err)
			return false, err
		}
		return check(fetched), nil
	})

	return fetched, err
}

// createFieldNotEqualSelector generates a field selector that matches resources that have a field value that DOES NOT match any of a set of values.
// This function panics if the generated selector cannot be parsed.
func createFieldNotEqualSelector(field string, values ...string) fields.Selector {
	var builder strings.Builder
	for i, value := range values {
		builder.WriteString(field)
		builder.WriteString("!=")
		builder.WriteString(value)
		if i < len(values)-1 {
			builder.WriteString(",")
		}
	}

	selector, err := fields.ParseSelector(builder.String())
	if err != nil {
		panic(fmt.Errorf("failed to build fields-not-equal selector: %s", err))
	}

	return selector
}

// MaskNotFound "masks" an given error by returning nil when it refers to a "NotFound" API status response, otherwise returns the error unaltered.
func MaskNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}

	return err
}

var (
	persistentCatalogNames        = []string{olmConfigMap}
	ephemeralCatalogFieldSelector = k8scontrollerclient.MatchingFieldsSelector{
		Selector: createFieldNotEqualSelector("metadata.name", persistentCatalogNames...),
	}
	persistentConfigMapNames         = []string{olmConfigMap}
	ephemeralConfigMapsFieldSelector = k8scontrollerclient.MatchingFieldsSelector{
		Selector: createFieldNotEqualSelector("metadata.name", persistentConfigMapNames...),
	}
	persistentCSVNames        = []string{packageServerCSV}
	ephemeralCSVFieldSelector = k8scontrollerclient.MatchingFieldsSelector{
		Selector: createFieldNotEqualSelector("metadata.name", persistentCSVNames...),
	}
)

// TearDown deletes all OLM resources in the corresponding namespace and at the cluster scope.
func TearDown(namespace string) {
	var (
		clientCtx   = context.Background()
		client      = ctx.Ctx().Client()
		inNamespace = k8scontrollerclient.InNamespace(namespace)
		logf        = ctx.Ctx().Logf
	)

	// Cleanup non persistent OLM CRs
	logf("cleaning up ephemeral test resources...")

	logf("deleting test subscriptions...")
	Eventually(func() error {
		return client.DeleteAllOf(clientCtx, &v1alpha1.Subscription{}, inNamespace)
	}).Should(Succeed(), "failed to delete test subscriptions")

	Eventually(func() (remaining []v1alpha1.Subscription, err error) {
		list := &v1alpha1.SubscriptionList{}
		err = client.List(clientCtx, list, inNamespace)
		if list != nil {
			remaining = list.Items
		}

		return
	}).Should(BeEmpty(), "failed to await deletion of test subscriptions")

	logf("deleting test installplans...")
	Eventually(func() error {
		return client.DeleteAllOf(clientCtx, &v1alpha1.InstallPlan{}, inNamespace)
	}).Should(Succeed(), "failed to delete test installplans")

	Eventually(func() (remaining []v1alpha1.InstallPlan, err error) {
		list := &v1alpha1.InstallPlanList{}
		err = client.List(clientCtx, list, inNamespace)
		if list != nil {
			remaining = list.Items
		}

		return
	}).Should(BeEmpty(), "failed to await deletion of test installplans")

	logf("deleting test catalogsources...")
	Eventually(func() error {
		return client.DeleteAllOf(clientCtx, &v1alpha1.CatalogSource{}, inNamespace, ephemeralCatalogFieldSelector)
	}).Should(Succeed(), "failed to delete test catalogsources")

	Eventually(func() (remaining []v1alpha1.CatalogSource, err error) {
		list := &v1alpha1.CatalogSourceList{}
		err = client.List(clientCtx, list, inNamespace, ephemeralCatalogFieldSelector)
		if list != nil {
			remaining = list.Items
		}

		return
	}).Should(BeEmpty(), "failed to await deletion of test catalogsources")

	logf("deleting test crds...")
	remainingCSVs := func() (csvs []v1alpha1.ClusterServiceVersion, err error) {
		list := &v1alpha1.ClusterServiceVersionList{}
		err = client.List(clientCtx, list, inNamespace, ephemeralCSVFieldSelector)
		if list != nil {
			csvs = list.Items
		}

		return
	}

	var crds []apiextensionsv1.CustomResourceDefinition
	Eventually(func() error {
		csvs, err := remainingCSVs()
		if err != nil {
			return err
		}

		for _, csv := range csvs {
			for _, desc := range csv.Spec.CustomResourceDefinitions.Owned {
				crd := &apiextensionsv1.CustomResourceDefinition{}
				err := client.Get(clientCtx, types.NamespacedName{Name: desc.Name}, crd)
				if apierrors.IsNotFound(err) {
					continue
				}
				if err != nil {
					return err
				}
				crds = append(crds, *crd)
			}
		}

		return nil
	}).Should(Succeed(), "failed to aggregate test crds for deletion")

	Eventually(func() error {
		for _, crd := range crds {
			// Note: NotFound errors will be masked, so we can simply iterate until no other errors are returned.
			// This is pretty inefficient, so if we're concerned about the number of API calls, we should replace this with something more sparing.
			if err := client.Delete(clientCtx, &crd); MaskNotFound(err) != nil {
				return err
			}
		}

		return nil
	}).Should(Succeed(), "failed to delete test crds")

	logf("deleting test csvs...")
	Eventually(func() error {
		return client.DeleteAllOf(clientCtx, &v1alpha1.ClusterServiceVersion{}, inNamespace, ephemeralCSVFieldSelector)
	}).Should(Succeed(), "failed to delete test csvs")

	Eventually(remainingCSVs).Should(BeEmpty(), "failed to await deletion of test csvs")

	logf("test resources deleted")
}

func buildCatalogSourceCleanupFunc(crc versioned.Interface, namespace string, catalogSource *v1alpha1.CatalogSource) cleanupFunc {
	return func() {
		ctx.Ctx().Logf("Deleting catalog source %s...", catalogSource.GetName())
		err := crc.OperatorsV1alpha1().CatalogSources(namespace).Delete(context.TODO(), catalogSource.GetName(), metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	}
}

func buildConfigMapCleanupFunc(c operatorclient.ClientInterface, namespace string, configMap *corev1.ConfigMap) cleanupFunc {
	return func() {
		ctx.Ctx().Logf("Deleting config map %s...", configMap.GetName())
		err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Delete(context.TODO(), configMap.GetName(), metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
	}
}

func buildServiceAccountCleanupFunc(t GinkgoTInterface, c operatorclient.ClientInterface, namespace string, serviceAccount *corev1.ServiceAccount) cleanupFunc {
	return func() {
		t.Logf("Deleting service account %s...", serviceAccount.GetName())
		require.NoError(t, c.KubernetesInterface().CoreV1().ServiceAccounts(namespace).Delete(context.TODO(), serviceAccount.GetName(), metav1.DeleteOptions{}))
	}
}

func createInvalidGRPCCatalogSource(crc versioned.Interface, name, namespace string) (*v1alpha1.CatalogSource, cleanupFunc) {

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
			SourceType: "grpc",
			Image:      "localhost:0/not/exists:catsrc",
		},
	}

	ctx.Ctx().Logf("Creating catalog source %s in namespace %s...", name, namespace)
	catalogSource, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Create(context.TODO(), catalogSource, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())
	ctx.Ctx().Logf("Catalog source %s created", name)
	return catalogSource, buildCatalogSourceCleanupFunc(crc, namespace, catalogSource)
}

func createInternalCatalogSource(c operatorclient.ClientInterface, crc versioned.Interface, name, namespace string, manifests []registry.PackageManifest, crds []apiextensions.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*v1alpha1.CatalogSource, cleanupFunc) {
	configMap, configMapCleanup := createConfigMapForCatalogData(c, name, namespace, manifests, crds, csvs)

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

	ctx.Ctx().Logf("Creating catalog source %s in namespace %s...", name, namespace)
	catalogSource, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Create(context.TODO(), catalogSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}
	ctx.Ctx().Logf("Catalog source %s created", name)

	cleanupInternalCatalogSource := func() {
		configMapCleanup()
		buildCatalogSourceCleanupFunc(crc, namespace, catalogSource)()
	}
	return catalogSource, cleanupInternalCatalogSource
}

func createInternalCatalogSourceWithPriority(c operatorclient.ClientInterface, crc versioned.Interface, name,
	namespace string, manifests []registry.PackageManifest, crds []apiextensions.CustomResourceDefinition,
	csvs []v1alpha1.ClusterServiceVersion, priority int) (*v1alpha1.CatalogSource, cleanupFunc) {

	configMap, configMapCleanup := createConfigMapForCatalogData(c, name, namespace, manifests, crds, csvs)
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
			Priority:   priority,
		},
	}
	catalogSource.SetNamespace(namespace)

	ctx.Ctx().Logf("Creating catalog source %s in namespace %s...", name, namespace)
	catalogSource, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Create(context.TODO(), catalogSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}
	ctx.Ctx().Logf("Catalog source %s created", name)

	cleanupInternalCatalogSource := func() {
		configMapCleanup()
		buildCatalogSourceCleanupFunc(crc, namespace, catalogSource)()
	}
	return catalogSource, cleanupInternalCatalogSource
}

func createV1CRDInternalCatalogSource(t GinkgoTInterface, c operatorclient.ClientInterface, crc versioned.Interface, name, namespace string, manifests []registry.PackageManifest, crds []apiextensionsv1.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*v1alpha1.CatalogSource, cleanupFunc) {
	configMap, configMapCleanup := createV1CRDConfigMapForCatalogData(t, c, name, namespace, manifests, crds, csvs)

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

	ctx.Ctx().Logf("Creating catalog source %s in namespace %s...", name, namespace)
	catalogSource, err := crc.OperatorsV1alpha1().CatalogSources(namespace).Create(context.TODO(), catalogSource, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}
	ctx.Ctx().Logf("Catalog source %s created", name)

	cleanupInternalCatalogSource := func() {
		configMapCleanup()
		buildCatalogSourceCleanupFunc(crc, namespace, catalogSource)()
	}
	return catalogSource, cleanupInternalCatalogSource
}

func createConfigMapForCatalogData(c operatorclient.ClientInterface, name, namespace string, manifests []registry.PackageManifest, crds []apiextensions.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*corev1.ConfigMap, cleanupFunc) {
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
		Expect(err).ToNot(HaveOccurred())
		catalogConfigMap.Data[registry.ConfigMapPackageName] = string(manifestsRaw)
	}

	// Add raw CRDs
	var crdsRaw []byte
	if crds != nil {
		crdStrings := []string{}
		for _, crd := range crds {
			crdStrings = append(crdStrings, serializeCRD(crd))
		}
		var err error
		crdsRaw, err = yaml.Marshal(crdStrings)
		Expect(err).ToNot(HaveOccurred())
	}
	catalogConfigMap.Data[registry.ConfigMapCRDName] = strings.Replace(string(crdsRaw), "- |\n  ", "- ", -1)

	// Add raw CSVs
	if csvs != nil {
		csvsRaw, err := yaml.Marshal(csvs)
		Expect(err).ToNot(HaveOccurred())
		catalogConfigMap.Data[registry.ConfigMapCSVName] = string(csvsRaw)
	}

	createdConfigMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Create(context.TODO(), catalogConfigMap, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}
	return createdConfigMap, buildConfigMapCleanupFunc(c, namespace, createdConfigMap)
}

func createV1CRDConfigMapForCatalogData(t GinkgoTInterface, c operatorclient.ClientInterface, name, namespace string, manifests []registry.PackageManifest, crds []apiextensionsv1.CustomResourceDefinition, csvs []v1alpha1.ClusterServiceVersion) (*corev1.ConfigMap, cleanupFunc) {
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
			crdStrings = append(crdStrings, serializeV1CRD(t, &crd))
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

	createdConfigMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(namespace).Create(context.TODO(), catalogConfigMap, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	}
	return createdConfigMap, buildConfigMapCleanupFunc(c, namespace, createdConfigMap)
}

func serializeCRD(crd apiextensions.CustomResourceDefinition) string {
	scheme := runtime.NewScheme()

	Expect(extScheme.AddToScheme(scheme)).Should(Succeed())
	Expect(k8sscheme.AddToScheme(scheme)).Should(Succeed())
	Expect(v1beta1.AddToScheme(scheme)).Should(Succeed())

	out := &v1beta1.CustomResourceDefinition{}
	Expect(scheme.Convert(&crd, out, nil)).To(Succeed())
	out.TypeMeta = metav1.TypeMeta{
		Kind:       "CustomResourceDefinition",
		APIVersion: "apiextensions.k8s.io/v1beta1",
	}

	// set up object serializer
	serializer := k8sjson.NewYAMLSerializer(k8sjson.DefaultMetaFactory, scheme, scheme)

	// create an object manifest
	var manifest bytes.Buffer
	Expect(serializer.Encode(out, &manifest)).To(Succeed())
	return manifest.String()
}

func serializeV1CRD(t GinkgoTInterface, crd *apiextensionsv1.CustomResourceDefinition) string {
	scheme := runtime.NewScheme()
	require.NoError(t, apiextensionsv1.AddToScheme(scheme))

	// set up object serializer
	serializer := k8sjson.NewYAMLSerializer(k8sjson.DefaultMetaFactory, scheme, scheme)

	// create an object manifest
	var manifest bytes.Buffer
	require.NoError(t, serializer.Encode(crd, &manifest))
	return manifest.String()
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

// Local determines whether test is running locally or in a container on openshift-CI.
// Queries for a clusterversion object specific to OpenShift.
func Local(client operatorclient.ClientInterface) (bool, error) {
	const ClusterVersionGroup = "config.openshift.io"
	const ClusterVersionVersion = "v1"
	const ClusterVersionKind = "ClusterVersion"
	gv := metav1.GroupVersion{Group: ClusterVersionGroup, Version: ClusterVersionVersion}.String()

	groups, err := client.KubernetesInterface().Discovery().ServerResourcesForGroupVersion(gv)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return true, fmt.Errorf("checking if cluster is local: checking server groups: %s", err)
	}

	for _, group := range groups.APIResources {
		if group.Kind == ClusterVersionKind {
			return false, nil
		}
	}

	return true, nil
}

// predicateFunc is a predicate for watch events.
type predicateFunc func(event watch.Event) (met bool)

// awaitPredicates waits for all predicates to be met by events of a watch in the order given.
func awaitPredicates(ctx context.Context, w watch.Interface, fns ...predicateFunc) {
	if len(fns) < 1 {
		panic("no predicates given to await")
	}

	i := 0
	for i < len(fns) {
		select {
		case <-ctx.Done():
			Expect(ctx.Err()).ToNot(HaveOccurred())
			return
		case event, ok := <-w.ResultChan():
			if !ok {
				return
			}

			if fns[i](event) {
				i++
			}
		}
	}
}

// filteredPredicate filters events to the given predicate by event type to the given types.
// When no event types are given as arguments, all event types are passed through.
func filteredPredicate(fn predicateFunc, eventTypes ...watch.EventType) predicateFunc {
	return func(event watch.Event) bool {
		valid := true
		for _, eventType := range eventTypes {
			if valid = eventType == event.Type; valid {
				break
			}
		}

		if !valid {
			return false
		}

		return fn(event)
	}
}

func deploymentPredicate(fn func(*appsv1.Deployment) bool) predicateFunc {
	return func(event watch.Event) bool {
		deployment, ok := event.Object.(*appsv1.Deployment)
		Expect(ok).To(BeTrue(), "unexpected event object type %T in deployment", event.Object)

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

func Apply(obj controllerclient.Object, changeFunc interface{}) func() error {
	return ctx.Ctx().SSAClient().Apply(context.Background(), obj, changeFunc)
}
