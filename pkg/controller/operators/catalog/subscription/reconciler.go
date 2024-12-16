package subscription

import (
	"context"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

type Reconciler interface {
	Reconcile(ctx context.Context, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error)
}

type ReconcilerFunc func(ctx context.Context, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error)

func (r ReconcilerFunc) Reconcile(ctx context.Context, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	return r(ctx, sub)
}

type ReconcilerChain []Reconciler

func (r ReconcilerChain) Reconcile(ctx context.Context, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	var err error
	for _, rec := range r {
		if sub, err = rec.Reconcile(ctx, sub); err != nil || sub == nil {
			break
		}
	}
	return sub, err
}
