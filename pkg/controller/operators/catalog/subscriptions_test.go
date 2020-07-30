package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilclock "k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/clientfake"
)

func TestSyncSubscriptions(t *testing.T) {
	clockFake := utilclock.NewFakeClock(time.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC))
	now := metav1.NewTime(clockFake.Now())
	testNamespace := "testNamespace"

	type fields struct {
		clientOptions     []clientfake.Option
		sourcesLastUpdate metav1.Time
		resolveSteps      []*v1alpha1.Step
		resolveSubs       []*v1alpha1.Subscription
		bundleLookups     []v1alpha1.BundleLookup
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
		wantInstallPlans  []v1alpha1.InstallPlan
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
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated:           now,
						InstallPlanGeneration: 1,
					},
				},
			},
			wantInstallPlans: []v1alpha1.InstallPlan{
				{
					Spec: v1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{
							"csv.v.1",
						},
						Approval:   v1alpha1.ApprovalAutomatic,
						Approved:   true,
						Generation: 1,
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
		},
		{
			name: "NoStatus/NoCurrentCSV/FoundInCatalog",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
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
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated:           now,
						InstallPlanGeneration: 1,
					},
				},
			},
			wantInstallPlans: []v1alpha1.InstallPlan{
				{
					Spec: v1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{
							"csv.v.1",
						},
						Approval:   v1alpha1.ApprovalAutomatic,
						Approved:   true,
						Generation: 1,
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
		},
		{
			name: "NoStatus/NoCurrentCSV/BundlePath",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
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
							CurrentCSV: "",
							State:      "",
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
							CurrentCSV: "",
							State:      v1alpha1.SubscriptionStateUpgradePending,
						},
					},
				},
				bundleLookups: []v1alpha1.BundleLookup{
					{
						Path:       "bundle-path-a",
						Identifier: "bundle-a",
						CatalogSourceRef: &corev1.ObjectReference{
							Namespace: testNamespace,
							Name:      "src",
						},
						Conditions: []v1alpha1.BundleLookupCondition{
							{
								Type:               v1alpha1.BundleLookupPending,
								Status:             corev1.ConditionTrue,
								Reason:             "JobIncomplete",
								Message:            "unpack job not completed",
								LastTransitionTime: &now,
							},
						},
					},
				},
			},
			args: args{
				obj: &v1alpha1.Subscription{
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
						CurrentCSV: "",
						State:      v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						InstallPlanGeneration: 1,
						LastUpdated:           now,
					},
				},
			},
			wantInstallPlans: []v1alpha1.InstallPlan{
				{
					Spec: v1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{"bundle-a"},
						Approval:                   v1alpha1.ApprovalAutomatic,
						Approved:                   true,
						Generation:                 1,
					},
					Status: v1alpha1.InstallPlanStatus{
						Phase:          v1alpha1.InstallPlanPhaseInstalling,
						CatalogSources: []string{},
						BundleLookups: []v1alpha1.BundleLookup{
							{
								Path:       "bundle-path-a",
								Identifier: "bundle-a",
								CatalogSourceRef: &corev1.ObjectReference{
									Namespace: testNamespace,
									Name:      "src",
								},
								Conditions: []v1alpha1.BundleLookupCondition{
									{
										Type:               v1alpha1.BundleLookupPending,
										Status:             corev1.ConditionTrue,
										Reason:             "JobIncomplete",
										Message:            "unpack job not completed",
										LastTransitionTime: &now,
									},
								},
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
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated:           now,
						InstallPlanGeneration: 1,
					},
				},
			},
			wantInstallPlans: []v1alpha1.InstallPlan{
				{
					Spec: v1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{
							"csv.v.2",
						},
						Approval:   v1alpha1.ApprovalAutomatic,
						Approved:   true,
						Generation: 1,
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
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated:           now,
						InstallPlanGeneration: 1,
					},
				},
			},
			wantInstallPlans: []v1alpha1.InstallPlan{
				{
					Spec: v1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{
							"csv.v.2",
							"dep.v.1",
						},
						Approval:   v1alpha1.ApprovalAutomatic,
						Approved:   true,
						Generation: 1,
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
		},
		{
			name: "ExistingInstallPlanGenerationRespected",
			fields: fields{
				clientOptions: []clientfake.Option{clientfake.WithSelfLinks(t)},
				existingOLMObjs: []runtime.Object{
					&v1alpha1.Subscription{
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
							CurrentCSV: "",
							State:      "",
						},
					},
					&v1alpha1.InstallPlan{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "ip",
							Namespace: testNamespace,
						},
						Spec: v1alpha1.InstallPlanSpec{
							Approval: v1alpha1.ApprovalAutomatic,
							Approved: true,
							ClusterServiceVersionNames: []string{
								"some-csv",
							},
							// Claim the gen 1 to ensure a new InstallPlan is created at gen 2
							Generation: 1,
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
						InstallPlanRef: &corev1.ObjectReference{
							Namespace:  testNamespace,
							Kind:       v1alpha1.InstallPlanKind,
							APIVersion: v1alpha1.InstallPlanAPIVersion,
						},
						LastUpdated:           now,
						InstallPlanGeneration: 2,
					},
				},
			},
			wantInstallPlans: []v1alpha1.InstallPlan{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ip",
						Namespace: testNamespace,
					},
					Spec: v1alpha1.InstallPlanSpec{
						Approval: v1alpha1.ApprovalAutomatic,
						Approved: true,
						ClusterServiceVersionNames: []string{
							"some-csv",
						},
						// Claim the gen 1 to ensure a new InstallPlan is created at gen 2
						Generation: 1,
					},
				},
				{
					Spec: v1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{
							"csv.v.1",
						},
						Approval:   v1alpha1.ApprovalAutomatic,
						Approved:   true,
						Generation: 2,
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
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test operator
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			o, err := NewFakeOperator(ctx, testNamespace, []string{testNamespace}, withClock(clockFake), withClientObjs(tt.fields.existingOLMObjs...), withK8sObjs(tt.fields.existingObjects...), withFakeClientOptions(tt.fields.clientOptions...))
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

			o.sourcesLastUpdate.Set(tt.fields.sourcesLastUpdate.Time)
			o.resolver = &fakes.FakeStepResolver{
				ResolveStepsStub: func(string, resolver.SourceQuerier) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
					return tt.fields.resolveSteps, tt.fields.bundleLookups, tt.fields.resolveSubs, tt.fields.resolveErr
				},
			}

			namespace := &corev1.Namespace{
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
				fetched, err := o.client.OperatorsV1alpha1().Subscriptions(testNamespace).Get(context.TODO(), s.GetName(), metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, s, fetched)
			}

			installPlans, err := o.client.OperatorsV1alpha1().InstallPlans(testNamespace).List(context.TODO(), metav1.ListOptions{})
			require.Len(t, installPlans.Items, len(tt.wantInstallPlans))

			haveIPs := make(map[string]v1alpha1.InstallPlan)
			for _, ip := range installPlans.Items {
				haveIPs[ip.GetName()] = ip
			}

			for _, ip := range tt.wantInstallPlans {
				have, ok := haveIPs[ip.GetName()]
				require.True(t, ok, "installplan %s missing", ip.GetName())
				require.Equal(t, have.Spec, ip.Spec)
				require.Equal(t, have.Status, ip.Status)
			}
		})
	}
}

type operatorBuilder interface {
	operator(ctx context.Context) (*Operator, error)
}

type objs struct {
	clientObjs []runtime.Object
	k8sObjs    []runtime.Object
}

type srnFields struct {
	clientOptions []clientfake.Option
	existingObjs  objs
	resolver      resolver.StepResolver
	reconciler    reconciler.RegistryReconcilerFactory
}

type srnArgs struct {
	namespace string
}

type srnTest struct {
	fields srnFields
	args   srnArgs
}

func (t srnTest) operator(ctx context.Context) (*Operator, error) {
	return NewFakeOperator(
		ctx,
		t.args.namespace,
		[]string{t.args.namespace},
		withClientObjs(t.fields.existingObjs.clientObjs...),
		withK8sObjs(t.fields.existingObjs.k8sObjs...),
		withFakeClientOptions(t.fields.clientOptions...),
		withResolver(t.fields.resolver),
	)
}

func benchOperator(ctx context.Context, test operatorBuilder, b *testing.B) (*Operator, error) {
	b.StartTimer()
	defer b.StopTimer()

	return test.operator(ctx)
}

func benchmarkSyncResolvingNamespace(test srnTest, b *testing.B) {
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()

		o, err := benchOperator(ctx, test, b)
		require.NoError(b, err)

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: test.args.namespace,
			},
		}
		require.NoError(b, o.syncResolvingNamespace(namespace))
	}
}

func BenchmarkSyncResolvingNamespace(b *testing.B) {
	ns := "default"
	name := "sub"
	benchmarkSyncResolvingNamespace(srnTest{
		fields: srnFields{
			clientOptions: []clientfake.Option{clientfake.WithSelfLinks(b)},
			existingObjs: objs{
				clientObjs: []runtime.Object{
					&v1alpha1.Subscription{
						ObjectMeta: metav1.ObjectMeta{
							Name:      name,
							Namespace: ns,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							CatalogSource:          "src",
							CatalogSourceNamespace: ns,
						},
						Status: v1alpha1.SubscriptionStatus{
							CurrentCSV: "",
							State:      "",
						},
					},
				},
			},
			reconciler: &fakes.FakeRegistryReconcilerFactory{
				ReconcilerForSourceStub: func(*v1alpha1.CatalogSource) reconciler.RegistryReconciler {
					return &fakes.FakeRegistryReconciler{
						CheckRegistryServerStub: func(*v1alpha1.CatalogSource) (bool, error) {
							return true, nil
						},
					}
				},
			},
			resolver: &fakes.FakeStepResolver{
				ResolveStepsStub: func(string, resolver.SourceQuerier) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
					steps := []*v1alpha1.Step{
						{
							Resolving: "csv.v.2",
							Resource: v1alpha1.StepResource{
								CatalogSource:          "src",
								CatalogSourceNamespace: ns,
								Group:                  v1alpha1.GroupName,
								Version:                v1alpha1.GroupVersion,
								Kind:                   v1alpha1.ClusterServiceVersionKind,
								Name:                   "csv.v.2",
								Manifest:               "{}",
							},
						},
					}
					subs := []*v1alpha1.Subscription{
						{
							TypeMeta: metav1.TypeMeta{
								Kind:       v1alpha1.SubscriptionKind,
								APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
							},
							ObjectMeta: metav1.ObjectMeta{
								Name:      name,
								Namespace: ns,
							},
							Spec: &v1alpha1.SubscriptionSpec{
								CatalogSource:          "src",
								CatalogSourceNamespace: ns,
							},
							Status: v1alpha1.SubscriptionStatus{
								CurrentCSV: "csv.v.2",
								State:      "SubscriptionStateAtLatest",
							},
						},
					}

					return steps, nil, subs, nil
				},
			},
		},
		args: srnArgs{
			namespace: ns,
		},
	}, b)
}
