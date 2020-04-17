package source

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("source")

type Dynamic struct {
	// Injected by the controller manager
	client discovery.ServerResourcesInterface
	cache  ctrlcache.Cache
	stop   <-chan struct{}

	mu     sync.RWMutex
	active map[schema.GroupVersionKind]source.Source
}

var (
	_ source.Source    = &Dynamic{}
	_ inject.Cache     = &Dynamic{}
	_ inject.Stoppable = &Dynamic{}
)

// Start is internal and should be called only by the Controller to register an EventHandler with the Informer to enqueue reconcile.Requests.
func (d *Dynamic) Start(handle handler.EventHandler, queue workqueue.RateLimitingInterface, predicates ...predicate.Predicate) error {
	if d.client == nil {
		return fmt.Errorf("must inject rest config before starting dynamic source")
	}
	if d.cache == nil {
		return fmt.Errorf("must inject cache before starting dynamic source")
	}
	if d.stop == nil {
		return fmt.Errorf("must inject stop channel before starting dynamic source")
	}

	// TODO(njhale): Build an aggregate watch implementation that tracks all APIResourceList for available groups.
	// TODO(njhale): Stop sources when GVKs are removed
	var (
		synced sync.WaitGroup
		once   sync.Once
	)
	synced.Add(1)

	go wait.Until(func() {
		defer once.Do(func() {
			synced.Done()
		})

		resourceLists, err := d.client.ServerPreferredResources()
		if err != nil {
			log.Error(err, "could not get preferred resources")
			return
		}
		watchable := discovery.FilteredBy(discovery.SupportsAllVerbs{Verbs: []string{"list", "watch"}}, resourceLists)

		for _, rl := range watchable {
			gv, err := schema.ParseGroupVersion(rl.GroupVersion)
			if err != nil {
				log.Error(err, "could not parse group version")
				continue
			}

			for _, r := range rl.APIResources {
				gvk := schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: r.Kind}
				if d.isActive(gvk) {
					log.V(4).Info("dynamic source already started", "gvk", gvk)
					continue
				}

				if err := d.activate(gvk, handle, queue, predicates...); err != nil {
					log.Error(err, "failed to start dynamic source", "gvk", gvk)
				}
			}
		}

	}, 15*time.Second, d.stop)

	synced.Wait()

	return nil
}

func (d *Dynamic) activate(gvk schema.GroupVersionKind, handle handler.EventHandler, queue workqueue.RateLimitingInterface, predicates ...predicate.Predicate) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.active == nil {
		d.active = map[schema.GroupVersionKind]source.Source{}
	}

	r := &unstructured.Unstructured{}
	r.SetGroupVersionKind(gvk)
	s := &source.Kind{Type: r}
	if err = s.InjectCache(d.cache); err != nil {
		return
	}

	if err = s.Start(handle, queue, predicates...); err != nil {
		return
	}

	log.Info("dynamic source started", "gvk", gvk)

	d.active[gvk] = s

	return
}

func (d *Dynamic) isActive(gvk schema.GroupVersionKind) (active bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	_, active = d.active[gvk]

	return
}

// InjectCache injects the Cache dependency.
// It is primarily used by the ControllerManager to share a Cache between Sources.
func (d *Dynamic) InjectCache(c ctrlcache.Cache) error {
	if d.cache == nil {
		d.cache = c
	}
	return nil
}

// InjectStopChannel is internal should be called only by the Controller.
// It is used to inject the stop channel initialized by the ControllerManager.
func (d *Dynamic) InjectStopChannel(stop <-chan struct{}) error {
	if d.stop == nil {
		d.stop = stop
	}

	return nil
}

func (d *Dynamic) InjectConfig(config *rest.Config) (err error) {
	d.client, err = discovery.NewDiscoveryClientForConfig(config)
	return err
}

// Active returns the set of GVKs for which the dynamic source has already started sources.
func (d *Dynamic) Active() (active []schema.GroupVersionKind) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for gvk := range d.active {
		active = append(active, gvk)
	}

	if len(active) < 1 {
		return
	}

	sort.Slice(active, func(i, j int) bool {
		return active[i].String() < active[j].String()
	})

	return
}
