package subscription

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/operator-framework/api/pkg/operators/reference"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/api"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

// catalogHealthReconciler reconciles catalog health status for subscriptions.
type catalogHealthReconciler struct {
	now                       func() *metav1.Time
	catalogLister             listers.CatalogSourceLister
	registryReconcilerFactory reconciler.RegistryReconcilerFactory
	globalCatalogNamespace    string
	sourceProvider            cache.SourceProvider
}

// Reconcile reconciles subscription catalog health conditions.
func (c *catalogHealthReconciler) Reconcile(ctx context.Context, sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	out := sub.DeepCopy()
	now := c.now()

	// Gather catalog health and transition state
	ns := sub.GetNamespace()
	var catalogHealth []v1alpha1.SubscriptionCatalogHealth
	catalogHealth, err := c.catalogHealth(ns)
	if err != nil {
		return nil, err
	}

	out, err = c.updateHealth(out, now, catalogHealth...)
	if err != nil {
		return nil, err
	}

	return c.updateDeprecatedStatus(ctx, out, now)
}

// updateDeprecatedStatus adds deprecation status conditions to the subscription when present in the cache entry then
// returns a bool value of true if any changes to the existing subscription have occurred.
func (c *catalogHealthReconciler) updateDeprecatedStatus(ctx context.Context, sub *v1alpha1.Subscription, now *metav1.Time) (*v1alpha1.Subscription, error) {
	if c.sourceProvider == nil {
		return sub, nil
	}

	source, ok := c.sourceProvider.Sources(sub.Spec.CatalogSourceNamespace)[cache.SourceKey{
		Name:      sub.Spec.CatalogSource,
		Namespace: sub.Spec.CatalogSourceNamespace,
	}]

	if !ok {
		return sub, nil
	}
	snapshot, err := source.Snapshot(ctx)
	if err != nil {
		return sub, nil
	}
	if len(snapshot.Entries) == 0 {
		return sub, nil
	}

	var rollupMessages []string
	var deprecations *cache.Deprecations

	found := false
	for _, entry := range snapshot.Entries {
		// Find the cache entry that matches this subscription
		if entry.SourceInfo == nil || entry.Package() != sub.Spec.Package {
			continue
		}
		if sub.Spec.Channel != "" && entry.Channel() != sub.Spec.Channel {
			continue
		}
		if sub.Status.InstalledCSV != entry.Name {
			continue
		}
		deprecations = entry.SourceInfo.Deprecations
		found = true
		break
	}
	if !found {
		// No matching entry found
		return sub, nil
	}
	out := sub.DeepCopy()
	conditionTypes := []v1alpha1.SubscriptionConditionType{
		v1alpha1.SubscriptionPackageDeprecated,
		v1alpha1.SubscriptionChannelDeprecated,
		v1alpha1.SubscriptionBundleDeprecated,
	}
	for _, conditionType := range conditionTypes {
		oldCondition := sub.Status.GetCondition(conditionType)
		var deprecation *api.Deprecation
		if deprecations != nil {
			switch conditionType {
			case v1alpha1.SubscriptionPackageDeprecated:
				deprecation = deprecations.Package
			case v1alpha1.SubscriptionChannelDeprecated:
				deprecation = deprecations.Channel
			case v1alpha1.SubscriptionBundleDeprecated:
				deprecation = deprecations.Bundle
			}
		}
		if deprecation != nil {
			if conditionType == v1alpha1.SubscriptionChannelDeprecated && sub.Spec.Channel == "" {
				// Special case: If optional field sub.Spec.Channel is unset do not apply a channel
				// deprecation message and remove them if any exist.
				out.Status.RemoveConditions(conditionType)
				continue
			}
			newCondition := v1alpha1.SubscriptionCondition{
				Type:               conditionType,
				Message:            deprecation.Message,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: c.now(),
			}
			rollupMessages = append(rollupMessages, deprecation.Message)
			if oldCondition.Message != newCondition.Message {
				// oldCondition's message was empty or has changed; add or update the condition
				out.Status.SetCondition(newCondition)
			}
		} else if oldCondition.Status == corev1.ConditionTrue {
			// No longer deprecated at this level; remove the condition
			out.Status.RemoveConditions(conditionType)
		}
	}

	if len(rollupMessages) > 0 {
		rollupCondition := v1alpha1.SubscriptionCondition{
			Type:               v1alpha1.SubscriptionDeprecated,
			Message:            strings.Join(rollupMessages, "; "),
			Status:             corev1.ConditionTrue,
			LastTransitionTime: now,
		}
		out.Status.SetCondition(rollupCondition)
	} else {
		// No rollup message means no deprecation conditions were set; remove the rollup if it exists
		out.Status.RemoveConditions(v1alpha1.SubscriptionDeprecated)
	}

	return out, nil
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

	// Sort to ensure ordering
	sort.Slice(catalogs, func(i, j int) bool {
		return catalogs[i].GetNamespace()+catalogs[i].GetName() < catalogs[j].GetNamespace()+catalogs[j].GetName()
	})

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
	if catalog.Status.Reason == v1alpha1.CatalogSourceSpecInvalidError {
		// The catalog's spec is bad, mark unhealthy
		return false, nil
	}

	// Check connection health
	rec := c.registryReconcilerFactory.ReconcilerForSource(catalog)
	if rec == nil {
		return false, fmt.Errorf("could not get reconciler for catalog: %#v", catalog)
	}

	return rec.CheckRegistryServer(logrus.NewEntry(logrus.New()), catalog)
}

func (c *catalogHealthReconciler) updateHealth(sub *v1alpha1.Subscription, now *metav1.Time, catalogHealth ...v1alpha1.SubscriptionCatalogHealth) (*v1alpha1.Subscription, error) {
	out := sub.DeepCopy()

	healthSet := make(map[types.UID]v1alpha1.SubscriptionCatalogHealth, len(catalogHealth))
	healthy := true
	missingTargeted := true

	cond := sub.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
	for _, h := range catalogHealth {
		ref := h.CatalogSourceRef
		healthSet[ref.UID] = h
		healthy = healthy && h.Healthy

		if ref.Namespace == sub.Spec.CatalogSourceNamespace && ref.Name == sub.Spec.CatalogSource {
			missingTargeted = false
			if !h.Healthy {
				cond.Message = fmt.Sprintf("targeted catalogsource %s/%s unhealthy", ref.Namespace, ref.Name)
			}
		}
	}

	switch {
	case missingTargeted:
		cond.Message = fmt.Sprintf("targeted catalogsource %s/%s missing", sub.Spec.CatalogSourceNamespace, sub.Spec.CatalogSource)
		fallthrough
	case !healthy:
		cond.Status = corev1.ConditionTrue
		cond.Reason = v1alpha1.UnhealthyCatalogSourceFound
	default:
		cond.Status = corev1.ConditionFalse
		cond.Reason = v1alpha1.AllCatalogSourcesHealthy
		cond.Message = "all available catalogsources are healthy"
	}

	// Check for changes in CatalogHealth
	updated := false
	switch numNew, numOld := len(healthSet), len(sub.Status.CatalogHealth); {
	case numNew > numOld:
		cond.Reason = v1alpha1.CatalogSourcesAdded
	case numNew < numOld:
		cond.Reason = v1alpha1.CatalogSourcesDeleted
	case numNew == 0 && numNew == numOld:
		cond.Reason = v1alpha1.NoCatalogSourcesFound
		cond.Message = "dependency resolution requires at least one catalogsource"
	case numNew == numOld:
		// Check against existing subscription
		for _, oldHealth := range sub.Status.CatalogHealth {
			uid := oldHealth.CatalogSourceRef.UID
			if newHealth, ok := healthSet[uid]; !ok || !newHealth.Equals(oldHealth) {
				cond.Reason = v1alpha1.CatalogSourcesUpdated
				updated = true
				break
			}
		}
	}

	if !updated && cond.Equals(sub.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)) {
		return out, nil
	}

	cond.LastTransitionTime = now
	out.Status.SetCondition(cond)
	out.Status.LastUpdated = *now
	out.Status.CatalogHealth = catalogHealth

	return out, nil
}
