package catalog

import (
	"errors"
	"fmt"

	ipv1alpha1 "github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/subscription/v1alpha1"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	ErrNilSubscription = errors.New("invalid Subscription object: <nil>")
)

func (o *Operator) syncSubscription(sub *v1alpha1.Subscription) error {
	if sub == nil || sub.Spec == nil {
		return ErrNilSubscription
	}
	catalog, ok := o.sources[sub.Spec.CatalogSource]
	if !ok {
		return fmt.Errorf("unknown catalog source %s", sub.Spec.CatalogSource)
	}
	// find latest CSV if no CSVs are installed already
	if sub.Spec.AtCSV == "" {
		csv, err := catalog.FindCSVForPackageNameUnderChannel(sub.Spec.Package, sub.Spec.Channel)
		if err != nil {
			return fmt.Errorf("failed to find CSV for package %s in channel %s: %v",
				sub.Spec.Package, sub.Spec.Channel, err)
		}
		if csv == nil {
			return fmt.Errorf("failed to find CSV for package %s in channel %s: nil CSV",
				sub.Spec.Package, sub.Spec.Channel)
		}
		sub.Spec.AtCSV = csv.GetName()
		_, err = o.subscriptionClient.UpdateSubscription(sub)
		return err
	}
	// check that desired CSV has been installed
	csv, err := o.csvClient.GetCSVByName(sub.GetNamespace(), sub.Spec.AtCSV)
	if err != nil || csv == nil {
		log.Infof("error fetching CSV %s via k8s api: %v", sub.Spec.AtCSV, err)
		if sub.Status.Install != nil {
			log.Infof("installplan for %s already exists", sub.Spec.AtCSV)
			return nil
		}
		// install CSV if doesn't exist
		ip := &ipv1alpha1.InstallPlan{}
		owner := []metav1.OwnerReference{
			{
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
				Kind:       v1alpha1.SubscriptionKind,
				Name:       sub.GetName(),
				UID:        sub.GetUID(),
			},
		}
		ip.SetOwnerReferences(owner)
		ip.SetGenerateName(fmt.Sprintf("install-%s", sub.Spec.AtCSV))
		if _, err := o.ipClient.CreateInstallPlan(ip); err != nil {
			return fmt.Errorf("failed to ensure current CSV %s installed: %v", sub.Spec.AtCSV, err)
		}
		sub.Status.Install = &v1alpha1.InstallPlanReference{
			UID:  ip.GetUID(),
			Name: ip.GetName(),
		}
		_, err = o.subscriptionClient.UpdateSubscription(sub)
		return err
	}
	// poll catalog for an update
	repl, err := catalog.FindReplacementCSVForPackageNameUnderChannel(
		sub.Spec.Package, sub.Spec.Channel, sub.Spec.AtCSV)
	if err != nil {
		return fmt.Errorf("failed to lookup replacement CSV for %s: %v", sub.Spec.AtCSV, err)
	}
	if repl == nil {
		return fmt.Errorf("nil replacement CSV for %s returned from catalog", sub.Spec.AtCSV)
	}
	// update subscription with new latest
	sub.Spec.AtCSV = repl.GetName()
	sub.Status.Install = nil
	_, err = o.subscriptionClient.UpdateSubscription(sub)
	return err
}
