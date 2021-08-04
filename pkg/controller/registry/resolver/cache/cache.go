package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-registry/pkg/api"
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
	Expire(catalog registry.CatalogKey)
}

type OperatorCache struct {
	logger       logrus.FieldLogger
	rcp          RegistryClientProvider
	catsrcLister v1alpha1.CatalogSourceLister
	snapshots    map[registry.CatalogKey]*CatalogSnapshot
	ttl          time.Duration
	sem          chan struct{}
	m            sync.RWMutex
}

const defaultCatalogSourcePriority int = 0

type catalogSourcePriority int

var _ OperatorCacheProvider = &OperatorCache{}

func NewOperatorCache(rcp RegistryClientProvider, log logrus.FieldLogger, catsrcLister v1alpha1.CatalogSourceLister) *OperatorCache {
	const (
		MaxConcurrentSnapshotUpdates = 4
	)

	return &OperatorCache{
		logger:       log,
		rcp:          rcp,
		catsrcLister: catsrcLister,
		snapshots:    make(map[registry.CatalogKey]*CatalogSnapshot),
		ttl:          5 * time.Minute,
		sem:          make(chan struct{}, MaxConcurrentSnapshotUpdates),
	}
}

type NamespacedOperatorCache struct {
	Namespaces []string
	existing   *registry.CatalogKey
	Snapshots  map[registry.CatalogKey]*CatalogSnapshot
}

func (c *NamespacedOperatorCache) Error() error {
	var errs []error
	for key, snapshot := range c.Snapshots {
		snapshot.m.Lock()
		err := snapshot.err
		snapshot.m.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("error using catalog %s (in namespace %s): %w", key.Name, key.Namespace, err))
		}
	}
	return errors.NewAggregate(errs)
}

func (c *OperatorCache) Expire(catalog registry.CatalogKey) {
	c.m.Lock()
	defer c.m.Unlock()
	s, ok := c.snapshots[catalog]
	if !ok {
		return
	}
	s.expiry = time.Unix(0, 0)
}

func (c *OperatorCache) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	const (
		CachePopulateTimeout = time.Minute
	)

	now := time.Now()
	clients := c.rcp.ClientsForNamespaces(namespaces...)

	result := NamespacedOperatorCache{
		Namespaces: namespaces,
		Snapshots:  make(map[registry.CatalogKey]*CatalogSnapshot),
	}

	var misses []registry.CatalogKey
	func() {
		c.m.RLock()
		defer c.m.RUnlock()
		for key := range clients {
			snapshot, ok := c.snapshots[key]
			if ok {
				func() {
					snapshot.m.RLock()
					defer snapshot.m.RUnlock()
					if !snapshot.Expired(now) && snapshot.Operators != nil && len(snapshot.Operators) > 0 {
						result.Snapshots[key] = snapshot
					} else {
						misses = append(misses, key)
					}
				}()
			}
			if !ok {
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
		if snapshot, ok := c.snapshots[misses[i]]; ok && !snapshot.Expired(now) && snapshot.Operators != nil && len(snapshot.Operators) > 0 {
			result.Snapshots[misses[i]] = snapshot
			misses[found], misses[i] = misses[i], misses[found]
			found++
		}
	}
	misses = misses[found:]

	for _, miss := range misses {
		ctx, cancel := context.WithTimeout(context.Background(), CachePopulateTimeout)

		catsrcPriority := defaultCatalogSourcePriority
		// Ignoring error and treat catsrc priority as 0 if not found.
		catsrc, err := c.catsrcLister.CatalogSources(miss.Namespace).Get(miss.Name)
		if err == nil {
			catsrcPriority = catsrc.Spec.Priority
		}

		s := CatalogSnapshot{
			logger:   c.logger.WithField("catalog", miss),
			Key:      miss,
			expiry:   now.Add(c.ttl),
			pop:      cancel,
			Priority: catalogSourcePriority(catsrcPriority),
		}
		s.m.Lock()
		c.snapshots[miss] = &s
		result.Snapshots[miss] = &s
		go c.populate(ctx, &s, clients[miss])
	}

	return &result
}

func (c *OperatorCache) populate(ctx context.Context, snapshot *CatalogSnapshot, registry client.Interface) {
	defer snapshot.m.Unlock()
	defer func() {
		// Don't cache an errorred snapshot.
		if snapshot.err != nil {
			snapshot.expiry = time.Time{}
		}
	}()

	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	// Fetching default channels this way makes many round trips
	// -- may need to either add a new API to fetch all at once,
	// or embed the information into Bundle.
	defaultChannels := make(map[string]string)

	it, err := registry.ListBundles(ctx)
	if err != nil {
		snapshot.logger.Errorf("failed to list bundles: %s", err.Error())
		snapshot.err = err
		return
	}
	c.logger.WithField("catalog", snapshot.Key.String()).Debug("updating cache")
	var operators []*Operator
	for b := it.Next(); b != nil; b = it.Next() {
		defaultChannel, ok := defaultChannels[b.PackageName]
		if !ok {
			if p, err := registry.GetPackage(ctx, b.PackageName); err != nil {
				snapshot.logger.Warnf("failed to retrieve default channel for bundle, continuing: %v", err)
				continue
			} else {
				defaultChannels[b.PackageName] = p.DefaultChannelName
				defaultChannel = p.DefaultChannelName
			}
		}
		o, err := NewOperatorFromBundle(b, "", snapshot.Key, defaultChannel)
		if err != nil {
			snapshot.logger.Warnf("failed to construct operator from bundle, continuing: %v", err)
			continue
		}
		o.ProvidedAPIs = o.ProvidedAPIs.StripPlural()
		o.RequiredAPIs = o.RequiredAPIs.StripPlural()
		o.Replaces = b.Replaces
		EnsurePackageProperty(o, b.PackageName, b.Version)
		operators = append(operators, o)
	}
	if err := it.Error(); err != nil {
		snapshot.logger.Warnf("error encountered while listing bundles: %s", err.Error())
		snapshot.err = err
	}
	snapshot.Operators = operators
}

func EnsurePackageProperty(o *Operator, name, version string) {
	for _, p := range o.Properties {
		if p.Type == opregistry.PackageType {
			return
		}
	}
	prop := opregistry.PackageProperty{
		PackageName: name,
		Version:     version,
	}
	bytes, err := json.Marshal(prop)
	if err != nil {
		return
	}
	o.Properties = append(o.Properties, &api.Property{
		Type:  opregistry.PackageType,
		Value: string(bytes),
	})
}

func (c *NamespacedOperatorCache) Catalog(k registry.CatalogKey) OperatorFinder {
	// all catalogs match the empty catalog
	if k.Empty() {
		return c
	}
	if snapshot, ok := c.Snapshots[k]; ok {
		return snapshot
	}
	return EmptyOperatorFinder{}
}

func (c *NamespacedOperatorCache) FindPreferred(preferred *registry.CatalogKey, p ...OperatorPredicate) []*Operator {
	var result []*Operator
	if preferred != nil && preferred.Empty() {
		preferred = nil
	}
	sorted := NewSortableSnapshots(c.existing, preferred, c.Namespaces, c.Snapshots)
	sort.Sort(sorted)
	for _, snapshot := range sorted.snapshots {
		result = append(result, snapshot.Find(p...)...)
	}
	return result
}

func (c *NamespacedOperatorCache) WithExistingOperators(snapshot *CatalogSnapshot) MultiCatalogOperatorFinder {
	o := &NamespacedOperatorCache{
		Namespaces: c.Namespaces,
		existing:   &snapshot.Key,
		Snapshots:  c.Snapshots,
	}
	o.Snapshots[snapshot.Key] = snapshot
	return o
}

func (c *NamespacedOperatorCache) Find(p ...OperatorPredicate) []*Operator {
	return c.FindPreferred(nil, p...)
}

type CatalogSnapshot struct {
	logger    logrus.FieldLogger
	Key       registry.CatalogKey
	expiry    time.Time
	Operators []*Operator
	m         sync.RWMutex
	pop       context.CancelFunc
	Priority  catalogSourcePriority
	err       error
}

func (s *CatalogSnapshot) Cancel() {
	s.pop()
}

func (s *CatalogSnapshot) Expired(at time.Time) bool {
	return !at.Before(s.expiry)
}

// NewRunningOperatorSnapshot creates a CatalogSnapshot that represents a set of existing installed operators
// in the cluster.
func NewRunningOperatorSnapshot(logger logrus.FieldLogger, key registry.CatalogKey, o []*Operator) *CatalogSnapshot {
	return &CatalogSnapshot{
		logger:    logger,
		Key:       key,
		Operators: o,
	}
}

type SortableSnapshots struct {
	snapshots  []*CatalogSnapshot
	namespaces map[string]int
	preferred  *registry.CatalogKey
	existing   *registry.CatalogKey
}

func NewSortableSnapshots(existing, preferred *registry.CatalogKey, namespaces []string, snapshots map[registry.CatalogKey]*CatalogSnapshot) SortableSnapshots {
	sorted := SortableSnapshots{
		existing:   existing,
		preferred:  preferred,
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
	// existing operators are preferred over catalog operators
	if s.existing != nil &&
		s.snapshots[i].Key.Name == s.existing.Name &&
		s.snapshots[i].Key.Namespace == s.existing.Namespace {
		return true
	}
	if s.existing != nil &&
		s.snapshots[j].Key.Name == s.existing.Name &&
		s.snapshots[j].Key.Namespace == s.existing.Namespace {
		return false
	}

	// preferred catalog is less than all other catalogs
	if s.preferred != nil &&
		s.snapshots[i].Key.Name == s.preferred.Name &&
		s.snapshots[i].Key.Namespace == s.preferred.Namespace {
		return true
	}
	if s.preferred != nil &&
		s.snapshots[j].Key.Name == s.preferred.Name &&
		s.snapshots[j].Key.Namespace == s.preferred.Namespace {
		return false
	}

	// the rest are sorted first on priority, namespace and then by name
	if s.snapshots[i].Priority != s.snapshots[j].Priority {
		return s.snapshots[i].Priority > s.snapshots[j].Priority
	}
	if s.snapshots[i].Key.Namespace != s.snapshots[j].Key.Namespace {
		return s.namespaces[s.snapshots[i].Key.Namespace] < s.namespaces[s.snapshots[j].Key.Namespace]
	}

	return s.snapshots[i].Key.Name < s.snapshots[j].Key.Name
}

// Swap swaps the elements with indexes i and j.
func (s SortableSnapshots) Swap(i, j int) {
	s.snapshots[i], s.snapshots[j] = s.snapshots[j], s.snapshots[i]
}

func (s *CatalogSnapshot) Find(p ...OperatorPredicate) []*Operator {
	s.m.RLock()
	defer s.m.RUnlock()
	return Filter(s.Operators, p...)
}

type OperatorFinder interface {
	Find(...OperatorPredicate) []*Operator
}

type MultiCatalogOperatorFinder interface {
	Catalog(registry.CatalogKey) OperatorFinder
	FindPreferred(*registry.CatalogKey, ...OperatorPredicate) []*Operator
	WithExistingOperators(*CatalogSnapshot) MultiCatalogOperatorFinder
	Error() error
	OperatorFinder
}

type EmptyOperatorFinder struct{}

func (f EmptyOperatorFinder) Find(...OperatorPredicate) []*Operator {
	return nil
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
	return And(p...).Test(o)
}
