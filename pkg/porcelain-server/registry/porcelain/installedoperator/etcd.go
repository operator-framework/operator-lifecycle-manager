package installedoperator

import (
	"context"

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"

	operatorsinformers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
	porcelainv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/registry"
)

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(scheme *runtime.Scheme, optsGetter generic.RESTOptionsGetter, nsInformer coreinformers.NamespaceInformer, csvInformer operatorsinformers.ClusterServiceVersionInformer) (*REST, error) {
	strategy := NewStrategy(scheme)

	store := &genericregistry.Store{
		NewFunc:                  func() runtime.Object { return &porcelain.InstalledOperator{} },
		NewListFunc:              func() runtime.Object { return &porcelain.InstalledOperatorList{} },
		PredicateFunc:            MatchInstalled,
		DefaultQualifiedResource: porcelain.Resource("installedoperators"),

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
		DeleteStrategy: strategy,
	}
	options := &generic.StoreOptions{RESTOptions: optsGetter, AttrFunc: GetAttrs}
	if err := store.CompleteWithOptions(options); err != nil {
		return nil, err
	}

	// Add target namespace IndexFunc to the shared CSV indexer.
	// This lets us quickly lookup which CSVs are targeting a given namespace.
	csvIndexer := csvInformer.Informer().GetIndexer()
	csvIndexer.AddIndexers(cache.Indexers{TargetNamespaceIndexFuncKey: TargetNamespaceIndexFunc})

	return &REST{
		REST:       &registry.REST{store},
		nsIndexer:  nsInformer.Informer().GetIndexer(),
		csvIndexer: csvIndexer,
	}, nil
}

type REST struct {
	*registry.REST

	// nsIndexer is used to lookup namespaces.
	nsIndexer cache.Indexer

	// csvIndexer is used to lookup CSVs that watch a request namespace.
	csvIndexer cache.Indexer
}

// Get returns a synthesized Installed resource if a CSV is targeting the request namespace.
func (r *REST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	// Get the keys of CSVs targeting the request namespace
	klog.V(4).Infof("Proxying get request for %s", name)
	targetNamespace, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("no namespace in request")
	}

	var operatorNamespace string
	err := r.forEachTargeting(targetNamespace, func(ns, n string) (stop bool, err error) {
		if stop = name == n; stop {
			operatorNamespace = ns
		}
		return
	})
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	if operatorNamespace == "" {
		return nil, apierrors.NewNotFound(r.DefaultQualifiedResource, name)
	}

	// Change the request namespace and grab the installed resource.
	nctx := request.WithNamespace(ctx, operatorNamespace)
	obj, err := r.REST.Get(nctx, name, options)
	if err != nil {
		return nil, err
	}

	// Update the object's Namespace and UID to match the expected.
	// This is important when references Installed resources are used as OwnerReferences in the
	// request namespace, since k8s GC has trouble with inter-namespace OwnerReferences.
	// Additionally, request validation checks that the returned object's namespace matches
	// that of the request.
	synthesizeObject(targetNamespace, obj)

	return obj, nil
}

// List returns a list of synthesized Installed resources for CSVs that target the request namespace.
func (r *REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	// Get the keys of CSVs targeting the request namespace
	targetNamespace, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("no namespace in request")
	}

	var getNamespaces func() []string
	if targetNamespace != metav1.NamespaceAll {
		// Always return a list containing only the target namespace
		namespaces := []string{targetNamespace}
		getNamespaces = func() []string {
			return namespaces
		}
	} else {
		getNamespaces = r.nsIndexer.ListKeys
	}

	targeting := &porcelain.InstalledOperatorList{}
	for _, namespace := range getNamespaces() {
		err := r.forEachTargetingNamespace(namespace, func(ns string) (stop bool, err error) {
			nctx := request.WithNamespace(ctx, ns)
			objs, err := r.REST.List(nctx, options)
			if err != nil {
				stop = true
				return
			}

			installedList, ok := objs.(*porcelain.InstalledOperatorList)
			if !ok {
				return true, errors.Errorf("failed to assert installed list: %v", objs)
			}

			for i := range installedList.Items {
				synthesizeObject(namespace, &installedList.Items[i])
			}

			targeting.Items = append(targeting.Items, installedList.Items...)
			return
		})
		if err != nil {
			return nil, apierrors.NewInternalError(err)
		}
	}

	return targeting, nil
}

// Watch returns a watch that aggregates InstalledOperator events for operators that target the request namespace.
//
// If the request specifies all namespaces, events are synthesized for every <InstalledOperator, target namespace> pair.
func (r *REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	klog.V(4).Info("Proxying watch request")
	targetNamespace, ok := request.NamespaceFrom(ctx)
	if !ok {
		return nil, apierrors.NewBadRequest("no namespace in request")
	}

	var getNamespaces func() []string
	if targetNamespace != metav1.NamespaceAll {
		// Always return a list containing only the target namespace
		namespaces := []string{targetNamespace}
		getNamespaces = func() []string {
			return namespaces
		}
	} else {
		getNamespaces = r.nsIndexer.ListKeys
	}

	actx := request.WithNamespace(ctx, metav1.NamespaceAll)
	all, err := r.REST.Watch(actx, options)
	if err != nil {
		return nil, err
	}

	return synthesizingWatch(actx, all, getNamespaces)
}

// Delete is a no-op that exists to satisfy a k8s GC requirement so Installed resources may be used in OwnerReferences.
func (r *REST) Delete(ctx context.Context, name string, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	// Return a forbidden error
	klog.V(4).Infof("Returning forbidden error for delete request: %s, %v, %v", name, ctx, options)
	return nil, false, apierrors.NewForbidden(porcelainv1alpha1.Resource("installedoperators"), name, errors.New("synthetic resource deletion forbidden"))
}

// targeting returns a list of keys representing all CSVs targeting the given namespace.
func (r *REST) targeting(targetNamespace string) (keys []string, err error) {
	// Append keys for operators directly targeting
	specific, err := r.csvIndexer.IndexKeys(TargetNamespaceIndexFuncKey, targetNamespace)
	if err != nil {
		return nil, err
	}
	keys = append(keys, specific...)

	// Append keys for global operators
	global, err := r.csvIndexer.IndexKeys(TargetNamespaceIndexFuncKey, metav1.NamespaceAll)
	if err != nil {
		return nil, err
	}
	keys = append(keys, global...)

	return
}

func (r *REST) forEachTargeting(targetNamespace string, do func(namespace, name string) (stop bool, err error)) error {
	targeting, err := r.targeting(targetNamespace)
	if err != nil {
		return err
	}

	var errs []error
	for _, t := range targeting {
		namespace, name, err := cache.SplitMetaNamespaceKey(t)
		if err != nil {
			utilruntime.HandleError(errors.Wrapf(err, "failed to split csv key %s for list of installed resources in %s", t, targetNamespace))
			continue
		}
		stop, err := do(namespace, name)
		if err != nil {
			errs = append(errs, err)
		}
		if stop {
			break
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *REST) forEachTargetingNamespace(targetNamespace string, do func(namespace string) (stop bool, err error)) error {
	visited := map[string]struct{}{}
	return r.forEachTargeting(targetNamespace, func(ns, _ string) (stop bool, err error) {
		if _, ok := visited[ns]; ok {
			return
		}
		visited[ns] = struct{}{}
		return do(ns)
	})
}

// synthesizeObject updates the UID and Namespace of the given object.
//
// The given namespace is used as a replacement for the Namespace field and is prepended to the UID field.
func synthesizeObject(namespace string, base runtime.Object) error {
	m, err := porcelain.InstalledOperatorMetaAccessor(base)
	if err != nil {
		return err
	}
	m.WithNamespace(namespace)
	m.Sanitize()

	return nil
}

func synthesizingWatch(ctx context.Context, base watch.Interface, getNamespaces func() []string) (watch.Interface, error) {
	result := make(chan watch.Event, len(getNamespaces()))
	proxy := watch.NewProxyWatcher(result)
	go func() {
		defer close(result)
		for {
			select {
			case <-ctx.Done():
				utilruntime.HandleError(ctx.Err())
				return
			case <-proxy.StopChan():
				klog.V(4).Info("Watch stopped")
				return
			case in, ok := <-base.ResultChan():
				if !ok {
					utilruntime.HandleError(errors.New("source watch closed"))
					return
				}

				m, err := porcelain.InstalledOperatorMetaAccessor(in.Object)
				if err != nil {
					utilruntime.HandleError(errors.Wrapf(err, "error accessing installed operator meta"))
					break
				}

				klog.V(4).Infof("Watch event incoming for %s/%s", m.GetNamespace(), m.GetName())

				func() {
					for _, ns := range getNamespaces() {
						if !m.TargetsNamespace(ns) {
							klog.V(4).Infof("%s/%s does not target %s, skipping: %v", m.GetNamespace(), m.GetName(), ns, m.GetAnnotations())
							continue
						}

						event := in.DeepCopy()
						if err := synthesizeObject(ns, event.Object); err != nil {
							utilruntime.HandleError(errors.Wrapf(err, "error synthesizing objects"))
							continue
						}

						select {
						case <-ctx.Done():
							utilruntime.HandleError(errors.New("context closed before synthesized event emitted"))
							return
						case <-proxy.StopChan():
							utilruntime.HandleError(errors.New("watch stopped before synthesized event emitted"))
							return
						case result <- *event:
						}
					}
				}()
			}
		}
	}()

	return proxy, nil
}
