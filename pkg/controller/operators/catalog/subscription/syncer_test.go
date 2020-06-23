package subscription

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
)

const (
	name             = "test-subscription"
	packageName      = "test-package"
	channel          = "test-channel"
	installedCSVName = "test-csv"
)

func TestSync(t *testing.T) {
	type fields struct {
		syncer kubestate.Syncer
	}
	type args struct {
		event kubestate.ResourceEvent
	}
	type want struct {
		err           error
		subscriptions []v1alpha1.Subscription
	}

	tests := []struct {
		description string
		fields      fields
		args        args
		want        want
	}{
		{
			description: "v1alpha1/OK",
			fields: fields{
				syncer: &subscriptionSyncer{
					logger: logrus.New(),
				},
			},
			args: args{
				event: kubestate.NewResourceEvent(
					kubestate.ResourceAdded,
					&v1alpha1.Subscription{
						TypeMeta: metav1.TypeMeta{
							Kind:       v1alpha1.SubscriptionKind,
							APIVersion: v1alpha1.SubscriptionCRDAPIVersion,
						},
						ObjectMeta: metav1.ObjectMeta{
							Name: name,
						},
						Spec: &v1alpha1.SubscriptionSpec{
							Package: packageName,
							Channel: channel,
						},
						Status: v1alpha1.SubscriptionStatus{
							InstalledCSV: installedCSVName,
						},
					},
				),
			},
			want: want{
				err: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			require.Equal(t, tt.want.err, tt.fields.syncer.Sync(ctx, tt.args.event))
		})
	}

}
