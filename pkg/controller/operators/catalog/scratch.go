package catalog

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	exv "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

// Q: What do we want to do?
// A: Register a set of views, relations, and actions to be triggered when an object changes.
//
// Q: What kind of objects?
// A: Arbitrary
//
// Q: What should produce object change events?
// A: A cache being updated (objects, indices)
//
// Q: How do we inform a set of views of an event?
// A: A view updates:
//		- View store updates
//      - Indices update (calling index funcs)
//		- Related views should be enqueued (by key) in their workqueues
//		- Related actions should be enqueued (by key) in their workqueues

type View interface {
	Key(value interface{}) (key string, err error)
	Value(obj interface{}) (value interface{}, err error)
	Indexers() cache.Indexers
}

// Views are a set of ViewFuncs keyed by their resource type
type Views map[reflect.Type]View

// Viewer is a storage interface that supports building alternate views of stored data.
type Viewer interface {
	cache.Indexer

	// View gets the value for a view type and given object.
	View(viewType reflect.Type, obj interface{}) (value interface{}, err error)

	// AddViews adds more views to this store. If you call this after you already have data
	// in the store, the results are undefined.
	AddViews(views ...View) error
}

type viewCache struct {
	cache.Indexer
	lock       sync.RWMutex
	views      Views
	defaultKey cache.KeyFunc
}

func (c *viewCache) View(viewType reflect.Type, obj interface{}) (value interface{}, err error) {
	c.lock.RLock()
	defer c.lock.RUnlock()

	view, ok := c.views[viewType]
	if !ok {
		return nil, fmt.Errorf("view %s not found", viewType.Name())
	}

	value, err = view.Value(obj)
	if err != nil {
		return nil, err
	}

	key, err := view.Key(value)
	if err != nil {
		return nil, err
	}

	stored, _, err := c.GetByKey(key)

	return stored, err
}

func (c *viewCache) AddViews(views ...View) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	if len(c.views) > 0 {
		return fmt.Errorf("cannot add views to running viewer")
	}

	if c.views == nil {
		c.views = Views{}
	}

	for _, view := range views {
		c.views[reflect.TypeOf(view)] = view
	}

	return nil
}

// viewKey invokes the key function matching the view object type.
// This allows a store expecting a single key function to support multiple types.
func (c *viewCache) viewKey(value interface{}) (string, error) {
	// Check for a view that matches the dynamic type.
	if view, ok := c.views[reflect.TypeOf(value)]; ok {
		return view.Key(value)
	}

	// TODO: have a default key function if a view doesn't exist? This could be useful for storing runtime.Objects in the same cache.
	return "", fmt.Errorf("view type %T unmanaged", value)
}

type modifierFunc func(obj interface{}) error

func (c *viewCache) modifyViews(obj interface{}, modify modifierFunc) error {
	c.lock.RLock()
	defer c.lock.RUnlock()

	errs := []error{}
	for _, view := range c.views {
		value, err := view.Value(obj)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if err := modify(value); err != nil {
			errs = append(errs, err)
		}
	}

	// TODO: run indexers on unmodified object?
	// TODO: modify object as is?

	return utilerrors.NewAggregate(errs)
}

// Add sets an item in the cache.
func (c *viewCache) Add(obj interface{}) error {
	return c.modifyViews(obj, c.Indexer.Add)
}

// Update sets an item in the cache to its updated state.
func (c *viewCache) Update(obj interface{}) error {
	return c.modifyViews(obj, c.Indexer.Update)
}

// Delete removes an item from the cache.
func (c *viewCache) Delete(obj interface{}) error {
	return c.modifyViews(obj, c.Indexer.Delete)
}

// NewViewer returns a new Viewer containing all of the given views.
func NewViewer(views Views) Viewer {
	c := &viewCache{views: views}
	indexers := cache.Indexers{}
	for _, view := range views {
		for name, index := range view.Indexers() {
			indexers[name] = index
		}
	}
	c.Indexer = cache.NewIndexer(c.viewKey, indexers)
	return c
}

type viewOperator struct {
	*queueinformer.Operator
	logger       *logrus.Logger
	resyncPeriod time.Duration
	kubeconfig   string
	namespaces   []string
	client       versioned.Interface
	checker      reconciler.RegistryChecker
	viewOptions  []viewOption
	viewer       Viewer
}

type OperatorOption func(*viewOperator)

func NewViewOperator(configmapRegistryImage string, options ...OperatorOption) (*viewOperator, error) {
	// Set defaults
	op := &viewOperator{
		logger:       logrus.New(),
		resyncPeriod: 15 * time.Minute,
		namespaces:   []string{""},
		viewOptions:  []viewOption{},
	}

	// Apply all options
	for _, option := range options {
		option(op)
	}

	// Set additional defaults if not set by options
	if op.client == nil {
		c, err := client.NewClient("")
		if err != nil {
			return nil, err
		}
		op.client = c
	}

	if op.OpClient == nil {
		op.OpClient = operatorclient.NewClientFromConfig("", op.logger)
	}

	if op.Operator == nil {
		queueinformer.NewOperatorFromClient(op.OpClient, op.logger)
	}

	if op.viewer == nil {
		op.viewer = NewViewer(Views{})
	}

	// TODO: View method for adding notification recipients - mapping view to queue
	// TODO: Build index functions for notification recipients.

	// TODO: Notify - Lists viewer indexes by index key and enqueues them in the appropriate workqueue.
	// TODO: Act - Updates the cluster in some way using the views

	// Create an OperatorLister
	lister := operatorlister.NewLister()

	// Setup Informer
	namespace := metav1.NamespaceAll
	crFactory := exv.NewSharedInformerFactoryWithOptions(op.client, op.resyncPeriod, exv.WithNamespace(namespace))
	catsrcInformer := crFactory.Operators().V1alpha1().CatalogSources()
	subInformer := crFactory.Operators().V1alpha1().Subscriptions()

	// Setup View
	scv := NewSubCatalogView(configmapRegistryImage, append(op.viewOptions, withSubIndexer(subInformer.Informer().GetIndexer()))...)
	op.viewer.AddViews(scv)

	queueName := "catsrc/" + namespace
	catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), queueName)

	// Create an informer for each catalog namespace
	deleteCatalog := &cache.ResourceEventHandlerFuncs{
		DeleteFunc: func(obj interface{}) {
			catsrc, ok := obj.(*v1alpha1.CatalogSource)
			if !ok {
				logrus.Warnf("incorrect delete type %T, skipping catalogsource view removal", obj)
				return
			}
			logger := op.logger.WithFields(logrus.Fields{
				"catsrc":    catsrc.GetName(),
				"namespace": catsrc.GetNamespace(),
			})

			if err := op.viewer.Delete(obj); err != nil {
				logger.WithError(err).Warn("failed to remove views")
				return
			}

			logger.Debug("views removed")
		},
	}

	// TODO: Use MetaView as a mechanism to notify CatalogSource changes
	// Pod create pod queue informers
	factory := informers.NewSharedInformerFactoryWithOptions(op.OpClient.KubernetesInterface(), op.resyncPeriod, informers.WithNamespace(namespace))
	podInformer := factory.Core().V1().Pods()
	lister.CoreV1().RegisterPodLister(namespace, podInformer.Lister())
	op.RegisterQueueInformer(
		queueinformer.NewInformer(
			catsrcQueue,
			catsrcInformer.Informer(),
			func(obj interface{}) (syncErr error) {
				// TODO: implement (View -> Notify) -> Act stages.
				// View stage: generates all views - collect aggregate error
				// Notify: notify all interested parties by looking at the indexes (only if they have changed) - collect aggregate error
				// Act stage: uses views to act on the cluster - collect aggregate error
				// View and Notify should be synchronous
				// Actions can execute in parallel to (View and Notify) and possibly to themselves
				// Preferrably these stages are encapsulated by a QueueInformer wrapper... ViewInformer?

				// Generate all views
				var errs []error
				if err := op.viewer.Add(obj); err != nil {
					errs = append(errs, err)
				}

				// Act on views
				// TODO: A few ways we could associate views with actions:
				// * A view _has an_ action - a direct function call (e.g. view.Act())
				// * A view _informs_ an action - a key is enqueued on an action queue when a view is updated
				//   * How do we decide which actions to enqueue?
				//   * How do we order enqueuing?
				//   * Can some actions be orthogonal?
				//   * Should the relation be reversed - action relates to a set of views)?
				//   * Is there a pre-existing model we can use for this - petri-net?

				// What would this look like with a petri-net:
				// * Views are places
				// *

				syncErr = utilerrors.NewAggregate(errs)
				return
			},
			deleteCatalog,
			queueName,
			metrics.NewMetricsNil(),
			op.logger))

	return op, nil
}

// Define

// Define views

// metaView is a transparent view that can be used as a basis for more complex views.
// It holds a set of commonly used utilities.
// TODO: should this just be the Operator itself?
type metaView struct {
	logger       *logrus.Logger
	namespace    string
	opClient     operatorclient.ClientInterface
	lister       operatorlister.OperatorLister
	getReference func(runtime.Object) (*corev1.ObjectReference, error)
	now          func() metav1.Time
}

func (view *metaView) Key(value interface{}) (key string, err error) {
	// Use the most common key func (namespace/name)
	// TODO: could we use metaView to store anything that implements meta.Interface by default?
	return cache.DeletionHandlingMetaNamespaceKeyFunc(value)
}

func (view *metaView) Value(obj interface{}) (value interface{}, err error) {
	// Passthrough
	value = obj
	return
}

func (view *metaView) Indexers() cache.Indexers {
	// Use the most common indexer (namespace)
	return cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}
}

type viewOption func(View)

func applyViewOptions(view View, options ...viewOption) {
	for _, option := range options {
		option(view)
	}
}

// NewMetaView returns a new metaView with the given options applied.
func NewMetaView(options ...viewOption) *metaView {
	// Set defaults
	view := &metaView{
		lister:       operatorlister.NewLister(),
		getReference: operators.GetReference,
		now:          timeNow,
	}

	applyViewOptions(view, options...)

	// Set additional defaults if not updated by options
	if view.opClient == nil {
		view.opClient = operatorclient.NewClientFromConfig("", view.logger)
	}

	return view
}

// subCatalogView implements the SubscriptionCatalogStatus view over CatalogSources.
type subCatalogView struct {
	*metaView
	subIndexer        cache.Indexer
	reconcilerFactory reconciler.RegistryReconcilerFactory
}

func withSubIndexer(subIndexer cache.Indexer) viewOption {
	return func(view View) {
		if scv, ok := view.(*subCatalogView); ok {
			scv.subIndexer = subIndexer
		}
	}
}

func NewSubCatalogView(configmapRegistryImage string, options ...viewOption) *subCatalogView {
	// Set defaults
	view := &subCatalogView{
		metaView: NewMetaView(),
		// TODO: Missing subCatalogIndexer can result in bad configuration
	}

	applyViewOptions(view, options...)

	// Set additional defaults if not updated by options
	if view.reconcilerFactory == nil {
		view.reconcilerFactory = reconciler.NewRegistryReconcilerFactory(view.lister, view.opClient, configmapRegistryImage)
	}

	return view
}

const (
	subCatalogPrefix string = "subcatalogstatus"
	subCatalogKey    string = subCatalogPrefix + "/%s/%s"
)

func (view *subCatalogView) Key(value interface{}) (key string, err error) {
	scs, ok := value.(*v1alpha1.SubscriptionCatalogStatus)
	if !ok {
		err = fmt.Errorf("unexpected view value type %T, expected %T", value, (*v1alpha1.SubscriptionCatalogStatus)(nil))
		return
	}

	key = fmt.Sprintf(subCatalogKey, scs.CatalogSourceRef.Name, scs.CatalogSourceRef.Namespace)
	return
}

func (view *subCatalogView) Value(obj interface{}) (value interface{}, err error) {
	catalog, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		return nil, fmt.Errorf("unexpected object type, expected %T and got %T", (*v1alpha1.CatalogSource)(nil), obj)
	}

	// Check the registry server health
	healthy, err := view.reconcilerFactory.ReconcilerForSource(catalog).CheckRegistryServer(catalog)
	if err != nil {
		return
	}

	// Create the view
	ref, err := view.getReference(catalog)
	if err != nil {
		return
	}

	value = &v1alpha1.SubscriptionCatalogStatus{
		CatalogSourceRef: ref,
		LastUpdated:      view.now(),
		Healthy:          healthy,
	}

	return
}

const (
	subCatalogIndex string = "subcatalog"
)

// subCatalogIndexer returns a set of indices for a given SubscriptionCatalogStatus
func (view *subCatalogView) subCatalogIndexer(obj interface{}) ([]string, error) {
	scs, ok := obj.(*v1alpha1.SubscriptionCatalogStatus)
	if !ok {
		// Can't build indices for this type
		return []string{}, nil
	}

	if scs.CatalogSourceRef.Namespace == view.namespace {
		// The CatalogSource is global, get keys for subscriptions in all watched namespaces
		// TODO: No guarantees that subIndex contains cache.NamespaceIndex
		return view.subIndexer.IndexKeys(cache.NamespaceIndex, scs.CatalogSourceRef.Namespace)
	}

	return view.subIndexer.IndexKeys(cache.NamespaceIndex, scs.CatalogSourceRef.Namespace)
}

func (view *subCatalogView) Indexers() cache.Indexers {
	return cache.Indexers{
		subCatalogIndex: view.subCatalogIndexer,
	}
}
