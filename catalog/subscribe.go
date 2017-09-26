package catalog

import (
	"github.com/coreos-inc/operator-client/pkg/client"
	"github.com/coreos/go-semver/semver"
)

// Service Catalog

// 1. InstallDeclaration loop

// Strawpeople:
//
// ```yaml
// declare:
//  - Vault AppType 1.0.0 (ref by sha?)
// approval: manual/automatic
// status: Unresolved
//```
//
//```yaml
// declare:
//  - Vault AppType (ref by sha?)
// approval: manual/automatic
// resolved:
//   - Vault AppType 1.0.0
//   - Vault OpVer
//   - VaultService CRD
//   - Etcd AppType
//   - Etcd OpVer
//   - EtcdCluster CRD
// status: resolved
//```

// States: Unresolved, Resolved, Approved, Complete

// A. Watches for new InstallDeclarations in a namespace
//     i. If Unresolved, attempt to resolve those resources and write them back to the
//        InstallDeclaration
//    ii. If Resolved, wait for state to be Approved
//   iii. If approval is set to automatic, state is transitioned to Approved
//    iv. If Approved, creates all resolved resources, reports back status
//     v. If Complete, nothing

// 2. Subscription loop

//```yaml
// type: Subscription
// declare:
//  - Vault Apptype
// source: quay
// package: vault
// channel: stable
// approval: manual/automatic
// status:
//   current: v1.0.0
// ---
// type: CatalogSource
// url: quay.io/catalog
// name: quay
//```

// A. Watches for Subscription objects
//      i. If no InstallDeclaration exists for the Subscription, creates it
//     ii. Checks CatalogSource for updates
//    iii. If newer version is available in the channel and is greater than current, creates an
//         InstallDeclaration for it.

// TEMP - to be defined elsewhere
type Subscription interface {
	AppType() string
	CurrentVersion() *semver.Version
	Namespace() string
}

// TEMP - to be defined elsewhere
type InstallDeclaration struct{}

// Catalog represents a remote, or local, listing available versions of applications as well as
// methods to fetch and resolve an application version manifest into an InstallDeclaration and into
// a list of its dependencies
type Catalog interface {
	FetchLatestVersion(apptype string) (*semver.Version, error) // TODO location? appid?
	FetchInstallDeclarationForAppVersion(apptype string, ver *semver.Version) (*InstallDeclaration, error)
	ResolveDependencies(decl *InstallDeclaration) error
}

// TEMP - to be filled in with k8s and logic
type Installer interface {
	CheckForInstallDeclaration(namespace string, sub Subscription) (bool, error)
	CreateInstallDeclaration(namespace string, delc *InstallDeclaration) error
}

// SubscriptionController to use for handling Subscription resource events
type SubscriptionController struct {
	catalog   Catalog
	client    client.Interface
	installer Installer
}

// installDeclarationForSubscription is a helper method that fetches the install declaration for a
// given Subscription from the catalog and installs it into the proper namespace
func (ctl *SubscriptionController) installDeclarationForSubscription(sub Subscription) (*InstallDeclaration, error) {
	decl, err := ctl.catalog.FetchInstallDeclarationForAppVersion(sub.AppType(), sub.CurrentVersion())
	if err != nil {
		return nil, err
	}
	return decl, ctl.installer.CreateInstallDeclaration(sub.Namespace(), decl)
}

// HandleNewSubscription is the handler in the Subscription controller that checks if an
// InstallDeclaration object exists in the namespace for a given Subscription and creates one if not
func (ctl *SubscriptionController) HandleNewSubscription(sub Subscription) error {
	ok, err := ctl.installer.CheckForInstallDeclaration(sub.Namespace(), sub)
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
func (ctl *SubscriptionController) CheckCatalogForUpdate(sub Subscription) error {
	ver, err := ctl.catalog.FetchLatestVersion(sub.AppType())
	if err != nil {
		return err
	}
	currVer := sub.CurrentVersion()
	if !currVer.LessThan(*ver) {
		return nil
	}
	_, err = ctl.installDeclarationForSubscription(sub)
	return err
}
