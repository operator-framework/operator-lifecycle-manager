package catalog

import (
	"fmt"

	installplanv1alpha1 "github.com/coreos-inc/alm/apis/installplan/v1alpha1"
	subscriptionv1alpha1 "github.com/coreos-inc/alm/apis/subscription/v1alpha1"
	"github.com/coreos-inc/operator-client/pkg/client"
	//	"github.com/coreos/go-semver/semver"

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
	catalog   appcache.AppCache
	client    client.Interface
	installer Installer
}

// TEMP - to be filled in with k8s and logic
type Installer interface {
	CheckForInstallPlan(namespace string, sub subscriptionv1alpha1.Subscription) (bool, error)
	CreateInstallPlan(namespace string, plan *installplanv1alpha1.InstallPlan) error
}

// // installplanForSubscription is a helper method that fetches the install plan for a
// // given Subscription from the catalog and installs it into the proper namespace
// func (ctl *SubscriptionController) installplanForSubscription(sub subscriptionv1alpha1.Subscription) (*opverv1alpha1.OperatorVersion, error) {
// 	plan, err := ctl.catalog.FindCloudServiceVersionByName(fmt.Sprintf("%s:%s", sub.Spec.AppType, sub.Status.CurrentVersion))
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &plan, ctl.installer.CreateInstallplan(sub.GetNamespace(), plan)
// }

// // HandleSubscription is the handler in the subscriptionv1alpha1.Subscription controller that checks if an
// // Installplan object exists in the namespace for a given Subscription and creates one if not
// func (ctl *SubscriptionController) HandleNewSubscription(sub subscriptionv1alpha1.Subscription) error {
// 	ok, err := ctl.installer.CheckForInstallplan(sub.GetNamespace(), sub)
// 	if err != nil {
// 		return err
// 	}
// 	if ok {
// 		return nil
// 	}
// 	_, err = ctl.installplanForSubscription(sub)
// 	return err
// }

// // CheckCatalogForUpdate polls catalog for most recent version of an app and creates a new
// // Installplan is a more recent version exists
// func (ctl *SubscriptionController) CheckCatalogForUpdate(sub subscriptionv1alpha1.Subscription) error {
// 	versionStr, err := ctl.catalog.FindCloudServiceVersionByName(
// 		fmt.Sprintf("%s:%s", sub.Spec.AppType, sub.Status.Channel))
// 	if err != nil {
// 		return err
// 	}
// 	ver := semver.New(versionStr)
// 	currVer := semver.New(sub.Status.CurrentVersion)
// 	if !currVer.LessThan(*ver) {
// 		return nil
// 	}
// 	_, err = ctl.installplanForSubscription(sub)
// 	return err
// }
