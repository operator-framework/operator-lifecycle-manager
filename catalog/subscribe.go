package catalog

import (
	"time"

	"github.com/coreos/go-semver/semver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	installdeclarationv1alpha1 "github.com/coreos-inc/alm/apis/installdeclaration/v1alpha1"
	subscriptionv1alpha1 "github.com/coreos-inc/alm/apis/subscription/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/queueinformer"
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
	FetchInstallDeclarationForAppVersion(apptype, version string) (*installdeclarationv1alpha1.InstallDeclaration, error)
	ResolveDependencies(decl *installdeclarationv1alpha1.InstallDeclaration) error
}

// TEMP - to be filled in with k8s and logic
type Installer interface {
	CheckForInstallDeclaration(namespace string, sub subscriptionv1alpha1.Subscription) (bool, error)
	CreateInstallDeclaration(namespace string, delc *installdeclarationv1alpha1.InstallDeclaration) error
}

// SubscriptionOperator to use for handling subscriptionv1alpha1.Subscription resource events
type SubscriptionOperator struct {
	catalog Catalog
	*queueinformer.Operator
	restClient *rest.RESTClient
	installer  Installer
}

func NewSubscriptionOperator(kubeconfig string) (*SubscriptionOperator, error) {
	subscriptionClient, err := client.NewSubscriptionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	subscriptionWatcher := cache.NewListWatchFromClient(
		subscriptionClient,
		"subscription-v1s",
		metav1.NamespaceAll,
		fields.Everything(),
	)
	subscriptionInformer := cache.NewSharedIndexInformer(
		subscriptionWatcher,
		&subscriptionv1alpha1.Subscription{},
		15*time.Minute,
		cache.Indexers{},
	)

	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}
	catalog := &memCatalog{}
	op := &SubscriptionOperator{
		catalog,
		queueOperator,
		subscriptionClient,
	}

	subscriptionQueueInformer := queueinformer.New("subscriptions", subscriptionInformer, op.syncSubscription, nil)
	op.RegisterQueueInformer(subscriptionQueueInformer)

	return op, nil
}

func (ctl *SubscriptionOperator) syncSubscription(obj interface{}) error {
	subscription, ok := obj.(*v1alpha1.Subscription)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Subscription failed")
	}
	return ctl.HandleNewSubscription(subscription)
}

// installDeclarationForSubscription is a helper method that fetches the install declaration for a
// given Subscription from the catalog and installs it into the proper namespace
func (ctl *SubscriptionOperator) installDeclarationForSubscription(sub subscriptionv1alpha1.Subscription) (*installdeclarationv1alpha1.InstallDeclaration, error) {
	decl, err := ctl.catalog.FetchInstallDeclarationForAppVersion(sub.Spec.AppType, sub.Status.CurrentVersion)
	if err != nil {
		return nil, err
	}
	return &decl, ctl.installer.CreateInstallDeclaration(sub.GetNamespace(), &decl)
}

// HandleSubscription is the handler in the subscriptionv1alpha1.Subscription controller that checks if an
// InstallDeclaration object exists in the namespace for a given Subscription and creates one if not
func (ctl *SubscriptionOperator) HandleNewSubscription(sub *subscriptionv1alpha1.Subscription) error {
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
func (ctl *SubscriptionOperator) CheckCatalogForUpdate(sub *ubscriptionv1alpha1.Subscription) error {
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
