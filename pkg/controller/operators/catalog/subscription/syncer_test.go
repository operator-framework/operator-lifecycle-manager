package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"

	"github.com/stretchr/testify/require"

	listerv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
)

func TestSync(t *testing.T) {
	type args struct {
		sub       *v1alpha1.Subscription
		eventType kubestate.ResourceEventType
	}
	type want struct {
		err error
	}

	tests := []struct {
		description string
		args        args
		want        want
	}{
		{
			description: "v1alpha1/OK",
			args: args{
				eventType: kubestate.ResourceAdded,
				sub: &v1alpha1.Subscription{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-sub",
						Namespace: "test-namespace",
					},
				},
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

			nsResolveQueue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())
			syncer := subscriptionSyncer{
				logger:             logrus.New(),
				subscriptionLister: createFakeSubscriptionLister(ctx, []runtime.Object{tt.args.sub}),
				nsResolveQueue:     nsResolveQueue,
			}

			require.Equal(t, tt.want.err, syncer.Sync(ctx, kubestate.NewResourceEvent(tt.args.eventType, tt.args.sub)))
		})
	}
}

func createFakeSubscriptionLister(ctx context.Context, existingObjs []runtime.Object) listerv1alpha1.SubscriptionLister {
	// Create client fakes
	clientFake := fake.NewReactionForwardingClientsetDecorator(existingObjs)
	wakeupInterval := 5 * time.Minute

	// Create informers and register listers
	operatorsFactory := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(metav1.NamespaceAll))
	subInformer := operatorsFactory.Operators().V1alpha1().Subscriptions()

	stopChan := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stopChan)
	}()
	go subInformer.Informer().Run(stopChan)
	cache.WaitForCacheSync(stopChan, subInformer.Informer().HasSynced)
	return subInformer.Lister()
}
