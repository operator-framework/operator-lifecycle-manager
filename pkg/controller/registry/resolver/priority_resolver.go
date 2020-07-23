package resolver

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"sort"
)

const (
	defaultRHCatalogSourceName        = ""
	defaultCertifiedCatalogSourceName = ""
	defaultCommunityCatalogSourceName = ""
)

type PriorityResolver interface {
	SortLexicographically()
}

type CatalogSrc struct {
	Priority int
	registry.CatalogKey
}

func SortLexicographically(s SortableSnapshots) {
	sort.SliceStable(s, func(i, j int) bool {

		// preferred catalog is less than all others
		if s.preferred != nil &&
			s.snapshots[i].key.Name == s.preferred.Name &&
			s.snapshots[i].key.Namespace == s.preferred.Namespace {
			return true
		}
		if s.preferred != nil &&
			s.snapshots[j].key.Name == s.preferred.Name &&
			s.snapshots[j].key.Namespace == s.preferred.Namespace {
			return false
		}

		//if s.snapshots[i]. != s.snapshots[j].priority.CatalogSource {
		//	return s.snapshots[i].priority.CatalogSource < s.snapshots[j].priority.CatalogSource
		//}

		if s.snapshots[i].key.Namespace != s.snapshots[j].key.Namespace {
			return s.namespaces[s.snapshots[i].key.Namespace] < s.namespaces[s.snapshots[j].key.Namespace]
		}
		return s.snapshots[i].key.Name < s.snapshots[j].key.Name
	})
}

func SetCatalogPriority(key registry.CatalogKey, catsrc int) {
	if catsrc == 0 {
		defaultCatsrcPriorities(key)
	}
}

// defaultCatsrcPriorities sets the default value of a catalog source priority.
func defaultCatsrcPriorities(key registry.CatalogKey) CatalogSrc {
	if key.Name == defaultRHCatalogSourceName {
		return CatalogSrc{-100, key}
	}

	if key.Name == defaultCertifiedCatalogSourceName {
		return CatalogSrc{-200, key}
	}

	if key.Name == defaultCommunityCatalogSourceName {
		return CatalogSrc{-300, key}
	}

	return CatalogSrc{0, key}
}
