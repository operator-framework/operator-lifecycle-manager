package resolver

import (
	"fmt"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
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

func (i *BundleInstallable) AddDependency(dependencies []solver.Identifier) {
	i.constraints = append(i.constraints, solver.Dependency(dependencies...))
}

func (i *BundleInstallable) BundleSourceInfo() (string, string, registry.CatalogKey, error) {
	info := strings.Split(string(i.identifier), "/")
	// This should be enforced by Kube naming constraints
	if len(info) != 4 {
		return "", "", registry.CatalogKey{}, fmt.Errorf("Unable to parse identifier %s for source info", i.identifier)
	}
	catalog := registry.CatalogKey{
		Name:      info[0],
		Namespace: info[1],
	}
	channel := info[2]
	csvName := info[3]
	return csvName, channel, catalog, nil
}

func bundleId(bundle, channel string, catalog registry.CatalogKey) solver.Identifier {
	return solver.Identifier(fmt.Sprintf("%s/%s/%s", catalog.String(), channel, bundle))
}

func NewBundleInstallable(bundle, channel string, catalog registry.CatalogKey, constraints ...solver.Constraint) BundleInstallable {
	return BundleInstallable{
		identifier:  bundleId(bundle, channel, catalog),
		constraints: constraints,
	}
}

func NewSubscriptionInstallable(pkg string) SubscriptionInstallable {
	return SubscriptionInstallable{
		identifier:  solver.Identifier(pkg),
		constraints: []solver.Constraint{solver.Mandatory()},
	}
}

type SubscriptionInstallable struct {
	identifier  solver.Identifier
	constraints []solver.Constraint
}

func (r SubscriptionInstallable) Identifier() solver.Identifier {
	return r.identifier
}

func (r SubscriptionInstallable) Constraints() []solver.Constraint {
	return r.constraints
}

func (r *SubscriptionInstallable) AddDependency(dependencies []solver.Identifier) {
	r.constraints = append(r.constraints, solver.Dependency(dependencies...))
}
