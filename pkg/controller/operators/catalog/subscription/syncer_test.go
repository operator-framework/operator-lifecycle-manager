package subscription

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
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
					&v1alpha1.Subscription{},
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
