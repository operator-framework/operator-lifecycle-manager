package e2e

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/operator-framework/operator-lifecycle-manager/test/e2e/dsl"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var _ = Describe("Garbage collection for dependent resources", func() {
	var (
		kubeClient     operatorclient.ClientInterface
		operatorClient versioned.Interface
		ns             corev1.Namespace
	)

	BeforeEach(func() {
		kubeClient = ctx.Ctx().KubeClient()
		operatorClient = ctx.Ctx().OperatorClient()

		namespaceName := genName("gc-e2e-")
		ns = SetupGeneratedTestNamespace(namespaceName, namespaceName)
	})

	AfterEach(func() {
		TeardownNamespace(ns.GetName())
	})

	Context("Given a ClusterRole owned by a CustomResourceDefinition", func() {
		var (
			crd *apiextensionsv1.CustomResourceDefinition
			cr  *rbacv1.ClusterRole
		)

		BeforeEach(func() {
			group := fmt.Sprintf("%s.com", rand.String(16))

			crd = &apiextensionsv1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("plural.%s", group),
				},
				Spec: apiextensionsv1.CustomResourceDefinitionSpec{
					Group: group,
					Scope: apiextensionsv1.ClusterScoped,
					Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
						{
							Name:    "v1",
							Served:  true,
							Storage: true,
							Schema: &apiextensionsv1.CustomResourceValidation{
								OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{Type: "object"},
							},
						},
					},
					Names: apiextensionsv1.CustomResourceDefinitionNames{
						Plural:   "plural",
						Singular: "singular",
						Kind:     "Kind",
						ListKind: "KindList",
					},
				},
			}

			// Create a CustomResourceDefinition
			var err error
			Eventually(func() error {
				crd, err = kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Create(context.Background(), crd, metav1.CreateOptions{})
				return err
			}).Should(Succeed())

			cr = &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName:    "clusterrole-",
					OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(crd)},
				},
			}

			// Create a ClusterRole for the crd
			Eventually(func() error {
				cr, err = kubeClient.CreateClusterRole(cr)
				return err
			}).Should(Succeed())
		})

		AfterEach(func() {

			// Clean up cluster role
			IgnoreError(kubeClient.DeleteClusterRole(cr.GetName(), &metav1.DeleteOptions{}))

			// Clean up CRD
			IgnoreError(kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{}))
		})

		When("CustomResourceDefinition is deleted", func() {

			BeforeEach(func() {
				// Delete CRD
				Eventually(func() bool {
					err := kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.GetName(), metav1.DeleteOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("should delete the associated ClusterRole", func() {
				Eventually(func() bool {
					_, err := kubeClient.GetClusterRole(cr.GetName())
					return apierrors.IsNotFound(err)
				}).Should(BeTrue(), "get cluster role should eventually return \"not found\"")
			})

		})
	})

	Context("Given a ClusterRole owned by a APIService", func() {
		var (
			apiService *apiregistrationv1.APIService
			cr         *rbacv1.ClusterRole
		)

		BeforeEach(func() {
			group := rand.String(16)

			apiService = &apiregistrationv1.APIService{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("v1.%s", group),
				},
				Spec: apiregistrationv1.APIServiceSpec{
					Group:                group,
					Version:              "v1",
					GroupPriorityMinimum: 1,
					VersionPriority:      1,
				},
			}
			// Create an API Service
			var err error
			Eventually(func() error {
				apiService, err = kubeClient.CreateAPIService(apiService)
				return err
			}).Should(Succeed())

			cr = &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName:    "clusterrole-",
					OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(apiService)},
				},
			}

			Eventually(func() error {
				// Create a ClusterRole
				cr, err = kubeClient.CreateClusterRole(cr)
				return err
			}).Should(Succeed())
		})

		AfterEach(func() {

			IgnoreError(kubeClient.DeleteClusterRole(cr.GetName(), &metav1.DeleteOptions{}))

			IgnoreError(kubeClient.DeleteAPIService(apiService.GetName(), &metav1.DeleteOptions{}))

		})

		When("APIService is deleted", func() {

			BeforeEach(func() {
				// Delete API service
				Eventually(func() bool {
					err := kubeClient.DeleteAPIService(apiService.GetName(), &metav1.DeleteOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("should delete the associated ClusterRole", func() {
				Eventually(func() bool {
					_, err := kubeClient.GetClusterRole(cr.GetName())
					return apierrors.IsNotFound(err)
				}).Should(BeTrue(), "get cluster role should eventually return \"not found\"")
			})

		})
	})

	// TestOwnerReferenceGCBehavior runs a simple check on OwnerReference behavior to ensure
	// a resource with multiple OwnerReferences will not be garbage collected when one of its
	// owners has been deleted.
	// Test Case:
	//				CSV-A     CSV-B                        CSV-B
	//				   \      /      --Delete CSV-A-->       |
	//				   ConfigMap						 ConfigMap
	Context("Given a dependent resource associated with multiple owners", func() {
		var (
			ownerA   v1alpha1.ClusterServiceVersion
			ownerB   v1alpha1.ClusterServiceVersion
			fetchedA *v1alpha1.ClusterServiceVersion
			fetchedB *v1alpha1.ClusterServiceVersion

			dependent *corev1.ConfigMap

			propagation metav1.DeletionPropagation
			options     metav1.DeleteOptions
		)

		BeforeEach(func() {

			ownerA = newCSV("ownera", ns.GetName(), "", semver.MustParse("0.0.0"), nil, nil, nil)
			ownerB = newCSV("ownerb", ns.GetName(), "", semver.MustParse("0.0.0"), nil, nil, nil)

			// create all owners
			var err error
			Eventually(func() error {
				fetchedA, err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Create(context.Background(), &ownerA, metav1.CreateOptions{})
				return err
			}).Should(Succeed())

			Eventually(func() error {
				fetchedB, err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Create(context.Background(), &ownerB, metav1.CreateOptions{})
				return err
			}).Should(Succeed())

			dependent = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: "dependent",
				},
				Data: map[string]string{},
			}

			// add owners
			ownerutil.AddOwner(dependent, fetchedA, true, false)
			ownerutil.AddOwner(dependent, fetchedB, true, false)

			// create ConfigMap dependent
			Eventually(func() error {
				_, err = kubeClient.KubernetesInterface().CoreV1().ConfigMaps(ns.GetName()).Create(context.Background(), dependent, metav1.CreateOptions{})
				return err
			}).Should(Succeed(), "dependent could not be created")

			propagation = metav1.DeletePropagationForeground
			options = metav1.DeleteOptions{PropagationPolicy: &propagation}
		})

		When("removing one of the owner using 'Foreground' deletion policy", func() {

			BeforeEach(func() {
				// delete ownerA in the foreground (to ensure any "blocking" dependents are deleted before ownerA)
				Eventually(func() bool {
					err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Delete(context.Background(), fetchedA.GetName(), options)
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// wait for deletion of ownerA
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Get(context.Background(), ownerA.GetName(), metav1.GetOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("should not have deleted the dependent since ownerB CSV is still present", func() {
				Eventually(func() error {
					_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(ns.GetName()).Get(context.Background(), dependent.GetName(), metav1.GetOptions{})
					return err
				}).Should(Succeed(), "dependent deleted after one of the owner was deleted")
				ctx.Ctx().Logf("dependent still exists after one owner was deleted")
			})
		})

		When("removing both the owners using 'Foreground' deletion policy", func() {

			BeforeEach(func() {
				// delete ownerA in the foreground (to ensure any "blocking" dependents are deleted before ownerA)
				Eventually(func() bool {
					err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Delete(context.Background(), fetchedA.GetName(), options)
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// wait for deletion of ownerA
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Get(context.Background(), ownerA.GetName(), metav1.GetOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// delete ownerB in the foreground (to ensure any "blocking" dependents are deleted before ownerB)
				Eventually(func() bool {
					err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Delete(context.Background(), fetchedB.GetName(), options)
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// wait for deletion of ownerB
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Get(context.Background(), ownerB.GetName(), metav1.GetOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("should have deleted the dependent since both the owners were deleted", func() {
				Eventually(func() bool {
					_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(ns.GetName()).Get(context.Background(), dependent.GetName(), metav1.GetOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue(), "expected dependency configmap would be properly garabage collected")
				ctx.Ctx().Logf("dependent successfully garbage collected after both owners were deleted")
			})

		})

	})

	When("a bundle with configmap and secret objects is installed", func() {

		const (
			packageName   = "busybox"
			channelName   = "alpha"
			subName       = "test-subscription"
			secretName    = "mysecret"
			configmapName = "special-config"
		)

		BeforeEach(func() {
			const (
				sourceName = "test-catalog"
				imageName  = "quay.io/olmtest/single-bundle-index:objects"
			)
			var installPlanRef string

			// create catalog source
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
					Image:      imageName,
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			}

			Eventually(func() error {
				cs, err := operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
				if err != nil {
					return err
				}
				source = cs.DeepCopy()

				return nil
			}).Should(Succeed(), "could not create catalog source")

			// Wait for the CatalogSource to be ready
			_, err := fetchCatalogSourceOnStatus(operatorClient, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, ns.GetName(), subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, ns.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			ctx.Ctx().Logf("install plan %s completed", installPlanRef)

			// confirm extra bundle objects (secret and configmap) are installed
			Eventually(func() error {
				_, err := kubeClient.GetSecret(ns.GetName(), secretName)
				return err
			}).Should(Succeed(), "expected no error getting secret object associated with CSV")

			Eventually(func() error {
				_, err := kubeClient.GetConfigMap(ns.GetName(), configmapName)
				return err
			}).Should(Succeed(), "expected no error getting configmap object associated with CSV")
		})

		When("the CSV is deleted", func() {

			const csvName = "busybox.v2.0.0"

			BeforeEach(func() {
				// Delete subscription first
				Eventually(func() bool {
					err := operatorClient.OperatorsV1alpha1().Subscriptions(ns.GetName()).Delete(context.Background(), subName, metav1.DeleteOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// wait for deletion
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().Subscriptions(ns.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// Delete CSV
				Eventually(func() bool {
					err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Delete(context.Background(), csvName, metav1.DeleteOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				// wait for deletion
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Get(context.Background(), csvName, metav1.GetOptions{})
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("OLM should delete the associated configmap and secret", func() {
				// confirm extra bundle objects (secret and configmap) are no longer installed on the cluster
				Eventually(func() bool {
					_, err := kubeClient.GetSecret(ns.GetName(), secretName)
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				Eventually(func() bool {
					_, err := kubeClient.GetConfigMap(ns.GetName(), configmapName)
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())
				ctx.Ctx().Logf("dependent successfully garbage collected after csv owner was deleted")
			})
		})
	})

	When("a bundle with a configmap is installed", func() {

		const (
			subName       = "test-subscription"
			configmapName = "special-config"
		)

		BeforeEach(func() {
			const (
				packageName = "busybox"
				channelName = "alpha"
				sourceName  = "test-catalog"
				imageName   = "quay.io/olmtest/single-bundle-index:objects-upgrade-samename"
			)

			var installPlanRef string
			// create catalog source
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
					Image:      imageName,
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			}

			var err error
			Eventually(func() error {
				source, err = operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
				return err
			}).Should(Succeed(), "could not create catalog source")

			// Wait for the CatalogSource to be ready
			_, err = fetchCatalogSourceOnStatus(operatorClient, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, ns.GetName(), subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, ns.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			Eventually(func() error {
				_, err := kubeClient.GetConfigMap(ns.GetName(), configmapName)
				return err
			}).Should(Succeed(), "expected no error getting configmap object associated with CSV")
		})

		When("the subscription is updated to a later CSV with a configmap with the same name but new data", func() {

			const (
				upgradeChannelName = "beta"
				newCSVname         = "busybox.v3.0.0"
			)
			var installPlanRef string

			BeforeEach(func() {
				Eventually(func() error {
					// update subscription first
					sub, err := operatorClient.OperatorsV1alpha1().Subscriptions(ns.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
					if err != nil {
						return fmt.Errorf("could not get subscription")
					}
					// update channel on sub
					sub.Spec.Channel = upgradeChannelName
					_, err = operatorClient.OperatorsV1alpha1().Subscriptions(ns.GetName()).Update(context.Background(), sub, metav1.UpdateOptions{})
					return err
				}).Should(Succeed(), "could not update subscription")

				// Wait for the Subscription to succeed
				sub, err := fetchSubscription(operatorClient, ns.GetName(), subName, subscriptionStateAtLatestChecker)
				Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

				installPlanRef = sub.Status.InstallPlanRef.Name

				// Wait for the installplan to complete (5 minute timeout)
				_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, ns.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
				Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

				// Ensure the new csv is installed
				Eventually(func() error {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Get(context.Background(), newCSVname, metav1.GetOptions{})
					return err
				}).Should(BeNil())
			})

			It("OLM should have upgraded associated configmap in place", func() {
				Eventually(func() (string, error) {
					cfg, err := kubeClient.GetConfigMap(ns.GetName(), configmapName)
					if err != nil {
						return "", err
					}
					// check data in configmap to ensure it is the new data (configmap was updated in the newer bundle)
					// new value in the configmap is "updated-very-much"
					return cfg.Data["special.how"], nil
				}).Should(Equal("updated-very-much"))
				ctx.Ctx().Logf("dependent successfully updated after csv owner was updated")
			})
		})
	})

	When("a bundle with a new configmap is installed", func() {

		const (
			subName       = "test-subscription"
			configmapName = "special-config"
		)

		BeforeEach(func() {
			const (
				packageName = "busybox"
				channelName = "alpha"
				sourceName  = "test-catalog"
				imageName   = "quay.io/olmtest/single-bundle-index:objects-upgrade-diffname"
			)

			var installPlanRef string
			// create catalog source
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
					Image:      imageName,
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			}

			var err error
			Eventually(func() error {
				source, err = operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.Background(), source, metav1.CreateOptions{})
				return err
			}).Should(Succeed())

			// Wait for the CatalogSource to be ready
			_, err = fetchCatalogSourceOnStatus(operatorClient, source.GetName(), source.GetNamespace(), catalogSourceRegistryPodSynced)
			Expect(err).ToNot(HaveOccurred(), "catalog source did not become ready")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, ns.GetName(), subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, ns.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			Eventually(func() error {
				_, err := kubeClient.GetConfigMap(ns.GetName(), configmapName)
				return err
			}).Should(Succeed(), "expected no error getting configmap object associated with CSV")
		})

		When("the subscription is updated to a later CSV with a configmap with a new name", func() {

			const (
				upgradeChannelName    = "beta"
				upgradedConfigMapName = "not-special-config"
				newCSVname            = "busybox.v3.0.0"
			)
			var installPlanRef string

			BeforeEach(func() {
				Eventually(func() error {
					// update subscription first
					sub, err := operatorClient.OperatorsV1alpha1().Subscriptions(ns.GetName()).Get(context.Background(), subName, metav1.GetOptions{})
					if err != nil {
						return fmt.Errorf("could not get subscription")
					}
					// update channel on sub
					sub.Spec.Channel = upgradeChannelName
					_, err = operatorClient.OperatorsV1alpha1().Subscriptions(ns.GetName()).Update(context.Background(), sub, metav1.UpdateOptions{})
					return err
				}).Should(Succeed(), "could not update subscription")

				// Wait for the Subscription to succeed
				sub, err := fetchSubscription(operatorClient, ns.GetName(), subName, subscriptionStateAtLatestChecker)
				Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

				installPlanRef = sub.Status.InstallPlanRef.Name

				// Wait for the installplan to complete (5 minute timeout)
				_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, ns.GetName(), buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
				Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

				// Ensure the new csv is installed
				Eventually(func() error {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns.GetName()).Get(context.Background(), newCSVname, metav1.GetOptions{})
					return err
				}).Should(BeNil())
			})

			// flake issue: https://github.com/operator-framework/operator-lifecycle-manager/issues/2626
			It("[FLAKE] should have removed the old configmap and put the new configmap in place", func() {
				Eventually(func() bool {
					_, err := kubeClient.GetConfigMap(ns.GetName(), configmapName)
					return apierrors.IsNotFound(err)
				}).Should(BeTrue())

				Eventually(func() error {
					_, err := kubeClient.GetConfigMap(ns.GetName(), upgradedConfigMapName)
					return err
				}).Should(BeNil())
				ctx.Ctx().Logf("dependent successfully updated after csv owner was updated")
			})
		})
	})
})
