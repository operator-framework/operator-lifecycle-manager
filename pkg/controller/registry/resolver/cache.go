package resolver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/blang/semver"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-registry/pkg/client"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

type ClientProvider interface {
	Get() (client.Interface, error)
}

type LazyClient struct {
	address string
	c       client.Interface
	m       sync.Mutex
}

func (lc *LazyClient) Get() (client.Interface, error) {
	lc.m.Lock()
	defer lc.m.Unlock()
	if lc.c != nil {
		return lc.c, nil
	}
	c, err := client.NewClient(lc.address)
	if err != nil {
		return nil, err
	}
	lc.c = c
	return lc.c, nil
}

type RegistryClientProvider interface {
	ClientsForNamespaces(namespaces ...string) map[CatalogKey]ClientProvider
}

type DefaultRegistryClientProvider struct {
	logger logrus.FieldLogger
	c      versioned.Interface
}

func NewDefaultRegistryClientProvider(c versioned.Interface) *DefaultRegistryClientProvider {
	return &DefaultRegistryClientProvider{
		logger: logrus.New(),
		c:      c,
	}
}

func (rcp *DefaultRegistryClientProvider) ClientsForNamespaces(namespaces ...string) map[CatalogKey]ClientProvider {
	result := make(map[CatalogKey]ClientProvider)
	for _, namespace := range namespaces {
		list, err := rcp.c.OperatorsV1alpha1().CatalogSources(namespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			rcp.logger.Errorf("failed to list catalogsources in %s: %s", namespace, err.Error())
			continue
		}
		for _, source := range list.Items {
			if source.Status.RegistryServiceStatus == nil {
				continue
			}
			key := CatalogKey{
				Namespace: source.Namespace,
				Name:      source.Name,
			}
			result[key] = &LazyClient{address: source.Address()}
		}
	}
	return result
}

type OperatorCacheProvider interface {
	Namespaced(namespaces ...string) MultiCatalogOperatorFinder
}

type OperatorCache struct {
	logger    logrus.FieldLogger
	rcp       RegistryClientProvider
	snapshots map[CatalogKey]*CatalogSnapshot
	ttl       time.Duration
	sem       chan struct{}
	m         sync.RWMutex
}

var _ OperatorCacheProvider = &OperatorCache{}

func NewOperatorCache(rcp RegistryClientProvider) *OperatorCache {
	const (
		MaxConcurrentSnapshotUpdates = 4
	)

	return &OperatorCache{
		logger:    logrus.New(),
		rcp:       rcp,
		snapshots: make(map[CatalogKey]*CatalogSnapshot),
		ttl:       5 * time.Minute,
		sem:       make(chan struct{}, MaxConcurrentSnapshotUpdates),
	}
}

type NamespacedOperatorCache struct {
	snapshots map[CatalogKey]*CatalogSnapshot
}

func (c *OperatorCache) Namespaced(namespaces ...string) MultiCatalogOperatorFinder {
	const (
		CachePopulateTimeout = time.Minute
	)

	now := time.Now()
	clients := c.rcp.ClientsForNamespaces(namespaces...)

	result := NamespacedOperatorCache{
		snapshots: make(map[CatalogKey]*CatalogSnapshot),
	}

	var misses []CatalogKey
	func() {
		c.m.RLock()
		defer c.m.RUnlock()
		for key := range clients {
			if snapshot, ok := c.snapshots[key]; ok && !snapshot.Expired(now) {
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
	var expired []CatalogKey
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
		if snapshot, ok := c.snapshots[misses[i]]; ok && !snapshot.Expired(now) {
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

func (c *OperatorCache) populate(ctx context.Context, snapshot *CatalogSnapshot, provider ClientProvider) {
	defer snapshot.m.Unlock()

	c.sem <- struct{}{}
	defer func() { <-c.sem }()

	registry, err := provider.Get()
	if err != nil {
		snapshot.logger.Errorf("failed to connect to registry: %s", err.Error())
		return
	}

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

func (c *NamespacedOperatorCache) Catalog(k CatalogKey) OperatorFinder {
	if snapshot, ok := c.snapshots[k]; ok {
		return snapshot
	}
	return EmptyOperatorFinder{}
}

func (c *NamespacedOperatorCache) Find(p ...OperatorPredicate) []*Operator {
	var result []*Operator
	for _, snapshot := range c.snapshots {
		result = append(result, snapshot.Find(p...)...)
	}
	return result
}

type CatalogSnapshot struct {
	logger    logrus.FieldLogger
	key       CatalogKey
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

type OperatorPredicate func(*Operator) bool

func (s *CatalogSnapshot) Find(p ...OperatorPredicate) []*Operator {
	s.m.RLock()
	defer s.m.RUnlock()

	var result []*Operator
	for _, o := range s.operators {
		var failed bool
		for _, each := range p {
			if !each(o) {
				failed = true
				break
			}
		}
		if !failed {
			result = append(result, o)
		}
	}
	return result
}

func (n *NamespacedOperatorCache) GetCSVNameFromAllCatalogs(csvName string) ([]*Operator, error) {
	var result []*Operator
	for _, s := range n.snapshots {
		result = append(result, s.Find(func(o *Operator) bool {
			return o.name == csvName
		})...)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("operator %s not found in any catalog", csvName)
	}
	return result, nil
}

func (n *NamespacedOperatorCache) GetCsvFromAllCatalogsWithFilter(csvName string, filter installableFilter) ([]*Operator, error) {
	var result []*Operator
	for _, s := range n.snapshots {
		result = append(result, s.Find(func(o *Operator) bool {
			candidate := true
			if filter.channel != "" && o.Bundle().GetChannelName() != filter.channel {
				candidate = false
			}
			if !filter.catalog.IsEmpty() && !filter.catalog.IsEqual(o.SourceInfo().Catalog) {
				candidate = false
			}
			return candidate && o.name == csvName
		})...)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("operator with csvName %s not found in any catalog", csvName)
	}
	return result, nil
}

type OperatorFinder interface {
	Find(...OperatorPredicate) []*Operator
}

type MultiCatalogOperatorFinder interface {
	Catalog(CatalogKey) OperatorFinder
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

func ProvidingAPI(api registry.APIKey) OperatorPredicate {
	return func(o *Operator) bool {
		_, provided := o.ProvidedAPIs()[api]
		return provided
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
