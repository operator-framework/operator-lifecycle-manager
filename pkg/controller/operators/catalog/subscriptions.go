package catalog

import (
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

var (
	ErrNilSubscription = errors.New("invalid Subscription object: <nil>")
)

const (
	PackageLabel          = "olm.package"
	CatalogLabel          = "olm.catalog"
	CatalogNamespaceLabel = "olm.catalog.namespace"
	ChannelLabel          = "olm.channel"
)

func labelsForSubscription(sub *v1alpha1.Subscription) map[string]string {
	return map[string]string{
		PackageLabel:          sub.Spec.Package,
		CatalogLabel:          sub.Spec.CatalogSource,
		CatalogNamespaceLabel: sub.Spec.CatalogSourceNamespace,
		ChannelLabel:          sub.Spec.Channel,
	}
}

// TODO: remove this once UI no longer needs them
func legacyLabelsForSubscription(sub *v1alpha1.Subscription) map[string]string {
	return map[string]string{
		"alm-package": sub.Spec.Package,
		"alm-catalog": sub.Spec.CatalogSource,
		"alm-channel": sub.Spec.Channel,
	}
}

func ensureLabels(sub *v1alpha1.Subscription) *v1alpha1.Subscription {
	labels := sub.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range labelsForSubscription(sub) {
		labels[k] = v
	}
	for k, v := range legacyLabelsForSubscription(sub) {
		labels[k] = v
	}
	sub.SetLabels(labels)
	return sub
}

// ---------------------------------------------------------------------------------------------------------
// Views needed by Subscriptions

// subCatViewer implements the SubscriptionCatalogStatus view over CatalogSources.
type subCatViewer struct {
	*metaViewer
	getReference func(obj runtime.Object) (*corev1.ObjectReference, error)
	namespace    string
}

type subCatViewerOption func(*subCatViewer)

func withMetaViewerOptions(options ...metaViewerOption) subCatViewerOption {
	return func(viewer *subCatViewer) {
		for _, option := range options {
			option(viewer.metaViewer)
		}
	}
}

// newSubCatViewer returns a Viewer that produces SubscriptionCatalogStatues from CatalogSources.
func newSubCatViewer(mv *metaViewer, options ...subCatViewerOption) *subCatViewer {
	// Set defaults
	viewer := &subCatViewer{
		metaViewer:   mv,
		getReference: operators.GetReference,
	}

	for _, option := range options {
		option(viewer)
	}

	return viewer
}

const (
	subCatPrefix      string = "subcatalogstatus"
	subCatKeyTemplate string = subCatPrefix + "/%s/%s"
)

func (viewer *subCatViewer) Key(obj interface{}) (key string, err error) {
	catalog, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		err = fmt.Errorf("unexpected object value type %T, expected %T", obj, new(v1alpha1.CatalogSource))
		return
	}

	key = fmt.Sprintf(subCatKeyTemplate, catalog.GetNamespace(), catalog.GetName())
	return
}

func (viewer *subCatViewer) KeyByView(view interface{}) (key string, err error) {
	scs, ok := view.(*v1alpha1.SubscriptionCatalogStatus)
	if !ok {
		err = fmt.Errorf("unexpected view value type %T, expected %T", view, new(v1alpha1.SubscriptionCatalogStatus))
		return
	}

	key = fmt.Sprintf(subCatKeyTemplate, scs.CatalogSourceRef.Namespace, scs.CatalogSourceRef.Name)
	return
}

func (viewer *subCatViewer) View(obj interface{}) (view interface{}, err error) {
	catalog, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		return nil, fmt.Errorf("unexpected object type, expected %T and got %T", new(v1alpha1.CatalogSource), obj)
	}

	// Check the registry server health
	healthy, err := viewer.reconciler.ReconcilerForSource(catalog).CheckRegistryServer(catalog)
	if err != nil {
		return
	}

	// Create the view
	ref, err := viewer.getReference(catalog)
	if err != nil {
		return
	}

	view = &v1alpha1.SubscriptionCatalogStatus{
		CatalogSourceRef: ref,
		LastUpdated:      viewer.now(),
		Healthy:          healthy,
	}

	return
}

const (
	subCatIndexKey string = "subcatalog"
)

// subCatViewIndex returns a set of indices for a given SubscriptionCatalogStatus.
func (viewer *subCatViewer) subCatViewIndex(view interface{}) ([]string, error) {
	scs, ok := view.(*v1alpha1.SubscriptionCatalogStatus)
	if !ok {
		// Can't build indices for this type. Fail silently.
		return []string{}, nil
	}

	if scs.CatalogSourceRef.Namespace == viewer.Operator.namespace {
		// The CatalogSource is global, get keys for subscriptions in all watched namespaces
		namespaces := viewer.watchedNamespaces
		if len(namespaces) == 1 && namespaces[0] == metav1.NamespaceAll {
			// Need to get all namespace names
			nsList, err := viewer.lister.CoreV1().NamespaceLister().List(labels.Everything())
			if err != nil {
				return nil, err
			}
			namespaces = make([]string, len(nsList))
			for i, ns := range nsList {
				namespaces[i] = ns.GetName()
			}
		}

		keySet := sets.String{}
		for _, namespace := range namespaces {
			// TODO: namespace is probably metav1.NamespaceAll, in which case I don't think it will be indexed propery in other indexers.
			indexer := viewer.subIndexerSet.Get(namespace)
			if indexer == nil {
				return nil, fmt.Errorf("no subscription indexer found for namespace %s", scs.CatalogSourceRef.Namespace)
			}

			keys, err := indexer.IndexKeys(cache.NamespaceIndex, namespace)
			if err != nil {
				return nil, err
			}
			viewer.Log.WithField("subscription-namespace", namespace).Debugf("keys: %v", keys)

			keySet.Insert(keys...)
		}
		// panic("YARP")

		return keySet.List(), nil
	}

	indexer := viewer.subIndexerSet.Get(scs.CatalogSourceRef.Namespace)
	if indexer == nil {
		return nil, fmt.Errorf("no subscription indexer found for namespace %s", scs.CatalogSourceRef.Namespace)
	}

	return indexer.IndexKeys(cache.NamespaceIndex, scs.CatalogSourceRef.Namespace)
}

// setSubCatStatus sets the SubscriptionCatalogStatus field on the given Subscription using the SubscriptionCatalogView.
func (viewer *subCatViewer) setSubCatStatus(obj interface{}) error {
	sub, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		return fmt.Errorf("casting Subscription failed")
	}

	// Get views from catalogs in the global namespace
	viewIndexer := viewer.viewIndexerSet.Get(viewer.Operator.namespace)
	if viewIndexer == nil {
		// TODO: panic here?
		return fmt.Errorf("global namespace indexer nil")
	}

	// Generate the Subscription key
	indexKey := fmt.Sprintf("%s/%s", sub.GetNamespace(), sub.GetName())

	var subCatStatus []v1alpha1.SubscriptionCatalogStatus
	objs, err := viewIndexer.ByIndex(subCatIndexKey, indexKey)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if scs, ok := o.(*v1alpha1.SubscriptionCatalogStatus); ok {
			subCatStatus = append(subCatStatus, *scs)
		} else {
			panic(fmt.Sprintf("obj %v not of type %T", o, new(v1alpha1.SubscriptionCatalogStatus)))
		}
	}

	if sub.GetNamespace() != viewer.Operator.namespace || viewer.watchedNamespaces[0] != metav1.NamespaceAll {
		// Get views from the Subscription namespace
		viewIndexer = viewer.viewIndexerSet.Get(sub.GetNamespace())
		if viewIndexer == nil {
			// TODO: panic here?
			return fmt.Errorf("%s indexer nil", sub.GetNamespace())
		}

		objs, err = viewIndexer.ByIndex(subCatIndexKey, indexKey)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if scs, ok := o.(*v1alpha1.SubscriptionCatalogStatus); ok {
				subCatStatus = append(subCatStatus, *scs)
			} else {
				panic(fmt.Sprintf("obj %v not of type %T", o, new(v1alpha1.SubscriptionCatalogStatus)))
			}
		}
	}

	// Update the catalog status if a change has been made
	if sub.Status.SetSubscriptionCatalogStatus(subCatStatus) {
		sub.Status.LastUpdated = viewer.now()
		_, err := viewer.client.OperatorsV1alpha1().Subscriptions(sub.GetNamespace()).UpdateStatus(sub)
		if err != nil {
			return err
		}
	}

	return nil
}
