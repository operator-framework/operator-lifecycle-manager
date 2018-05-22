package catalog

import (
	"errors"
	"fmt"

	ipv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/subscription/v1alpha1"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	ErrNilSubscription = errors.New("invalid Subscription object: <nil>")
)

const (
	PackageLabel = "alm-package"
	CatalogLabel = "alm-catalog"
	ChannelLabel = "alm-channel"
)

// FIXME(alecmerdler): Rewrite this whole block to be more clear
func (o *Operator) syncSubscription(sub *v1alpha1.Subscription) (*v1alpha1.Subscription, error) {
	if sub == nil || sub.Spec == nil {
		return nil, ErrNilSubscription
	}

	sub = ensureLabels(sub)

	// Only sync if catalog has been updated since last sync time
	if o.sourcesLastUpdate.Before(&sub.Status.LastUpdated) && sub.Status.State == v1alpha1.SubscriptionStateAtLatest {
		log.Infof("skipping sync: no new updates to catalog since last sync at %s",
			sub.Status.LastUpdated.String())
		return nil, nil
	}

	o.sourcesLock.Lock()
	defer o.sourcesLock.Unlock()

	catalog, ok := o.sources[sub.Spec.CatalogSource]
	if !ok {
		return sub, fmt.Errorf("unknown catalog source %s", sub.Spec.CatalogSource)
	}

	// Find latest CSV if no CSVs are installed already
	if sub.Status.CurrentCSV == "" {
		if sub.Spec.StartingCSV != "" {
			sub.Status.CurrentCSV = sub.Spec.StartingCSV
		} else {
			csv, err := catalog.FindCSVForPackageNameUnderChannel(sub.Spec.Package, sub.Spec.Channel)
			if err != nil {
				return sub, fmt.Errorf("failed to find CSV for package %s in channel %s: %v", sub.Spec.Package, sub.Spec.Channel, err)
			}
			if csv == nil {
				return sub, fmt.Errorf("failed to find CSV for package %s in channel %s: nil CSV", sub.Spec.Package, sub.Spec.Channel)
			}
			sub.Status.CurrentCSV = csv.GetName()
		}
		sub.Status.State = v1alpha1.SubscriptionStateUpgradeAvailable
		return sub, nil
	}

	// Check that desired CSV has been installed
	csv, err := o.client.ClusterserviceversionV1alpha1().ClusterServiceVersions(sub.GetNamespace()).Get(sub.Status.CurrentCSV, metav1.GetOptions{})
	if err != nil || csv == nil {
		log.Infof("error fetching CSV %s via k8s api: %v", sub.Status.CurrentCSV, err)
		if sub.Status.Install != nil && sub.Status.Install.Name != "" {
			ip, err := o.client.InstallplanV1alpha1().InstallPlans(sub.GetNamespace()).Get(sub.Status.Install.Name, metav1.GetOptions{})
			if err != nil {
				log.Errorf("get installplan %s error: %v", sub.Status.Install.Name, err)
			}
			if err == nil && ip != nil {
				log.Infof("installplan for %s already exists", sub.Status.CurrentCSV)
				return sub, nil
			}
			log.Infof("installplan %s not found: creating new plan", sub.Status.Install.Name)
			sub.Status.Install = nil
		}
		// Install CSV if doesn't exist
		sub.Status.State = v1alpha1.SubscriptionStateUpgradePending
		ip := &ipv1alpha1.InstallPlan{
			ObjectMeta: metav1.ObjectMeta{},
			Spec: ipv1alpha1.InstallPlanSpec{
				ClusterServiceVersionNames: []string{sub.Status.CurrentCSV},
				Approval:                   sub.GetInstallPlanApproval(),
			},
		}
		owner := []metav1.OwnerReference{
			{
				APIVersion: v1alpha1.SchemeGroupVersion.String(),
				Kind:       v1alpha1.SubscriptionKind,
				Name:       sub.GetName(),
				UID:        sub.GetUID(),
			},
		}
		ip.SetOwnerReferences(owner)
		ip.SetGenerateName(fmt.Sprintf("install-%s-", sub.Status.CurrentCSV))
		ip.SetNamespace(sub.GetNamespace())
		res, err := o.client.InstallplanV1alpha1().InstallPlans(sub.GetNamespace()).Create(ip)
		if err != nil {
			return sub, fmt.Errorf("failed to ensure current CSV %s installed: %v", sub.Status.CurrentCSV, err)
		}
		if res == nil {
			return sub, errors.New("unexpected installplan returned by k8s api on create: <nil>")
		}
		sub.Status.Install = &v1alpha1.InstallPlanReference{
			UID:        res.GetUID(),
			Name:       res.GetName(),
			APIVersion: res.APIVersion,
			Kind:       res.Kind,
		}
		return sub, nil
	}

	// Poll catalog for an update
	repl, err := catalog.FindReplacementCSVForPackageNameUnderChannel(sub.Spec.Package, sub.Spec.Channel, sub.Status.CurrentCSV)
	if err != nil {
		sub.Status.State = v1alpha1.SubscriptionStateAtLatest
		return sub, fmt.Errorf("failed to lookup replacement CSV for %s: %v", sub.Status.CurrentCSV, err)
	}
	if repl == nil {
		sub.Status.State = v1alpha1.SubscriptionStateAtLatest
		return sub, fmt.Errorf("nil replacement CSV for %s returned from catalog", sub.Status.CurrentCSV)
	}
	// Update subscription with new latest
	sub.Status.CurrentCSV = repl.GetName()
	sub.Status.Install = nil
	sub.Status.State = v1alpha1.SubscriptionStateUpgradeAvailable
	return sub, nil
}

func ensureLabels(sub *v1alpha1.Subscription) *v1alpha1.Subscription {
	labels := sub.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[PackageLabel] = sub.Spec.Package
	labels[CatalogLabel] = sub.Spec.CatalogSource
	labels[ChannelLabel] = sub.Spec.Channel
	sub.SetLabels(labels)
	return sub
}
