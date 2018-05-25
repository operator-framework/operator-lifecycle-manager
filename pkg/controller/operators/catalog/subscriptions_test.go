package catalog

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	core "k8s.io/client-go/testing"

	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	ipv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/subscription/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
)

func RequireActions(t *testing.T, expected, actual []core.Action) {
	require.EqualValues(t, len(expected), len(actual), "Expected\n\t%#v\ngot\n\t%#v", expected, actual)
	for i, a := range actual {
		e := expected[i]
		require.True(t, equality.Semantic.DeepEqual(e, a), "Expected\n\t%#v\ngot\n\t%#v", e, a)
	}
}

func TestSyncSubscription(t *testing.T) {
	var (
		nowTime      = metav1.Date(2018, time.January, 26, 20, 40, 0, 0, time.UTC)
		earlierTime  = metav1.Date(2018, time.January, 19, 20, 20, 0, 0, time.UTC)
		earliestTime = metav1.Date(2017, time.December, 10, 12, 00, 0, 0, time.UTC)
	)
	timeNow = func() metav1.Time { return nowTime }

	type initial struct {
		catalogName         string
		sourcesLastUpdate   metav1.Time
		findLatestCSVResult *csvv1alpha1.ClusterServiceVersion
		findLatestCSVError  error

		findReplacementCSVResult *csvv1alpha1.ClusterServiceVersion
		findReplacementCSVError  error

		getInstallPlanResult *ipv1alpha1.InstallPlan
		getInstallPlanError  error

		createInstallPlanResult *ipv1alpha1.InstallPlan
		createInstallPlanError  error

		updateSubscriptionError error

		getCSVResult *csvv1alpha1.ClusterServiceVersion
		getCSVError  error
	}
	type args struct {
		subscription *v1alpha1.Subscription
	}
	type expected struct {
		csvName                 string
		namespace               string
		packageName             string
		channelName             string
		subscription            *v1alpha1.Subscription
		installPlan             *ipv1alpha1.InstallPlan
		existingInstallPlanName string
		err                     string
	}
	table := []struct {
		name     string
		subName  string
		initial  initial
		args     args
		expected expected
	}{
		{
			name:     "invalid input",
			subName:  "nil subscription",
			args:     args{subscription: nil},
			expected: expected{err: "invalid Subscription object: <nil>"},
		},
		{
			name:     "invalid input",
			subName:  "subscription.Spec is nil",
			args:     args{subscription: &v1alpha1.Subscription{}},
			expected: expected{err: "invalid Subscription object: <nil>"},
		},
		{
			name:    "invalid input",
			subName: "no catalog source exists for subscription's specified catalog name",
			initial: initial{catalogName: "sparkly-flying-unicorns"},
			args: args{subscription: &v1alpha1.Subscription{
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
				},
			}},
			expected: expected{err: "unknown catalog source flying-unicorns"},
		},
		{
			name:    "no updates",
			subName: "subscription synced already since last catalog update and at latest CSV",
			initial: initial{
				catalogName:       "flying-unicorns",
				sourcesLastUpdate: earliestTime,
			},
			args: args{subscription: &v1alpha1.Subscription{
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
				},
				Status: v1alpha1.SubscriptionStatus{
					LastUpdated: earlierTime,
					State:       v1alpha1.SubscriptionStateAtLatest,
				},
			}},
			expected: expected{},
		},
		{
			name:    "no updates",
			subName: "subscription synced already since last catalog update but CSV install pending",
			initial: initial{
				catalogName: "flying-unicorns",
				findLatestCSVResult: &csvv1alpha1.ClusterServiceVersion{
					TypeMeta: metav1.TypeMeta{
						Kind:       csvv1alpha1.ClusterServiceVersionKind,
						APIVersion: csvv1alpha1.ClusterServiceVersionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "latest-and-greatest",
					},
				},
				getInstallPlanResult: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name: "existing-install",
					},
				},
				sourcesLastUpdate: earliestTime,
			},
			args: args{subscription: &v1alpha1.Subscription{
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV:  "latest-and-greatest",
					LastUpdated: earliestTime,
					State:       v1alpha1.SubscriptionStateUpgradePending,
					Install: &v1alpha1.InstallPlanReference{
						Kind:       ipv1alpha1.InstallPlanKind,
						APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
						Name:       "existing-install",
					},
				},
			}},
			expected: expected{
				csvName: "latest-and-greatest",
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV:  "latest-and-greatest",
						LastUpdated: earliestTime,
						State:       v1alpha1.SubscriptionStateUpgradePending,
						Install: &v1alpha1.InstallPlanReference{
							Kind:       ipv1alpha1.InstallPlanKind,
							APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
							Name:       "existing-install",
						},
					},
				},
				err: "",
			},
		},
		{
			name:    "clean install",
			subName: "catalog error",
			initial: initial{
				catalogName:        "flying-unicorns",
				findLatestCSVError: errors.New("CatErr"),
			},
			args: args{subscription: &v1alpha1.Subscription{
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
			}},
			expected: expected{
				packageName: "rainbows",
				channelName: "magical",
				err:         "failed to find CSV for package rainbows in channel magical: CatErr",
			},
		},
		{
			name:    "clean install",
			subName: "catalog returns nil csv",
			initial: initial{
				catalogName:         "flying-unicorns",
				findLatestCSVResult: nil,
			},
			args: args{subscription: &v1alpha1.Subscription{
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
			}},
			expected: expected{
				packageName: "rainbows",
				channelName: "magical",
				err:         "failed to find CSV for package rainbows in channel magical: nil CSV",
			},
		},
		{
			name:    "clean install",
			subName: "successfully sets latest version",
			initial: initial{
				catalogName: "flying-unicorns",
				findLatestCSVResult: &csvv1alpha1.ClusterServiceVersion{
					TypeMeta: metav1.TypeMeta{
						Kind:       csvv1alpha1.ClusterServiceVersionKind,
						APIVersion: csvv1alpha1.ClusterServiceVersionAPIVersion,
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "latest-and-greatest",
					},
				},
				sourcesLastUpdate: earlierTime,
			},
			args: args{subscription: &v1alpha1.Subscription{
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					LastUpdated: earliestTime,
				},
			}},
			expected: expected{
				packageName: "rainbows",
				channelName: "magical",
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV:  "latest-and-greatest",
						LastUpdated: earliestTime,
						Install:     nil,
						State:       v1alpha1.SubscriptionStateUpgradeAvailable,
					},
				},
				err: "",
			},
		},
		{
			name:    "clean install",
			subName: "successfully sets starting version if specified",
			initial: initial{
				catalogName:       "flying-unicorns",
				sourcesLastUpdate: earlierTime,
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-subscription",
					Namespace: "fairy-land",
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
					StartingCSV:   "wayback",
				},
				Status: v1alpha1.SubscriptionStatus{
					LastUpdated: earliestTime,
					Install:     nil,
				},
			}},
			expected: expected{
				namespace: "fairy-land",
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "fairy-land",
						Name:      "test-subscription",
						Labels:    map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
						StartingCSV:   "wayback",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV:  "wayback",
						LastUpdated: earliestTime,
						Install:     nil,
						State:       v1alpha1.SubscriptionStateUpgradeAvailable,
					},
				},
				err: "",
			},
		},
		{
			name:    "install in progress",
			subName: "NoOp",
			initial: initial{
				catalogName:  "flying-unicorns",
				getCSVResult: nil,
				getInstallPlanResult: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "fairy-land",
						Name:      "existing-install",
					},
				},
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "pending",
					Install: &v1alpha1.InstallPlanReference{
						Kind:       ipv1alpha1.InstallPlanKind,
						APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
						Name:       "existing-install",
					},
				},
			}},
			expected: expected{
				existingInstallPlanName: "existing-install",
				csvName:                 "pending",
				namespace:               "fairy-land",
				err:                     "",
			},
		},
		{
			name:    "no csv or installplan",
			subName: "get installplan error",
			initial: initial{
				catalogName:         "flying-unicorns",
				getCSVResult:        nil,
				getCSVError:         errors.New("GetCSVError"),
				getInstallPlanError: errors.New("GetInstallPlanError"),
				createInstallPlanResult: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name: "installplan-1",
						UID:  types.UID("UID-OK"),
					},
				},
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "latest-and-greatest",
					Install: &v1alpha1.InstallPlanReference{
						Kind:       ipv1alpha1.InstallPlanKind,
						APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
						Name:       "dead-install",
					},
				},
			}},
			expected: expected{
				csvName:                 "latest-and-greatest",
				existingInstallPlanName: "dead-install",
				namespace:               "fairy-land",
				installPlan: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "install-latest-and-greatest-",
						Namespace:    "fairy-land",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "app.coreos.com/v1alpha1",
								Kind:       "Subscription-v1",
								Name:       "test-subscription",
								UID:        types.UID("subscription-uid"),
							},
						},
					},
					Spec: ipv1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{"latest-and-greatest"},
						Approval:                   ipv1alpha1.ApprovalAutomatic,
					},
				},
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Labels:    map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
						Namespace: "fairy-land",
						Name:      "test-subscription",
						UID:       types.UID("subscription-uid"),
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "latest-and-greatest",
						Install: &v1alpha1.InstallPlanReference{
							Kind:       ipv1alpha1.InstallPlanKind,
							APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
							UID:        types.UID("UID-OK"),
							Name:       "installplan-1",
						},
						State: v1alpha1.SubscriptionStateUpgradePending,
					},
				},
				err: "",
			},
		},
		{
			name:    "no csv or installplan",
			subName: "creates installplan successfully",
			initial: initial{
				catalogName:  "flying-unicorns",
				getCSVResult: nil,
				createInstallPlanResult: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name: "installplan-1",
						UID:  types.UID("UID-OK"),
					},
				},
				createInstallPlanError: nil,
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "latest-and-greatest",
					Install:    nil,
				},
			}},
			expected: expected{
				installPlan: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "install-latest-and-greatest-",
						Namespace:    "fairy-land",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "app.coreos.com/v1alpha1",
								Kind:       "Subscription-v1",
								Name:       "test-subscription",
								UID:        types.UID("subscription-uid"),
							},
						},
					},
					Spec: ipv1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{"latest-and-greatest"},
						Approval:                   ipv1alpha1.ApprovalAutomatic,
					},
				},
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Labels:    map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
						Namespace: "fairy-land",
						Name:      "test-subscription",
						UID:       types.UID("subscription-uid"),
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "latest-and-greatest",
						Install: &v1alpha1.InstallPlanReference{
							Kind:       ipv1alpha1.InstallPlanKind,
							APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
							UID:        types.UID("UID-OK"),
							Name:       "installplan-1",
						},
						State: v1alpha1.SubscriptionStateUpgradePending,
					},
				},
				csvName:   "latest-and-greatest",
				namespace: "fairy-land",
				err:       "",
			},
		},
		{
			name:    "no csv or installplan",
			subName: "creates installplan successfully with manual approval",
			initial: initial{
				catalogName:  "flying-unicorns",
				getCSVResult: nil,
				createInstallPlanResult: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						Name: "installplan-1",
						UID:  types.UID("UID-OK"),
					},
				},
				createInstallPlanError: nil,
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource:       "flying-unicorns",
					Package:             "rainbows",
					Channel:             "magical",
					InstallPlanApproval: ipv1alpha1.ApprovalManual,
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "latest-and-greatest",
					Install:    nil,
				},
			}},
			expected: expected{
				installPlan: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "install-latest-and-greatest-",
						Namespace:    "fairy-land",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "app.coreos.com/v1alpha1",
								Kind:       "Subscription-v1",
								Name:       "test-subscription",
								UID:        types.UID("subscription-uid"),
							},
						},
					},
					Spec: ipv1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{"latest-and-greatest"},
						Approval:                   ipv1alpha1.ApprovalManual,
					},
				},
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Labels:    map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
						Namespace: "fairy-land",
						Name:      "test-subscription",
						UID:       types.UID("subscription-uid"),
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource:       "flying-unicorns",
						Package:             "rainbows",
						Channel:             "magical",
						InstallPlanApproval: ipv1alpha1.ApprovalManual,
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "latest-and-greatest",
						Install: &v1alpha1.InstallPlanReference{
							Kind:       ipv1alpha1.InstallPlanKind,
							APIVersion: ipv1alpha1.SchemeGroupVersion.String(),
							UID:        types.UID("UID-OK"),
							Name:       "installplan-1",
						},
						State: v1alpha1.SubscriptionStateUpgradePending,
					},
				},
				csvName:   "latest-and-greatest",
				namespace: "fairy-land",
				err:       "",
			},
		},
		{
			name:    "no csv or installplan",
			subName: "installplan error",
			initial: initial{
				catalogName:            "flying-unicorns",
				getCSVResult:           nil,
				getCSVError:            errors.New("GetCSVError"),
				createInstallPlanError: errors.New("CreateInstallPlanError"),
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "pending",
					Install:    nil,
				},
			}},
			expected: expected{
				csvName:   "pending",
				namespace: "fairy-land",
				installPlan: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "install-pending-",
						Namespace:    "fairy-land",
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "app.coreos.com/v1alpha1",
								Kind:       "Subscription-v1",
								Name:       "test-subscription",
								UID:        types.UID("subscription-uid"),
							},
						},
					},
					Spec: ipv1alpha1.InstallPlanSpec{
						ClusterServiceVersionNames: []string{"pending"},
						Approval:                   ipv1alpha1.ApprovalAutomatic,
					},
				},
				err: "failed to ensure current CSV pending installed: CreateInstallPlanError",
			},
		},
		{
			name:    "csv installed",
			subName: "catalog error",
			initial: initial{
				catalogName: "flying-unicorns",
				getCSVResult: &csvv1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "toupgrade",
						Namespace: "fairy-land",
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       csvv1alpha1.ClusterServiceVersionKind,
						APIVersion: csvv1alpha1.ClusterServiceVersionAPIVersion,
					},
				},
				findReplacementCSVError: errors.New("CatalogError"),
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "toupgrade",
					Install:    nil,
				},
			}},
			expected: expected{
				csvName:     "toupgrade",
				namespace:   "fairy-land",
				packageName: "rainbows",
				channelName: "magical",
				err:         "failed to lookup replacement CSV for toupgrade: CatalogError",
			},
		},
		{
			name:    "csv installed",
			subName: "catalog nil replacement",
			initial: initial{
				catalogName: "flying-unicorns",
				getCSVResult: &csvv1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "toupgrade",
						Namespace: "fairy-land",
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       csvv1alpha1.ClusterServiceVersionKind,
						APIVersion: csvv1alpha1.ClusterServiceVersionAPIVersion,
					},
				},
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "toupgrade",
					Install:    nil,
				},
			}},
			expected: expected{
				csvName:     "toupgrade",
				namespace:   "fairy-land",
				packageName: "rainbows",
				channelName: "magical",
				err:         "nil replacement CSV for toupgrade returned from catalog",
			},
		},
		{
			name:    "csv installed",
			subName: "sets upgrade version",
			initial: initial{
				catalogName: "flying-unicorns",
				getCSVResult: &csvv1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "toupgrade",
						Namespace: "fairy-land",
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       csvv1alpha1.ClusterServiceVersionKind,
						APIVersion: csvv1alpha1.ClusterServiceVersionAPIVersion,
					},
				},
				findReplacementCSVResult: &csvv1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "next",
					},
				},
			},
			args: args{subscription: &v1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fairy-land",
					Name:      "test-subscription",
					UID:       types.UID("subscription-uid"),
				},
				Spec: &v1alpha1.SubscriptionSpec{
					CatalogSource: "flying-unicorns",
					Package:       "rainbows",
					Channel:       "magical",
				},
				Status: v1alpha1.SubscriptionStatus{
					CurrentCSV: "toupgrade",
					Install:    nil,
				},
			}},
			expected: expected{
				csvName:     "toupgrade",
				namespace:   "fairy-land",
				packageName: "rainbows",
				channelName: "magical",
				subscription: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "fairy-land",
						Name:      "test-subscription",
						UID:       types.UID("subscription-uid"),
						Labels:    map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"},
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "next",
						Install:    nil,
						State:      v1alpha1.SubscriptionStateUpgradeAvailable,
					},
				},
			},
		},
	}
	for _, tt := range table {
		testName := fmt.Sprintf("%s: %s", tt.name, tt.subName)
		t.Run(testName, func(t *testing.T) {
			// configure cluster state
			existingObjects := []runtime.Object{}
			expectedActions := []core.Action{}

			if tt.initial.getCSVResult != nil {
				existingObjects = append(existingObjects, tt.initial.getCSVResult)
			}
			if tt.initial.getInstallPlanResult != nil {
				existingObjects = append(existingObjects, tt.initial.getInstallPlanResult)
			}
			if tt.args.subscription != nil {
				existingObjects = append(existingObjects, tt.args.subscription)
			}

			clientFake := fake.NewSimpleClientset(existingObjects...)

			// configure expected actions
			if tt.expected.csvName != "" {
				expectedActions = append(expectedActions,
					core.NewGetAction(
						schema.GroupVersionResource{Group: "app.coreos.com", Version: "v1alpha1", Resource: "clusterserviceversion-v1s"},
						tt.expected.namespace,
						tt.expected.csvName,
					),
				)
			}

			if tt.initial.getInstallPlanError != nil {
				expectedActions = append(expectedActions,
					core.NewGetAction(
						schema.GroupVersionResource{Group: "app.coreos.com", Version: "v1alpha1", Resource: "installplan-v1s"},
						tt.args.subscription.GetNamespace(),
						tt.args.subscription.Status.Install.Name,
					),
				)
			}

			if tt.expected.installPlan != nil {
				expectedActions = append(expectedActions,
					core.NewCreateAction(
						schema.GroupVersionResource{Group: "app.coreos.com", Version: "v1alpha1", Resource: "installplan-v1s"},
						tt.expected.namespace,
						tt.expected.installPlan,
					),
				)
			}

			if tt.args.subscription != nil {
				if tt.args.subscription.Status.Install != nil && tt.initial.getInstallPlanError == nil {
					expectedActions = append(expectedActions,
						core.NewGetAction(
							schema.GroupVersionResource{Group: "app.coreos.com", Version: "v1alpha1", Resource: "installplan-v1s"},
							tt.args.subscription.GetNamespace(),
							tt.args.subscription.Status.Install.Name,
						),
					)
				}
			}

			// fake api calls
			if tt.initial.getCSVError != nil {
				clientFake.PrependReactor("get", "clusterserviceversion-v1s", func(action core.Action) (bool, runtime.Object, error) {
					if action.(core.GetAction).GetName() != tt.expected.csvName {
						return false, nil, nil
					}
					return true, nil, tt.initial.getCSVError
				})

			}

			if tt.initial.getInstallPlanError != nil {
				clientFake.PrependReactor("get", "installplan-v1s", func(action core.Action) (bool, runtime.Object, error) {
					if action.(core.GetAction).GetName() != tt.expected.existingInstallPlanName {
						return false, nil, nil
					}
					return true, nil, tt.initial.getInstallPlanError
				})
			}

			if tt.initial.updateSubscriptionError != nil {
				clientFake.PrependReactor("update", "subscription-v1s", func(action core.Action) (bool, runtime.Object, error) {
					return true, nil, tt.initial.updateSubscriptionError
				})
			}

			if tt.initial.createInstallPlanResult != nil {
				clientFake.PrependReactor("create", "installplan-v1s", func(action core.Action) (bool, runtime.Object, error) {
					return true, tt.initial.createInstallPlanResult, nil
				})
			}
			if tt.initial.createInstallPlanError != nil {
				clientFake.PrependReactor("create", "installplan-v1s", func(action core.Action) (bool, runtime.Object, error) {
					return true, nil, tt.initial.createInstallPlanError
				})
			}

			// fake catalog
			catalogFake := new(fakes.FakeSource)
			if tt.expected.packageName != "" && tt.expected.channelName != "" {
				if tt.expected.csvName == "" {
					defer func() {
						require.Equal(t, 1, catalogFake.FindCSVForPackageNameUnderChannelCallCount())
						pkg, chnl := catalogFake.FindCSVForPackageNameUnderChannelArgsForCall(0)
						require.Equal(t, tt.expected.packageName, pkg)
						require.Equal(t, tt.expected.channelName, chnl)
					}()

					catalogFake.FindCSVForPackageNameUnderChannelReturns(tt.initial.findLatestCSVResult, tt.initial.findLatestCSVError)
				} else {
					defer func() {
						require.Equal(t, 1, catalogFake.FindReplacementCSVForPackageNameUnderChannelCallCount())
						pkg, chnl, csvName := catalogFake.FindReplacementCSVForPackageNameUnderChannelArgsForCall(0)
						require.Equal(t, tt.expected.packageName, pkg)
						require.Equal(t, tt.expected.channelName, chnl)
						require.Equal(t, tt.expected.csvName, csvName)
					}()
					catalogFake.FindReplacementCSVForPackageNameUnderChannelReturns(tt.initial.findReplacementCSVResult, tt.initial.findReplacementCSVError)
				}
			}

			op := &Operator{
				client:    clientFake,
				namespace: "ns",
				sources: map[string]registry.Source{
					tt.initial.catalogName: catalogFake,
				},
				sourcesLastUpdate: tt.initial.sourcesLastUpdate,
			}

			// run subscription sync
			sub, err := op.syncSubscription(tt.args.subscription)
			if tt.expected.err != "" {
				require.EqualError(t, err, tt.expected.err)
			} else {
				require.Nil(t, err)
			}

			// verify subscription changes happened correctly
			if tt.expected.subscription != nil {
				require.NoError(t, err)
				require.Equal(t, tt.expected.subscription.Spec, sub.Spec)

				// If we fail to update the subscription these won't be set
				if tt.initial.updateSubscriptionError == nil {
					require.Equal(t, map[string]string{PackageLabel: "rainbows", CatalogLabel: "flying-unicorns", ChannelLabel: "magical"}, sub.GetLabels())
					require.Equal(t, tt.expected.subscription.Status, sub.Status)
				}
			}

			// verify api interactions
			RequireActions(t, expectedActions, clientFake.Actions())
		})

	}
}
