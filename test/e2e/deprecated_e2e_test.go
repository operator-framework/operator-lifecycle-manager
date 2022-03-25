package e2e

import (
	"context"
	"fmt"
	"time"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var missingAPI = `{"apiVersion":"verticalpodautoscalers.autoscaling.k8s.io/v1","kind":"VerticalPodAutoscaler","metadata":{"name":"my.thing","namespace":"foo"}}`

var _ = Describe("Not found APIs", func() {

	var ns corev1.Namespace

	BeforeEach(func() {
		namespaceName := genName("deprecated-e2e-")
		og := operatorsv1.OperatorGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-operatorgroup", namespaceName),
				Namespace: namespaceName,
			},
		}
		ns = SetupGeneratedTestNamespaceWithOperatorGroup(namespaceName, og)

		csv := newCSV("test-csv", ns.GetName(), "", semver.Version{}, nil, nil, nil)
		Expect(ctx.Ctx().Client().Create(context.TODO(), &csv)).To(Succeed())
	})

	AfterEach(func() {
		TeardownNamespace(ns.GetName())
	})

	Context("objects with APIs that are not on-cluster are created in the installplan", func() {
		When("installplan contains a missing API", func() {
			It("the ip enters a failed state with a helpful error message", func() {
				ip := &operatorsv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-plan-api",
						Namespace: ns.GetName(),
					},
					Spec: operatorsv1alpha1.InstallPlanSpec{
						Approval:                   operatorsv1alpha1.ApprovalAutomatic,
						Approved:                   true,
						ClusterServiceVersionNames: []string{},
					},
				}
				Expect(ctx.Ctx().Client().Create(context.Background(), ip)).To(Succeed())

				ip.Status = operatorsv1alpha1.InstallPlanStatus{
					Phase:          operatorsv1alpha1.InstallPlanPhaseInstalling,
					CatalogSources: []string{},
					Plan: []*operatorsv1alpha1.Step{
						{
							Resolving: "test-csv",
							Status:    operatorsv1alpha1.StepStatusUnknown,
							Resource: operatorsv1alpha1.StepResource{
								Name:     "my.thing",
								Group:    "verticalpodautoscalers.autoscaling.k8s.io",
								Version:  "v1",
								Kind:     "VerticalPodAutoscaler",
								Manifest: missingAPI,
							},
						},
					},
				}

				Expect(ctx.Ctx().Client().Status().Update(context.Background(), ip)).To(Succeed(), "failed to update the resource")

				errMessage := "api-server resource not found installing VerticalPodAutoscaler my.thing: GroupVersionKind " +
					"verticalpodautoscalers.autoscaling.k8s.io/v1, Kind=VerticalPodAutoscaler not found on the cluster"
				// The IP sits in the Installing phase with the GVK missing error
				Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
					return ip, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(ip), ip)
				}).Should(And(HavePhase(operatorsv1alpha1.InstallPlanPhaseInstalling)), HaveMessage(errMessage))

				// Eventually the IP fails with the GVK missing error, after installplan retries, which is by default 1 minute.
				Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
					return ip, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(ip), ip)
				}, 2*time.Minute).Should(And(HavePhase(operatorsv1alpha1.InstallPlanPhaseFailed)), HaveMessage(errMessage))
			})
		})
	})
})
