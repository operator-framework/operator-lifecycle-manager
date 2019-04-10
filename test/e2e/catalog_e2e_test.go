//  +build !bare

package e2e

import (
	"fmt"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

func TestCatalogLoadingBetweenRestarts(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	// create a simple catalogsource
	packageName := genName("nginx")
	stableChannel := "stable"
	packageStable := packageName + "-stable"
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: packageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	crdPlural := genName("ins")
	crd := newCRD(crdPlural)
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	csv := newCSV(packageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSourceName := genName("mock-ocs-")
	_, cleanupCatalogSource := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	defer cleanupCatalogSource()

	// ensure the mock catalog exists and has been synced by the catalog operator
	catalogSource, err := fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// get catalog operator deployment
	deployment, err := getOperatorDeployment(c, operatorNamespace, labels.Set{"app": "catalog-operator"})
	require.NoError(t, err)
	require.NotNil(t, deployment, "Could not find catalog operator deployment")

	// rescale catalog operator
	t.Log("Rescaling catalog operator...")
	err = rescaleDeployment(c, deployment)
	require.NoError(t, err, "Could not rescale catalog operator")
	t.Log("Catalog operator rescaled")

	// check for last synced update to catalogsource
	t.Log("Checking for catalogsource lastSync updates")
	_, err = fetchCatalogSource(t, crc, catalogSourceName, operatorNamespace, func(cs *v1alpha1.CatalogSource) bool {
		if cs.Status.LastSync.After(catalogSource.Status.LastSync.Time) {
			t.Logf("lastSync updated: %s -> %s", catalogSource.Status.LastSync, cs.Status.LastSync)
			return true
		}
		return false
	})
	require.NoError(t, err, "Catalog source changed after rescale")
	t.Logf("Catalog source sucessfully loaded after rescale")
}

func TestDefaultCatalogLoading(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)
	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSource, err := fetchCatalogSource(t, crc, "olm-operators", operatorNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	requirement, err := labels.NewRequirement("olm.catalogSource", selection.Equals, []string{catalogSource.GetName()})
	require.NoError(t, err)
	selector := labels.NewSelector().Add(*requirement).String()
	pods, err := c.KubernetesInterface().CoreV1().Pods(operatorNamespace).List(metav1.ListOptions{LabelSelector: selector})
	require.NoError(t, err)
	for _, p := range pods.Items {
		for _, s := range p.Status.ContainerStatuses {
			require.True(t, s.Ready)
			require.Zero(t, s.RestartCount)
		}
	}
}

func TestConfigMapUpdateTriggersRegistryPodRollout(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	mainPackageName := genName("nginx-")
	dependentPackageName := genName("nginxdep-")

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

	stableChannel := "stable"

	mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

	crdPlural := genName("ins-")

	dependentCRD := newCRD(crdPlural)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	mainCatalogName := genName("mock-ocs-main-")

	// Create separate manifests for each CatalogSource
	mainManifests := []registry.PackageManifest{
		{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: mainPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	dependentManifests := []registry.PackageManifest{
		{
			PackageName: dependentPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: dependentPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create the initial catalogsource
	createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})

	// Attempt to get the catalog source before creating install plan
	fetchedInitialCatalog, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Get initial configmap
	configMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
	require.NoError(t, err)

	// Check pod created
	initialPods, err := c.KubernetesInterface().CoreV1().Pods(testNamespace).List(metav1.ListOptions{LabelSelector: "olm.configMapResourceVersion=" + configMap.ResourceVersion})
	require.NoError(t, err)
	require.Equal(t, 1, len(initialPods.Items))

	// Update catalog configmap
	updateInternalCatalog(t, c, crc, mainCatalogName, testNamespace, []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, dependentCSV}, append(mainManifests, dependentManifests...))

	// Get updated configmap
	updatedConfigMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
	require.NoError(t, err)

	fetchedUpdatedCatalog, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, func(catalog *v1alpha1.CatalogSource) bool {
		if catalog.Status.LastSync != fetchedInitialCatalog.Status.LastSync && catalog.Status.ConfigMapResource.ResourceVersion != fetchedInitialCatalog.Status.ConfigMapResource.ResourceVersion {
			fmt.Println("catalog updated")
			return true
		}
		fmt.Println("waiting for catalog pod to be available")
		return false
	})
	require.NoError(t, err)

	require.NotEqual(t, updatedConfigMap.ResourceVersion, configMap.ResourceVersion)
	require.NotEqual(t, fetchedUpdatedCatalog.Status.ConfigMapResource.ResourceVersion, fetchedInitialCatalog.Status.ConfigMapResource.ResourceVersion)
	require.Equal(t, updatedConfigMap.GetResourceVersion(), fetchedUpdatedCatalog.Status.ConfigMapResource.ResourceVersion)

	// Await 1 CatalogSource registry pod matching the updated labels
	singlePod := podCount(1)
	selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": mainCatalogName, "olm.configMapResourceVersion": updatedConfigMap.GetResourceVersion()})
	podList, err := awaitPods(t, c, testNamespace, selector.String(), singlePod)
	require.NoError(t, err)
	require.Equal(t, 1, len(podList.Items), "expected pod list not of length 1")

	// Await 1 CatalogSource registry pod matching the updated labels
	selector = labels.SelectorFromSet(map[string]string{"olm.catalogSource": mainCatalogName})
	podList, err = awaitPods(t, c, testNamespace, selector.String(), singlePod)
	require.NoError(t, err)
	require.Equal(t, 1, len(podList.Items), "expected pod list not of length 1")

	// Create Subscription
	subscriptionName := genName("sub-")
	createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, fetchedUpdatedCatalog.GetName(), mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)

	ipList, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).List(metav1.ListOptions{})
	ipCount := 0
	for _, ip := range ipList.Items {
		if ownerutil.IsOwnedBy(&ip, subscription) {
			ipCount += 1
		}
	}
	require.NoError(t, err)
}

func TestConfigMapReplaceTriggersRegistryPodRollout(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	mainPackageName := genName("nginx-")
	dependentPackageName := genName("nginxdep-")

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)

	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

	stableChannel := "stable"

	mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

	crdPlural := genName("ins-")

	dependentCRD := newCRD(crdPlural)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	mainCatalogName := genName("mock-ocs-main-")

	// Create separate manifests for each CatalogSource
	mainManifests := []registry.PackageManifest{
		{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: mainPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	dependentManifests := []registry.PackageManifest{
		{
			PackageName: dependentPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: dependentPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create the initial catalogsource
	_, cleanupCatalog := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})

	// Attempt to get the catalog source before creating install plan
	fetchedInitialCatalog, err := fetchCatalogSource(t, crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	// Get initial configmap
	configMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
	require.NoError(t, err)

	// Check pod created
	initialPods, err := c.KubernetesInterface().CoreV1().Pods(testNamespace).List(metav1.ListOptions{LabelSelector: "olm.configMapResourceVersion=" + configMap.ResourceVersion})
	require.NoError(t, err)
	require.Equal(t, 1, len(initialPods.Items))

	// delete the first catalog
	cleanupCatalog()

	// create a catalog with the same name
	createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, append(mainManifests, dependentManifests...), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, dependentCSV})

	// Create Subscription
	subscriptionName := genName("sub-")
	createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
	require.NoError(t, err)

}

func TestGrpcAddressCatalogSource(t *testing.T) {
	// Create an internal (configmap) CatalogSource with stable and dependency csv
	// Create an internal (configmap) replacement CatalogSource with a stable, stable-replacement, and dependency csv
	// Copy both configmap-server pods to the test namespace
	// Delete both CatalogSources
	// Create an "address" CatalogSource with a Spec.Address field set to the stable copied pod's PodIP
	// Create a Subscription to the stable package
	// Wait for the stable Subscription to be Successful
	// Wait for the stable CSV to be Successful
	// Update the "address" CatalogSources's Spec.Address field with the PodIP of the replacement copied pod's PodIP
	// Wait for the replacement CSV to be Successful

	defer cleaner.NotifyTestComplete(t, true)

	mainPackageName := genName("nginx-")
	dependentPackageName := genName("nginxdep-")

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	mainPackageReplacement := fmt.Sprintf("%s-replacement", mainPackageStable)
	dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

	stableChannel := "stable"

	mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	dependentNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

	crdPlural := genName("ins-")

	dependentCRD := newCRD(crdPlural)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", *semver.New("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	replacementCSV := newCSV(mainPackageReplacement, testNamespace, mainPackageStable, *semver.New("0.2.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	mainSourceName := genName("mock-ocs-main-")
	replacementSourceName := genName("mock-ocs-main-with-replacement-")

	// Create separate manifests for each CatalogSource
	mainManifests := []registry.PackageManifest{
		{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: mainPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	replacementManifests := []registry.PackageManifest{
		{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: mainPackageReplacement},
			},
			DefaultChannelName: stableChannel,
		},
	}

	dependentManifests := []registry.PackageManifest{
		{
			PackageName: dependentPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: dependentPackageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Create ConfigMap CatalogSources
	createInternalCatalogSource(t, c, crc, mainSourceName, testNamespace, append(mainManifests, dependentManifests...), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, dependentCSV})
	createInternalCatalogSource(t, c, crc, replacementSourceName, testNamespace, append(replacementManifests, dependentManifests...), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{replacementCSV, mainCSV, dependentCSV})

	// Wait for ConfigMap CatalogSources to be ready
	mainSource, err := fetchCatalogSource(t, crc, mainSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)
	replacementSource, err := fetchCatalogSource(t, crc, replacementSourceName, testNamespace, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Replicate catalog pods with no OwnerReferences
	mainCopy := replicateCatalogPod(t, c, crc, mainSource)
	mainCopy = awaitPod(t, c, mainCopy.GetNamespace(), mainCopy.GetName(), hasPodIP)
	replacementCopy := replicateCatalogPod(t, c, crc, replacementSource)
	replacementCopy = awaitPod(t, c, replacementCopy.GetNamespace(), replacementCopy.GetName(), hasPodIP)

	addressSourceName := genName("address-catalog-")

	// Create a CatalogSource pointing to the grpc pod
	addressSource := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      addressSourceName,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: v1alpha1.SourceTypeGrpc,
			Address:    fmt.Sprintf("%s:%s", mainCopy.Status.PodIP, "50051"),
		},
	}

	addressSource, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Create(addressSource)
	require.NoError(t, err)
	defer func() {
		err := crc.OperatorsV1alpha1().CatalogSources(testNamespace).Delete(addressSourceName, &metav1.DeleteOptions{})
		require.NoError(t, err)
	}()

	// Delete CatalogSources
	err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Delete(mainSourceName, &metav1.DeleteOptions{})
	require.NoError(t, err)
	err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Delete(replacementSourceName, &metav1.DeleteOptions{})
	require.NoError(t, err)

	// Create Subscription
	subscriptionName := genName("sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, addressSourceName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Update the catalog's address to point at the other registry pod's cluster ip
	addressSource, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Get(addressSourceName, metav1.GetOptions{})
	require.NoError(t, err)
	addressSource.Spec.Address = fmt.Sprintf("%s:%s", replacementCopy.Status.PodIP, "50051")
	_, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Update(addressSource)
	require.NoError(t, err)

	// Wait for the replacement CSV to be installed
	_, err = awaitCSV(t, crc, testNamespace, replacementCSV.GetName(), csvSucceededChecker)
	require.NoError(t, err)
}

func TestDeleteRegistryPodTriggersRecreation(t *testing.T) {
	// Create internal CatalogSource containing csv in package
	// Wait for a registry pod to be created
	// Create a Subscription for package
	// Wait for the Subscription to succeed
	// Wait for csv to succeed
	// Delete the registry pod
	// Wait for a new registry pod to be created

	defer cleaner.NotifyTestComplete(t, true)

	// Create internal CatalogSource containing csv in package
	packageName := genName("nginx-")
	packageStable := fmt.Sprintf("%s-stable", packageName)
	stableChannel := "stable"
	namedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)
	catalogName := genName("catsrc-")
	crd := newCRD(genName("ins-"))
	csv := newCSV(packageStable, testNamespace, "", *semver.New("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
	manifests := []registry.PackageManifest{
		{
			PackageName: packageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: packageStable},
			},
			DefaultChannelName: stableChannel,
		},
	}

	c := newKubeClient(t)
	crc := newCRClient(t)
	_, cleanupCatalog := createInternalCatalogSource(t, c, crc, catalogName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	defer cleanupCatalog()

	// Wait for a new registry pod to be created
	selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": catalogName})
	singlePod := podCount(1)
	registryPods, err := awaitPods(t, c, testNamespace, selector.String(), singlePod)
	require.NoError(t, err, "error awaiting registry pod")
	require.NotNil(t, registryPods, "nil registry pods")
	require.Equal(t, 1, len(registryPods.Items), "unexpected number of registry pods found")

	// Store the UID for later comparison
	uid := registryPods.Items[0].GetUID()
	name := registryPods.Items[0].GetName()

	// Create a Subscription for package
	subscriptionName := genName("sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, catalogName, packageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	// Wait for the Subscription to succeed
	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Wait for csv to succeed
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, testNamespace, csvSucceededChecker)
	require.NoError(t, err)

	// Delete the registry pod
	err = c.KubernetesInterface().CoreV1().Pods(testNamespace).Delete(name, &metav1.DeleteOptions{})
	require.NoError(t, err)

	// Wait for a new registry pod to be created
	notUID := func(pods *corev1.PodList) bool {
		for _, pod := range pods.Items {
			if pod.GetUID() == uid {
				return false
			}
		}

		return true
	}
	registryPods, err = awaitPods(t, c, testNamespace, selector.String(), unionPodsCheck(singlePod, notUID))
	require.NoError(t, err, "error waiting for replacement registry pod")
	require.NotNil(t, registryPods, "nil replacement registry pods")
	require.Equal(t, 1, len(registryPods.Items), "unexpected number of replacement registry pods found")
}

func getOperatorDeployment(c operatorclient.ClientInterface, namespace string, operatorLabels labels.Set) (*appsv1.Deployment, error) {
	deployments, err := c.ListDeploymentsWithLabels(namespace, operatorLabels)
	if err != nil || deployments == nil || len(deployments.Items) != 1 {
		return nil, fmt.Errorf("Error getting single operator deployment for label: %v", operatorLabels)
	}
	return &deployments.Items[0], nil
}

func rescaleDeployment(c operatorclient.ClientInterface, deployment *appsv1.Deployment) error {
	// scale down
	var replicas int32 = 0
	deployment.Spec.Replicas = &replicas
	deployment, updated, err := c.UpdateDeployment(deployment)
	if err != nil || updated == false || deployment == nil {
		return fmt.Errorf("Failed to scale down deployment")
	}

	waitForScaleup := func() (bool, error) {
		fetchedDeployment, err := c.GetDeployment(deployment.GetNamespace(), deployment.GetName())
		if err != nil {
			return true, err
		}
		if fetchedDeployment.Status.Replicas == replicas {
			return true, nil
		}

		return false, nil
	}

	// wait for deployment to scale down
	err = wait.Poll(pollInterval, pollDuration, waitForScaleup)
	if err != nil {
		return err
	}

	// scale up
	replicas = 1
	deployment.Spec.Replicas = &replicas
	deployment, updated, err = c.UpdateDeployment(deployment)
	if err != nil || updated == false || deployment == nil {
		return fmt.Errorf("Failed to scale up deployment")
	}

	// wait for deployment to scale up
	err = wait.Poll(pollInterval, pollDuration, waitForScaleup)

	return err
}

func replicateCatalogPod(t *testing.T, c operatorclient.ClientInterface, crc versioned.Interface, catalog *v1alpha1.CatalogSource) *corev1.Pod {
	initialPods, err := c.KubernetesInterface().CoreV1().Pods(catalog.GetNamespace()).List(metav1.ListOptions{LabelSelector: "olm.catalogSource=" + catalog.GetName()})
	require.NoError(t, err)
	require.Equal(t, 1, len(initialPods.Items))

	pod := initialPods.Items[0]
	copied := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: catalog.GetNamespace(),
			Name:      catalog.GetName() + "-copy",
		},
		Spec: pod.Spec,
	}

	copied, err = c.KubernetesInterface().CoreV1().Pods(catalog.GetNamespace()).Create(copied)
	require.NoError(t, err)

	return copied
}
