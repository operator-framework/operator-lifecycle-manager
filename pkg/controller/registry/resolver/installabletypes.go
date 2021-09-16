package resolver

import (
	"fmt"
	"strings"
	"sync"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	operatorregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type BundleInstallable struct {
	identifier  solver.Identifier
	constraints []solver.Constraint

	Replaces string
}

func (i BundleInstallable) Identifier() solver.Identifier {
	return i.identifier
}

func (i BundleInstallable) Constraints() []solver.Constraint {
	return i.constraints
}

func (i *BundleInstallable) MakeProhibited() {
	i.constraints = append(i.constraints, solver.Prohibited())
}

func (i *BundleInstallable) AddConflict(id solver.Identifier) {
	i.constraints = append(i.constraints, solver.Conflict(id))
}

func (i *BundleInstallable) AddConstraint(c solver.Constraint) {
	i.constraints = append(i.constraints, c)
}

func (i *BundleInstallable) BundleSourceInfo() (string, string, cache.SourceKey, error) {
	info := strings.Split(i.identifier.String(), "/")
	// This should be enforced by Kube naming constraints
	if len(info) != 4 {
		return "", "", cache.SourceKey{}, fmt.Errorf("Unable to parse identifier %s for source info", i.identifier)
	}
	catalog := cache.SourceKey{
		Name:      info[0],
		Namespace: info[1],
	}
	channel := info[2]
	csvName := info[3]
	return csvName, channel, catalog, nil
}

func bundleId(bundle, channel string, catalog cache.SourceKey) solver.Identifier {
	return solver.IdentifierFromString(fmt.Sprintf("%s/%s/%s", catalog.String(), channel, bundle))
}

// ConstraintProvder knows how to provide solver constraints for a given cache entry.
type ConstraintProvider interface {
	// Constraints returns a set of solver constraints for a cache entry.
	Constraints(o *cache.Operator) ([]solver.Constraint, error)
}

// ConstraintProviderFunc allows a function to implement the ConstraintProvider interface.
type ConstraintProviderFunc func(o *cache.Operator) ([]solver.Constraint, error)

func (c ConstraintProviderFunc) Constraints(o *cache.Operator) ([]solver.Constraint, error) {
	return c(o)
}

// constraintProviderList provides aggregate constraints from a list of ConstraintProviders.
type constraintProviderList struct {
	mu        sync.RWMutex
	providers []ConstraintProvider
}

// add appends the given ConstraintProviders to the list aggregated over by the constraintProviderList.
// add is threadsafe.
func (c *constraintProviderList) add(providers ...ConstraintProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.providers = append(c.providers, providers...)
}

func (c *constraintProviderList) Constraints(o *cache.Operator) ([]solver.Constraint, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var constraints []solver.Constraint
	for _, provider := range c.providers {
		cons, err := provider.Constraints(o)
		if err != nil {
			return nil, err
		}

		constraints = append(constraints, cons...)
	}

	return constraints, nil
}

var (
	// systemConstraintProviders is the list of constraint providers used by all solvers.
	systemConstraintProviders = constraintProviderList{
		providers: []ConstraintProvider{
			freestandingCSVConstraint(),
			deprecatedConstraint(),
		},
	}
)

func freestandingCSVConstraint() ConstraintProviderFunc {
	return func(o *cache.Operator) ([]solver.Constraint, error) {
		if !(o.SourceInfo.Catalog.Virtual() && o.SourceInfo.Subscription == nil) {
			return nil, nil
		}

		// CSVs already associated with a Subscription
		// may be replaced, but freestanding CSVs must
		// appear in any solution.
		return []solver.Constraint{PrettyConstraint(
			solver.Mandatory(),
			fmt.Sprintf("clusterserviceversion %s exists and is not referenced by a subscription", o.Name),
		)}, nil
	}
}

func deprecatedConstraint() ConstraintProviderFunc {
	return func(o *cache.Operator) ([]solver.Constraint, error) {
		id := bundleId(o.Name, o.Channel(), o.SourceInfo.Catalog)
		for _, p := range o.Properties {
			if p.GetType() == operatorregistry.DeprecatedType {
				return []solver.Constraint{PrettyConstraint(
					solver.Prohibited(),
					fmt.Sprintf("bundle %s is deprecated", id),
				)}, nil
			}
		}

		return nil, nil
	}
}

// AddSystemConstraintProviders adds providers to the list of providers used system-wide, across all solvers.
func AddSystemConstraintProviders(providers ...ConstraintProvider) {
	systemConstraintProviders.add(providers...)
}

func NewBundleInstallableFromOperator(o *cache.Operator) (BundleInstallable, error) {
	if o.SourceInfo == nil {
		return BundleInstallable{}, fmt.Errorf("unable to resolve the source of bundle %s", o.Name)
	}

	constraints, err := systemConstraintProviders.Constraints(o)
	if err != nil {
		return BundleInstallable{}, err
	}

	return BundleInstallable{
		identifier:  bundleId(o.Name, o.Channel(), o.SourceInfo.Catalog),
		constraints: constraints,
	}, nil
}

type GenericInstallable struct {
	identifier  solver.Identifier
	constraints []solver.Constraint
}

func (i GenericInstallable) Identifier() solver.Identifier {
	return i.identifier
}

func (i GenericInstallable) Constraints() []solver.Constraint {
	return i.constraints
}

func NewInvalidSubscriptionInstallable(name string, reason string) solver.Installable {
	return GenericInstallable{
		identifier: solver.IdentifierFromString(fmt.Sprintf("subscription:%s", name)),
		constraints: []solver.Constraint{
			PrettyConstraint(solver.Mandatory(), fmt.Sprintf("subscription %s exists", name)),
			PrettyConstraint(solver.Prohibited(), reason),
		},
	}
}

func NewSubscriptionInstallable(name string, dependencies []solver.Identifier) solver.Installable {
	result := GenericInstallable{
		identifier: solver.IdentifierFromString(fmt.Sprintf("subscription:%s", name)),
		constraints: []solver.Constraint{
			PrettyConstraint(solver.Mandatory(), fmt.Sprintf("subscription %s exists", name)),
		},
	}

	if len(dependencies) == 0 {
		result.constraints = append(result.constraints, PrettyConstraint(solver.Dependency(), fmt.Sprintf("no operators found matching the criteria of subscription %s", name)))
		return result
	}

	s := make([]string, len(dependencies))
	for i, each := range dependencies {
		s[i] = each.String()
	}
	var req string
	if len(s) == 1 {
		req = s[0]
	} else {
		req = fmt.Sprintf("at least one of %s or %s", strings.Join(s[:len(s)-1], ", "), s[len(s)-1])
	}
	result.constraints = append(result.constraints, PrettyConstraint(solver.Dependency(dependencies...), fmt.Sprintf("subscription %s requires %s", name, req)))

	return result
}

func NewSingleAPIProviderInstallable(group, version, kind string, providers []solver.Identifier) solver.Installable {
	gvk := fmt.Sprintf("%s (%s/%s)", kind, group, version)
	result := GenericInstallable{
		identifier: solver.IdentifierFromString(gvk),
	}
	if len(providers) <= 1 {
		// The constraints are pointless without more than one provider.
		return result
	}
	result.constraints = append(result.constraints, PrettyConstraint(solver.Mandatory(), fmt.Sprintf("there can be only one provider of %s", gvk)))

	var s []string
	for _, p := range providers {
		s = append(s, p.String())
	}
	msg := fmt.Sprintf("%s and %s provide %s", strings.Join(s[:len(s)-1], ", "), s[len(s)-1], gvk)
	result.constraints = append(result.constraints, PrettyConstraint(solver.AtMost(1, providers...), msg))

	return result
}

func NewSinglePackageInstanceInstallable(pkg string, providers []solver.Identifier) solver.Installable {
	result := GenericInstallable{
		identifier: solver.IdentifierFromString(pkg),
	}
	if len(providers) <= 1 {
		// The constraints are pointless without more than one provider.
		return result
	}
	result.constraints = append(result.constraints, PrettyConstraint(solver.Mandatory(), fmt.Sprintf("there can be only one operator from package %s", pkg)))

	var s []string
	for _, p := range providers {
		s = append(s, p.String())
	}
	msg := fmt.Sprintf("%s and %s originate from package %s", strings.Join(s[:len(s)-1], ", "), s[len(s)-1], pkg)
	result.constraints = append(result.constraints, PrettyConstraint(solver.AtMost(1, providers...), msg))

	return result
}

func PrettyConstraint(c solver.Constraint, msg string) solver.Constraint {
	return prettyConstraint{
		Constraint: c,
		msg:        msg,
	}
}

type prettyConstraint struct {
	solver.Constraint
	msg string
}

func (pc prettyConstraint) String(_ solver.Identifier) string {
	return pc.msg
}
