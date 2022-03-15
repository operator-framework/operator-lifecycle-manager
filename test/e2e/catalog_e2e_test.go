//go:build !bare
// +build !bare

package e2e

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/operator-framework/api/pkg/lib/version"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalogtemplate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/catalogsource"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Catalog represents a store of bundles which OLM can use to install Operators", func() {
	var (
		c   operatorclient.ClientInterface
		crc versioned.Interface
		ns  corev1.Namespace
	)
	BeforeEach(func() {
		c = newKubeClient()
		crc = newCRClient()
		namespaceName := genName("catsrc-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", namespaceName),
				Namespace: namespaceName,
			},
		}
		ns = SetupGeneratedTestNamespaceWithOperatorGroup(namespaceName, og)
	})

	AfterEach(func() {
		TeardownNamespace(ns.GetName())
	})

	It("loading between restarts", func() {
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

		crd := newCRD(genName("ins"))
		csv := newCSV(packageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)

		catalogSourceName := genName("mock-ocs-")
		_, cleanupSource := createInternalCatalogSource(c, crc, catalogSourceName, ns.GetName(), manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
		defer cleanupSource()

		// ensure the mock catalog exists and has been synced by the catalog operator
		catalogSource, err := fetchCatalogSourceOnStatus(crc, catalogSourceName, ns.GetName(), catalogSourceRegistryPodSynced)
		Expect(err).ShouldNot(HaveOccurred())

		// get catalog operator deployment
		deployment, err := getOperatorDeployment(c, operatorNamespace, labels.Set{"app": "catalog-operator"})
		Expect(err).ShouldNot(HaveOccurred())
		Expect(deployment).ToNot(BeNil(), "Could not find catalog operator deployment")

		// rescale catalog operator
		By("Rescaling catalog operator...")
		err = rescaleDeployment(c, deployment)
		Expect(err).ShouldNot(HaveOccurred(), "Could not rescale catalog operator")
		By("Catalog operator rescaled")

		// check for last synced update to catalogsource
		By("Checking for catalogsource lastSync updates")
		_, err = fetchCatalogSourceOnStatus(crc, catalogSourceName, ns.GetName(), func(cs *v1alpha1.CatalogSource) bool {
			before := catalogSource.Status.GRPCConnectionState
			after := cs.Status.GRPCConnectionState
			if after != nil && after.LastConnectTime.After(before.LastConnectTime.Time) {
				ctx.Ctx().Logf("lastSync updated: %s -> %s", before.LastConnectTime, after.LastConnectTime)
				return true
			}
			return false
		})
		Expect(err).ShouldNot(HaveOccurred(), "Catalog source changed after rescale")
		By("Catalog source successfully loaded after rescale")
	})

	It("global update triggers subscription sync", func() {
		globalNS := operatorNamespace

		// Determine which namespace is global. Should be `openshift-marketplace` for OCP 4.2+.
		// Locally it is `olm`
		namespaces, _ := c.KubernetesInterface().CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		for _, ns := range namespaces.Items {
			if ns.GetName() == "openshift-marketplace" {
				globalNS = "openshift-marketplace"
			}
		}

		mainPackageName := genName("nginx-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		mainPackageReplacement := fmt.Sprintf("%s-replacement", mainPackageStable)

		stableChannel := "stable"

		mainCRD := newCRD(genName("ins-"))
		mainCSV := newCSV(mainPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, nil)
		replacementCSV := newCSV(mainPackageReplacement, ns.GetName(), mainPackageStable, semver.MustParse("0.2.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, nil)

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

		// Create the initial catalog source
		cs, cleanup := createInternalCatalogSource(c, crc, mainCatalogName, globalNS, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanup()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, cs.GetName(), cs.GetNamespace(), catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionSpec := &v1alpha1.SubscriptionSpec{
			CatalogSource:          cs.GetName(),
			CatalogSourceNamespace: cs.GetNamespace(),
			Package:                mainPackageName,
			Channel:                stableChannel,
			StartingCSV:            mainCSV.GetName(),
			InstallPlanApproval:    v1alpha1.ApprovalManual,
		}

		// Create Subscription
		subscriptionName := genName("sub-")
		createSubscriptionForCatalogWithSpec(GinkgoT(), crc, ns.GetName(), subscriptionName, subscriptionSpec)

		subscription, err := fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())

		installPlanName := subscription.Status.Install.Name
		requiresApprovalChecker := buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseRequiresApproval)
		fetchedInstallPlan, err := fetchInstallPlanWithNamespace(GinkgoT(), crc, installPlanName, ns.GetName(), requiresApprovalChecker)
		Expect(err).ShouldNot(HaveOccurred())

		fetchedInstallPlan.Spec.Approved = true
		_, err = crc.OperatorsV1alpha1().InstallPlans(ns.GetName()).Update(context.Background(), fetchedInstallPlan, metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		_, err = awaitCSV(crc, ns.GetName(), mainCSV.GetName(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

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
		updateInternalCatalog(GinkgoT(), c, crc, cs.GetName(), cs.GetNamespace(), []apiextensions.CustomResourceDefinition{mainCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, replacementCSV}, mainManifests)

		// Get updated catalogsource
		fetchedUpdatedCatalog, err := fetchCatalogSourceOnStatus(crc, cs.GetName(), cs.GetNamespace(), catalogSourceRegistryPodSynced)
		Expect(err).ShouldNot(HaveOccurred())

		subscription, err = fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionStateUpgradePendingChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())

		// Ensure the timing
		catalogConnState := fetchedUpdatedCatalog.Status.GRPCConnectionState
		subUpdatedTime := subscription.Status.LastUpdated
		Expect(subUpdatedTime.Time).Should(BeTemporally("<", catalogConnState.LastConnectTime.Add(60*time.Second)))
	})

	It("config map update triggers registry pod rollout", func() {

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		dependentCRD := newCRD(genName("ins-"))
		mainCSV := newCSV(mainPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, nil)
		dependentCSV := newCSV(dependentPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, nil)

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
		createInternalCatalogSource(c, crc, mainCatalogName, ns.GetName(), mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})

		// Attempt to get the catalog source before creating install plan
		fetchedInitialCatalog, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, ns.GetName(), catalogSourceRegistryPodSynced)
		Expect(err).ShouldNot(HaveOccurred())

		// Get initial configmap
		configMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(ns.GetName()).Get(context.Background(), fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Check pod created
		initialPods, err := c.KubernetesInterface().CoreV1().Pods(ns.GetName()).List(context.Background(), metav1.ListOptions{LabelSelector: "olm.configMapResourceVersion=" + configMap.ResourceVersion})
		Expect(err).ShouldNot(HaveOccurred())
		Expect(initialPods.Items).To(HaveLen(1))

		// Update catalog configmap
		updateInternalCatalog(GinkgoT(), c, crc, mainCatalogName, ns.GetName(), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, dependentCSV}, append(mainManifests, dependentManifests...))

		fetchedUpdatedCatalog, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, ns.GetName(), func(catalog *v1alpha1.CatalogSource) bool {
			before := fetchedInitialCatalog.Status.ConfigMapResource
			after := catalog.Status.ConfigMapResource
			if after != nil && before.LastUpdateTime.Before(&after.LastUpdateTime) &&
				after.ResourceVersion != before.ResourceVersion {
				ctx.Ctx().Logf("catalog updated")
				return true
			}
			ctx.Ctx().Logf("waiting for catalog pod to be available")
			return false
		})
		Expect(err).ShouldNot(HaveOccurred())

		var updatedConfigMap *corev1.ConfigMap
		Eventually(func() (types.UID, error) {
			var err error
			// Get updated configmap
			updatedConfigMap, err = c.KubernetesInterface().CoreV1().ConfigMaps(ns.GetName()).Get(context.Background(), fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
			if err != nil {
				return "", err
			}
			if len(updatedConfigMap.ObjectMeta.OwnerReferences) == 0 {
				return "", nil
			}
			return updatedConfigMap.ObjectMeta.OwnerReferences[0].UID, nil
		}).Should(Equal(fetchedUpdatedCatalog.ObjectMeta.UID))

		Expect(configMap.ResourceVersion).ShouldNot(Equal(updatedConfigMap.ResourceVersion))
		Expect(fetchedInitialCatalog.Status.ConfigMapResource.ResourceVersion).ShouldNot(Equal(fetchedUpdatedCatalog.Status.ConfigMapResource.ResourceVersion))
		Expect(fetchedUpdatedCatalog.Status.ConfigMapResource.ResourceVersion).Should(Equal(updatedConfigMap.GetResourceVersion()))

		// Await 1 CatalogSource registry pod matching the updated labels
		singlePod := podCount(1)
		selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": mainCatalogName, "olm.configMapResourceVersion": updatedConfigMap.GetResourceVersion()})
		podList, err := awaitPods(GinkgoT(), c, ns.GetName(), selector.String(), singlePod)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(podList.Items).To(HaveLen(1), "expected pod list not of length 1")

		// Await 1 CatalogSource registry pod matching the updated labels
		selector = labels.SelectorFromSet(map[string]string{"olm.catalogSource": mainCatalogName})
		podList, err = awaitPods(GinkgoT(), c, ns.GetName(), selector.String(), singlePod)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(podList.Items).To(HaveLen(1), "expected pod list not of length 1")

		// Create Subscription
		subscriptionName := genName("sub-")
		createSubscriptionForCatalog(crc, ns.GetName(), subscriptionName, fetchedUpdatedCatalog.GetName(), mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

		subscription, err := fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionStateAtLatestChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())
		_, err = fetchCSV(crc, subscription.Status.CurrentCSV, ns.GetName(), buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
		Expect(err).ShouldNot(HaveOccurred())

		ipList, err := crc.OperatorsV1alpha1().InstallPlans(ns.GetName()).List(context.Background(), metav1.ListOptions{})
		ipCount := 0
		for _, ip := range ipList.Items {
			if ownerutil.IsOwnedBy(&ip, subscription) {
				ipCount += 1
			}
		}
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("config map replace triggers registry pod rollout", func() {

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)

		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		dependentCRD := newCRD(genName("ins-"))
		mainCSV := newCSV(mainPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, nil)
		dependentCSV := newCSV(dependentPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, nil)

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
		_, cleanupSource := createInternalCatalogSource(c, crc, mainCatalogName, ns.GetName(), mainManifests, nil, []v1alpha1.ClusterServiceVersion{mainCSV})

		// Attempt to get the catalog source before creating install plan
		fetchedInitialCatalog, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, ns.GetName(), catalogSourceRegistryPodSynced)
		Expect(err).ShouldNot(HaveOccurred())
		// Get initial configmap
		configMap, err := c.KubernetesInterface().CoreV1().ConfigMaps(ns.GetName()).Get(context.Background(), fetchedInitialCatalog.Spec.ConfigMap, metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Check pod created
		initialPods, err := c.KubernetesInterface().CoreV1().Pods(ns.GetName()).List(context.Background(), metav1.ListOptions{LabelSelector: "olm.configMapResourceVersion=" + configMap.ResourceVersion})
		Expect(err).ShouldNot(HaveOccurred())
		Expect(initialPods.Items).To(HaveLen(1))

		// delete the first catalog
		cleanupSource()

		// create a catalog with the same name
		createInternalCatalogSource(c, crc, mainCatalogName, ns.GetName(), append(mainManifests, dependentManifests...), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, dependentCSV})

		// Create Subscription
		subscriptionName := genName("sub-")
		createSubscriptionForCatalog(crc, ns.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)

		subscription, err := fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionStateAtLatestChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		_, err = fetchCSV(crc, subscription.Status.CurrentCSV, ns.GetName(), buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded))
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("gRPC address catalog source", func() {

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

		mainPackageName := genName("nginx-")
		dependentPackageName := genName("nginxdep-")

		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		mainPackageReplacement := fmt.Sprintf("%s-replacement", mainPackageStable)
		dependentPackageStable := fmt.Sprintf("%s-stable", dependentPackageName)

		stableChannel := "stable"

		dependentCRD := newCRD(genName("ins-"))
		mainCSV := newCSV(mainPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, nil)
		replacementCSV := newCSV(mainPackageReplacement, ns.GetName(), mainPackageStable, semver.MustParse("0.2.0"), nil, []apiextensions.CustomResourceDefinition{dependentCRD}, nil)
		dependentCSV := newCSV(dependentPackageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{dependentCRD}, nil, nil)

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
		createInternalCatalogSource(c, crc, mainSourceName, ns.GetName(), append(mainManifests, dependentManifests...), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{mainCSV, dependentCSV})
		createInternalCatalogSource(c, crc, replacementSourceName, ns.GetName(), append(replacementManifests, dependentManifests...), []apiextensions.CustomResourceDefinition{dependentCRD}, []v1alpha1.ClusterServiceVersion{replacementCSV, mainCSV, dependentCSV})

		// Wait for ConfigMap CatalogSources to be ready
		mainSource, err := fetchCatalogSourceOnStatus(crc, mainSourceName, ns.GetName(), catalogSourceRegistryPodSynced)
		Expect(err).ShouldNot(HaveOccurred())
		replacementSource, err := fetchCatalogSourceOnStatus(crc, replacementSourceName, ns.GetName(), catalogSourceRegistryPodSynced)
		Expect(err).ShouldNot(HaveOccurred())

		// Replicate catalog pods with no OwnerReferences
		mainCopy := replicateCatalogPod(c, mainSource)
		mainCopy = awaitPod(GinkgoT(), c, mainCopy.GetNamespace(), mainCopy.GetName(), hasPodIP)
		replacementCopy := replicateCatalogPod(c, replacementSource)
		replacementCopy = awaitPod(GinkgoT(), c, replacementCopy.GetNamespace(), replacementCopy.GetName(), hasPodIP)

		addressSourceName := genName("address-catalog-")

		// Create a CatalogSource pointing to the grpc pod
		addressSource := &v1alpha1.CatalogSource{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.CatalogSourceKind,
				APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      addressSourceName,
				Namespace: ns.GetName(),
			},
			Spec: v1alpha1.CatalogSourceSpec{
				SourceType: v1alpha1.SourceTypeGrpc,
				Address:    net.JoinHostPort(mainCopy.Status.PodIP, "50051"),
			},
		}

		addressSource, err = crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Create(context.Background(), addressSource, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			err := crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Delete(context.Background(), addressSourceName, metav1.DeleteOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		}()

		// Wait for the CatalogSource to be ready
		_, err = fetchCatalogSourceOnStatus(crc, addressSource.GetName(), addressSource.GetNamespace(), catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

		// Delete CatalogSources
		err = crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Delete(context.Background(), mainSourceName, metav1.DeleteOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		err = crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Delete(context.Background(), replacementSourceName, metav1.DeleteOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Create Subscription
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalog(crc, ns.GetName(), subscriptionName, addressSourceName, mainPackageName, stableChannel, "", v1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		subscription, err := fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionStateAtLatestChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())
		_, err = fetchCSV(crc, subscription.Status.CurrentCSV, ns.GetName(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// Update the catalog's address to point at the other registry pod's cluster ip
		addressSource, err = crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Get(context.Background(), addressSourceName, metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		addressSource.Spec.Address = net.JoinHostPort(replacementCopy.Status.PodIP, "50051")
		_, err = crc.OperatorsV1alpha1().CatalogSources(ns.GetName()).Update(context.Background(), addressSource, metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for the replacement CSV to be installed
		_, err = awaitCSV(crc, ns.GetName(), replacementCSV.GetName(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
	})

	It("delete internal registry pod triggers recreation", func() {

		// Create internal CatalogSource containing csv in package
		// Wait for a registry pod to be created
		// Delete the registry pod
		// Wait for a new registry pod to be created

		// Create internal CatalogSource containing csv in package
		packageName := genName("nginx-")
		packageStable := fmt.Sprintf("%s-stable", packageName)
		stableChannel := "stable"
		sourceName := genName("catalog-")
		crd := newCRD(genName("ins-"))
		csv := newCSV(packageStable, ns.GetName(), "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{crd}, nil, nil)
		manifests := []registry.PackageManifest{
			{
				PackageName: packageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: packageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		_, cleanupSource := createInternalCatalogSource(c, crc, sourceName, ns.GetName(), manifests, []apiextensions.CustomResourceDefinition{crd}, []v1alpha1.ClusterServiceVersion{csv})
		defer cleanupSource()

		// Wait for a new registry pod to be created
		selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": sourceName})
		singlePod := podCount(1)
		registryPods, err := awaitPods(GinkgoT(), c, ns.GetName(), selector.String(), singlePod)
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")
		Expect(registryPods).ToNot(BeNil(), "nil registry pods")
		Expect(registryPods.Items).To(HaveLen(1), "unexpected number of registry pods found")

		// Store the UID for later comparison
		uid := registryPods.Items[0].GetUID()

		// Delete the registry pod
		Eventually(func() error {
			backgroundDeletion := metav1.DeletePropagationBackground
			return c.KubernetesInterface().CoreV1().Pods(ns.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{PropagationPolicy: &backgroundDeletion}, metav1.ListOptions{LabelSelector: selector.String()})
		}).Should(Succeed())

		// Wait for a new registry pod to be created
		notUID := func(pods *corev1.PodList) bool {
			uids := make([]string, 0)
			for _, pod := range pods.Items {
				uids = append(uids, string(pod.GetUID()))
				if pod.GetUID() == uid {
					ctx.Ctx().Logf("waiting for %v not to contain %s", uids, uid)
					return false
				}
			}
			ctx.Ctx().Logf("waiting for %v to not be empty and not contain %s", uids, uid)
			return len(pods.Items) > 0
		}
		registryPods, err = awaitPods(GinkgoT(), c, ns.GetName(), selector.String(), unionPodsCheck(singlePod, notUID))
		Expect(err).ShouldNot(HaveOccurred(), "error waiting for replacement registry pod")
		Expect(registryPods).ToNot(BeNil(), "nil replacement registry pods")
		Expect(registryPods.Items).To(HaveLen(1), "unexpected number of replacement registry pods found")
	})

	It("delete gRPC registry pod triggers recreation", func() {

		// Create gRPC CatalogSource using an external registry image (community-operators)
		// Wait for a registry pod to be created
		// Delete the registry pod
		// Wait for a new registry pod to be created

		// Create gRPC CatalogSource using an external registry image (community-operators)
		source := &v1alpha1.CatalogSource{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.CatalogSourceKind,
				APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("catalog-"),
				Namespace: ns.GetName(),
			},
			Spec: v1alpha1.CatalogSourceSpec{
				SourceType: v1alpha1.SourceTypeGrpc,
				Image:      communityOperatorsImage,
			},
		}

		source, err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())

		// Wait for a new registry pod to be created
		selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": source.GetName()})
		singlePod := podCount(1)
		registryPods, err := awaitPods(GinkgoT(), c, source.GetNamespace(), selector.String(), singlePod)
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")
		Expect(registryPods).ToNot(BeNil(), "nil registry pods")
		Expect(registryPods.Items).To(HaveLen(1), "unexpected number of registry pods found")

		// Store the UID for later comparison
		uid := registryPods.Items[0].GetUID()

		// Delete the registry pod
		Eventually(func() error {
			backgroundDeletion := metav1.DeletePropagationBackground
			return c.KubernetesInterface().CoreV1().Pods(ns.GetName()).DeleteCollection(context.Background(), metav1.DeleteOptions{PropagationPolicy: &backgroundDeletion}, metav1.ListOptions{LabelSelector: selector.String()})
		}).Should(Succeed())

		// Wait for a new registry pod to be created
		notUID := func(pods *corev1.PodList) bool {
			uids := make([]string, 0)
			for _, pod := range pods.Items {
				uids = append(uids, string(pod.GetUID()))
				if pod.GetUID() == uid {
					ctx.Ctx().Logf("waiting for %v not to contain %s", uids, uid)
					return false
				}
			}
			ctx.Ctx().Logf("waiting for %v to not be empty and not contain %s", uids, uid)
			return len(pods.Items) > 0
		}
		registryPods, err = awaitPods(GinkgoT(), c, ns.GetName(), selector.String(), unionPodsCheck(singlePod, notUID))
		Expect(err).ShouldNot(HaveOccurred(), "error waiting for replacement registry pod")
		Expect(registryPods).ShouldNot(BeNil(), "nil replacement registry pods")
		Expect(registryPods.Items).To(HaveLen(1), "unexpected number of replacement registry pods found")
	})

	It("image update", func() {
		if ok, err := inKind(c); ok && err == nil {
			Skip("This spec fails when run using KIND cluster. See https://github.com/operator-framework/operator-lifecycle-manager/issues/2420 for more details")
		} else if err != nil {
			Skip("Could not determine whether running in a kind cluster. Skipping.")
		}
		// Create an image based catalog source from public Quay image
		// Use a unique tag as identifier
		// See https://quay.io/repository/olmtest/catsrc-update-test?namespace=olmtest for registry
		// Push an updated version of the image with the same identifier
		// Confirm catalog source polling feature is working as expected: a newer version of the catalog source pod comes up
		// etcd operator updated from 0.9.0 to 0.9.2-clusterwide
		// Subscription should detect the latest version of the operator in the new catalog source and pull it

		// create internal registry for purposes of pushing/pulling IF running e2e test locally
		// registry is insecure and for purposes of this test only

		local, err := Local(c)
		Expect(err).NotTo(HaveOccurred(), "cannot determine if test running locally or on CI: %s", err)

		var registryURL string
		var registryAuth string
		if local {
			registryURL, err = createDockerRegistry(c, ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "error creating container registry: %s", err)
			defer deleteDockerRegistry(c, ns.GetName())

			// ensure registry pod is ready before attempting port-forwarding
			_ = awaitPod(GinkgoT(), c, ns.GetName(), registryName, podReady)

			err = registryPortForward(ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "port-forwarding local registry: %s", err)
		} else {
			registryURL = openshiftregistryFQDN
			registryAuth, err = openshiftRegistryAuth(c, ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "error getting openshift registry authentication: %s", err)
		}

		// testImage is the name of the image used throughout the test - the image overwritten by skopeo
		// the tag is generated randomly and appended to the end of the testImage
		testImage := fmt.Sprint("docker://", registryURL, "/catsrc-update", ":")
		tag := genName("x")

		// 1. copy old catalog image into test-specific tag in internal docker registry
		// create skopeo pod to actually do the work of copying (on openshift) or exec out to local skopeo
		if local {
			_, err := skopeoLocalCopy(testImage, tag, catsrcImage, "old")
			Expect(err).NotTo(HaveOccurred(), "error copying old registry file: %s", err)
		} else {
			skopeoArgs := skopeoCopyCmd(testImage, tag, catsrcImage, "old", registryAuth)
			err = createSkopeoPod(c, skopeoArgs, ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "error creating skopeo pod: %s", err)

			// wait for skopeo pod to exit successfully
			awaitPod(GinkgoT(), c, ns.GetName(), skopeo, func(pod *corev1.Pod) bool {
				return pod.Status.Phase == corev1.PodSucceeded
			})

			err = deleteSkopeoPod(c, ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "error deleting skopeo pod: %s", err)
		}

		// 2. setup catalog source

		sourceName := genName("catalog-")
		packageName := "busybox"
		channelName := "alpha"

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
				Namespace: ns.GetName(),
				Labels:    map[string]string{"olm.catalogSource": sourceName},
			},
			Spec: v1alpha1.CatalogSourceSpec{
				SourceType: v1alpha1.SourceTypeGrpc,
				Image:      image,
				UpdateStrategy: &v1alpha1.UpdateStrategy{
					RegistryPoll: &v1alpha1.RegistryPoll{
						// Using RawInterval rather than Interval due to this issue:
						// https://github.com/operator-framework/operator-lifecycle-manager/issues/2621
						RawInterval: "1m0s",
					},
				},
			},
		}

		source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Delete(context.Background(), source.GetName(), metav1.DeleteOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		}()

		// wait for new catalog source pod to be created
		// Wait for a new registry pod to be created
		selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": source.GetName()})
		singlePod := podCount(1)
		registryPods, err := awaitPods(GinkgoT(), c, source.GetNamespace(), selector.String(), singlePod)
		Expect(err).ToNot(HaveOccurred(), "error awaiting registry pod")
		Expect(registryPods).ShouldNot(BeNil(), "nil registry pods")
		Expect(registryPods.Items).To(HaveLen(1), "unexpected number of registry pods found")

		// Create a Subscription for package
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalog(crc, source.GetNamespace(), subscriptionName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		// Wait for the Subscription to succeed
		subscription, err := fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionStateAtLatestChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())

		// Wait for csv to succeed
		_, err = fetchCSV(crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		registryCheckFunc := func(podList *corev1.PodList) bool {
			if len(podList.Items) > 1 {
				return false
			}
			return podList.Items[0].Status.ContainerStatuses[0].ImageID != ""
		}
		// get old catalog source pod
		registryPod, err := awaitPods(GinkgoT(), c, source.GetNamespace(), selector.String(), registryCheckFunc)
		// 3. Update image on registry via skopeo: this should trigger a newly updated version of the catalog source pod
		// to be deployed after some time
		// Make another skopeo pod to do the work of copying the image
		if local {
			_, err := skopeoLocalCopy(testImage, tag, catsrcImage, "new")
			Expect(err).NotTo(HaveOccurred(), "error copying new registry file: %s", err)
		} else {
			skopeoArgs := skopeoCopyCmd(testImage, tag, catsrcImage, "new", registryAuth)
			err = createSkopeoPod(c, skopeoArgs, ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "error creating skopeo pod: %s", err)

			// wait for skopeo pod to exit successfully
			awaitPod(GinkgoT(), c, ns.GetName(), skopeo, func(pod *corev1.Pod) bool {
				return pod.Status.Phase == corev1.PodSucceeded
			})

			err = deleteSkopeoPod(c, ns.GetName())
			Expect(err).NotTo(HaveOccurred(), "error deleting skopeo pod: %s", err)
		}

		// update catalog source with annotation (to kick resync)
		source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(context.Background(), source.GetName(), metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")
		source.Annotations = make(map[string]string)
		source.Annotations["testKey"] = "testValue"
		_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(context.Background(), source, metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")

		time.Sleep(11 * time.Second)

		// ensure new registry pod container image is as we expect
		podCheckFunc := func(podList *corev1.PodList) bool {
			ctx.Ctx().Logf("pod list length %d\n", len(podList.Items))
			for _, pod := range podList.Items {
				ctx.Ctx().Logf("pod list name %v\n", pod.Name)
			}

			for _, pod := range podList.Items {
				ctx.Ctx().Logf("old image id %s\n new image id %s\n", registryPod.Items[0].Status.ContainerStatuses[0].ImageID,
					pod.Status.ContainerStatuses[0].ImageID)
				if pod.Status.ContainerStatuses[0].ImageID != registryPod.Items[0].Status.ContainerStatuses[0].ImageID {
					return true
				}
			}
			// update catalog source with annotation (to kick resync)
			source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(context.Background(), source.GetName(), metav1.GetOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "error getting catalog source pod")
			source.Annotations["testKey"] = genName("newValue")
			_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(context.Background(), source, metav1.UpdateOptions{})
			Expect(err).ShouldNot(HaveOccurred(), "error updating catalog source pod with test annotation")
			return false
		}
		// await new catalog source and ensure old one was deleted
		registryPods, err = awaitPodsWithInterval(GinkgoT(), c, source.GetNamespace(), selector.String(), 30*time.Second, 10*time.Minute, podCheckFunc)
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")
		Expect(registryPods).ShouldNot(BeNil(), "nil registry pods")
		Expect(registryPods.Items).To(HaveLen(1), "unexpected number of registry pods found")

		// update catalog source with annotation (to kick resync)
		source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(context.Background(), source.GetName(), metav1.GetOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")
		source.Annotations["testKey"] = "newValue"
		_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(context.Background(), source, metav1.UpdateOptions{})
		Expect(err).ShouldNot(HaveOccurred(), "error awaiting registry pod")

		subChecker := func(sub *v1alpha1.Subscription) bool {
			return sub.Status.InstalledCSV == "busybox.v2.0.0"
		}
		// Wait for the Subscription to succeed
		subscription, err = fetchSubscription(crc, ns.GetName(), subscriptionName, subChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())

		// Wait for csv to succeed
		csv, err := fetchCSV(crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())

		// check version of running csv to ensure the latest version (0.9.2) was installed onto the cluster
		v := csv.Spec.Version
		busyboxVersion := semver.Version{
			Major: 2,
			Minor: 0,
			Patch: 0,
		}

		Expect(v).Should(Equal(version.OperatorVersion{Version: busyboxVersion}), "latest version of operator not installed: catalog source update failed")
	})

	It("Dependency has correct replaces field", func() {
		// Create a CatalogSource that contains the busybox v1 and busybox-dependency v1 images
		// Create a Subscription for busybox v1, which has a dependency on busybox-dependency v1.
		// Wait for the busybox and busybox2 Subscriptions to succeed
		// Wait for the CSVs to succeed
		// Update the catalog to point to an image that contains the busybox v2 and busybox-dependency v2 images.
		// Wait for the new Subscriptions to succeed and check if they include the new CSVs
		// Wait for the CSVs to succeed and confirm that the have the correct Spec.Replaces fields.

		sourceName := genName("catalog-")
		packageName := "busybox"
		channelName := "alpha"

		catSrcImage := "quay.io/olmtest/busybox-dependencies-index"

		// Create gRPC CatalogSource
		source := &v1alpha1.CatalogSource{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.CatalogSourceKind,
				APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      sourceName,
				Namespace: ns.GetName(),
			},
			Spec: v1alpha1.CatalogSourceSpec{
				SourceType: v1alpha1.SourceTypeGrpc,
				Image:      catSrcImage + ":1.0.0-with-ListBundles-method",
			},
		}

		source, err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		defer func() {
			err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Delete(context.Background(), source.GetName(), metav1.DeleteOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		}()

		// Wait for the CatalogSource to be ready
		_, err = fetchCatalogSourceOnStatus(crc, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

		// Create a Subscription for busybox
		subscriptionName := genName("sub-")
		cleanupSubscription := createSubscriptionForCatalog(crc, source.GetNamespace(), subscriptionName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)
		defer cleanupSubscription()

		// Wait for the Subscription to succeed
		subscription, err := fetchSubscription(crc, ns.GetName(), subscriptionName, subscriptionStateAtLatestChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())
		Expect(subscription.Status.InstalledCSV).To(Equal("busybox.v1.0.0"))

		// Confirm that a subscription was created for busybox-dependency
		subscriptionList, err := crc.OperatorsV1alpha1().Subscriptions(source.GetNamespace()).List(context.Background(), metav1.ListOptions{})
		Expect(err).ShouldNot(HaveOccurred())
		dependencySubscriptionName := ""
		for _, sub := range subscriptionList.Items {
			if strings.HasPrefix(sub.GetName(), "busybox-dependency") {
				dependencySubscriptionName = sub.GetName()
			}
		}
		Expect(dependencySubscriptionName).ToNot(BeEmpty())

		// Wait for the Subscription to succeed
		subscription, err = fetchSubscription(crc, ns.GetName(), dependencySubscriptionName, subscriptionStateAtLatestChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())
		Expect(subscription.Status.InstalledCSV).To(Equal("busybox-dependency.v1.0.0"))

		// Update the catalog image
		Eventually(func() (bool, error) {
			existingSource, err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(context.Background(), sourceName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			existingSource.Spec.Image = catSrcImage + ":2.0.0-with-ListBundles-method"

			source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(context.Background(), existingSource, metav1.UpdateOptions{})
			if err == nil {
				return true, nil
			}
			return false, nil
		}).Should(BeTrue())

		// Wait for the CatalogSource to be ready
		_, err = fetchCatalogSourceOnStatus(crc, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

		// Wait for the busybox v2 Subscription to succeed
		subChecker := func(sub *v1alpha1.Subscription) bool {
			return sub.Status.InstalledCSV == "busybox.v2.0.0"
		}
		subscription, err = fetchSubscription(crc, ns.GetName(), subscriptionName, subChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())

		// Wait for busybox v2 csv to succeed and check the replaces field
		csv, err := fetchCSV(crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(csv.Spec.Replaces).To(Equal("busybox.v1.0.0"))

		// Wait for the busybox-dependency v2 Subscription to succeed
		subChecker = func(sub *v1alpha1.Subscription) bool {
			return sub.Status.InstalledCSV == "busybox-dependency.v2.0.0"
		}
		subscription, err = fetchSubscription(crc, ns.GetName(), dependencySubscriptionName, subChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(subscription).ShouldNot(BeNil())

		// Wait for busybox-dependency v2 csv to succeed and check the replaces field
		csv, err = fetchCSV(crc, subscription.Status.CurrentCSV, subscription.GetNamespace(), csvSucceededChecker)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(csv.Spec.Replaces).To(Equal("busybox-dependency.v1.0.0"))
	})
	It("registry polls on the correct interval", func() {
		// Create a catalog source with polling enabled
		// Confirm the following
		//   a) the new update pod is spun up roughly in line with the registry polling interval
		//   b) the update pod is removed quickly when the image is found to not have changed
		// This is more of a behavioral test that ensures the feature is working as designed.

		c := newKubeClient()
		crc := newCRClient()

		sourceName := genName("catalog-")
		source := &v1alpha1.CatalogSource{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.CatalogSourceKind,
				APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      sourceName,
				Namespace: ns.GetName(),
				Labels:    map[string]string{"olm.catalogSource": sourceName},
			},
			Spec: v1alpha1.CatalogSourceSpec{
				SourceType: v1alpha1.SourceTypeGrpc,
				Image:      "quay.io/olmtest/catsrc-update-test:new",
				UpdateStrategy: &v1alpha1.UpdateStrategy{
					RegistryPoll: &v1alpha1.RegistryPoll{
						RawInterval: "45s",
					},
				},
			},
		}

		source, err := crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		// wait for new catalog source pod to be created and report ready
		selector := labels.SelectorFromSet(map[string]string{"olm.catalogSource": source.GetName()})
		singlePod := podCount(1)
		catalogPods, err := awaitPods(GinkgoT(), c, source.GetNamespace(), selector.String(), singlePod)
		Expect(err).ToNot(HaveOccurred())
		Expect(catalogPods).ToNot(BeNil())

		Eventually(func() (bool, error) {
			podList, err := c.KubernetesInterface().CoreV1().Pods(source.GetNamespace()).List(context.Background(), metav1.ListOptions{LabelSelector: selector.String()})
			if err != nil {
				return false, err
			}

			for _, p := range podList.Items {
				if podReady(&p) {
					return true, nil
				}
				return false, nil
			}

			return false, nil
		}).Should(BeTrue())

		// Wait roughly the polling interval for update pod to show up
		updateSelector := labels.SelectorFromSet(map[string]string{"catalogsource.operators.coreos.com/update": source.GetName()})
		updatePods, err := awaitPodsWithInterval(GinkgoT(), c, source.GetNamespace(), updateSelector.String(), 5*time.Second, 2*time.Minute, singlePod)
		Expect(err).ToNot(HaveOccurred())
		Expect(updatePods).ToNot(BeNil())
		Expect(updatePods.Items).To(HaveLen(1))

		// No update to image: update pod should be deleted quickly
		noPod := podCount(0)
		updatePods, err = awaitPodsWithInterval(GinkgoT(), c, source.GetNamespace(), updateSelector.String(), 1*time.Second, 30*time.Second, noPod)
		Expect(err).ToNot(HaveOccurred())
		Expect(updatePods.Items).To(HaveLen(0))
	})

	It("adding catalog template adjusts image used", func() {
		// This test attempts to create a catalog source, and update it with a template annotation
		// and ensure that the image gets changed according to what's in the template as well as
		// check the status conditions are updated accordingly
		sourceName := genName("catalog-")
		source := &v1alpha1.CatalogSource{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.CatalogSourceKind,
				APIVersion: v1alpha1.CatalogSourceCRDAPIVersion,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      sourceName,
				Namespace: ns.GetName(),
				Labels:    map[string]string{"olm.catalogSource": sourceName},
			},
			Spec: v1alpha1.CatalogSourceSpec{
				SourceType: v1alpha1.SourceTypeGrpc,
				Image:      "quay.io/olmtest/catsrc-update-test:old",
			},
		}

		By("creating a catalog source")

		var err error
		Eventually(func() error {
			source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
			return err
		}).Should(Succeed())

		By("updating the catalog source with template annotation")

		Eventually(func() error {
			source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(context.Background(), source.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			// create an annotation using the kube templates
			source.SetAnnotations(map[string]string{
				catalogsource.CatalogImageTemplateAnnotation: fmt.Sprintf("quay.io/olmtest/catsrc-update-test:%s.%s.%s", catalogsource.TemplKubeMajorV, catalogsource.TemplKubeMinorV, catalogsource.TemplKubePatchV),
			})

			// Update the catalog image
			_, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Update(context.Background(), source, metav1.UpdateOptions{})
			return err
		}).Should(Succeed())

		// wait for status condition to show up
		Eventually(func() (bool, error) {
			source, err = crc.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Get(context.Background(), sourceName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			// if the conditions array has the entry we know things got updated
			condition := meta.FindStatusCondition(source.Status.Conditions, catalogtemplate.StatusTypeTemplatesHaveResolved)
			if condition != nil {
				return true, nil
			}

			return false, nil
		}).Should(BeTrue())

		// source should be the latest we got from the eventually block
		Expect(source.Status.Conditions).ToNot(BeNil())

		templatesResolvedCondition := meta.FindStatusCondition(source.Status.Conditions, catalogtemplate.StatusTypeTemplatesHaveResolved)
		if Expect(templatesResolvedCondition).ToNot(BeNil()) {
			Expect(templatesResolvedCondition.Reason).To(BeIdenticalTo(catalogtemplate.ReasonAllTemplatesResolved))
			Expect(templatesResolvedCondition.Status).To(BeIdenticalTo(metav1.ConditionTrue))
		}
		resolvedImageCondition := meta.FindStatusCondition(source.Status.Conditions, catalogtemplate.StatusTypeResolvedImage)
		if Expect(resolvedImageCondition).ToNot(BeNil()) {
			Expect(resolvedImageCondition.Reason).To(BeIdenticalTo(catalogtemplate.ReasonAllTemplatesResolved))
			Expect(resolvedImageCondition.Status).To(BeIdenticalTo(metav1.ConditionTrue))

			// if we can, try to determine the server version so we can check the resulting image
			if serverVersion, err := crc.Discovery().ServerVersion(); err != nil {
				if serverGitVersion, err := semver.Parse(serverVersion.GitVersion); err != nil {
					expectedImage := fmt.Sprintf("quay.io/olmtest/catsrc-update-test:%s.%s.%s", serverVersion.Major, serverVersion.Minor, strconv.FormatUint(serverGitVersion.Patch, 10))
					Expect(resolvedImageCondition.Message).To(BeIdenticalTo(expectedImage))
				}
			}
		}
	})
})

const (
	openshiftregistryFQDN = "image-registry.openshift-image-registry.svc:5000/openshift-operators"
	catsrcImage           = "docker://quay.io/olmtest/catsrc-update-test:"
)

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
	Eventually(waitForScaleup, 5*time.Minute, 1*time.Second).Should(BeTrue())

	// scale up
	replicas = 1
	deployment.Spec.Replicas = &replicas
	deployment, updated, err = c.UpdateDeployment(deployment)
	if err != nil || updated == false || deployment == nil {
		return fmt.Errorf("Failed to scale up deployment")
	}

	// wait for deployment to scale up
	Eventually(waitForScaleup, 5*time.Minute, 1*time.Second).Should(BeTrue())

	return err
}

func replicateCatalogPod(c operatorclient.ClientInterface, catalog *v1alpha1.CatalogSource) *corev1.Pod {
	initialPods, err := c.KubernetesInterface().CoreV1().Pods(catalog.GetNamespace()).List(context.Background(), metav1.ListOptions{LabelSelector: "olm.catalogSource=" + catalog.GetName()})
	Expect(err).ToNot(HaveOccurred())
	Expect(initialPods.Items).To(HaveLen(1))

	pod := initialPods.Items[0]
	copied := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: catalog.GetNamespace(),
			Name:      catalog.GetName() + "-copy",
		},
		Spec: pod.Spec,
	}

	copied, err = c.KubernetesInterface().CoreV1().Pods(catalog.GetNamespace()).Create(context.Background(), copied, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())

	return copied
}
