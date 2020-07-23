package solver

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

const (
	defaultRHCatalaogSourceName        = "redhat-operators"
	defaultCertifiedCatalaogSourceName = "certified-operators"
	defaultCommunityCatalaogSourceName = "community-operators"
)

type Priorities struct {
	CatalogSource int
}

func NewPriorities(key registry.CatalogKey, catsrc int) Priorities {
	if catsrc == 0 {
		return defaultCatsrcPriorities(key, catsrc)
	}
	return Priorities{CatalogSource: catsrc}
}

// defaultCatsrcPriorities sets the default value of a catalog source priority.
func defaultCatsrcPriorities(key registry.CatalogKey, catsrc int) Priorities {
	if key.Name == defaultRHCatalaogSourceName {
		return Priorities{CatalogSource: -100}
	}

	if key.Name == defaultCertifiedCatalaogSourceName {
		return Priorities{CatalogSource: -200}
	}

	if key.Name == defaultCommunityCatalaogSourceName {
		return Priorities{CatalogSource: -300}
	}

	return Priorities{CatalogSource: 0}
}
