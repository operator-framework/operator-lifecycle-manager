package catalog

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
)

func TestSyncSubscriptions(t *testing.T) {
	testNamespace := "testNamespace"
	type fields struct {
		sourcesLastUpdate metav1.Time
		resolveSteps      []*v1alpha1.Step
		resolveSubs       []*v1alpha1.Subscription
		resolveErr        error
		existingOLMObjs   []runtime.Object
		existingObjects   []runtime.Object
	}
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name              string
		fields            fields
		args              args
		wantErr           error
		wantInstallPlan   *v1alpha1.InstallPlan
		wantSubscriptions []*v1alpha1.Subscription
	}{
		{
			name: "BadObject",
			args: args{
				obj: &v1alpha1.ClusterServiceVersion{},
			},
			wantErr: fmt.Errorf("casting Subscription failed"),
		},
		{
			name: "NoStatus/NoCurrentCSV/FoundInCatalog",
			fields: fields{
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
				resolveSteps: []*v1alpha1.Step{
					{
						Resolving: "csv.v.1",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:    v1alpha1.GroupName,
							Version:  v1alpha1.GroupVersion,
							Kind:     v1alpha1.ClusterServiceVersionKind,
							Name:     "csv.v.1",
							Manifest: "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "csv.v.1",
							State:      "SubscriptionStateAtLatest",
						},
					},
				},
			},
			args: args{
				obj: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
					},
				},
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "csv.v.1",
						State:      "SubscriptionStateAtLatest",
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
					},
				},
			},
			wantInstallPlan: &v1alpha1.InstallPlan{
				Spec: v1alpha1.InstallPlanSpec{
					ClusterServiceVersionNames: []string{
						"csv.v.1",
					},
					Approval: v1alpha1.ApprovalAutomatic,
					Approved: true,
				},
				Status: v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseInstalling,
					CatalogSources: []string{
						"src",
					},
					Plan: []*v1alpha1.Step{
						{
							Resolving: "csv.v.1",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:    v1alpha1.GroupName,
								Version:  v1alpha1.GroupVersion,
								Kind:     v1alpha1.ClusterServiceVersionKind,
								Name:     "csv.v.1",
								Manifest: "{}",
							},
						},
					},
				},
			},
		},
		{
			name: "Status/HaveCurrentCSV/UpdateFoundInCatalog",
			fields: fields{
				existingOLMObjs: []runtime.Object{
					&v1alpha1.ClusterServiceVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv.v.1",
							Namespace: testNamespace,
						},
						Status: v1alpha1.ClusterServiceVersionStatus{
							Phase: v1alpha1.CSVPhaseSucceeded,
						},
					},
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
				resolveSteps: []*v1alpha1.Step{
					{
						Resolving: "csv.v.2",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:    v1alpha1.GroupName,
							Version:  v1alpha1.GroupVersion,
							Kind:     v1alpha1.ClusterServiceVersionKind,
							Name:     "csv.v.2",
							Manifest: "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "csv.v.2",
							State:      "SubscriptionStateAtLatest",
						},
					},
				},
			},
			args: args{
				obj: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
					},
				},
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "csv.v.2",
						State:      "SubscriptionStateAtLatest",
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
					},
				},
			},
			wantInstallPlan: &v1alpha1.InstallPlan{
				Spec: v1alpha1.InstallPlanSpec{
					ClusterServiceVersionNames: []string{
						"csv.v.2",
					},
					Approval: v1alpha1.ApprovalAutomatic,
					Approved: true,
				},
				Status: v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseInstalling,
					CatalogSources: []string{
						"src",
					},
					Plan: []*v1alpha1.Step{
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:    v1alpha1.GroupName,
								Version:  v1alpha1.GroupVersion,
								Kind:     v1alpha1.ClusterServiceVersionKind,
								Name:     "csv.v.2",
								Manifest: "{}",
							},
						},
					},
				},
			},
		},
		{
			name: "Status/HaveCurrentCSV/UpdateFoundInCatalog/UpdateRequiresDependency",
			fields: fields{
				existingOLMObjs: []runtime.Object{
					&v1alpha1.ClusterServiceVersion{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "csv.v.1",
							Namespace: testNamespace,
						},
						Status: v1alpha1.ClusterServiceVersionStatus{
							Phase: v1alpha1.CSVPhaseSucceeded,
						},
					},
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
				resolveSteps: []*v1alpha1.Step{
					{
						Resolving: "csv.v.2",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:    v1alpha1.GroupName,
							Version:  v1alpha1.GroupVersion,
							Kind:     v1alpha1.ClusterServiceVersionKind,
							Name:     "csv.v.2",
							Manifest: "{}",
						},
					},
					{
						Resolving: "csv.v.2",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:    v1alpha1.GroupName,
							Version:  v1alpha1.GroupVersion,
							Kind:     v1alpha1.ClusterServiceVersionKind,
							Name:     "dep.v.1",
							Manifest: "{}",
						},
					},
					{
						Resolving: "csv.v.2",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:    v1alpha1.GroupName,
							Version:  v1alpha1.GroupVersion,
							Kind:     v1alpha1.SubscriptionKind,
							Name:     "sub-dep",
							Manifest: "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "csv.v.2",
							State:      "SubscriptionStateAtLatest",
						},
					},
				},
			},
			args: args{
				obj: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "",
						State:      "",
					},
				},
			},
			wantSubscriptions: []*v1alpha1.Subscription{
				{
					TypeMeta: metav1.TypeMeta{
						Kind:       v1alpha1.SubscriptionKind,
						APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sub",
						Namespace: testNamespace,
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:          "src",
						CatalogSourceNamespace: testNamespace,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "csv.v.2",
						State:      "SubscriptionStateAtLatest",
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
					},
				},
			},
			wantInstallPlan: &v1alpha1.InstallPlan{
				Spec: v1alpha1.InstallPlanSpec{
					ClusterServiceVersionNames: []string{
						"csv.v.2",
						"dep.v.1",
					},
					Approval: v1alpha1.ApprovalAutomatic,
					Approved: true,
				},
				Status: v1alpha1.InstallPlanStatus{
					Phase: v1alpha1.InstallPlanPhaseInstalling,
					CatalogSources: []string{
						"src",
					},
					Plan: []*v1alpha1.Step{
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:    v1alpha1.GroupName,
								Version:  v1alpha1.GroupVersion,
								Kind:     v1alpha1.ClusterServiceVersionKind,
								Name:     "csv.v.2",
								Manifest: "{}",
							},
						},
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:    v1alpha1.GroupName,
								Version:  v1alpha1.GroupVersion,
								Kind:     v1alpha1.ClusterServiceVersionKind,
								Name:     "dep.v.1",
								Manifest: "{}",
							},
						},
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:    v1alpha1.GroupName,
								Version:  v1alpha1.GroupVersion,
								Kind:     v1alpha1.SubscriptionKind,
								Name:     "sub-dep",
								Manifest: "{}",
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test operator
			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			o, _, err := NewFakeOperator(tt.fields.existingOLMObjs, tt.fields.existingObjects, nil, nil, testNamespace, stopCh)
			require.NoError(t, err)

			o.configmapRegistryReconciler = &fakes.FakeRegistryReconciler{
				EnsureRegistryServerStub: func(source *v1alpha1.CatalogSource) error {
					return nil
				},
			}
			o.sourcesLastUpdate = tt.fields.sourcesLastUpdate
			o.resolver = &fakes.FakeResolver{
				ResolveStepsStub: func(string, resolver.SourceQuerier) ([]*v1alpha1.Step, []*v1alpha1.Subscription, error) {
					return tt.fields.resolveSteps, tt.fields.resolveSubs, tt.fields.resolveErr
				},
			}

			require.Equal(t, tt.wantErr, o.syncSubscriptions(tt.args.obj))

			for _, s := range tt.wantSubscriptions {
				fetched, err := o.client.OperatorsV1alpha1().Subscriptions(testNamespace).Get(s.GetName(), metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, s, fetched)
			}
			if tt.wantInstallPlan != nil {
				installPlans, err := o.client.OperatorsV1alpha1().InstallPlans(testNamespace).List(metav1.ListOptions{})
				require.NoError(t, err)
				require.Equal(t, 1, len(installPlans.Items))
				ip := installPlans.Items[0]
				require.Equal(t, tt.wantInstallPlan.Spec, ip.Spec)
				require.Equal(t, tt.wantInstallPlan.Status, ip.Status)
			}
		})
	}
}
