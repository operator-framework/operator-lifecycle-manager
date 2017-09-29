package subscriptions

import (
	"errors"

	"github.com/coreos-inc/operator-client/pkg/client"

	subscriptionv1alpha1 "github.com/coreos-inc/alm/apis/subscription/v1alpha1"
	"github.com/coreos-inc/alm/appcache"
)

// Subscription loop
//
//    A. Watches for Subscription objects
//         i. If no Installplan exists for the Subscription, creates it
//        ii. Checks CatalogSource for updates
//       iii. If newer version is available in the channel and is greater than current, creates an
//            Installplan for it.

// SubscriptionController to use for handling subscriptionv1alpha1.Subscription resource events
type SubscriptionController struct {
	catalog appcache.AppCache
	client  client.Interface
}

// HandleSubscription is the handler in the subscriptionv1alpha1.Subscription controller that checks if an
// Installplan object exists in the namespace for a given Subscription and creates one if not
func (ctl *SubscriptionController) HandleNewSubscription(sub subscriptionv1alpha1.Subscription) error {
	return errors.new("Method not implemented")
}

// CheckCatalogForUpdate polls catalog for most recent version of an app and creates a new
// Installplan is a more recent version exists
func (ctl *SubscriptionController) CheckCatalogForUpdate(sub subscriptionv1alpha1.Subscription) error {
	return errors.new("Method not implemented")
}
