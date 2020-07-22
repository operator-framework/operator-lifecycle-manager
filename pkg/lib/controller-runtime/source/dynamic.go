package source

import (
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Dynamic sources events for all kinds exposed in discovery.
type Dynamic struct {
	// Injected by the controller manager
	client discovery.ServerResourcesInterface
	cache  ctrlcache.Cache
	stop   <-chan struct{}

	mu      sync.RWMutex
	sources map[schema.GroupVersionKind]source.Source
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

		informable, err := d.InformableGVKs()
		if err != nil {
			log.Error(err, "could not get available gvks")
		}

		for _, gvk := range informable {
			if d.sourceStarted(gvk) {
				log.V(4).Info("dynamic source already started", "gvk", gvk)
				continue
			}

			if err := d.startSource(gvk, handle, queue, predicates...); err != nil {
				log.Error(err, "failed to start dynamic source", "gvk", gvk)
			}
		}

	}, 15*time.Second, d.stop)

	synced.Wait()

	return nil
}

var (
	knownInformables []schema.GroupVersionKind
	informablesLock  sync.RWMutex
)

// InformableGVKs returns the set of GVKs available in discovery that support the list and watch verbs (informable).
func (d *Dynamic) InformableGVKs() (informables []schema.GroupVersionKind, err error) {
	if d.client == nil {
		return nil, fmt.Errorf("no client")
	}

	var resourceLists []*metav1.APIResourceList
	resourceLists, err = d.client.ServerPreferredResources()
	if err != nil {
		informablesLock.RLock()
		defer informablesLock.RUnlock()
		log.Error(err, "could not get preferred resources, returning latest known gvks")
		return knownInformables, nil
	}

	latest := discovery.FilteredBy(discovery.SupportsAllVerbs{Verbs: []string{"list", "watch"}}, resourceLists)
	for _, rl := range latest {
		gv, err := schema.ParseGroupVersion(rl.GroupVersion)
		if err != nil {
			log.Error(err, "could not parse group version")
			continue
		}

		for _, r := range rl.APIResources {
			gvk := schema.GroupVersionKind{Group: gv.Group, Version: gv.Version, Kind: r.Kind}
			informables = append(informables, gvk)
		}
	}

	if len(informables) > 0 {
		// Memoize the latest informables in case discovery fails on successive calls
		informablesLock.Lock()
		defer informablesLock.Unlock()
		knownInformables = informables
	}

	return
}

func (d *Dynamic) startSource(gvk schema.GroupVersionKind, handle handler.EventHandler, queue workqueue.RateLimitingInterface, predicates ...predicate.Predicate) (err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sources == nil {
		d.sources = map[schema.GroupVersionKind]source.Source{}
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

	d.sources[gvk] = s

	return
}

func (d *Dynamic) sourceStarted(gvk schema.GroupVersionKind) (added bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	_, added = d.sources[gvk]

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

// InjectConfig injects the the REST Config dependency.
func (d *Dynamic) InjectConfig(config *rest.Config) (err error) {
	d.client, err = discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		log.Error(err, "error injecting config")
	} else {
		log.Info("config injected!")
	}
	return err
}
