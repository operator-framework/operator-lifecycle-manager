package catalog

import (
	"errors"
	"fmt"
	"testing"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/subscription/v1alpha1"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

func TestSyncSubscription(t *testing.T) {

	type initialState struct {
		findLatestCSVResult *csvv1alpha1.ClusterServiceVersion
		findLatestCSVError  error

		findReplacementCSVResult *csvv1alpha1.ClusterServiceVersion
		findReplacementCSVError  error

		createInstallPlanError  error
		updateSubscriptionError error

		getCSVResult *csvv1alpha1.ClusterServiceVersion
		getCSVError  error
	}
	type args struct {
		subscription *v1alpha1.Subscription
	}
	type expected struct {
		subscription *v1alpha1.Subscription
		err          error
	}
	table := []struct {
		name     string
		subName  string
		initial  initialState
		args     args
		expected expected
	}{
		{
			name:     "returns error for invalid subscription",
			subName:  "subscription is nil",
			args:     args{subscription: nil},
			expected: expected{nil, errors.New("invalid Subscription object: <nil>")},
		},
	}
	for _, tt := range table {
		testName := fmt.Sprintf("%s: %s", tt.name, tt.subName)
		t.Run(testName, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			var (
				ipClientMock           = NewMockInstallPlanInterface(ctrl)
				csvClientMock          = NewMockClusterServiceVersionInterface(ctrl)
				subscriptionClientMock = NewMockSubscriptionInterface(ctrl)
			)

			op := &Operator{
				ipClient:           ipClientMock,
				csvClient:          csvClientMock,
				subscriptionClient: subscriptionClientMock,
				namespace:          ns,
				sources:            map[string]catlib.Source{},
			}
			err := op.syncSubscription(tt.args.subscription)
			if tt.expected.err != nil {
				require.EqualError(t, err, tt.expected.err.Error())
			} else {
				require.Nil(t, err)
			}

		})
	}
}
