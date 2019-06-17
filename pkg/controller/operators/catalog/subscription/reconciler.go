package subscription

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/reference"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

// catalogHealthReconciler reconciles catalog health status for subscriptions.
type catalogHealthReconciler struct {
	now                       func() *metav1.Time
	client                    versioned.Interface
	catalogLister             listers.CatalogSourceLister
	registryReconcilerFactory reconciler.RegistryReconcilerFactory
	globalCatalogNamespace    string
}

// Reconcile reconciles subscription catalog health conditions.
func (c *catalogHealthReconciler) Reconcile(ctx context.Context, in kubestate.State) (out kubestate.State, err error) {
	next := in
	var prev kubestate.State

	// loop until this state can no longer transition
	for err == nil && out == nil && next != nil && !next.Terminal() && prev != next {
		select {
		case <-ctx.Done():
			err = errors.New("subscription catalog health reconciliation timed out")
		default:
			switch s := next.(type) {
			case CatalogHealthKnownState:
				// Target state already known, no work to do
				out = s
			case CatalogHealthState:
				// Gather catalog health and transition state
				ns := s.Subscription().GetNamespace()
				var catalogHealth []v1alpha1.SubscriptionCatalogHealth
				catalogHealth, err = c.catalogHealth(ns)
				if err != nil {
					break
				}

				prev = s
				next, err = s.UpdateHealth(c.now(), c.client.OperatorsV1alpha1().Subscriptions(ns), catalogHealth...)
			case SubscriptionExistsState:
				if s == nil {
					break
				}
				if s.Subscription() == nil {
					break
				}

				// Set up fresh state
				next = NewCatalogHealthState(s)
			default:
				// Ignore all other typestates
				utilruntime.HandleError(fmt.Errorf("unexpected subscription state in catalog health reconciler %T", next))
				out = s
			}
		}
	}

	if prev == next {
		out = prev
	}

	return
}

// catalogHealth gets the health of catalogs that can affect Susbcriptions in the given namespace.
// This means all catalogs in the given namespace, as well as any catalogs in the operator's global catalog namespace.
func (c *catalogHealthReconciler) catalogHealth(namespace string) ([]v1alpha1.SubscriptionCatalogHealth, error) {
	catalogs, err := c.catalogLister.CatalogSources(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	if namespace != c.globalCatalogNamespace {
		globals, err := c.catalogLister.CatalogSources(c.globalCatalogNamespace).List(labels.Everything())
		if err != nil {
			return nil, err
		}

		catalogs = append(catalogs, globals...)
	}

	catalogHealth := make([]v1alpha1.SubscriptionCatalogHealth, len(catalogs))
	now := c.now()
	var errs []error
	for i, catalog := range catalogs {
		h, err := c.health(now, catalog)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		// Prevent assignment when any error has been encountered since the results will be discarded
		if errs == nil {
			catalogHealth[i] = *h
		}
	}

	if errs != nil || len(catalogHealth) == 0 {
		// Assign meaningful zero value
		catalogHealth = nil
	}

	return catalogHealth, utilerrors.NewAggregate(errs)
}

// health returns a SusbcriptionCatalogHealth for the given catalog with the given now.
func (c *catalogHealthReconciler) health(now *metav1.Time, catalog *v1alpha1.CatalogSource) (*v1alpha1.SubscriptionCatalogHealth, error) {
	healthy, err := c.healthy(catalog)
	if err != nil {
		return nil, err
	}

	ref, err := reference.GetReference(catalog)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, errors.New("nil reference")
	}

	h := &v1alpha1.SubscriptionCatalogHealth{
		CatalogSourceRef: ref,
		// TODO: Should LastUpdated be set here, or at time of subscription update?
		LastUpdated: now,
		Healthy:     healthy,
	}

	return h, nil
}

// healthy returns true if the given catalog is healthy, false otherwise, and any error encountered
// while checking the catalog's registry server.
func (c *catalogHealthReconciler) healthy(catalog *v1alpha1.CatalogSource) (bool, error) {
	return c.registryReconcilerFactory.ReconcilerForSource(catalog).CheckRegistryServer(catalog)
}

// ReconcilerFromLegacySyncHandler returns a reconciler that invokes the given legacy sync handler and on delete funcs.
// Since the reconciler does not return an updated kubestate, it MUST be the last reconciler in a given chain.
func ReconcilerFromLegacySyncHandler(sync queueinformer.LegacySyncHandler, onDelete func(obj interface{})) kubestate.Reconciler {
	var rec kubestate.ReconcilerFunc = func(ctx context.Context, in kubestate.State) (out kubestate.State, err error) {
		out = in
		switch s := in.(type) {
		case SubscriptionExistsState:
			if sync != nil {
				err = sync(s.Subscription())
			}
		case SubscriptionDeletedState:
			if onDelete != nil {
				onDelete(s.Subscription())
			}
		case SubscriptionState:
			if sync != nil {
				err = sync(s.Subscription())
			}
		default:
			utilruntime.HandleError(fmt.Errorf("unexpected subscription state in legacy reconciler: %T", s))
		}

		return
	}

	return rec
}
