package catalog

import (
	installdeclarationv1alpha1 "github.com/coreos-inc/alm/apis/installdeclaration/v1alpha1"
	subscriptionv1alpha1 "github.com/coreos-inc/alm/apis/subscription/v1alpha1"
	"github.com/coreos-inc/operator-client/pkg/client"
	"github.com/coreos/go-semver/semver"
)

// Service Catalog

// 1. InstallDeclaration loop
//
//    States: Unresolved, Resolved, Approved, Complete
//
//    A. Watches for new InstallDeclarations in a namespace
//        i. If Unresolved, attempt to resolve those resources and write them back to the
//           InstallDeclaration
//       ii. If Resolved, wait for state to be Approved
//      iii. If approval is set to automatic, state is transitioned to Approved
//       iv. If Approved, creates all resolved resources, reports back status
//        v. If Complete, nothing

// 2. Subscription loop
//
//    A. Watches for Subscription objects
//         i. If no InstallDeclaration exists for the Subscription, creates it
//        ii. Checks CatalogSource for updates
//       iii. If newer version is available in the channel and is greater than current, creates an
//            InstallDeclaration for it.

// Catalog represents a remote, or local, listing available versions of applications as well as
// methods to fetch and resolve an application version manifest into an InstallDeclaration and into
// a list of its dependencies
type Catalog interface {
	FetchLatestVersion(apptype, channel string) (string, error) // TODO location? appid?
	FetchInstallDeclarationForAppVersion(apptype, version string) (installdeclarationv1alpha1.InstallDeclaration, error)
	ResolveDependencies(decl *installdeclarationv1alpha1.InstallDeclaration) error
}

// TEMP - to be filled in with k8s and logic
type Installer interface {
	CheckForInstallDeclaration(namespace string, sub subscriptionv1alpha1.Subscription) (bool, error)
	CreateInstallDeclaration(namespace string, delc *installdeclarationv1alpha1.InstallDeclaration) error
}

// SubscriptionController to use for handling subscriptionv1alpha1.Subscription resource events
type SubscriptionController struct {
	catalog   Catalog
	client    client.Interface
	installer Installer
}

// installDeclarationForSubscription is a helper method that fetches the install declaration for a
// given Subscription from the catalog and installs it into the proper namespace
func (ctl *SubscriptionController) installDeclarationForSubscription(sub subscriptionv1alpha1.Subscription) (*installdeclarationv1alpha1.InstallDeclaration, error) {
	decl, err := ctl.catalog.FetchInstallDeclarationForAppVersion(sub.Spec.AppType, sub.Status.CurrentVersion)
	if err != nil {
		return nil, err
	}
	return &decl, ctl.installer.CreateInstallDeclaration(sub.GetNamespace(), &decl)
}

// HandleSubscription is the handler in the subscriptionv1alpha1.Subscription controller that checks if an
// InstallDeclaration object exists in the namespace for a given Subscription and creates one if not
func (ctl *SubscriptionController) HandleNewSubscription(sub subscriptionv1alpha1.Subscription) error {
	ok, err := ctl.installer.CheckForInstallDeclaration(sub.GetNamespace(), sub)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = ctl.installDeclarationForSubscription(sub)
	return err
}

// CheckCatalogForUpdate polls catalog for most recent version of an app and creates a new
// InstallDeclaration is a more recent version exists
func (ctl *SubscriptionController) CheckCatalogForUpdate(sub subscriptionv1alpha1.Subscription) error {
	versionStr, err := ctl.catalog.FetchLatestVersion(sub.Spec.AppType, sub.Spec.Channel)
	if err != nil {
		return err
	}
	ver := semver.New(versionStr)
	currVer := semver.New(sub.Status.CurrentVersion)
	if !currVer.LessThan(*ver) {
		return nil
	}
	_, err = ctl.installDeclarationForSubscription(sub)
	return err
}
