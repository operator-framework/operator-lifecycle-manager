package e2e

import (
	"context"
	"time"

	"github.com/blang/semver/v4"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/operator-framework/operator-lifecycle-manager/test/e2e/ctx"
)

var missingAPI = `{"apiVersion":"verticalpodautoscalers.autoscaling.k8s.io/v1","kind":"VerticalPodAutoscaler","metadata":{"name":"my.thing","namespace":"foo"}}`

var _ = Describe("Not found APIs", func() {
	BeforeEach(func() {
		csv := newCSV("test-csv", testNamespace, "", semver.Version{}, nil, nil, nil)
		Expect(ctx.Ctx().Client().Create(context.TODO(), &csv)).To(Succeed())
	})
	AfterEach(func() {
		TearDown(testNamespace)
	})

	When("objects with APIs that are not on-cluster are created in the installplan", func() {
		// each entry is an installplan with a deprecated resource
		type payload struct {
			name       string
			ip         *operatorsv1alpha1.InstallPlan
			errMessage string
		}

		var tableEntries []table.TableEntry
		tableEntries = []table.TableEntry{
			table.Entry("contains an entry with a missing API not found on cluster ", payload{
				name: "installplan contains a missing API",
				ip: &operatorsv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: *namespace, // this is necessary due to ginkgo table semantics, see https://github.com/onsi/ginkgo/issues/378
						Name:      "test-plan-api",
					},
					Spec: operatorsv1alpha1.InstallPlanSpec{
						Approval:                   operatorsv1alpha1.ApprovalAutomatic,
						Approved:                   true,
						ClusterServiceVersionNames: []string{},
					},
					Status: operatorsv1alpha1.InstallPlanStatus{
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
					},
				},
				errMessage: "api-server resource not found installing VerticalPodAutoscaler my.thing: GroupVersionKind " +
					"verticalpodautoscalers.autoscaling.k8s.io/v1, Kind=VerticalPodAutoscaler not found on the cluster",
			}),
		}

		table.DescribeTable("the ip enters a failed state with a helpful error message", func(tt payload) {
			Expect(ctx.Ctx().Client().Create(context.Background(), tt.ip)).To(Succeed())
			Expect(ctx.Ctx().Client().Status().Update(context.Background(), tt.ip)).To(Succeed())

			// The IP sits in the Installing phase with the GVK missing error
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return tt.ip, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(tt.ip), tt.ip)
			}).Should(And(HavePhase(operatorsv1alpha1.InstallPlanPhaseInstalling)), HaveMessage(tt.errMessage))

			// Eventually the IP fails with the GVK missing error, after installplan retries, which is by default 1 minute.
			Eventually(func() (*operatorsv1alpha1.InstallPlan, error) {
				return tt.ip, ctx.Ctx().Client().Get(context.Background(), client.ObjectKeyFromObject(tt.ip), tt.ip)
			}, 2*time.Minute).Should(And(HavePhase(operatorsv1alpha1.InstallPlanPhaseFailed)), HaveMessage(tt.errMessage))
		}, tableEntries...)
	})
})
