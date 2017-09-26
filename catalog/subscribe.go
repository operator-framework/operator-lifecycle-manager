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
	CurrentVersion() semver.Version
}

// TEMP - to be defined elsewhere
type InstallDeclaration struct{}

type Catalog interface {
	FetchLatestVersion(apptype string) (*semver.Version, error) // TODO location? appid?
	FetchInstallDeclarationForAppVersion(apptype string, ver *semver.Version) (InstallDeclaration, error)
	ResolveDependencies(decl InstallDeclaration) error
}

// TEMP - to be filled in with k8s and logic
type Installer interface {
	CheckForInstallDeclaration(sub Subscription) (bool, error)
	CreateInstallDeclaration(InstallDeclaration) error
}

type SubscriptionController struct {
	catalog   Catalog
	client    client.Interface
	installer Installer
}

func (ctl *SubscriptionController) installDeclarationForSubscription(sub Subscription) (*InstallDeclaration, error) {
	decl, err := ctl.catalog.FetchInstallDeclarationForAppVersion(sub.AppType(), sub.CurrentVersion())
	if err != nil {
		return nil, err
	}
	return &decl, ctl.installer.CreateInstallDeclaration(decl)
}

func (ctl *SubscriptionController) HandleNewSubscription(sub Subscription) error {
	ok, err := ctl.installer.CheckForInstallDeclaration(sub)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = ctl.installDeclarationForSubscription(sub)
	return err
}

func (ctl *SubscriptionController) CheckCatalogForUpdate(sub Subscription) error {
	ver, err := ctl.catalog.FetchLatestVersion(sub.AppType())
	if err != nil {
		return err
	}
	currVer := sub.CurrentVersion()
	if !currVer.LessThan(ver) {
		return nil
	}
	_, err = ctl.installDeclarationForSubscription(sub)
	return err
}
