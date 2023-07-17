package resolver

import (
	"fmt"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/solver"
	operatorregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type BundleVariable struct {
	identifier  solver.Identifier
	constraints []solver.Constraint

	Replaces string
}

func (i BundleVariable) Identifier() solver.Identifier {
	return i.identifier
}

func (i BundleVariable) Constraints() []solver.Constraint {
	return i.constraints
}

func (i *BundleVariable) MakeProhibited() {
	i.constraints = append(i.constraints, solver.Prohibited())
}

func (i *BundleVariable) AddConflict(id solver.Identifier) {
	i.constraints = append(i.constraints, solver.Conflict(id))
}

func (i *BundleVariable) AddConstraint(c solver.Constraint) {
	i.constraints = append(i.constraints, c)
}

func (i *BundleVariable) BundleSourceInfo() (string, string, cache.SourceKey, error) {
	info := strings.Split(i.identifier.String(), "/")
	// This should be enforced by Kube naming constraints
	if len(info) != 4 {
		return "", "", cache.SourceKey{}, fmt.Errorf("unable to parse identifier %s for source info", i.identifier)
	}
	catalog := cache.SourceKey{
		Name:      info[0],
		Namespace: info[1],
	}
	channel := info[2]
	csvName := info[3]
	return csvName, channel, catalog, nil
}

func bundleID(bundle, channel string, catalog cache.SourceKey) solver.Identifier {
	return solver.IdentifierFromString(fmt.Sprintf("%s/%s/%s", catalog.String(), channel, bundle))
}

func NewBundleVariableFromOperator(o *cache.Entry) (BundleVariable, error) {
	if o.SourceInfo == nil {
		return BundleVariable{}, fmt.Errorf("unable to resolve the source of bundle %s", o.Name)
	}
	id := bundleID(o.Name, o.Channel(), o.SourceInfo.Catalog)
	var constraints []solver.Constraint
	if o.SourceInfo.Catalog.Virtual() && o.SourceInfo.Subscription == nil {
		// CSVs already associated with a Subscription
		// may be replaced, but freestanding CSVs must
		// appear in any solution.
		constraints = append(constraints, PrettyConstraint(
			solver.Mandatory(),
			fmt.Sprintf("clusterserviceversion %s exists and is not referenced by a subscription", o.Name),
		))
	}
	for _, p := range o.Properties {
		if p.GetType() == operatorregistry.DeprecatedType {
			constraints = append(constraints, PrettyConstraint(
				solver.Prohibited(),
				fmt.Sprintf("bundle %s is deprecated", id),
			))
			break
		}
	}
	return BundleVariable{
		identifier:  id,
		constraints: constraints,
	}, nil
}

type GenericVariable struct {
	identifier  solver.Identifier
	constraints []solver.Constraint
}

func (i GenericVariable) Identifier() solver.Identifier {
	return i.identifier
}

func (i GenericVariable) Constraints() []solver.Constraint {
	return i.constraints
}

func NewInvalidSubscriptionVariable(name string, reason string) solver.Variable {
	return GenericVariable{
		identifier: solver.IdentifierFromString(fmt.Sprintf("subscription:%s", name)),
		constraints: []solver.Constraint{
			PrettyConstraint(solver.Mandatory(), fmt.Sprintf("subscription %s exists", name)),
			PrettyConstraint(solver.Prohibited(), reason),
		},
	}
}

func NewSubscriptionVariable(name string, dependencies []solver.Identifier) solver.Variable {
	result := GenericVariable{
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

func NewSingleAPIProviderVariable(group, version, kind string, providers []solver.Identifier) solver.Variable {
	gvk := fmt.Sprintf("%s (%s/%s)", kind, group, version)
	result := GenericVariable{
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

func NewSinglePackageInstanceVariable(pkg string, providers []solver.Identifier) solver.Variable {
	result := GenericVariable{
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
