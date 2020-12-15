package e2e

import (
	"context"
	"fmt"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/operator-framework/operator-lifecycle-manager/test/e2e/dsl"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
	)

	BeforeEach(func() {
		kubeClient = ctx.Ctx().KubeClient()
		operatorClient = ctx.Ctx().OperatorClient()
	})

	AfterEach(func() {
		TearDown(testNamespace)
	})

	Context("Given a ClusterRole owned by a CustomResourceDefinition", func() {
		var (
			crd *apiextensionsv1.CustomResourceDefinition
			cr  *rbacv1.ClusterRole
		)

		BeforeEach(func() {
			group := fmt.Sprintf("%s.com", rand.String(16))

			// Create a CustomResourceDefinition
			var err error
			crd, err = kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Create(context.TODO(), &apiextensionsv1.CustomResourceDefinition{
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
			}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			// Create a ClusterRole for the crd
			cr, err = kubeClient.CreateClusterRole(&rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName:    "clusterrole-",
					OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(crd)},
				},
			})
			Expect(err).NotTo(HaveOccurred())

		})

		AfterEach(func() {

			// Clean up cluster role
			IgnoreError(kubeClient.DeleteClusterRole(cr.GetName(), &metav1.DeleteOptions{}))

			// Clean up CRD
			IgnoreError(kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), crd.GetName(), metav1.DeleteOptions{}))
		})

		When("CustomResourceDefinition is deleted", func() {

			BeforeEach(func() {
				// Delete CRD
				Expect(kubeClient.ApiextensionsInterface().ApiextensionsV1().CustomResourceDefinitions().Delete(context.TODO(), crd.GetName(), metav1.DeleteOptions{})).To(Succeed())
			})

			It("should delete the associated ClusterRole", func() {

				Eventually(func() bool {
					_, err := kubeClient.GetClusterRole(cr.GetName())
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue(), "get cluster role should eventually return \"not found\"")
			})

		})
	})

	Context("Given a ClusterRole owned by a APIService", func() {

		var (
			as *apiregistrationv1.APIService
			cr *rbacv1.ClusterRole
		)

		BeforeEach(func() {
			group := rand.String(16)

			// Create an API Service
			var err error
			as, err = kubeClient.CreateAPIService(&apiregistrationv1.APIService{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("v1.%s", group),
				},
				Spec: apiregistrationv1.APIServiceSpec{
					Group:                group,
					Version:              "v1",
					GroupPriorityMinimum: 1,
					VersionPriority:      1,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Create a ClusterRole
			cr, err = kubeClient.CreateClusterRole(&rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName:    "clusterrole-",
					OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(as)},
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {

			IgnoreError(kubeClient.DeleteClusterRole(cr.GetName(), &metav1.DeleteOptions{}))

			IgnoreError(kubeClient.DeleteAPIService(as.GetName(), &metav1.DeleteOptions{}))

		})

		When("APIService is deleted", func() {

			BeforeEach(func() {
				// Delete API service
				Expect(kubeClient.DeleteAPIService(as.GetName(), &metav1.DeleteOptions{})).To(Succeed())
			})

			It("should delete the associated ClusterRole", func() {
				Eventually(func() bool {
					_, err := kubeClient.GetClusterRole(cr.GetName())
					return k8serrors.IsNotFound(err)
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

			ownerA = newCSV("ownera", testNamespace, "", semver.MustParse("0.0.0"), nil, nil, nil)
			ownerB = newCSV("ownerb", testNamespace, "", semver.MustParse("0.0.0"), nil, nil, nil)

			// create all owners
			var err error
			fetchedA, err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(context.TODO(), &ownerA, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			fetchedB, err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Create(context.TODO(), &ownerB, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

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
			_, err = kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Create(context.TODO(), dependent, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred(), "dependent could not be created")

			propagation = metav1.DeletePropagationForeground
			options = metav1.DeleteOptions{PropagationPolicy: &propagation}
		})

		When("removing one of the owner using 'Foreground' deletion policy", func() {

			BeforeEach(func() {
				// delete ownerA in the foreground (to ensure any "blocking" dependents are deleted before ownerA)
				err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), fetchedA.GetName(), options)
				Expect(err).NotTo(HaveOccurred())

				// wait for deletion of ownerA
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), ownerA.GetName(), metav1.GetOptions{})
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())

			})

			It("should not have deleted the dependent since ownerB CSV is still present", func() {
				_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(context.TODO(), dependent.GetName(), metav1.GetOptions{})
				Expect(err).NotTo(HaveOccurred(), "dependent deleted after one of the owner was deleted")
				ctx.Ctx().Logf("dependent still exists after one owner was deleted")

			})

		})

		When("removing both the owners using 'Foreground' deletion policy", func() {

			BeforeEach(func() {
				// delete ownerA in the foreground (to ensure any "blocking" dependents are deleted before ownerA)
				err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), fetchedA.GetName(), options)
				Expect(err).NotTo(HaveOccurred())

				// wait for deletion of ownerA
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), ownerA.GetName(), metav1.GetOptions{})
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())

				// delete ownerB in the foreground (to ensure any "blocking" dependents are deleted before ownerB)
				err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), fetchedB.GetName(), options)
				Expect(err).NotTo(HaveOccurred())

				// wait for deletion of ownerB
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), ownerB.GetName(), metav1.GetOptions{})
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("should have deleted the dependent since both the owners were deleted", func() {
				_, err := kubeClient.KubernetesInterface().CoreV1().ConfigMaps(testNamespace).Get(context.TODO(), dependent.GetName(), metav1.GetOptions{})
				Expect(err).To(HaveOccurred())
				Expect(k8serrors.IsNotFound(err)).To(BeTrue())
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
					Namespace: testNamespace,
					Labels:    map[string]string{"olm.catalogSource": sourceName},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      imageName,
				},
			}

			Eventually(func() error {
				cs, err := operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.TODO(), source, metav1.CreateOptions{})
				if err != nil {
					return err
				}
				source = cs.DeepCopy()

				return nil
			}).Should(Succeed(), "could not create catalog source")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			ctx.Ctx().Logf("install plan %s completed", installPlanRef)

			// confirm extra bundle objects (secret and configmap) are installed
			Eventually(func() error {
				_, err := kubeClient.GetSecret(testNamespace, secretName)
				return err
			}).Should(Succeed(), "expected no error getting secret object associated with CSV")

			Eventually(func() error {
				_, err := kubeClient.GetConfigMap(testNamespace, configmapName)
				return err
			}).Should(Succeed(), "expected no error getting configmap object associated with CSV")
		})

		When("the CSV is deleted", func() {
			const csvName = "busybox.v2.0.0"

			BeforeEach(func() {
				// Delete subscription first
				err := operatorClient.OperatorsV1alpha1().Subscriptions(testNamespace).Delete(context.TODO(), subName, metav1.DeleteOptions{})
				Expect(err).To(BeNil())

				// wait for deletion
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), subName, metav1.GetOptions{})
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())

				// Delete CSV
				err = operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Delete(context.TODO(), csvName, metav1.DeleteOptions{})
				Expect(err).To(BeNil())

				// wait for deletion
				Eventually(func() bool {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), csvName, metav1.GetOptions{})
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())
			})

			It("OLM should delete the associated configmap and secret", func() {
				// confirm extra bundle objects (secret and configmap) are no longer installed on the cluster
				Eventually(func() bool {
					_, err := kubeClient.GetSecret(testNamespace, secretName)
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())

				Eventually(func() bool {
					_, err := kubeClient.GetConfigMap(testNamespace, configmapName)
					return k8serrors.IsNotFound(err)
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
					Namespace: testNamespace,
					Labels:    map[string]string{"olm.catalogSource": sourceName},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      imageName,
				},
			}

			source, err := operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.TODO(), source, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred(), "could not create catalog source")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			Eventually(func() error {
				_, err := kubeClient.GetConfigMap(testNamespace, configmapName)
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
				// update subscription first
				sub, err := operatorClient.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), subName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred(), "could not get subscription")

				// update channel on sub
				sub.Spec.Channel = upgradeChannelName
				_, err = operatorClient.OperatorsV1alpha1().Subscriptions(testNamespace).Update(context.TODO(), sub, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred(), "could not update subscription")

				// Wait for the Subscription to succeed
				sub, err = fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
				Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

				installPlanRef = sub.Status.InstallPlanRef.Name

				// Wait for the installplan to complete (5 minute timeout)
				_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
				Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

				// Ensure the new csv is installed
				Eventually(func() error {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), newCSVname, metav1.GetOptions{})
					return err
				}).Should(BeNil())
			})

			It("OLM should have upgraded associated configmap in place", func() {
				Eventually(func() (string, error) {
					cfg, err := kubeClient.GetConfigMap(testNamespace, configmapName)
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
					Namespace: testNamespace,
					Labels:    map[string]string{"olm.catalogSource": sourceName},
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      imageName,
				},
			}

			source, err := operatorClient.OperatorsV1alpha1().CatalogSources(source.GetNamespace()).Create(context.TODO(), source, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred(), "could not create catalog source")

			// Create a Subscription for package
			_ = createSubscriptionForCatalog(operatorClient, source.GetNamespace(), subName, source.GetName(), packageName, channelName, "", v1alpha1.ApprovalAutomatic)

			// Wait for the Subscription to succeed
			sub, err := fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
			Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

			installPlanRef = sub.Status.InstallPlanRef.Name

			// Wait for the installplan to complete (5 minute timeout)
			_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
			Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

			Eventually(func() error {
				_, err := kubeClient.GetConfigMap(testNamespace, configmapName)
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
				// update subscription first
				sub, err := operatorClient.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), subName, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred(), "could not get subscription")

				// update channel on sub
				sub.Spec.Channel = upgradeChannelName
				_, err = operatorClient.OperatorsV1alpha1().Subscriptions(testNamespace).Update(context.TODO(), sub, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred(), "could not update subscription")

				// Wait for the Subscription to succeed
				sub, err = fetchSubscription(operatorClient, testNamespace, subName, subscriptionStateAtLatestChecker)
				Expect(err).ToNot(HaveOccurred(), "could not get subscription at latest status")

				installPlanRef = sub.Status.InstallPlanRef.Name

				// Wait for the installplan to complete (5 minute timeout)
				_, err = fetchInstallPlan(GinkgoT(), operatorClient, installPlanRef, buildInstallPlanPhaseCheckFunc(v1alpha1.InstallPlanPhaseComplete))
				Expect(err).ToNot(HaveOccurred(), "could not get installplan at complete phase")

				// Ensure the new csv is installed
				Eventually(func() error {
					_, err := operatorClient.OperatorsV1alpha1().ClusterServiceVersions(testNamespace).Get(context.TODO(), newCSVname, metav1.GetOptions{})
					return err
				}).Should(BeNil())
			})

			It("should have removed the old configmap and put the new configmap in place", func() {
				Eventually(func() bool {
					_, err := kubeClient.GetConfigMap(testNamespace, configmapName)
					return k8serrors.IsNotFound(err)
				}).Should(BeTrue())

				Eventually(func() error {
					_, err := kubeClient.GetConfigMap(testNamespace, upgradedConfigMapName)
					return err
				}).Should(BeNil())
				ctx.Ctx().Logf("dependent successfully updated after csv owner was updated")
			})
		})
	})
})
