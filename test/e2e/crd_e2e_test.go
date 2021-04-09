package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("CRD Versions", func() {
	AfterEach(func() { TearDown(testNamespace) }, float64(30))

	It("creates v1beta1 crds with a v1beta1 schema successfully", func() {
		By("This test proves that OLM is able to handle v1beta1 CRDs successfully. Creating v1 CRDs has more " +
			"restrictions around the schema. v1beta1 validation schemas are not necessarily valid in v1. " +
			"OLM should support both v1beta1 and v1 CRDs")
		c := newKubeClient()
		crc := newCRClient()

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"

		crdPlural := genName("ins-v1beta1-")
		crdName := crdPlural + ".cluster.com"
		mainCRD := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
				// this validation is not a valid v1 structural schema because the "type: object" field is missing
				Validation: &apiextensions.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
						Description: "my crd schema",
					},
				},
			},
		}

		mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{mainCRD}, nil, nil)
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

		// Create the catalog sources
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{mainCRD}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(Equal(nil))
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(mainCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))
	})

	It("creates v1 CRDs with a v1 schema successfully", func() {
		By("v1 crds with a valid openapiv3 schema should be created successfully by OLM")
		c := newKubeClient()
		crc := newCRClient()

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"

		crdPlural := genName("ins-v1beta1-")
		crdName := crdPlural + ".cluster.com"
		v1crd := apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
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

		mainCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), nil, nil, nil)
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

		// Create the catalog sources
		_, cleanupMainCatalogSource := createV1CRDInternalCatalogSource(GinkgoT(), c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensionsv1.CustomResourceDefinition{v1crd}, []operatorsv1alpha1.ClusterServiceVersion{mainCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(Equal(nil))
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(mainCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))
	})

	It("blocks a CRD upgrade that could cause data loss", func() {
		By("checking the storage versions in the existing CRD status and the spec of the new CRD")

		c := newKubeClient()
		crc := newCRClient()

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"

		crdPlural := genName("ins-v1beta1-")
		crdName := crdPlural + ".cluster.com"
		oldCRD := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha2",
						Served:  true,
						Storage: true,
						Schema: &apiextensions.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
					{
						Name:    "v2alpha1",
						Served:  true,
						Storage: false,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		}

		alphaChannel := "alpha"
		mainPackageAlpha := fmt.Sprintf("%s-alpha", mainPackageName)
		newCRD := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: "cluster.com",
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha3",
						Served:  true,
						Storage: true,
						Schema: &apiextensions.CustomResourceValidation{
							OpenAPIV3Schema: &apiextensions.JSONSchemaProps{
								Type:        "object",
								Description: "my crd schema",
							},
						},
					},
					{
						Name:    "v2alpha2",
						Served:  true,
						Storage: false,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
			},
		}

		oldCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{oldCRD}, nil, nil)
		newCSV := newCSV(mainPackageAlpha, testNamespace, mainPackageStable, semver.MustParse("0.1.1"), []apiextensions.CustomResourceDefinition{newCRD}, nil, nil)
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

		// Create the catalog sources
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{oldCRD, newCRD}, []operatorsv1alpha1.ClusterServiceVersion{oldCSV, newCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err := fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		subscriptionCleanup := createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
		defer subscriptionCleanup()

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(oldCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		installPlanName := subscription.Status.InstallPlanRef.Name

		// Wait for InstallPlan to be status: Complete before checking resource presence
		fetchedInstallPlan, err := fetchInstallPlan(GinkgoT(), crc, installPlanName, buildInstallPlanPhaseCheckFunc(operatorsv1alpha1.InstallPlanPhaseComplete))
		Expect(err).ToNot(HaveOccurred())
		GinkgoT().Logf("Install plan %s fetched with status %s", fetchedInstallPlan.GetName(), fetchedInstallPlan.Status.Phase)
		Expect(fetchedInstallPlan.Status.Phase).To(Equal(operatorsv1alpha1.InstallPlanPhaseComplete))

		// old CRD has been installed onto the cluster - now upgrade the subscription to point to the channel with the new CRD
		// installing the new CSV should fail with a warning about data loss, since a storage version is missing in the new CRD
		// use server-side apply to apply the update to the subscription point to the alpha channel
		Eventually(Apply(subscription, func(s *operatorsv1alpha1.Subscription) error {
			s.Spec.Channel = alphaChannel
			return nil
		})).Should(Succeed())
		ctx.Ctx().Logf("updated subscription to point to alpha channel")

		subscriptionAtLatestWithDifferentInstallPlan := func(v *operatorsv1alpha1.Subscription) bool {
			return subscriptionStateAtLatestChecker(v) && v.Status.InstallPlanRef != nil && v.Status.InstallPlanRef.Name != fetchedInstallPlan.Name
		}

		// fetch new subscription
		s, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionAtLatestWithDifferentInstallPlan)
		Expect(err).ToNot(HaveOccurred())
		Expect(s).ToNot(BeNil())
		Expect(s.Status.InstallPlanRef).ToNot(Equal(nil))

		// Check the error on the installplan - should be related to data loss and the CRD upgrade missing a stored version
		Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
			return crc.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), s.Status.InstallPlanRef.Name, metav1.GetOptions{})
		}).Should(And(
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

	// Create a CRD on cluster with v1alpha1 (storage)
	// Update that CRD with v1alpha2 (storage), v1alpha1 (served)
	// Now the CRD should have two versions in status.storedVersions
	// Now make a catalog with a CRD with just v1alpha2 (storage)
	// That should fail because v1alpha1 is still in status.storedVersions - risk of data loss
	// Update the CRD status to remove the v1alpha1
	// Now the installplan should succeed

	It("allows a CRD upgrade that doesn't cause data loss", func() {
		By("manually editing the storage versions in the existing CRD status")

		c := newKubeClient()
		crc := newCRClient()

		crdPlural := genName("ins-v1-")
		crdName := crdPlural + ".cluster.com"
		crdGroup := "cluster.com"

		oldCRD := &apiextensionsv1beta1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
				Group: crdGroup,
				Versions: []apiextensionsv1beta1.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha1",
						Served:  true,
						Storage: true,
					},
				},
				Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		}
		_, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(context.TODO(), oldCRD, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "error creating old CRD")

		// wrap CRD update in a poll because of the object has been modified related errors
		Eventually(func() error {
			oldCRD, err = c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), oldCRD.GetName(), metav1.GetOptions{})
			if err != nil {
				return err
			}
			GinkgoT().Logf("old crd status stored versions: %#v", oldCRD.Status.StoredVersions)

			// set v1alpha1 to no longer served
			oldCRD.Spec.Versions[0].Storage = false
			// update CRD on-cluster with a new version
			oldCRD.Spec.Versions = append(oldCRD.Spec.Versions, apiextensionsv1beta1.CustomResourceDefinitionVersion{
				Name:    "v1alpha2",
				Served:  true,
				Storage: true,
			})

			updatedCRD, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Update(context.TODO(), oldCRD, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
			GinkgoT().Logf("updated crd status stored versions: %#v", updatedCRD.Status.StoredVersions) // both v1alpha1 and v1alpha2 should be in the status
			return nil
		}).Should(BeNil())

		// create CSV and catalog with just the catalog CRD
		catalogCRD := apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{
				Name: crdName,
			},
			Spec: apiextensions.CustomResourceDefinitionSpec{
				Group: crdGroup,
				Versions: []apiextensions.CustomResourceDefinitionVersion{
					{
						Name:    "v1alpha2",
						Served:  true,
						Storage: true,
					},
				},
				Names: apiextensions.CustomResourceDefinitionNames{
					Plural:   crdPlural,
					Singular: crdPlural,
					Kind:     crdPlural,
					ListKind: "list" + crdPlural,
				},
				Scope: "Namespaced",
			},
		}

		mainPackageName := genName("nginx-update2-")
		mainPackageStable := fmt.Sprintf("%s-stable", mainPackageName)
		stableChannel := "stable"
		catalogCSV := newCSV(mainPackageStable, testNamespace, "", semver.MustParse("0.1.0"), []apiextensions.CustomResourceDefinition{catalogCRD}, nil, nil)

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

		// Create the catalog sources
		_, cleanupMainCatalogSource := createInternalCatalogSource(c, crc, mainCatalogName, testNamespace, mainManifests, []apiextensions.CustomResourceDefinition{catalogCRD}, []operatorsv1alpha1.ClusterServiceVersion{catalogCSV})
		defer cleanupMainCatalogSource()

		// Attempt to get the catalog source before creating install plan
		_, err = fetchCatalogSourceOnStatus(crc, mainCatalogName, testNamespace, catalogSourceRegistryPodSynced)
		Expect(err).ToNot(HaveOccurred())

		subscriptionName := genName("sub-nginx-update2-")
		_ = createSubscriptionForCatalog(crc, testNamespace, subscriptionName, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

		subscription, err := fetchSubscription(crc, testNamespace, subscriptionName, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(catalogCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		// Check the error on the installplan - should be related to data loss and the CRD upgrade missing a stored version (v1alpha1)
		Eventually(
			func() (*operatorsv1alpha1.InstallPlan, error) {
				return crc.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.GetOptions{})
			},
			90*time.Second, // exhaust retries
		).Should(WithTransform(
			func(v *operatorsv1alpha1.InstallPlan) operatorsv1alpha1.InstallPlanPhase { return v.Status.Phase },
			Equal(operatorsv1alpha1.InstallPlanPhaseFailed),
		))

		// update CRD status to remove the v1alpha1 stored version
		newCRD, err := c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().Get(context.TODO(), oldCRD.GetName(), metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "error getting new CRD")
		newCRD.Status.StoredVersions = []string{"v1alpha2"}
		newCRD, err = c.ApiextensionsInterface().ApiextensionsV1beta1().CustomResourceDefinitions().UpdateStatus(context.TODO(), newCRD, metav1.UpdateOptions{})
		Expect(err).ToNot(HaveOccurred(), "error updating new CRD")
		GinkgoT().Logf("new crd status stored versions: %#v", newCRD.Status.StoredVersions) // only v1alpha2 should be in the status now

		// install should now succeed
		oldInstallPlanRef := subscription.Status.InstallPlanRef.Name
		err = crc.OperatorsV1alpha1().InstallPlans(testNamespace).Delete(context.TODO(), subscription.Status.InstallPlanRef.Name, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "error deleting failed install plan")
		// remove old subscription
		err = crc.OperatorsV1alpha1().Subscriptions(testNamespace).Delete(context.TODO(), subscription.GetName(), metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "error deleting old subscription")
		// remove old csv
		crc.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), mainPackageStable, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "error deleting old subscription")

		// recreate subscription
		subscriptionNameNew := genName("sub-nginx-update2-new-")
		_ = createSubscriptionForCatalog(crc, testNamespace, subscriptionNameNew, mainCatalogName, mainPackageName, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)

		subscription, err = fetchSubscription(crc, testNamespace, subscriptionNameNew, subscriptionHasInstallPlanChecker)
		Expect(err).ToNot(HaveOccurred())
		Expect(subscription).ToNot(BeNil())
		Expect(subscription.Status.InstallPlanRef).ToNot(Equal(nil))
		Expect(catalogCSV.GetName()).To(Equal(subscription.Status.CurrentCSV))

		// eventually the subscription should create a new install plan
		Eventually(func() bool {
			sub, _ := crc.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
			GinkgoT().Logf("waiting for subscription %s to generate a new install plan...", subscription.GetName())
			return sub.Status.InstallPlanRef.Name != oldInstallPlanRef
		}, 5*time.Minute, 10*time.Second).Should(BeTrue())

		// eventually the new installplan should succeed
		Eventually(func() bool {
			sub, _ := crc.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), subscription.GetName(), metav1.GetOptions{})
			ip, err := crc.OperatorsV1alpha1().InstallPlans(testNamespace).Get(context.TODO(), sub.Status.InstallPlanRef.Name, metav1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				return false
			}
			GinkgoT().Logf("waiting for installplan to succeed...currently %s", ip.Status.Phase)
			return ip.Status.Phase == operatorsv1alpha1.InstallPlanPhaseComplete
		}).Should(BeTrue())
		GinkgoT().Log("manually reconciled potentially unsafe CRD upgrade")
	})
})
