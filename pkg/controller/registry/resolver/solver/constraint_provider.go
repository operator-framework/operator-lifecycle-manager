package solver

import "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"

// ConstraintProvider knows how to provide solver constraints for a given cache entry.
// For instance, it could be used to surface additional constraints against an entry given some
// properties it may expose. E.g. olm.maxOpenShiftVersion could be checked against the cluster version
// and prohibit any entry that doesn't meet the requirement
type ConstraintProvider interface {
	// Constraints returns a set of solver constraints for a cache entry.
	Constraints(e *cache.Entry) ([]Constraint, error)
}

// ConstraintProviderFunc is a simple implementation of ConstraintProvider
type ConstraintProviderFunc func(e *cache.Entry) ([]Constraint, error)

func (c ConstraintProviderFunc) Constraints(e *cache.Entry) ([]Constraint, error) {
	return c(e)
}
