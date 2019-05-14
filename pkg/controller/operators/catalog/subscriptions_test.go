package catalog

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
)

func TestSyncSubscriptions(t *testing.T) {
	now := metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC)
	timeNow = func() metav1.Time { return now }
	testNamespace := "testNamespace"

	type fields struct {
		clientOptions     []clientfake.Option
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
			name: "NoStatus/NoCurrentCSV/MissingCatalogSourceNamespace",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource: "src",
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
							Group:                  v1alpha1.GroupName,
							Version:                v1alpha1.GroupVersion,
							Kind:                   v1alpha1.ClusterServiceVersionKind,
							Name:                   "csv.v.1",
							Manifest:               "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
						},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sub",
							Namespace: testNamespace,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource: "src",
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
						CatalogSource: "src",
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
						CatalogSource: "src",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "csv.v.1",
						State:      v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanRef: &v1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated: now,
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
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.ClusterServiceVersionKind,
								Name:                   "csv.v.1",
								Manifest:               "{}",
							},
						},
					},
				},
			},
		},
		{
			name: "NoStatus/NoCurrentCSV/FoundInCatalog",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
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
							Group:                  v1alpha1.GroupName,
							Version:                v1alpha1.GroupVersion,
							Kind:                   v1alpha1.ClusterServiceVersionKind,
							Name:                   "csv.v.1",
							Manifest:               "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
					{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SchemeGroupVersion.String(),
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
						State:      v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanRef: &v1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated: now,
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
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.ClusterServiceVersionKind,
								Name:                   "csv.v.1",
								Manifest:               "{}",
							},
						},
					},
				},
			},
		},
		{
			name: "Status/HaveCurrentCSV/UpdateFoundInCatalog",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
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
							Group:                  v1alpha1.GroupName,
							Version:                v1alpha1.GroupVersion,
							Kind:                   v1alpha1.ClusterServiceVersionKind,
							Name:                   "csv.v.2",
							Manifest:               "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
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
						State:      v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanRef: &v1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated: now,
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
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.ClusterServiceVersionKind,
								Name:                   "csv.v.2",
								Manifest:               "{}",
							},
						},
					},
				},
			},
		},
		{
			name: "Status/HaveCurrentCSV/UpdateFoundInCatalog/UpdateRequiresDependency",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
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
							Group:                  v1alpha1.GroupName,
							Version:                v1alpha1.GroupVersion,
							Kind:                   v1alpha1.ClusterServiceVersionKind,
							Name:                   "csv.v.2",
							Manifest:               "{}",
						},
					},
					{
						Resolving: "csv.v.2",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:                  v1alpha1.GroupName,
							Version:                v1alpha1.GroupVersion,
							Kind:                   v1alpha1.ClusterServiceVersionKind,
							Name:                   "dep.v.1",
							Manifest:               "{}",
						},
					},
					{
						Resolving: "csv.v.2",
						Resource: v1alpha1.StepResource{
							CatalogSource:          "src",
							CatalogSourceNamespace: testNamespace,
							Group:                  v1alpha1.GroupName,
							Version:                v1alpha1.GroupVersion,
							Kind:                   v1alpha1.SubscriptionKind,
							Name:                   "sub-dep",
							Manifest:               "{}",
						},
					},
				},
				resolveSubs: []*v1alpha1.Subscription{
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
						State:      v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanRef: &v1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated: now,
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
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.ClusterServiceVersionKind,
								Name:                   "csv.v.2",
								Manifest:               "{}",
							},
						},
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.ClusterServiceVersionKind,
								Name:                   "dep.v.1",
								Manifest:               "{}",
							},
						},
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: testNamespace,
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.SubscriptionKind,
								Name:                   "sub-dep",
								Manifest:               "{}",
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
			o, err := NewFakeOperator(testNamespace, []string{testNamespace}, stopCh, withClientObjs(tt.fields.existingOLMObjs...), withK8sObjs(tt.fields.existingObjects...), withFakeClientOptions(tt.fields.clientOptions...))
			require.NoError(t, err)

			o.reconciler = &fakes.FakeRegistryReconcilerFactory{
				ReconcilerForSourceStub: func(source *v1alpha1.CatalogSource) reconciler.RegistryReconciler {
					return &fakes.FakeRegistryReconciler{
						EnsureRegistryServerStub: func(source *v1alpha1.CatalogSource) error {
							return nil
						},
					}
				},
			}

			o.sourcesLastUpdate = tt.fields.sourcesLastUpdate
			o.resolver = &fakes.FakeResolver{
				ResolveStepsStub: func(string, resolver.SourceQuerier) ([]*v1alpha1.Step, []*v1alpha1.Subscription, error) {
					return tt.fields.resolveSteps, tt.fields.resolveSubs, tt.fields.resolveErr
				},
			}

			namespace := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testNamespace,
				},
			}
			if err := o.syncSubscriptions(tt.args.obj); err != nil {
				require.Equal(t, tt.wantErr, err)
			} else {
				require.Equal(t, tt.wantErr, o.syncResolvingNamespace(namespace))
			}

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
