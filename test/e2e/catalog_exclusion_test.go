package e2e

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/util"
	. "github.com/operator-framework/operator-lifecycle-manager/test/e2e/util/gomega"
	"google.golang.org/grpc/connectivity"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const magicCatalogDir = "magiccatalog"

var _ = Describe("Global Catalog Exclusion", func() {
	var (
		testNamespace       corev1.Namespace
		determinedE2eClient *util.DeterminedE2EClient
		operatorGroup       operatorsv1.OperatorGroup
		localCatalog        *MagicCatalog
	)

	BeforeEach(func() {
		determinedE2eClient = util.NewDeterminedClient(ctx.Ctx().E2EClient())

		By("creating a namespace with an own namespace operator group without annotations")
		e2eTestNamespace := genName("global-catalog-exclusion-e2e-")
		operatorGroup = operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   e2eTestNamespace,
				Name:        genName("og-"),
				Annotations: nil,
			},
			Spec: operatorsv1.OperatorGroupSpec{
				TargetNamespaces: []string{e2eTestNamespace},
			},
		}
		testNamespace = SetupGeneratedTestNamespaceWithOperatorGroup(e2eTestNamespace, operatorGroup)

		By("creating a broken catalog in the global namespace")
		globalCatalog := &v1alpha1.CatalogSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genName("bad-global-catalog-"),
				Namespace: operatorNamespace,
			},
			Spec: v1alpha1.CatalogSourceSpec{
				DisplayName: "Broken Global Catalog Source",
				SourceType:  v1alpha1.SourceTypeGrpc,
				Address:     "1.1.1.1:1337", // points to non-existing service
			},
		}
		_ = determinedE2eClient.Create(context.Background(), globalCatalog)

		By("creating a healthy catalog in the test namespace")
		localCatalogName := genName("good-catsrc-")
		var err error = nil

		fbcPath := filepath.Join(testdataDir, magicCatalogDir, "fbc_initial.yaml")
		localCatalog, err = NewMagicCatalogFromFile(determinedE2eClient, testNamespace.GetName(), localCatalogName, fbcPath)
		Expect(err).To(Succeed())

		// deploy catalog blocks until the catalog has reached a ready state or fails
		Expect(localCatalog.DeployCatalog(context.Background())).To(Succeed())

		By("checking that the global catalog is broken")
		// Adding this check here to speed up the test
		// the global catalog can fail while we wait for the local catalog to get to a ready state
		EventuallyResource(globalCatalog).Should(HaveGrpcConnectionWithLastConnectionState(connectivity.TransientFailure))
	})

	AfterEach(func() {
		TeardownNamespace(testNamespace.GetName())
	})

	When("a subscription referring to the local catalog is created", func() {
		var subscription *v1alpha1.Subscription

		BeforeEach(func() {
			subscription = &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNamespace.GetName(),
					Name:      genName("local-subscription-"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource:          localCatalog.GetName(),
					CatalogSourceNamespace: localCatalog.GetNamespace(),
					Package:                "packageA",
					Channel:                "stable",
					InstallPlanApproval:    v1alpha1.ApprovalAutomatic,
				},
			}

			By("creating a subscription")
			_ = determinedE2eClient.Create(context.Background(), subscription)
		})

		When("the operator group is annotated with olm.operatorframework.io/exclude-global-namespace-resolution=true", func() {

			It("the broken subscription should resolve and have state AtLatest", func() {
				By("checking that the subscription is not resolving and has a condition with type ResolutionFailed")
				EventuallyResource(subscription).Should(ContainSubscriptionConditionOfType(v1alpha1.SubscriptionResolutionFailed))

				By("annotating the operator group with olm.operatorframework.io/exclude-global-namespace-resolution=true")
				Eventually(func() error {
					annotatedOperatorGroup := operatorGroup.DeepCopy()
					if err := determinedE2eClient.Get(context.Background(), k8scontrollerclient.ObjectKeyFromObject(annotatedOperatorGroup), annotatedOperatorGroup); err != nil {
						return err
					}

					if annotatedOperatorGroup.Annotations == nil {
						annotatedOperatorGroup.Annotations = map[string]string{}
					}

					annotatedOperatorGroup.Annotations["olm.operatorframework.io/exclude-global-namespace-resolution"] = "true"
					if err := determinedE2eClient.Update(context.Background(), annotatedOperatorGroup); err != nil {
						return err
					}
					return nil
				}).Should(Succeed())

				By("checking that the subscription resolves and has state AtLatest")
				EventuallyResource(subscription).Should(HaveSubscriptionState(v1alpha1.SubscriptionStateAtLatest))
			})
		})
	})
})
