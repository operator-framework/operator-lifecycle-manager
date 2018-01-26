package catalog

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	ipv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/subscription/v1alpha1"
	catlib "github.com/coreos-inc/alm/pkg/catalog"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/diff"
)

type InstallPlanMatcher struct{ ip *ipv1alpha1.InstallPlan }

func MatchesInstallPlan(ip *ipv1alpha1.InstallPlan) gomock.Matcher {
	return &InstallPlanMatcher{ip}
}

func (e *InstallPlanMatcher) Matches(x interface{}) bool {
	ip, ok := x.(*ipv1alpha1.InstallPlan)
	if !ok {
		return false
	}
	eq := reflect.DeepEqual(e.ip, ip)
	if !eq {
		fmt.Printf("InstallPlans NOT EQUAL: %s\n", diff.ObjectDiff(e.ip, ip))
	}
	return eq
}

func (e *InstallPlanMatcher) String() string {
	return "matches expected InstallPlan"
}

type SubscriptionMatcher struct{ s *v1alpha1.Subscription }

func MatchesSubscription(s *v1alpha1.Subscription) gomock.Matcher {
	return &SubscriptionMatcher{s}
}

func (e *SubscriptionMatcher) Matches(x interface{}) bool {
	s, ok := x.(*v1alpha1.Subscription)
	if !ok {
		return false
	}
	eq := reflect.DeepEqual(*e.s, *s)
	if !eq {
		fmt.Printf("Subscriptions NOT EQUAL: %s\n", diff.ObjectReflectDiff(e.s, s))
	}
	return eq
}

func (e *SubscriptionMatcher) String() string {
	return "matches expected Subscription"
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
						AtCSV:         "latest-and-greatest",
					},
					Status: v1alpha1.SubscriptionStatus{
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
						AtCSV:         "latest-and-greatest",
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
					AtCSV:         "pending",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: &v1alpha1.InstallPlanReference{Name: "existing-install"},
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
					AtCSV:         "latest-and-greatest",
				},
				Status: v1alpha1.SubscriptionStatus{
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
						AtCSV:         "latest-and-greatest",
					},
					Status: v1alpha1.SubscriptionStatus{
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
					AtCSV:         "latest-and-greatest",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: nil,
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
						AtCSV:         "latest-and-greatest",
					},
					Status: v1alpha1.SubscriptionStatus{
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
					AtCSV:         "pending",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: nil,
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
					AtCSV:         "pending",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: nil,
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
					AtCSV:         "toupgrade",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: nil,
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
					AtCSV:         "toupgrade",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: nil,
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
					AtCSV:         "toupgrade",
				},
				Status: v1alpha1.SubscriptionStatus{
					Install: nil,
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
						AtCSV:         "next",
					},
					Status: v1alpha1.SubscriptionStatus{
						Install: nil,
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
			csvClientMock := NewMockClusterServiceVersionInterface(ctrl)
			if tt.expected.csvName != "" {
				csvClientMock.EXPECT().
					GetCSVByName(tt.expected.namespace, tt.expected.csvName).
					Return(tt.initial.getCSVResult, tt.initial.getCSVError)
			}

			ipClientMock := NewMockInstallPlanInterface(ctrl)
			if tt.expected.installPlan != nil {
				ipClientMock.EXPECT().
					CreateInstallPlan(MatchesInstallPlan(tt.expected.installPlan)).
					Return(tt.initial.createInstallPlanResult, tt.initial.createInstallPlanError)
			}

			if tt.expected.existingInstallPlanName != "" {
				ipClientMock.EXPECT().
					GetInstallPlanByName(tt.expected.namespace, tt.expected.existingInstallPlanName).
					Return(tt.initial.getInstallPlanResult, tt.initial.getInstallPlanError)
			}

			subscriptionClientMock := NewMockSubscriptionClientInterface(ctrl)
			if tt.expected.subscription != nil {
				subscriptionClientMock.EXPECT().
					UpdateSubscription(MatchesSubscription(tt.expected.subscription)).
					Return(nil, tt.initial.updateSubscriptionError)
			}

			catalogMock := NewMockSource(ctrl)
			if tt.expected.packageName != "" && tt.expected.channelName != "" {
				if tt.expected.csvName == "" {
					catalogMock.EXPECT().
						FindCSVForPackageNameUnderChannel(tt.expected.packageName, tt.expected.channelName).
						Return(tt.initial.findLatestCSVResult, tt.initial.findLatestCSVError)
				} else {
					catalogMock.EXPECT().
						FindReplacementCSVForPackageNameUnderChannel(tt.expected.packageName,
							tt.expected.channelName, tt.expected.csvName).
						Return(tt.initial.findReplacementCSVResult, tt.initial.findReplacementCSVError)
				}
			}

			op := &Operator{
				ipClient:           ipClientMock,
				csvClient:          csvClientMock,
				subscriptionClient: subscriptionClientMock,
				namespace:          "ns",
				sources: map[string]catlib.Source{
					tt.initial.catalogName: catalogMock,
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
