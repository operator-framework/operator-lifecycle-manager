package e2e

import (
	"context"
	"fmt"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	"time"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	corev1 "k8s.io/api/core/v1"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("CRD Versions", func() {
	var (
		generatedNamespace corev1.Namespace
		c                  operatorclient.ClientInterface
		crc                versioned.Interface
	)

	BeforeEach(func() {
		c = ctx.Ctx().KubeClient()
		crc = ctx.Ctx().OperatorClient()
		namespace := genName("crd-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", namespace),
				Namespace: namespace,
				Annotations: map[string]string{
					// Reduce the bundle unpack timeout to ensure error states are reached quickly
					bundle.BundleUnpackTimeoutAnnotationKey: "5s",
				},
			},
		}
		generatedNamespace = SetupGeneratedTestNamespaceWithOperatorGroup(namespace, og)
	})

	AfterEach(func() {
		TeardownNamespace(generatedNamespace.GetName())
	})

	// issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2640
	It("[FLAKE] creates v1 CRDs with a v1 schema successfully", func() {
		By("v1 crds with a valid openapiv3 schema should be created successfully by OLM")

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"

		crdPlural := genName("ins-")
		crdName := crdPlural + ".cluster.com"
		v1crd := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Scope: apiextensionsv1.NamespaceScoped,
				Group: "cluster.com",
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
			},
		}

		mainCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), nil, nil, nil)
		mainCatalogName := genName("mock-ocs-main-update2-")
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By("Create the catalog sources")
		_, cleanupMainCatalogSource := createV1CRDInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{v1crd}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()
		defer func() {
			_ = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.TODO(), mainCSV.GetName(), metav1.DeleteOptions{})
			_ = c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), v1crd.GetName(), metav1.DeleteOptions{})
		}()

		By("Attempt to get the catalog source before creating install plan")

		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(Equal(nil))
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(mainCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		By("Wait for InstallPlan to be status: Complete before checking resource presence")
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))
	})
	It("allows a CRD upgrade that doesn't cause data loss", func() {
		By(`Create a CRD on cluster with v1alpha1 (storage)`)
		By(`Update that CRD with v1alpha2 (storage), v1alpha1 (served)`)
		By(`Now the CRD should have two versions in status.storedVersions`)
		By(`Now make a catalog with a CRD with just v1alpha2 (storage)`)
		By(`That should fail because v1alpha1 is still in status.storedVersions - risk of data loss`)
		By(`Update the CRD status to remove the v1alpha1`)
		By(`Now the installplan should succeed`)

		By("manually editing the storage versions in the existing CRD status")

		crdPlural := genName("ins-v1-")
		crdName := crdPlural + ".cluster.com"
		crdGroup := "cluster.com"

		oldCRD := &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: crdGroup,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensionsv1.NamespaceScoped,
			},
		}
		_, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Create(context.TODO(), oldCRD, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "error creating old CRD")

		By("wrap CRD update in a poll because of the object has been modified related errors")
		Eventually(func() error {
			oldCRD, err = c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), oldCRD.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			GinkgoT().Logf("old crd status stored versions: %#v", oldCRD.Status.StoredVersions)

			By("set v1alpha1 to no longer stored")
			oldCRD.Spec.Versions[0].Storage = false
			By("update CRD on-cluster with a new version")
			oldCRD.Spec.Versions = append(oldCRD.Spec.Versions, apiextensionsv1.CustomResourceDefinitionVersion{
				Name:    "v1alpha2",
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
					},
				},
			})

			updatedCRD, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Update(context.TODO(), oldCRD, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
			GinkgoT().Logf("updated crd status stored versions: %#v", updatedCRD.Status.StoredVersions) // both v1alpha1 and v1alpha2 should be in the status
			return nil
		}).Should(BeNil())

		By("create CSV and catalog with just the catalog CRD")
		catalogCRD := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: crdGroup,
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha2",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensionsv1.NamespaceScoped,
			},
		}

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"
		catalogCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{catalogCRD}, nil, nil)
		defer func() {
			_ = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.TODO(), catalogCSV.GetName(), metav1.DeleteOptions{})
			_ = c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), catalogCRD.GetName(), metav1.DeleteOptions{})
		}()

		mainCatalogName := genName("mock-ocs-main-update2-")
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By("Create the catalog sources")
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{catalogCRD}, []operatorsv1alpha1.ClusterServiceVersion{catalogCSV})
		defer cleanupMainCatalogSource()

		By("Attempt to get the catalog source before creating install plan")
		_, err = fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		_ = createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(catalogCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		By("Check the error on the installplan - should be related to data loss and the CRD upgrade missing a stored version (v1alpha1)")
		Eventually(
			func() (*operatorsv1alpha1.InstallPlan, error) {
				return crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
			},
			90*time.Second, // exhaust retries
		).Should(WithTransform(
			func(v *operatorsv1alpha1.InstallPlan) operatorsv1alpha1.InstallPlanPhase { return v.Status.Phase },
			Equal(operatorsv1alpha1.InstallPlanPhaseFailed),
		))

		By("update CRD status to remove the v1alpha1 stored version")
		newCRD, err := c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), oldCRD.GetName(), metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "error getting new CRD")
		newCRD.Status.StoredVersions = []string{"v1alpha2"}
		newCRD, err = c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().UpdateStatus(context.TODO(), newCRD, metav1.UpdateOptions{})
		Expect(err).ToNot(HaveOccurred(), "error updating new CRD")
		GinkgoT().Logf("new crd status stored versions: %#v", newCRD.Status.StoredVersions) // only v1alpha2 should be in the status now

		By("install should now succeed")
		oldInstallPlanRef := subscription.Status.InstallPlanRef.Name
		err = crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Delete(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "error deleting failed install plan")
		By("remove old subscription")
		err = crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Delete(context.TODO(), subscription.GetName(), metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "error deleting old subscription")
		By("remove old csv")
		err = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.TODO(), mainPackageStable, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "error deleting old subscription")

		By("recreate subscription")
		subscriptionNameNew := genName("sub-nginx-update2-new-")
		_ = createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionNameNew, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

		subscription, err = fetchSubscription(crc, generatedNamespace.GetName(), subscriptionNameNew, subscriptionHasInstallPlanChecker())
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(catalogCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		By("eventually the subscription should create a new install plan")
		Eventually(func() bool {
			sub, _ := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
			GinkgoT().Logf("waiting for subscription %s to generate a new install plan...", subscription.GetName())
			return sub.Status.InstallPlanRef.Name != oldInstallPlanRef
		}, 5*time.Minute, 10*time.Second).Should(BeTrue())

		By("eventually the new installplan should succeed")
		Eventually(func() bool {
			sub, _ := crc.OperatorsV1alpha1().Subscriptions(generatedNamespace.GetName()).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
			ip, err := crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.TODO(), sub.Status.InstallPlanRef.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false
			}
			GinkgoT().Logf("waiting for installplan to succeed...currently %s", ip.Status.Phase)
			return ip.Status.Phase == operatorsv1alpha1.InstallPlanPhaseComplete
		}).Should(BeTrue())
		GinkgoT().Log("manually reconciled potentially unsafe CRD upgrade")
	})

	It("blocks a CRD upgrade that could cause data loss", func() {
		By("checking the storage versions in the existing CRD status and the spec of the new CRD")

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"

		crdPlural := genName("ins-")
		crdName := crdPlural + ".cluster.com"
		oldCRD := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha2",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
					{
						Name:    "v2alpha1",
						Served:  true,
						Storage: false,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensionsv1.NamespaceScoped,
			},
		}

		alphaChannel := "alpha"
		mainPackageAlpha := fmt.Sprintf("%s-alpha", mainPackageName)
		newCRD := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha3",
						Served:  true,
						Storage: true,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
					{
						Name:    "v2alpha2",
						Served:  true,
						Storage: false,
						Schema: &apiextensionsv1.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
				},
				Names: apiextensionsv1.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: apiextensionsv1.NamespaceScoped,
			},
		}

		oldCSV := newCSV(mainPackageStable, generatedNamespace.GetName(), "", semver.MustParse("0.1.0"), []apiextensionsv1.CustomResourceDefinition{oldCRD}, nil, nil)
		newCSV := newCSV(mainPackageAlpha, generatedNamespace.GetName(), mainPackageStable, semver.MustParse("0.1.1"), []apiextensionsv1.CustomResourceDefinition{newCRD}, nil, nil)
		mainCatalogName := genName("mock-ocs-main-update2-")
		mainManifests := []registry.PackageManifest{
			{
				PackageName: mainPackageName,
				Channels: []registry.PackageChannel{
					{Name: stableChannel, CurrentCSVName: mainPackageStable},
					{Name: alphaChannel, CurrentCSVName: mainPackageAlpha},
				},
				DefaultChannelName: stableChannel,
			},
		}

		By("Create the catalog sources")
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, generatedNamespace.GetName(), mainManifests, []apiextensionsv1.CustomResourceDefinition{oldCRD, newCRD}, []operatorsv1alpha1.ClusterServiceVersion{oldCSV, newCSV})
		defer cleanupMainCatalogSource()
		defer func() {
			_ = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.TODO(), oldCSV.GetName(), metav1.DeleteOptions{})
			_ = crc.OperatorsV1alpha1().ClusterServiceVersions(generatedNamespace.GetName()).Delete(context.TODO(), newCSV.GetName(), metav1.DeleteOptions{})
			_ = c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), oldCRD.GetName(), metav1.DeleteOptions{})
			_ = c.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), newCRD.GetName(), metav1.DeleteOptions{})
		}()

		By("Attempt to get the catalog source before creating install plan")
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, generatedNamespace.GetName(), catalogSourceRegistryPodSynced())
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, generatedNamespace.GetName(), subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionHasInstallPlanChecker())
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(oldCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		By("Wait for InstallPlan to be status: Complete before checking resource presence")
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, generatedNamespace.GetName(), buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))

		By("old CRD has been installed onto the cluster - now upgrade the subscription to point to the channel with the new CRD")
		By("installing the new CSV should fail with a warning about data loss, since a storage version is missing in the new CRD")
		By("use server-side apply to apply the update to the subscription point to the alpha channel")
		Eventually(Apply(subscription, func(s *operatorsv1alpha1.Subscription) error {
			s.Spec.Channel = alphaChannel
			return nil
		})).Should(Succeed())
		ctx.Ctx().Logf("updated subscription to point to alpha channel")

		checker := subscriptionStateAtLatestChecker()
		subscriptionAtLatestWithDifferentInstallPlan := func(v *operatorsv1alpha1.Subscription) bool {
			return checker(v) && v.Status.InstallPlanRef != nil && v.Status.InstallPlanRef.Name != fetchedInstallPlan.Name
		}

		By("fetch new subscription")
		s, err := fetchSubscription(crc, generatedNamespace.GetName(), subscriptionName, subscriptionAtLatestWithDifferentInstallPlan)
		Expect(err).ToNot(HaveOccurred())
		Expect(s).ToNot(BeNil())
		Expect(s.Status.InstallPlanRef).ToNot(Equal(nil))

		By("Check the error on the installplan - should be related to data loss and the CRD upgrade missing a stored version")
		Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
			return crc.OperatorsV1alpha1().InstallPlans(generatedNamespace.GetName()).Get(context.TODO(), s.Status.InstallPlanRef.Name, metav1.GetOptions{})
			// the install plan retry time out is 60 seconds, so we should expect the install plan to be failed within 2 minutes
		}).Within(2 * time.Minute).Should(And(
			WithTransform(
				func(v *operatorsv1alpha1.InstallPlan) operatorsv1alpha1.InstallPlanPhase {
					return v.Status.Phase
				},
				Equal(operatorsv1alpha1.InstallPlanPhaseFailed),
			),
			WithTransform(
				func(v *operatorsv1alpha1.InstallPlan) string {
					return v.Status.Conditions[len(v.Status.Conditions)-1].Message
				},
				ContainSubstring("risk of data loss"),
			),
		))
	})
})
