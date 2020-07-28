package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type RegistryClientProvider interface {
	ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface
}

type DefaultRegistryClientProvider struct {
	logger logrus.FieldLogger
	s      RegistryClientProvider
}

func NewDefaultRegistryClientProvider(log logrus.FieldLogger, store RegistryClientProvider) *DefaultRegistryClientProvider {
	return &DefaultRegistryClientProvider{
		logger: log,
		s:      store,
	}
}

func (rcp *DefaultRegistryClientProvider) ClientsForNamespaces(namespaces ...string) map[registry.CatalogKey]client.Interface {
	return rcp.s.ClientsForNamespaces(namespaces...)
}

type OperatorCacheProvider interface {
	Namespaced(namespaces ...string) MultiCatalogOperatorFinder
}

type OperatorCache struct {
	logger    logrus.FieldLogger
	rcp       RegistryClientProvider
	snapshots map[registry.CatalogKey]*CatalogSnapshot
	ttl       time.Duration
	sem       chan struct{}
	m         sync.RWMutex
}

var _ OperatorCacheProvider = &OperatorCache{}

func NewOperatorCache(rcp RegistryClientProvider, log logrus.FieldLogger) *OperatorCache {
	const (
		MaxConcurrentSnapshotUpdates = 4
	)

	return &OperatorCache{
		logger:    log,
		rcp:       rcp,
		snapshots: make(map[registry.CatalogKey]*CatalogSnapshot),
		ttl:       5 * time.Minute,
		sem:       make(chan struct{}, MaxConcurrentSnapshotUpdates),
	}
}

type NamespacedOperatorCache struct {
	namespaces []string
	snapshots  map[registry.CatalogKey]*CatalogSnapshot
}

func (c *OperatorCache) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	const (
		CachePopulateTimeout = time.Minute
	)

	now := time.Now()
	clients := c.rcp.ClientsForNamespaces(namespaces...)

	result := NamespacedOperatorCache{
		namespaces: namespaces,
		snapshots:  make(map[registry.CatalogKey]*CatalogSnapshot),
	}

	var misses []registry.CatalogKey
	func() {
		c.m.RLock()
		defer c.m.RUnlock()
		for key := range clients {
			if snapshot, ok := c.snapshots[key]; ok && !snapshot.Expired(now) && snapshot.operators != nil && len(snapshot.operators) > 0 {
				result.snapshots[key] = snapshot
			} else {
				misses = append(misses, key)
			}
		}
	}()

	if len(misses) == 0 {
		return &result
	}

	c.m.Lock()
	defer c.m.Unlock()

	// Take the opportunity to clear expired snapshots while holding the lock.
	var expired []registry.CatalogKey
	for key, snapshot := range c.snapshots {
		if snapshot.Expired(now) {
			snapshot.Cancel()
			expired = append(expired, key)
		}
	}
	for _, key := range expired {
		delete(c.snapshots, key)
	}

	// Check for any snapshots that were populated while waiting to acquire the lock.
	var found int
	for i := range misses {
		if snapshot, ok := c.snapshots[misses[i]]; ok && !snapshot.Expired(now) && snapshot.operators != nil && len(snapshot.operators) > 0 {
			result.snapshots[misses[i]] = snapshot
			misses[found], misses[i] = misses[i], misses[found]
			found++
		}
	}
	misses = misses[found:]

	for _, miss := range misses {
		ctx, cancel := context.WithTimeout(context.Background(), CachePopulateTimeout)
		s := CatalogSnapshot{
			logger: c.logger.WithField("catalog", miss),
			key:    miss,
			expiry: now.Add(c.ttl),
			pop:    cancel,
		}
		s.m.Lock()
		c.snapshots[miss] = &s
		result.snapshots[miss] = &s
		go c.populate(ctx, &s, clients[miss])
	}

	return &result
}

func (c *OperatorCache) populate(ctx context.Context, snapshot *CatalogSnapshot, registry client.Interface) {
	defer snapshot.m.Unlock()

	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	it, err := registry.ListBundles(ctx)
	if err != nil {
		snapshot.logger.Errorf("failed to list bundles: %s", err.Error())
		return
	}

	var operators []*Operator
	for b := it.Next(); b != nil; b = it.Next() {
		o, err := NewOperatorFromBundle(b, "", snapshot.key)
		if err != nil {
			snapshot.logger.Warnf("failed to construct operator from bundle, continuing: %s", err.Error())
			continue
		}
		o.providedAPIs = o.ProvidedAPIs().StripPlural()
		o.requiredAPIs = o.RequiredAPIs().StripPlural()
		o.replaces = b.Replaces
		operators = append(operators, o)
	}
	if err := it.Error(); err != nil {
		snapshot.logger.Warnf("error encountered while listing bundles: %s", err.Error())
	}

	snapshot.operators = operators
}

func (c *NamespacedOperatorCache) Catalog(k registry.CatalogKey) OperatorFinder {
	if snapshot, ok := c.snapshots[k]; ok {
		return snapshot
	}
	return EmptyOperatorFinder{}
}

func (c *NamespacedOperatorCache) Find(p ...OperatorPredicate) []*Operator {
	var result []*Operator
	sorted := NewSortableSnapshots(c.namespaces, c.snapshots)
	sort.Sort(sorted)
	for _, snapshot := range sorted.snapshots {
		result = append(result, snapshot.Find(p...)...)
	}
	return result
}

type CatalogSnapshot struct {
	logger    logrus.FieldLogger
	key       registry.CatalogKey
	expiry    time.Time
	operators []*Operator
	m         sync.RWMutex
	pop       context.CancelFunc
}

func (s *CatalogSnapshot) Cancel() {
	s.pop()
}

func (s *CatalogSnapshot) Expired(at time.Time) bool {
	return !at.Before(s.expiry)
}

type SortableSnapshots struct {
	snapshots  []*CatalogSnapshot
	namespaces map[string]int
}

func NewSortableSnapshots(namespaces []string, snapshots map[registry.CatalogKey]*CatalogSnapshot) SortableSnapshots {
	sorted := SortableSnapshots{
		snapshots:  make([]*CatalogSnapshot, 0),
		namespaces: make(map[string]int, 0),
	}
	for i, n := range namespaces {
		sorted.namespaces[n] = i
	}
	for _, s := range snapshots {
		sorted.snapshots = append(sorted.snapshots, s)
	}
	return sorted
}

var _ sort.Interface = SortableSnapshots{}

// Len is the number of elements in the collection.
func (s SortableSnapshots) Len() int {
	return len(s.snapshots)
}

// Less reports whether the element with
// index i should sort before the element with index j.
func (s SortableSnapshots) Less(i, j int) bool {
	if s.snapshots[i].key.Namespace != s.snapshots[j].key.Namespace {
		return s.namespaces[s.snapshots[i].key.Namespace] < s.namespaces[s.snapshots[j].key.Namespace]
	}
	return s.snapshots[i].key.Name < s.snapshots[j].key.Name
}

// Swap swaps the elements with indexes i and j.
func (s SortableSnapshots) Swap(i, j int) {
	s.snapshots[i], s.snapshots[j] = s.snapshots[j], s.snapshots[i]
}

type OperatorPredicate func(*Operator) bool

func (s *CatalogSnapshot) Find(p ...OperatorPredicate) []*Operator {
	s.m.RLock()
	defer s.m.RUnlock()
	return Filter(s.operators, p...)
}

type OperatorFinder interface {
	Find(...OperatorPredicate) []*Operator
}

type MultiCatalogOperatorFinder interface {
	Catalog(registry.CatalogKey) OperatorFinder
	OperatorFinder
}

type EmptyOperatorFinder struct{}

func (f EmptyOperatorFinder) Find(...OperatorPredicate) []*Operator {
	return nil
}

func InChannel(pkg, channel string) OperatorPredicate {
	return func(o *Operator) bool {
		return o.Package() == pkg && o.Bundle().ChannelName == channel
	}
}

func WithCSVName(name string) OperatorPredicate {
	return func(o *Operator) bool {
		return o.name == name
	}
}

func WithChannel(channel string) OperatorPredicate {
	return func(o *Operator) bool {
		return o.bundle.ChannelName == channel
	}
}

func WithPackage(pkg string) OperatorPredicate {
	return func(o *Operator) bool {
		return o.Package() == pkg
	}
}

func WithVersionInRange(r semver.Range) OperatorPredicate {
	return func(o *Operator) bool {
		return o.version != nil && r(*o.version)
	}
}

func ProvidingAPI(api opregistry.APIKey) OperatorPredicate {
	return func(o *Operator) bool {
		for _, p := range o.bundle.Properties {
			if p.Type != opregistry.GVKType {
				continue
			}
			var prop opregistry.GVKProperty
			err := json.Unmarshal([]byte(p.Value), &prop)
			if err != nil {
				continue
			}
			if prop.Kind == api.Kind && prop.Version == api.Version && prop.Group == api.Group {
				return true
			}
		}
		return false
	}
}

func SkipRangeIncludes(version semver.Version) OperatorPredicate {
	return func(o *Operator) bool {
		// TODO: lift range parsing to OperatorSurface
		semverRange, err := semver.ParseRange(o.bundle.SkipRange)
		return err == nil && semverRange(version)
	}
}

func Replaces(name string) OperatorPredicate {
	return func(o *Operator) bool {
		if o.Replaces() == name {
			return true
		}
		for _, s := range o.bundle.Skips {
			if s == name {
				return true
			}
		}
		return false
	}
}

func And(p ...OperatorPredicate) OperatorPredicate {
	return func(o *Operator) bool {
		for _, l := range p {
			if l(o) == false {
				return false
			}
		}
		return true
	}
}

func Or(p ...OperatorPredicate) OperatorPredicate {
	return func(o *Operator) bool {
		for _, l := range p {
			if l(o) == true {
				return true
			}
		}
		return false
	}
}

func AtLeast(n int, operators []*Operator) ([]*Operator, error) {
	if len(operators) < n {
		return nil, fmt.Errorf("expected at least %d operator(s), got %d", n, len(operators))
	}
	return operators, nil
}

func ExactlyOne(operators []*Operator) (*Operator, error) {
	if len(operators) != 1 {
		return nil, fmt.Errorf("expected exactly one operator, got %d", len(operators))
	}
	return operators[0], nil
}

func Filter(operators []*Operator, p ...OperatorPredicate) []*Operator {
	var result []*Operator
	for _, o := range operators {
		if Matches(o, p...) {
			result = append(result, o)
		}
	}
	return result
}

func Matches(o *Operator, p ...OperatorPredicate) bool {
	return And(p...)(o)
}
