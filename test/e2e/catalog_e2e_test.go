//  +build !bare

package e2e

import (
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/version"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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
	csv := newCSV(packageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)

	c := newKubeClient(t)
	crc := newCRClient(t)

	catalogSourceName := genName("mock-ocs-")
	_, cleanupSource := createInternalCatalogSource(t, c, crc, catalogSourceName, operatorNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	defer cleanupSource()

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
		before := catalogSource.Status.GRPCConnectionState
		after := cs.Status.GRPCConnectionState
		if after != nil && after.LastConnectTime.After(before.LastConnectTime.Time) {
			t.Logf("lastSync updated: %s -> %s", before.LastConnectTime, after.LastConnectTime)
			return true
		}
		return false
	})
	require.NoError(t, err, "Catalog source changed after rescale")
	t.Logf("Catalog source sucessfully loaded after rescale")
}

func TestGlobalCatalogUpdateTriggersSubscriptionSync(t *testing.T) {
	defer cleaner.NotifyTestComplete(t, true)

	globalNS := operatorNamespace
	c := newKubeClient(t)
	crc := newCRClient(t)

	// Determine which namespace is global. Should be `openshift-marketplace` for OCP 4.2+.
	// Locally it is `olm`
	namespaces, _ := c.KubernetesInterface().CoreV1().Namespaces().List(metav1.ListOptions{})
	for _, ns := range namespaces.Items {
		if ns.GetName() == "openshift-marketplace" {
			globalNS = "openshift-marketplace"
		}
	}

	mainPackageName := genName("nginx-")

	mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
	mainPackageReplacement := fmt.Sprintf("%s-replacement", mainPackageStable)

	stableChannel := "stable"

	mainNamedStrategy := newNginxInstallStrategy(genName("dep-"), nil, nil)

	crdPlural := genName("ins-")

	mainCRD := newCRD(crdPlural)
	mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, mainNamedStrategy)
	replacementCSV := newCSV(mainPackageReplacement, testNamespace, mainPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, mainNamedStrategy)

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

	// Create the initial catalogsource
	createInternalCatalogSource(t, c, crc, mainCatalogName, globalNS, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})

	// Attempt to get the catalog source before creating install plan
	_, err := fetchCatalogSource(t, crc, mainCatalogName, globalNS, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscriptionSpec := &v1alpha1.SubscriptionSpec{
		CatalogSource:          mainCatalogName,
		CatalogSourceNamespace: globalNS,
		Package:                mainPackageName,
		Channel:                stableChannel,
		StartingCSV:            mainCSV.GetName(),
		InstallPlanApproval:    v1alpha1.ApprovalManual,
	}

	// Create Subscription
	subscriptionName := genName("sub-")
	createSubscriptionForCatalogWithSpec(t, crc, testNamespace, subscriptionName, subscriptionSpec)

	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	installPlanName := subscription.Status.Install.Name
	requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseRequiresApproval)
	fetchedInstallPlan, err := fetchInstallPlan(t, crc, installPlanName, requiresApprovalChecker)
	require.NoError(t, err)

	fetchedInstallPlan.Spec.Approved = true
	_, err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Update(fetchedInstallPlan)
	require.NoError(t, err)

	_, err = awaitCSV(t, crc, testNamespace, mainCSV.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Update manifest
	mainManifests = []registry.PackageManifest{
		{
			PackageName: mainPackageName,
			Channels: []registry.PackageChannel{
				{Name: stableChannel, CurrentCSVName: replacementCSV.GetName()},
			},
			DefaultChannelName: stableChannel,
		},
	}

	// Update catalog configmap
	updateInternalCatalog(t, c, crc, mainCatalogName, globalNS, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, replacementCSV}, mainManifests)

	// Get updated catalogsource
	fetchedUpdatedCatalog, err := fetchCatalogSource(t, crc, mainCatalogName, globalNS, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateUpgradePendingChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Ensure the timing
	catalogConnState := fetchedUpdatedCatalog.Status.GRPCConnectionState
	subUpdatedTime := subscription.Status.LastUpdated
	timeLapse := subUpdatedTime.Sub(catalogConnState.LastConnectTime.Time).Seconds()
	require.True(t, timeLapse < 60)
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
	mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

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
		before := fetchedInitialCatalog.Status.ConfigMapResource
		after := catalog.Status.ConfigMapResource
		if after != nil && before.LastUpdateTime.Before(&after.LastUpdateTime) &&
			after.ResourceVersion != before.ResourceVersion {
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
	mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

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
	_, cleanupSource := createInternalCatalogSource(t, c, crc, mainCatalogName, testNamespace, mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})

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
	cleanupSource()

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
	mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	replacementCSV := newCSV(mainPackageReplacement, testNamespace, mainPackageStable, semver.MustParse("0.2.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, mainNamedStrategy)
	dependentCSV := newCSV(dependentPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, dependentNamedStrategy)

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
			Address:    net.JoinHostPort(mainCopy.Status.PodIP, "50051"),
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
	addressSource.Spec.Address = net.JoinHostPort(replacementCopy.Status.PodIP, "50051")
	_, err = crc.OperatorsV1alpha1().CatalogSources(testNamespace).Update(addressSource)
	require.NoError(t, err)

	// Wait for the replacement CSV to be installed
	_, err = awaitCSV(t, crc, testNamespace, replacementCSV.GetName(), csvSucceededChecker)
	require.NoError(t, err)
}

func TestDeleteInternalRegistryPodTriggersRecreation(t *testing.T) {
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
	sourceName := genName("catalog-")
	crd := newCRD(genName("ins-"))
	csv := newCSV(packageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, namedStrategy)
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
	_, cleanupSource := createInternalCatalogSource(t, c, crc, sourceName, testNamespace, manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
	defer cleanupSource()

	// Wait for a new registry pod to be created
	selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": sourceName})
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
	cleanupSubscription := createSubscriptionForCatalog(t, crc, testNamespace, subscriptionName, sourceName, packageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
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

func TestDeleteGRPCRegistryPodTriggersRecreation(t *testing.T) {
	// Create gRPC CatalogSource using an external registry image (community-operators)
	// Wait for a registry pod to be created
	// Create a Subscription for package
	// Wait for the Subscription to succeed
	// Wait for csv to succeed
	// Delete the registry pod
	// Wait for a new registry pod to be created

	defer cleaner.NotifyTestComplete(t, true)

	sourceName := genName("catalog-")
	packageName := "etcd"
	channelName := "clusterwide-alpha"

	// Create gRPC CatalogSource using an external registry image (community-operators)
	source := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sourceName,
			Namespace: testNamespace,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: v1alpha1.SourceTypeGrpc,
			Image:      communityOperatorsImage,
		},
	}

	crc := newCRClient(t)
	source, err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(source)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Delete(source.GetName(), &metav1.DeleteOptions{}))
	}()

	// Wait for a new registry pod to be created
	c := newKubeClient(t)
	selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": source.GetName()})
	singlePod := podCount(1)
	registryPods, err := awaitPods(t, c, source.GetNamespace(), selector.String(), singlePod)
	require.NoError(t, err, "error awaiting registry pod")
	require.NotNil(t, registryPods, "nil registry pods")
	require.Equal(t, 1, len(registryPods.Items), "unexpected number of registry pods found")

	// Store the UID for later comparison
	uid := registryPods.Items[0].GetUID()
	name := registryPods.Items[0].GetName()

	// Create a Subscription for package
	subscriptionName := genName("sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, source.GetNamespace(), subscriptionName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	// Wait for the Subscription to succeed
	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Wait for csv to succeed
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
	require.NoError(t, err)

	// Delete the registry pod
	require.NoError(t, c.KubernetesInterface().CoreV1().Pods(testNamespace).Delete(name, &metav1.DeleteOptions{}))

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

const (
	openshiftregistryFQDN = "image-registry.openshift-image-registry.svc:5000/openshift-operators"
	catsrcImage           = "docker://quay.io/olmtest/catsrc-update-test:"
)

func TestCatalogImageUpdate(t *testing.T) {
	// Create an image based catalog source from public Quay image
	// Use a unique tag as identifier
	// See https://quay.io/repository/olmtest/catsrc-update-test?namespace=olmtest for registry
	// Push an updated version of the image with the same identifier
	// Confirm catalog source polling feature is working as expected: a newer version of the catalog source pod comes up
	// etcd operator updated from 0.9.0 to 0.9.2-clusterwide
	// Subscription should detect the latest version of the operator in the new catalog source and pull it

	// create internal registry for purposes of pushing/pulling IF running e2e test locally
	// registry is insecure and for purposes of this test only
	c := newKubeClient(t)
	crc := newCRClient(t)

	local, err := Local(c)
	if err != nil {
		t.Fatalf("cannot determine if test running locally or on CI: %s", err)
	}

	var registryURL string
	var registryAuth string
	if local {
		registryURL, err = createDockerRegistry(c, testNamespace)
		if err != nil {
			t.Fatalf("error creating container registry: %s", err)
		}
		defer deleteDockerRegistry(c, testNamespace)

		// ensure registry pod is ready before attempting port-forwarding
		_ = awaitPod(t, c, testNamespace, registryName, podReady)
		
		err = registryPortForward(testNamespace)
		if err != nil {
			t.Fatalf("port-forwarding local registry: %s", err)
		}
	} else {
		registryURL = openshiftregistryFQDN
		registryAuth, err = openshiftRegistryAuth(c, testNamespace)
		if err != nil {
			t.Fatalf("error getting openshift registry authentication: %s", err)
		}
	}

	// testImage is the name of the image used throughout the test - the image overwritten by skopeo
	// the tag is generated randomly and appended to the end of the testImage
	testImage := fmt.Sprint("docker://", registryURL, "/catsrc-update", ":")
	tag := genName("x")

	// 1. copy old catalog image into test-specific tag in internal docker registry
	// create skopeo pod to actually do the work of copying (on openshift) or exec out to local skopeo
	if local {
		_, err := skopeoLocalCopy(testImage, tag, catsrcImage, "old")
		if err != nil {
			t.Fatalf("error copying old registry file: %s", err)
		}
	} else {
		skopeoArgs := skopeoCopyCmd(testImage, tag, catsrcImage, "old", registryAuth)
		err = createSkopeoPod(c, skopeoArgs, testNamespace)
		if err != nil {
			t.Fatalf("error creating skopeo pod: %s", err)
		}

		// wait for skopeo pod to exit successfully
		awaitPod(t, c, testNamespace, skopeo, func(pod *corev1.Pod) bool {
			return pod.Status.Phase == corev1.PodSucceeded
		})

		err = deleteSkopeoPod(c, testNamespace)
		if err != nil {
			t.Fatalf("error deleting skopeo pod: %s", err)
		}
	}

	// 2. setup catalog source
	defer cleaner.NotifyTestComplete(t, true)

	sourceName := genName("catalog-")
	packageName := "etcd"
	channelName := "clusterwide-alpha"

	// Create gRPC CatalogSource using an external registry image and poll interval
	var image string
	image = testImage[9:] // strip off docker://
	image = fmt.Sprint(image, tag)

	source := &v1alpha1.CatalogSource{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.CatalogSourceKind,
			APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sourceName,
			Namespace: testNamespace,
			Labels:    map[string]string{"olm.catalogSource": sourceName},
		},
		Spec: v1alpha1.CatalogSourceSpec{
			SourceType: v1alpha1.SourceTypeGrpc,
			Image:      image,
			UpdateStrategy: &v1alpha1.UpdateStrategy{
				RegistryPoll: &v1alpha1.RegistryPoll{
					Interval: &metav1.Duration{Duration: 1 * time.Minute},
				},
			},
		},
	}

	source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(source)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Delete(source.GetName(), &metav1.DeleteOptions{}))
	}()

	// wait for new catalog source pod to be created
	// Wait for a new registry pod to be created
	selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": source.GetName()})
	singlePod := podCount(1)
	registryPods, err := awaitPods(t, c, source.GetNamespace(), selector.String(), singlePod)
	require.NoError(t, err, "error awaiting registry pod")
	require.NotNil(t, registryPods, "nil registry pods")
	require.Equal(t, 1, len(registryPods.Items), "unexpected number of registry pods found")

	// Create a Subscription for package
	subscriptionName := genName("sub-")
	cleanupSubscription := createSubscriptionForCatalog(t, crc, source.GetNamespace(), subscriptionName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)
	defer cleanupSubscription()

	// Wait for the Subscription to succeed
	subscription, err := fetchSubscription(t, crc, testNamespace, subscriptionName, subscriptionStateAtLatestChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Wait for csv to succeed
	_, err = fetchCSV(t, crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
	require.NoError(t, err)

	registryCheckFunc := func(podList *corev1.PodList) bool {
		if len(podList.Items) > 1 {
			return false
		}
		return podList.Items[0].Status.ContainerStatuses[0].ImageID != ""
	}
	// get old catalog source pod
	registryPod, err := awaitPods(t, c, source.GetNamespace(), selector.String(), registryCheckFunc)
	// 3. Update image on registry via skopeo: this should trigger a newly updated version of the catalog source pod
	// to be deployed after some time
	// Make another skopeo pod to do the work of copying the image
	if local {
		_, err := skopeoLocalCopy(testImage, tag, catsrcImage, "new")
		if err != nil {
			t.Fatalf("error copying new registry file: %s", err)
		}
	} else {
		skopeoArgs := skopeoCopyCmd(testImage, tag, catsrcImage, "new", registryAuth)
		err = createSkopeoPod(c, skopeoArgs, testNamespace)
		if err != nil {
			t.Fatalf("error creating skopeo pod: %s", err)
		}

		// wait for skopeo pod to exit successfully
		awaitPod(t, c, testNamespace, skopeo, func(pod *corev1.Pod) bool {
			return pod.Status.Phase == corev1.PodSucceeded
		})

		err = deleteSkopeoPod(c, testNamespace)
		if err != nil {
			t.Fatalf("error deleting skopeo pod: %s", err)
		}
	}

	// update catalog source with annotation (to kick resync)
	source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(source.GetName(), metav1.GetOptions{})
	require.NoError(t, err, "error awaiting registry pod")
	source.Annotations = make(map[string]string)
	source.Annotations["testKey"] = "testValue"
	_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(source)
	require.NoError(t, err, "error awaiting registry pod")

	time.Sleep(11 * time.Second)

	// ensure new registry pod container image is as we expect
	podCheckFunc := func(podList *corev1.PodList) bool {
		fmt.Printf("pod list length %d\n", len(podList.Items))
		for _, pod := range podList.Items {
			fmt.Printf("pod list name %v\n", pod.Name)
		}

		for _, pod := range podList.Items {
			fmt.Printf("old image id %s\n new image id %s\n", registryPod.Items[0].Status.ContainerStatuses[0].ImageID,
				pod.Status.ContainerStatuses[0].ImageID)
			if pod.Status.ContainerStatuses[0].ImageID != registryPod.Items[0].Status.ContainerStatuses[0].ImageID {
				return true
			}
		}
		// update catalog source with annotation (to kick resync)
		source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(source.GetName(), metav1.GetOptions{})
		require.NoError(t, err, "error getting catalog source pod")
		source.Annotations["testKey"] = genName("newValue")
		_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(source)
		require.NoError(t, err, "error updating catalog source pod with test annotation")
		return false
	}
	// await new catalog source and ensure old one was deleted
	registryPods, err = awaitPodsWithInterval(t, c, source.GetNamespace(), selector.String(), 30*time.Second, 10*time.Minute, podCheckFunc)
	require.NoError(t, err, "error awaiting registry pod")
	require.NotNil(t, registryPods, "nil registry pods")
	require.Equal(t, 1, len(registryPods.Items), "unexpected number of registry pods found")

	// update catalog source with annotation (to kick resync)
	source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(source.GetName(), metav1.GetOptions{})
	require.NoError(t, err, "error awaiting registry pod")
	source.Annotations["testKey"] = "newValue"
	_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(source)
	require.NoError(t, err, "error awaiting registry pod")

	subChecker := func(sub *v1alpha1.Subscription) bool {
		return sub.Status.InstalledCSV == "etcdoperator.v0.9.2-clusterwide"
	}
	// Wait for the Subscription to succeed
	subscription, err = fetchSubscription(t, crc, testNamespace, subscriptionName, subChecker)
	require.NoError(t, err)
	require.NotNil(t, subscription)

	// Wait for csv to succeed
	csv, err := fetchCSV(t, crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
	require.NoError(t, err)

	// check version of running csv to ensure the latest version (0.9.2) was installed onto the cluster
	v := csv.Spec.Version
	etcdVersion := semver.Version{
		Major: 0,
		Minor: 9,
		Patch: 2,
		Pre: []semver.PRVersion{
			{
				VersionStr: "clusterwide",
			},
		},
	}
	if !reflect.DeepEqual(v, version.OperatorVersion{Version: etcdVersion}) {
		t.Errorf("latest version of operator not installed: catalog souce update failed")
	}
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
