package catalog

import (
	"errors"
	"fmt"
	"testing"
	"time"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	ipv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/subscription/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client/clientfakes"
	"github.com/coreos-inc/alm/pkg/registry"
	"github.com/coreos-inc/alm/pkg/registry/registryfakes"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

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
			subName: "subscription synced already since last catalog update",
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
				},
			}},
			expected: expected{},
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
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV:  "latest-and-greatest",
						LastUpdated: earliestTime,
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
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
						StartingCSV:   "wayback",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV:  "wayback",
						LastUpdated: earliestTime,
					},
				},
				err: "",
			},
		},
		{
			name:    "clean install",
			subName: "returns errors updating subscription",
			initial: initial{
				catalogName: "flying-unicorns",
				findLatestCSVResult: &csvv1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name: "latest-and-greatest",
					},
				},
				updateSubscriptionError: errors.New("UpdateErr"),
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
				subscription: &v1alpha1.Subscription{
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "latest-and-greatest",
					},
				},
				err: "UpdateErr",
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
						Name: "existing-install",
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
					Install:    &v1alpha1.InstallPlanReference{Name: "existing-install"},
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
						Name: "dead-install",
					},
				},
			}},
			expected: expected{
				csvName:                 "latest-and-greatest",
				existingInstallPlanName: "dead-install",
				namespace:               "fairy-land",
				installPlan: &ipv1alpha1.InstallPlan{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "install-latest-and-greatest",
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
				},
				subscription: &v1alpha1.Subscription{
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
							UID:  types.UID("UID-OK"),
							Name: "installplan-1",
						},
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
						GenerateName: "install-latest-and-greatest",
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
				},
				subscription: &v1alpha1.Subscription{
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
							UID:  types.UID("UID-OK"),
							Name: "installplan-1",
						},
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
						GenerateName: "install-pending",
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
				},
				err: "failed to ensure current CSV pending installed: CreateInstallPlanError",
			},
		},
		{
			name:    "no csv or installplan",
			subName: "installplan nil",
			initial: initial{
				catalogName:             "flying-unicorns",
				getCSVResult:            nil,
				getCSVError:             errors.New("GetCSVError"),
				createInstallPlanError:  nil,
				createInstallPlanResult: nil,
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
						GenerateName: "install-pending",
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
				},
				err: "unexpected installplan returned by k8s api on create: <nil>",
			},
		},
		{
			name:    "csv installed",
			subName: "catalog error",
			initial: initial{
				catalogName:             "flying-unicorns",
				getCSVResult:            &csvv1alpha1.ClusterServiceVersion{},
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
				catalogName:  "flying-unicorns",
				getCSVResult: &csvv1alpha1.ClusterServiceVersion{},
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
				catalogName:  "flying-unicorns",
				getCSVResult: &csvv1alpha1.ClusterServiceVersion{},
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
					},
					Spec: &v1alpha1.SubscriptionSpec{
						CatalogSource: "flying-unicorns",
						Package:       "rainbows",
						Channel:       "magical",
					},
					Status: v1alpha1.SubscriptionStatus{
						CurrentCSV: "next",
						Install:    nil,
					},
				},
			},
		},
	}
	for _, tt := range table {
		testName := fmt.Sprintf("%s: %s", tt.name, tt.subName)
		t.Run(testName, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			csvClientFake := new(clientfakes.FakeClusterServiceVersionInterface)
			if tt.expected.csvName != "" {
				defer func() {
					require.Equal(t, 1, csvClientFake.GetCSVByNameCallCount())
					ns, name := csvClientFake.GetCSVByNameArgsForCall(0)
					require.Equal(t, tt.expected.namespace, ns)
					require.Equal(t, tt.expected.csvName, name)
				}()
				csvClientFake.GetCSVByNameReturns(tt.initial.getCSVResult, tt.initial.getCSVError)
			}

			ipClientFake := new(clientfakes.FakeInstallPlanInterface)
			if tt.expected.installPlan != nil {
				defer func() {
					require.Equal(t, 1, ipClientFake.CreateInstallPlanCallCount())
					ip := ipClientFake.CreateInstallPlanArgsForCall(0)
					require.Equal(t, tt.expected.installPlan, ip)
				}()
				ipClientFake.CreateInstallPlanReturns(tt.initial.createInstallPlanResult, tt.initial.createInstallPlanError)
			}

			if tt.expected.existingInstallPlanName != "" {
				defer func() {
					require.Equal(t, 1, ipClientFake.GetInstallPlanByNameCallCount())
					ns, name := ipClientFake.GetInstallPlanByNameArgsForCall(0)
					require.Equal(t, tt.expected.namespace, ns)
					require.Equal(t, tt.expected.existingInstallPlanName, name)
				}()
				ipClientFake.GetInstallPlanByNameReturns(tt.initial.getInstallPlanResult, tt.initial.getInstallPlanError)
			}

			subscriptionClientFake := new(clientfakes.FakeSubscriptionClientInterface)
			if tt.expected.subscription != nil {
				defer func() {
					require.Equal(t, 1, subscriptionClientFake.UpdateSubscriptionCallCount())
					sub := subscriptionClientFake.UpdateSubscriptionArgsForCall(0)
					require.Equal(t, tt.expected.subscription, sub)
				}()
				subscriptionClientFake.UpdateSubscriptionReturns(nil, tt.initial.updateSubscriptionError)
			}

			catalogFake := new(registryfakes.FakeSource)
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
				ipClient:           ipClientFake,
				csvClient:          csvClientFake,
				subscriptionClient: subscriptionClientFake,
				namespace:          "ns",
				sources: map[string]registry.Source{
					tt.initial.catalogName: catalogFake,
				},
				sourcesLastUpdate: tt.initial.sourcesLastUpdate,
			}

			err := op.syncSubscription(tt.args.subscription)
			if tt.expected.err != "" {
				require.EqualError(t, err, tt.expected.err)
			} else {
				require.Nil(t, err)
			}
		})

	}
}
