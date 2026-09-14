package generator

import (
	"sync"

	"golang.org/x/tools/go/packages"
)

// Cache memoizes loaded packages by import path. It is safe to share between
// goroutines.
type Cache struct {
	mu         sync.Mutex
	packageMap map[string]interface{}
}

type FakeCache struct{}

func (c *FakeCache) Load(packagePath string) ([]*packages.Package, bool)    { return nil, false }
func (c *FakeCache) Store(packagePath string, packages []*packages.Package) {}

type Cacher interface {
	Load(packagePath string) ([]*packages.Package, bool)
	Store(packagePath string, packages []*packages.Package)
}

func (c *Cache) Load(packagePath string) ([]*packages.Package, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.packageMap[packagePath]
	if !ok {
		return nil, false
	}
	packages, ok := p.([]*packages.Package)
	return packages, ok
}

func (c *Cache) Store(packagePath string, packages []*packages.Package) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.packageMap == nil {
		c.packageMap = map[string]interface{}{}
	}
	c.packageMap[packagePath] = packages
}
