package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/operator-framework/operator-registry/pkg/client"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
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
	existing   *registry.CatalogKey
	snapshots  map[registry.CatalogKey]*CatalogSnapshot
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
		namespaces: namespaces,
		snapshots:  make(map[registry.CatalogKey]*CatalogSnapshot),
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
					if !snapshot.Expired(now) && snapshot.operators != nil && len(snapshot.operators) > 0 {
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

	// Fetching default channels this way makes many round trips
	// -- may need to either add a new API to fetch all at once,
	// or embed the information into Bundle.
	defaultChannels := make(map[string]string)

	it, err := registry.ListBundles(ctx)
	if err != nil {
		snapshot.logger.Errorf("failed to list bundles: %s", err.Error())
		return
	}
	c.logger.WithField("catalog", snapshot.key.String()).Debug("updating cache")
	var operators []*Operator
	for {
		b := it.Next()
		if b == nil {
			break
		}
		c.logger.WithField("op", b.GetCsvName()).WithField("bundledeps", b.Dependencies).Debug("prior to encoding as op")
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
		o, err := NewOperatorFromBundle(b, "", snapshot.key, defaultChannel)
		if err != nil {
			snapshot.logger.Warnf("failed to construct operator from bundle, continuing: %v", err)
			continue
		}
		o.providedAPIs = o.ProvidedAPIs().StripPlural()
		o.requiredAPIs = o.RequiredAPIs().StripPlural()
		o.replaces = b.Replaces
		ensurePackageProperty(o, b.PackageName, b.Version)
		c.logger.WithField("catalog", snapshot.key.String()).WithField("op", o.Identifier()).WithField("props", o.Properties()).WithField("deps", o.Dependencies()).Debug("adding to cache")
		operators = append(operators, o)
	}
	if err := it.Error(); err != nil {
		snapshot.logger.Warnf("error encountered while listing bundles: %s", err.Error())
	}
	snapshot.operators = operators
}

func ensurePackageProperty(o *Operator, name, version string) {
	for _, p := range o.Properties() {
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
	o.properties = append(o.properties, &api.Property{
		Type:  opregistry.PackageType,
		Value: string(bytes),
	})
}

func (c *NamespacedOperatorCache) Catalog(k registry.CatalogKey) OperatorFinder {
	// all catalogs match the empty catalog
	if k.Empty() {
		return c
	}
	if snapshot, ok := c.snapshots[k]; ok {
		return snapshot
	}
	return EmptyOperatorFinder{}
}

func (c *NamespacedOperatorCache) FindPreferred(preferred *registry.CatalogKey, p ...Predicate) []*Operator {
	var result []*Operator
	if preferred != nil && preferred.Empty() {
		preferred = nil
	}
	sorted := NewSortableSnapshots(c.existing, preferred, c.namespaces, c.snapshots)
	sort.Sort(sorted)
	for _, snapshot := range sorted.snapshots {
		result = append(result, snapshot.Find(p...)...)
	}
	return result
}

func (c *NamespacedOperatorCache) WithExistingOperators(snapshot *CatalogSnapshot) MultiCatalogOperatorFinder {
	o := &NamespacedOperatorCache{
		namespaces: c.namespaces,
		existing:   &snapshot.key,
		snapshots:  c.snapshots,
	}
	o.snapshots[snapshot.key] = snapshot
	return o
}

func (c *NamespacedOperatorCache) Find(p ...Predicate) []*Operator {
	return c.FindPreferred(nil, p...)
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

// NewRunningOperatorSnapshot creates a CatalogSnapshot that represents a set of existing installed operators
// in the cluster.
func NewRunningOperatorSnapshot(logger logrus.FieldLogger, key registry.CatalogKey, o []*Operator) *CatalogSnapshot {
	return &CatalogSnapshot{
		logger:    logger,
		key:       key,
		operators: o,
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

	// the rest are sorted first in namespace preference order, then by name
	if s.snapshots[i].key.Namespace != s.snapshots[j].key.Namespace {
		return s.namespaces[s.snapshots[i].key.Namespace] < s.namespaces[s.snapshots[j].key.Namespace]
	}
	return s.snapshots[i].key.Name < s.snapshots[j].key.Name
}

// Swap swaps the elements with indexes i and j.
func (s SortableSnapshots) Swap(i, j int) {
	s.snapshots[i], s.snapshots[j] = s.snapshots[j], s.snapshots[i]
}

type Predicate interface {
	fmt.Stringer
	Apply(*Operator) bool
}

type OperatorPredicate struct {
	f func(*Operator) bool
	desc string
}

func (p OperatorPredicate) Apply(o *Operator) bool {
	return p.f(o)
}

func (p OperatorPredicate) String() string {
	return p.desc
}

func (s *CatalogSnapshot) Find(p ...Predicate) []*Operator {
	s.m.RLock()
	defer s.m.RUnlock()

	descs := make([]string, 0)
	for _, l := range p {
		descs = append(descs, l.String())
	}

	out := Filter(s.operators, p...)

	outids := []string{}
	for _, o := range out {
		outids = append(outids, o.Identifier())
	}
	s.logger.Debugf("looking in %s with [%s], found [%v]", s.key.String(), strings.Join(descs, ", "), outids)
	return out
}

type OperatorFinder interface {
	Find(...Predicate) []*Operator
}

type MultiCatalogOperatorFinder interface {
	Catalog(registry.CatalogKey) OperatorFinder
	FindPreferred(*registry.CatalogKey, ...Predicate) []*Operator
	WithExistingOperators(*CatalogSnapshot) MultiCatalogOperatorFinder
	OperatorFinder
}

type EmptyOperatorFinder struct{}

func (f EmptyOperatorFinder) Find(...Predicate) []*Operator {
	return nil
}

func WithCSVName(name string) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			return o.name == name
		},
		desc: fmt.Sprintf("where name == %q", name),
	}
}

func WithChannel(channel string) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			// all operators match the empty channel
			if channel == "" {
				return true
			}
			if o.bundle == nil {
				return false
			}
			return o.bundle.ChannelName == channel
		},
		desc: fmt.Sprintf("where channel == %q", channel),
	}
}

func WithPackage(pkg string) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			return o.Package() == pkg
		},
		desc: fmt.Sprintf("where package == %q", pkg),
	}
}

func WithoutPackage(pkg string) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			return o.Package() != pkg
		},
		desc: fmt.Sprintf("where package != %q", pkg),
	}
}

func WithVersionInRange(r semver.Range, ver string) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			return o.version != nil && r(*o.version)
		},
		desc: fmt.Sprintf("where version in %q", ver),
	}
}

func ProvidingAPI(api opregistry.APIKey) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			if o.Identifier() == "lib-bucket-provisioner.v1.0.0" {
				fmt.Println("FOUND FOUND FOUND")
				fmt.Println(o.properties)
			}
			for _, p := range o.Properties() {
				if o.Identifier() == "lib-bucket-provisioner.v1.0.0" {
					fmt.Println(p.Type, p.Value)
				}
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
		},
		desc: fmt.Sprintf("where provided apis contain %q", fmt.Sprintf("%s/%s/%s", api.Group, api.Version, api.Kind)),
	}
}

func SkipRangeIncludes(version semver.Version) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			// TODO: lift range parsing to OperatorSurface
			semverRange, err := semver.ParseRange(o.bundle.SkipRange)
			return err == nil && semverRange(version)
		},
		desc: fmt.Sprintf("where skiprange inclused %q", version),
	}
}

func Replaces(name string) OperatorPredicate {
	return OperatorPredicate{
		f: func(o *Operator) bool {
			if o.Replaces() == name {
				return true
			}
			for _, s := range o.bundle.Skips {
				if s == name {
					return true
				}
			}
			return false
		},
		desc: fmt.Sprintf("where can update from %q", name),
	}
}

func And(p ...Predicate) Predicate {
	descs := make([]string, 0)
	for _, l := range p {
		descs = append(descs, l.String())
	}
	return OperatorPredicate{
		f: func(o *Operator) bool {
			for _, l := range p {
				if l.Apply(o) == false {
					return false
				}
			}
			return true
		},
		desc: fmt.Sprintf("where all [%s]", strings.Join(descs, ",")),
	}
}

func Or(p ...Predicate) Predicate {
	descs := make([]string, 0)
	for _, l := range p {
		descs = append(descs, l.String())
	}
	return OperatorPredicate{
		f: func(o *Operator) bool {
			for _, l := range p {
				if l.Apply(o) == true {
					return true
				}
			}
			return false
		},
		desc: fmt.Sprintf("where one [%s]", strings.Join(descs, ",")),
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

func Filter(operators []*Operator, p ...Predicate) []*Operator {
	var result []*Operator
	for _, o := range operators {
		if Matches(o, p...) {
			result = append(result, o)
		}
	}
	return result
}

func Matches(o *Operator, p ...Predicate) bool {
	return And(p...).Apply(o)
}
