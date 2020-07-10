package resolver

import (
	"fmt"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solve"
)

type BundleInstallable struct {
	identifier  solve.Identifier
	constraints []solve.Constraint
}

func (i BundleInstallable) Identifier() solve.Identifier {
	return i.identifier
}

func (i BundleInstallable) Constraints() []solve.Constraint {
	return i.constraints
}

func (i *BundleInstallable) MakeProhibited() {
	i.constraints = append(i.constraints, solve.Prohibited())
}

func (i *BundleInstallable) AddDependency(dependencies []solve.Identifier) {
	i.constraints = append(i.constraints, solve.Dependency(dependencies...))
}

func (i *BundleInstallable) AddDependencyFromSet(dependencySet map[solve.Identifier]struct{}) {
	dependencies := make([]solve.Identifier, 0)
	for dep := range dependencySet {
		dependencies = append(dependencies, dep)
	}
	i.constraints = append(i.constraints, solve.Dependency(dependencies...))
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

func NewBundleInstallable(bundle, channel string, catalog CatalogKey, constraints ...solve.Constraint) BundleInstallable {
	return BundleInstallable{
		identifier:  solve.Identifier(fmt.Sprintf("%s/%s/%s", catalog.String(), channel, bundle)),
		constraints: constraints,
	}
}

type VirtPackageInstallable struct {
	identifier  solve.Identifier
	constraints []solve.Constraint
}

func (v VirtPackageInstallable) Identifier() solve.Identifier {
	return v.identifier
}

func (v VirtPackageInstallable) Constraints() []solve.Constraint {
	return v.constraints
}

func (v *VirtPackageInstallable) AddDependency(dependencies []solve.Identifier) {
	v.constraints = append(v.constraints, solve.Dependency(dependencies...))
}

func (v *VirtPackageInstallable) AddDependencyFromSet(dependencySet map[solve.Identifier]struct{}) {
	dependencies := make([]solve.Identifier, 0)
	for dep := range dependencySet {
		dependencies = append(dependencies, dep)
	}
	v.constraints = append(v.constraints, solve.Dependency(dependencies...))
}

func NewVirtualPackageInstallable(bundle string) VirtPackageInstallable {
	return VirtPackageInstallable{
		identifier:  solve.Identifier(bundle),
		constraints: []solve.Constraint{solve.Mandatory()},
	}
}
