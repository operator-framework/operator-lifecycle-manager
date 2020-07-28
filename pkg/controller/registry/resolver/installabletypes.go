package resolver

import (
	"fmt"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/sat"
)

type BundleInstallable struct {
	identifier  sat.Identifier
	constraints []sat.Constraint
}

func (i BundleInstallable) Identifier() sat.Identifier {
	return i.identifier
}

func (i BundleInstallable) Constraints() []sat.Constraint {
	return i.constraints
}

func (i *BundleInstallable) MakeProhibited() {
	i.constraints = append(i.constraints, sat.Prohibited())
}

func (i *BundleInstallable) AddDependency(dependencies []sat.Identifier) {
	i.constraints = append(i.constraints, sat.Dependency(dependencies...))
}

func (i *BundleInstallable) AddDependencyFromSet(dependencySet map[sat.Identifier]struct{}) {
	dependencies := make([]sat.Identifier, 0)
	for dep := range dependencySet {
		dependencies = append(dependencies, dep)
	}
	i.constraints = append(i.constraints, sat.Dependency(dependencies...))
}

func (i *BundleInstallable) AddWeight(weight int) {
	i.constraints = append(i.constraints, sat.Weight(weight))
}

func (i *BundleInstallable) BundleSourceInfo() (string, string, CatalogKey, error) {
	info := strings.Split(string(i.identifier), "/")
	// This should be enforced by Kube naming constraints
	if len(info) != 4 {
		return "", "", CatalogKey{}, fmt.Errorf("Unable to parse identifier %s for source info", i.identifier)
	}
	catalog := CatalogKey{
		Name:      info[0],
		Namespace: info[1],
	}
	channel := info[2]
	csvName := info[3]
	return csvName, channel, catalog, nil
}

func NewBundleInstallable(bundle, channel string, catalog CatalogKey, constraints ...sat.Constraint) BundleInstallable {
	return BundleInstallable{
		identifier:  sat.Identifier(fmt.Sprintf("%s/%s/%s", catalog.String(), channel, bundle)),
		constraints: constraints,
	}
}

type VirtPackageInstallable struct {
	identifier  sat.Identifier
	constraints []sat.Constraint
}

func (v VirtPackageInstallable) Identifier() sat.Identifier {
	return v.identifier
}

func (v VirtPackageInstallable) Constraints() []sat.Constraint {
	return v.constraints
}

func (v *VirtPackageInstallable) AddDependency(dependencies []sat.Identifier) {
	v.constraints = append(v.constraints, sat.Dependency(dependencies...))
}

func (v *VirtPackageInstallable) AddDependencyFromSet(dependencySet map[sat.Identifier]struct{}) {
	dependencies := make([]sat.Identifier, 0)
	for dep := range dependencySet {
		dependencies = append(dependencies, dep)
	}
	v.constraints = append(v.constraints, sat.Dependency(dependencies...))
}

func NewVirtualPackageInstallable(bundle string) VirtPackageInstallable {
	return VirtPackageInstallable{
		identifier:  sat.Identifier(bundle),
		constraints: []sat.Constraint{sat.Mandatory()},
	}
}
