package resolver

import (
	"time"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

type InstrumentedResolver struct {
	resolver              StepResolver
	successMetricsEmitter func(time.Duration)
	failureMetricsEmitter func(time.Duration)
}

var _ StepResolver = &InstrumentedResolver{}

func NewInstrumentedResolver(resolver StepResolver, successMetricsEmitter, failureMetricsEmitter func(time.Duration)) *InstrumentedResolver {
	return &InstrumentedResolver{
		resolver:              resolver,
		successMetricsEmitter: successMetricsEmitter,
		failureMetricsEmitter: failureMetricsEmitter,
	}
}

func (ir *InstrumentedResolver) ResolveSteps(namespace string, sourceQuerier SourceQuerier) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
	start := time.Now()
	steps, lookups, subs, err := ir.resolver.ResolveSteps(namespace, sourceQuerier)
	if err != nil {
		ir.failureMetricsEmitter(time.Now().Sub(start))
	} else {
		ir.successMetricsEmitter(time.Now().Sub(start))
	}
	return steps, lookups, subs, err
}

func (ir *InstrumentedResolver) Expire(key registry.CatalogKey) {
	ir.resolver.Expire(key)
}
