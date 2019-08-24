package installedoperator

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorsinstall "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/install"
	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	operatorsv1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
	operatorsv1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
	porcelaininstall "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain/install"
	registry "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/registry/porcelain/installedoperator"
)

const (
	controllerAgentName = "installedoperator-controller"

	// SuccessSynced is used as part of the Event 'reason' when an Installed resource is synced.
	SuccessSynced = "Synced"

	// MessageResourceSynced is the message used for an Event fired when an Installed resource is synced successfully
	MessageResourceSynced = "Installed resource synced successfully"
)

func init() {
	// Add required types to scheme
	operatorsinstall.Install(scheme.Scheme)
	porcelaininstall.Install(scheme.Scheme)
}

// Controller is the controller implementation for installed operator resources.
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface

	// store gives direct access to storage for InstalledOperator resources.
	registry *registry.REST

	// ready is closed when the controller is ready to reconcile new resource changes.
	ready chan struct{}

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder

	csvIndexer cache.Indexer
	csvLister  operatorsv1alpha1listers.ClusterServiceVersionLister
	csvsSynced cache.InformerSynced
	subIndexer cache.Indexer
	subLister  operatorsv1alpha1listers.SubscriptionLister
	subsSynced cache.InformerSynced
	ogLister   operatorsv1listers.OperatorGroupLister
	ogsSynced  cache.InformerSynced
}

// NewController returns a new installed controller configured by the given options.
func NewController(options ...ControllerOption) (*Controller, error) {
	config := newControllerConfig()
	config.apply(options)
	if err := config.validate(); err != nil {
		return nil, err
	}
	config.complete()

	return newController(config), nil
}

func newController(config *controllerConfig) *Controller {
	// Create event broadcaster
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: config.kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset: config.kubeclientset,
		registry:      config.registry,
		workqueue:     config.workqueue,
		ready:         make(chan struct{}),
		recorder:      recorder,
		csvIndexer:    config.csvInformer.Informer().GetIndexer(),
		csvLister:     config.csvInformer.Lister(),
		csvsSynced:    config.csvInformer.Informer().HasSynced,
		subIndexer:    config.subInformer.Informer().GetIndexer(),
		subLister:     config.subInformer.Lister(),
		subsSynced:    config.subInformer.Informer().HasSynced,
		ogLister:      config.ogInformer.Lister(),
		ogsSynced:     config.ogInformer.Informer().HasSynced,
	}

	// Set up an event handler to track CSV changes.
	klog.Info("Setting up event handlers")
	config.csvInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			// Ignore events for copied CSVs
			// TODO: Prevent spoofing by ensuring the OG exists and matches the annotations.
			// May also need to do something in a deletion case if the annotations are removed.
			// It could be better to just check for an OG in the namespace directly, and see if it supports
			// the CSV's scope (installmodes).
			csv, ok := obj.(*operatorsv1alpha1.ClusterServiceVersion)
			return ok && !csv.IsCopied()
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: controller.handleCSV,
			UpdateFunc: func(oldObj, newObj interface{}) {
				controller.handleCSV(newObj)
			},
			DeleteFunc: controller.handleCSV,
		},
	})

	// Set up an event handler to track Subscription changes.
	config.subInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleSub,
		UpdateFunc: func(old, newObj interface{}) {
			controller.handleSub(newObj)
		},
		DeleteFunc: controller.handleSub,
	})

	// Set up an event handler to track OperatorGroup changes.
	config.ogInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleOG,
		UpdateFunc: func(old, newObj interface{}) {
			controller.handleOG(newObj)
		},
		DeleteFunc: controller.handleOG,
	})

	return controller
}

// sync builds an InstalledOperator resource for the given Key with the state of the Controller's CSV, Subscription,
// and OG caches, and then updates the Controller's store with that InstalledOperator resource.
// If the CSV matching the given Key no longer exists, the respective InstalledOperator resource is removed from the
// Controller's store if found.
func (c *Controller) sync(ctx context.Context, key Key) error {
	// Get the associated CSV
	nctx := genericapirequest.WithNamespace(ctx, key.Namespace)
	csv, err := c.csvLister.ClusterServiceVersions(key.Namespace).Get(key.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("CSV %s no longer exists, deleting installed", key)
			_, _, err := c.registry.REST.Delete(nctx, key.Name, metav1.NewDeleteOptions(0))
			if err != nil {
				if apierrors.IsNotFound(err) {
					utilruntime.HandleError(errors.Errorf("installed operator resource %s not found for delete", key))
					return nil
				}

				return err
			}

			return nil
		}

		return err
	}

	builder := porcelain.NewInstalledOperatorBuilder()
	if err := builder.SetClusterServiceVersion(csv); err != nil {
		utilruntime.HandleError(errors.Wrap(err, "error building installed operator from csv"))
	}

	if sub, err := c.relatedSubscription(key); err != nil {
		// Subscription is informational (non-fatal), continue after logging error
		utilruntime.HandleError(errors.Wrapf(err, "failed to get related subscription for installed operator resource %s", key))
	} else if err := builder.SetSubscription(sub); err != nil {
		utilruntime.HandleError(errors.Wrapf(err, "failed to set related subscription for installed operator resource %s", key))
	}

	obj, err := c.registry.REST.Get(nctx, key.Name, new(metav1.GetOptions))
	if err != nil {
		if !apierrors.IsNotFound(err) {
			utilruntime.HandleError(errors.Wrap(err, "error getting stored installed operator resource"))
			return nil
		}

		io, err := builder.Build()
		if err != nil {
			utilruntime.HandleError(errors.Wrap(err, "error building installed operator resource for create"))
			return nil
		}

		// Note: Using the registry's KeyFunc is important because it adds required key prefixes
		k, err := c.registry.KeyFunc(nctx, key.Name)
		if err != nil {
			utilruntime.HandleError(errors.Wrapf(err, "failed to create key for installed resource: %s", key))
			return nil
		}

		klog.V(4).Infof("Installed resource %s not found, generating initial installed: %v", key, io)
		if err := c.registry.Storage.Create(nctx, k, io.DeepCopy(), io, 0, false); err != nil {
			utilruntime.HandleError(errors.Wrapf(err, "failed to create installed resource: %v", io))
			return nil
		}
		klog.V(4).Infof("Installed operator resource created: %s", k)

		return nil
	}

	klog.V(4).Infof("Found installed operator resource %s, reconciling %v", key, obj)
	if err := builder.SetResourceVersionFromObject(obj); err != nil {
		utilruntime.HandleError(errors.Wrap(err, "error setting resource version from stored installed resource"))
		return nil
	}

	io, err := builder.Build()
	if err != nil {
		utilruntime.HandleError(errors.Wrap(err, "error building installed operator resource for update"))
		return nil
	}

	// Update the Installed store
	_, _, err = c.registry.Update(
		nctx,
		io.GetName(),
		rest.DefaultUpdatedObjectInfo(io),
		nil,
		nil,
		false,
		new(metav1.UpdateOptions),
	)
	if err != nil {
		utilruntime.HandleError(errors.Wrap(err, "failed to store installed resource"))
		return nil
	}
	klog.V(4).Infof("installed resource %s updated", key)

	// c.recorder.Event(io, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

// Key identifies a unique Installed resource.
// TODO: Generalize this?
type Key struct {
	// TODO: use pointers for strings?
	Namespace string
	Name      string
	// TODO: add GVK?
}

// String implements the fmt.Stringer interface for Keys.
func (k Key) String() string {
	// TODO: memoize string?
	return fmt.Sprintf("%s/%s", k.Namespace, k.Name)
}

// handleCSV enqueues the Installed resource associated with the given CSV.
// obj is expected to be a ClusterServiceVersion or DeletedFinalStateUnknown; unexpected types no-op.
func (c *Controller) handleCSV(obj interface{}) {
	var (
		csv *operatorsv1alpha1.ClusterServiceVersion
		ok  bool
	)
	if csv, ok = obj.(*operatorsv1alpha1.ClusterServiceVersion); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(errors.Errorf("error decoding csv, invalid type"))
			return
		}
		if csv, ok = tombstone.Obj.(*operatorsv1alpha1.ClusterServiceVersion); !ok {
			utilruntime.HandleError(errors.Errorf("error decoding csv tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted CSV '%s' from tombstone", csv.GetName())
	}

	c.enqueue(Key{
		Namespace: csv.GetNamespace(),
		Name:      csv.GetName(),
	})
	return
}

// handleSub enqueues the Installed resources associated with the given Subscription.
// obj is expected to be a Subscription or a DeletedFinalStateUnknown; unexpected types no-op
func (c *Controller) handleSub(obj interface{}) {
	var (
		sub *operatorsv1alpha1.Subscription
		ok  bool
	)
	if sub, ok = obj.(*operatorsv1alpha1.Subscription); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(errors.Errorf("error decoding subscription, invalid type"))
			return
		}
		if sub, ok = tombstone.Obj.(*operatorsv1alpha1.Subscription); !ok {
			utilruntime.HandleError(errors.Errorf("error decoding subscription tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted Subscription '%s' from tombstone", sub.GetName())
	}

	// Enqueue CSVs referenced by the Subscription
	current := sub.Status.CurrentCSV
	installed := sub.Status.InstalledCSV
	if current != "" {
		c.enqueue(Key{
			Namespace: sub.GetNamespace(),
			Name:      current,
		})
	}
	if installed != "" && installed != current {
		c.enqueue(Key{
			Namespace: sub.GetNamespace(),
			Name:      installed,
		})
	}

	return
}

// handleOG enqueues all Installed resource members of the given OperatorGroup.
// obj is expected to be a OperatorGroup or a DeletedFinalStateUnknown; unexpected types no-op
func (c *Controller) handleOG(obj interface{}) {
	var (
		og *operatorsv1.OperatorGroup
		ok bool
	)
	if og, ok = obj.(*operatorsv1.OperatorGroup); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(errors.Errorf("error decoding operatorgroup, invalid type"))
			return
		}
		og, ok = tombstone.Obj.(*operatorsv1.OperatorGroup)
		if !ok {
			utilruntime.HandleError(errors.Errorf("error decoding operatorgroup tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted OperatorGroup '%s' from tombstone", og.GetName())
	}

	// TODO: enqueue all non-copied CSVs in namespace
	return
}

func (c *Controller) relatedSubscription(ioKey Key) (*operatorsv1alpha1.Subscription, error) {
	objs, err := c.subIndexer.ByIndex(CSVSubscriptionIndexFuncKey, ioKey.String())
	if err != nil {
		return nil, errors.Wrap(err, "failed to get indexed subscriptions")
	}

	for _, obj := range objs {
		sub, ok := obj.(*operatorsv1alpha1.Subscription)
		if !ok {
			return nil, errors.Errorf("could not convert %T to subscription", obj)
		}
		if sub.Status.CurrentCSV == ioKey.Name || sub.Status.InstalledCSV == ioKey.Name {
			return sub, nil
		}
	}

	return nil, nil
}
