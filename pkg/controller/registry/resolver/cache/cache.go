package cache

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/errors"
)

const existingOperatorKey = "@existing"

type SourceKey struct {
	Name      string
	Namespace string
}

func (k *SourceKey) String() string {
	return fmt.Sprintf("%s/%s", k.Name, k.Namespace)
}

func (k *SourceKey) Empty() bool {
	return k.Name == "" && k.Namespace == ""
}

func (k *SourceKey) Equal(compare SourceKey) bool {
	return k.Name == compare.Name && k.Namespace == compare.Namespace
}

// Virtual indicates if this is a "virtual" catalog representing the currently installed operators in a namespace
func (k *SourceKey) Virtual() bool {
	return k.Name == existingOperatorKey && k.Namespace != ""
}

func NewVirtualSourceKey(namespace string) SourceKey {
	return SourceKey{
		Name:      existingOperatorKey,
		Namespace: namespace,
	}
}

type Source interface {
	Snapshot(context.Context) (*Snapshot, error)
}

type SourceProvider interface {
	// TODO: namespaces parameter is an artifact of SourceStore
	Sources(namespaces ...string) map[SourceKey]Source
}

type StaticSourceProvider map[SourceKey]Source

func (p StaticSourceProvider) Sources(namespaces ...string) map[SourceKey]Source {
	result := make(map[SourceKey]Source)
	for key, source := range p {
		for _, namespace := range namespaces {
			if key.Namespace == namespace {
				result[key] = source
				break
			}
		}
	}
	return result
}

type OperatorCacheProvider interface {
	Namespaced(namespaces ...string) MultiCatalogOperatorFinder
}

type SourcePriorityProvider interface {
	Priority(SourceKey) int
}

type constantSourcePriorityProvider int

func (spp constantSourcePriorityProvider) Priority(SourceKey) int {
	return int(spp)
}

type Cache struct {
	logger                 logrus.StdLogger
	sp                     SourceProvider
	sourcePriorityProvider SourcePriorityProvider
	snapshots              map[SourceKey]*snapshotHeader
	sem                    chan struct{}
	m                      sync.RWMutex
}

type Option func(*Cache)

func WithLogger(logger logrus.StdLogger) Option {
	return func(c *Cache) {
		c.logger = logger
	}
}

func WithSourcePriorityProvider(spp SourcePriorityProvider) Option {
	return func(c *Cache) {
		c.sourcePriorityProvider = spp
	}
}

func New(sp SourceProvider, options ...Option) *Cache {
	const (
		MaxConcurrentSnapshotUpdates = 4
	)

	cache := Cache{
		logger: func() logrus.StdLogger {
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			return logger
		}(),
		sp:                     sp,
		sourcePriorityProvider: constantSourcePriorityProvider(0),
		snapshots:              make(map[SourceKey]*snapshotHeader),
		sem:                    make(chan struct{}, MaxConcurrentSnapshotUpdates),
	}

	for _, opt := range options {
		opt(&cache)
	}

	return &cache
}

type NamespacedOperatorCache struct {
	snapshots map[SourceKey]*snapshotHeader
}

func (c *NamespacedOperatorCache) Error() error {
	var errs []error
	for key, snapshot := range c.snapshots {
		snapshot.m.RLock()
		err := snapshot.err
		snapshot.m.RUnlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("error using catalog %s (in namespace %s): %w", key.Name, key.Namespace, err))
		}
	}
	return errors.NewAggregate(errs)
}

func (c *Cache) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	const (
		CachePopulateTimeout = time.Minute
	)

	sources := c.sp.Sources(namespaces...)

	result := NamespacedOperatorCache{
		snapshots: make(map[SourceKey]*snapshotHeader),
	}

	var misses []SourceKey
	func() {
		c.m.RLock()
		defer c.m.RUnlock()
		for key := range sources {
			snapshot, ok := c.snapshots[key]
			if ok {
				func() {
					snapshot.m.RLock()
					defer snapshot.m.RUnlock()
					if snapshot.Valid() {
						result.snapshots[key] = snapshot
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
	var expired []SourceKey
	for key, snapshot := range c.snapshots {
		if !snapshot.Valid() {
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
		if hdr, ok := c.snapshots[misses[i]]; ok && hdr.Valid() {
			result.snapshots[misses[i]] = hdr
			misses[found], misses[i] = misses[i], misses[found]
			found++
		}
	}
	misses = misses[found:]

	for _, miss := range misses {
		ctx, cancel := context.WithTimeout(context.Background(), CachePopulateTimeout)

		hdr := snapshotHeader{
			key:      miss,
			pop:      cancel,
			priority: c.sourcePriorityProvider.Priority(miss),
		}

		hdr.m.Lock()
		c.snapshots[miss] = &hdr
		result.snapshots[miss] = &hdr

		go func(ctx context.Context, hdr *snapshotHeader, source Source) {
			defer hdr.m.Unlock()
			c.sem <- struct{}{}
			defer func() { <-c.sem }()
			if snapshot, err := source.Snapshot(ctx); err != nil {
				hdr.err = err
			} else if snapshot != nil {
				hdr.snapshot = snapshot
			} else {
				hdr.err = fmt.Errorf("source %q produced no snapshot and no error", hdr.key)
			}
		}(ctx, &hdr, sources[miss])
	}

	return &result
}

func (c *NamespacedOperatorCache) Catalog(k SourceKey) OperatorFinder {
	// all catalogs match the empty catalog
	if k.Empty() {
		return c
	}
	if snapshot, ok := c.snapshots[k]; ok {
		return snapshot
	}
	return EmptyOperatorFinder{}
}

func (c *NamespacedOperatorCache) FindPreferred(preferred *SourceKey, preferredNamespace string, p ...Predicate) []*Entry {
	var result []*Entry
	if preferred != nil && preferred.Empty() {
		preferred = nil
	}
	sorted := newSortableSnapshots(preferred, preferredNamespace, c.snapshots)
	sort.Sort(sorted)
	for _, snapshot := range sorted.snapshots {
		result = append(result, snapshot.Find(p...)...)
	}
	return result
}

func (c *NamespacedOperatorCache) Find(p ...Predicate) []*Entry {
	return c.FindPreferred(nil, "", p...)
}

type Snapshot struct {
	Entries []*Entry

	// Unless closed, the Snapshot is valid.
	Valid <-chan struct{}
}

func ValidOnce() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}

var _ Source = &Snapshot{}

func (s *Snapshot) Snapshot(context.Context) (*Snapshot, error) {
	return s, nil
}

type snapshotHeader struct {
	snapshot *Snapshot

	key      SourceKey
	m        sync.RWMutex
	pop      context.CancelFunc
	err      error
	priority int
}

func (hdr *snapshotHeader) Cancel() {
	hdr.pop()
}

func (hdr *snapshotHeader) Valid() bool {
	hdr.m.RLock()
	defer hdr.m.RUnlock()
	if hdr.snapshot == nil || hdr.err != nil {
		return false
	}
	select {
	case <-hdr.snapshot.Valid:
		return false
	default:
	}
	return true
}

type sortableSnapshots struct {
	snapshots          []*snapshotHeader
	preferredNamespace string
	preferred          *SourceKey
	existing           *SourceKey
}

func newSortableSnapshots(preferred *SourceKey, preferredNamespace string, snapshots map[SourceKey]*snapshotHeader) sortableSnapshots {
	var existing *SourceKey
	for key := range snapshots {
		if key.Virtual() && key.Namespace == preferredNamespace {
			existing = &key
			break
		}
	}
	sorted := sortableSnapshots{
		existing:           existing,
		preferred:          preferred,
		snapshots:          make([]*snapshotHeader, 0),
		preferredNamespace: preferredNamespace,
	}
	for _, s := range snapshots {
		sorted.snapshots = append(sorted.snapshots, s)
	}
	return sorted
}

var _ sort.Interface = sortableSnapshots{}

// Len is the number of elements in the collection.
func (s sortableSnapshots) Len() int {
	return len(s.snapshots)
}

// Less reports whether the element with
// index i should sort before the element with index j.
func (s sortableSnapshots) Less(i, j int) bool {
	// existing operators are preferred over catalog operators
	if s.existing != nil &&
		s.snapshots[i].key.Name == s.existing.Name &&
		s.snapshots[i].key.Namespace == s.existing.Namespace {
		return true
	}
	if s.existing != nil &&
		s.snapshots[j].key.Name == s.existing.Name &&
		s.snapshots[j].key.Namespace == s.existing.Namespace {
		return false
	}

	// preferred catalog is less than all other catalogs
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

	// the rest are sorted first on priority, namespace and then by name
	if s.snapshots[i].priority != s.snapshots[j].priority {
		return s.snapshots[i].priority > s.snapshots[j].priority
	}

	if s.snapshots[i].key.Namespace != s.snapshots[j].key.Namespace {
		if s.snapshots[i].key.Namespace == s.preferredNamespace {
			return true
		}
		if s.snapshots[j].key.Namespace == s.preferredNamespace {
			return false
		}
	}

	return s.snapshots[i].key.Name < s.snapshots[j].key.Name
}

// Swap swaps the elements with indexes i and j.
func (s sortableSnapshots) Swap(i, j int) {
	s.snapshots[i], s.snapshots[j] = s.snapshots[j], s.snapshots[i]
}

func (hdr *snapshotHeader) Find(p ...Predicate) []*Entry {
	hdr.m.RLock()
	defer hdr.m.RUnlock()
	if hdr.snapshot == nil {
		return nil
	}
	return Filter(hdr.snapshot.Entries, p...)
}

type OperatorFinder interface {
	Find(...Predicate) []*Entry
}

type MultiCatalogOperatorFinder interface {
	Catalog(SourceKey) OperatorFinder
	FindPreferred(preferred *SourceKey, preferredNamespace string, predicates ...Predicate) []*Entry
	Error() error
	OperatorFinder
}

type EmptyOperatorFinder struct{}

func (f EmptyOperatorFinder) Find(...Predicate) []*Entry {
	return nil
}

func ExactlyOne(operators []*Entry) (*Entry, error) {
	if len(operators) != 1 {
		return nil, fmt.Errorf("expected exactly one operator, got %d", len(operators))
	}
	return operators[0], nil
}

func Filter(operators []*Entry, p ...Predicate) []*Entry {
	var result []*Entry
	for _, o := range operators {
		if Matches(o, p...) {
			result = append(result, o)
		}
	}
	return result
}

func Matches(o *Entry, p ...Predicate) bool {
	return And(p...).Test(o)
}
