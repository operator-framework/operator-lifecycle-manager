//go:generate counterfeiter -o ../../../fakes/fake_api_intersection_reconciler.go . APIIntersectionReconciler
package resolver

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
)

type NamespaceSet map[string]struct{}

func NewNamespaceSet(namespaces []string) NamespaceSet {
	set := make(NamespaceSet)
	for _, namespace := range namespaces {
		set[namespace] = struct{}{}
	}

	return set
}

func (n NamespaceSet) Peek() string {
	for namespace := range n {
		return namespace
	}

	return ""
}

func (n NamespaceSet) Intersection(set NamespaceSet) NamespaceSet {
	intersection := make(NamespaceSet)
	// Handle special NamespaceAll cases
	if len(n) == 1 && n.Peek() == "" {
		for namespace := range set {
			intersection[namespace] = struct{}{}
		}
		return intersection
	}
	if len(set) == 1 && set.Peek() == "" {
		for namespace := range n {
			intersection[namespace] = struct{}{}
		}
		return intersection
	}

	for namespace := range n {
		if _, ok := set[namespace]; ok {
			intersection[namespace] = struct{}{}
		}
	}

	return intersection
}

type OperatorGroupSurface interface {
	Identifier() string
	Namespace() string
	Targets() NamespaceSet
	ProvidedAPIs() APISet
	GroupIntersection(groups ...OperatorGroupSurface) []OperatorGroupSurface
}

var _ OperatorGroupSurface = &OperatorGroup{}

type OperatorGroup struct {
	namespace    string
	name         string
	targets      NamespaceSet
	providedAPIs APISet
}

func NewOperatorGroup(group v1alpha2.OperatorGroup) *OperatorGroup {
	// Add operatorgroup namespace if not NamespaceAll
	namespaces := group.Status.Namespaces
	if len(namespaces) >= 1 && namespaces[0] != "" {
		namespaces = append(namespaces, group.GetNamespace())
	}
	// TODO: Sanitize OperatorGroup if len(namespaces) > 1 and contains ""
	gvksStr := group.GetAnnotations()[v1alpha2.OperatorGroupProvidedAPIsAnnotationKey]
	
	return &OperatorGroup{
		namespace: group.GetNamespace(),
		name: group.GetName(),
		targets: NewNamespaceSet(namespaces),
		providedAPIs: GVKStringToProvidedAPISet(gvksStr),
	}
}

func NewOperatorGroupSurfaces(groups ...v1alpha2.OperatorGroup) []OperatorGroupSurface {
	operatorGroups := make([]OperatorGroupSurface, len(groups))
	for i, group := range groups {
		operatorGroups[i] = NewOperatorGroup(group)
	}

	return operatorGroups
}

func (g *OperatorGroup) Identifier() string {
	return g.name + "/" + g.namespace
}

func (g *OperatorGroup) Namespace() string {
	return g.namespace
}

func (g *OperatorGroup) Targets() NamespaceSet {
	return g.targets
}

func (g *OperatorGroup) ProvidedAPIs() APISet {
	return g.providedAPIs
}

func (g *OperatorGroup) GroupIntersection(groups ...OperatorGroupSurface) []OperatorGroupSurface {
	intersection := []OperatorGroupSurface{}
	for _, group := range groups {
		if group.Identifier() == g.Identifier() {
			// Skip self if present
			continue
		}
		if len(g.targets.Intersection(group.Targets())) > 0 {
			// TODO: This uses tons of space - maps are copied every time
			intersection = append(intersection, group)
		}
	}

	return intersection
}

type APIReconciliationResult int

const (
	RemoveAPIs APIReconciliationResult = iota
	AddAPIs
	APIConflict
	NoAPIConflict
)

type APIIntersectionReconciler interface {
	Reconcile(add APISet, group OperatorGroupSurface, otherGroups ...OperatorGroupSurface) (APIReconciliationResult)
}

type APIIntersectionReconcileFunc func(add APISet, group OperatorGroupSurface, otherGroups ...OperatorGroupSurface) (APIReconciliationResult)
func (a APIIntersectionReconcileFunc) Reconcile(add APISet, group OperatorGroupSurface, otherGroups ...OperatorGroupSurface) (APIReconciliationResult) {
	return a(add, group, otherGroups...)
}

func ReconcileAPIIntersection(add APISet, group OperatorGroupSurface, otherGroups ...OperatorGroupSurface) (APIReconciliationResult) {
	groupIntersection := group.GroupIntersection(otherGroups...)
	providedAPIIntersection := make(APISet)
	for _, g := range groupIntersection {
		providedAPIIntersection = providedAPIIntersection.Union(g.ProvidedAPIs())
	}

	intersecting := len(add.Intersection(providedAPIIntersection)) > 0
	subset := add.IsSubset(group.ProvidedAPIs())

	if subset && intersecting {
		return RemoveAPIs
	}

	if !subset && intersecting {
		return APIConflict
	}

	if !subset {
		return AddAPIs
	}

	return NoAPIConflict
}