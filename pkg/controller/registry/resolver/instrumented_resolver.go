package resolver

import (
	"time"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

type InstrumentedResolver struct {
	operatorsV1alpha1Resolver Resolver
	successMetricsEmitter     func(time.Duration)
	failureMetricsEmitter     func(time.Duration)
}

var _ Resolver = &OperatorsV1alpha1Resolver{}

func NewInstrumentedResolver(resolver Resolver, successMetricsEmitter, failureMetricsEmitter func(time.Duration)) *InstrumentedResolver {
	return &InstrumentedResolver{
		operatorsV1alpha1Resolver: resolver,
		successMetricsEmitter:     successMetricsEmitter,
		failureMetricsEmitter:     failureMetricsEmitter,
	}
}

func (ir *InstrumentedResolver) ResolveSteps(namespace string, sourceQuerier SourceQuerier) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
	start := time.Now()
	steps, lookups, subs, err := ir.operatorsV1alpha1Resolver.ResolveSteps(namespace, sourceQuerier)
	if err != nil {
		ir.failureMetricsEmitter(time.Now().Sub(start))
	} else {
		ir.successMetricsEmitter(time.Now().Sub(start))
	}
	return steps, lookups, subs, err
}
