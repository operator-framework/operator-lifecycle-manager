//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_resolver.go . StepResolver
package resolver

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	controllerbundle "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/projection"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

const (
	BundleLookupConditionPacked v1alpha1.BundleLookupConditionType = "BundleLookupNotPersisted"
)

var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

type StepResolver interface {
	ResolveSteps(namespace string) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error)
	Expire(key registry.CatalogKey)
}

type OperatorStepResolver struct {
	subLister              v1alpha1listers.SubscriptionLister
	csvLister              v1alpha1listers.ClusterServiceVersionLister
	ipLister               v1alpha1listers.InstallPlanLister
	client                 versioned.Interface
	kubeclient             kubernetes.Interface
	globalCatalogNamespace string
	satResolver            *SatResolver
	log                    logrus.FieldLogger
}

var _ StepResolver = &OperatorStepResolver{}

func NewOperatorStepResolver(lister operatorlister.OperatorLister, client versioned.Interface, kubeclient kubernetes.Interface,
	globalCatalogNamespace string, provider cache.RegistryClientProvider, log logrus.FieldLogger) *OperatorStepResolver {
	return &OperatorStepResolver{
		subLister:              lister.OperatorsV1alpha1().SubscriptionLister(),
		csvLister:              lister.OperatorsV1alpha1().ClusterServiceVersionLister(),
		ipLister:               lister.OperatorsV1alpha1().InstallPlanLister(),
		client:                 client,
		kubeclient:             kubeclient,
		globalCatalogNamespace: globalCatalogNamespace,
		satResolver:            NewDefaultSatResolver(cache.NewDefaultRegistryClientProvider(log, provider), lister.OperatorsV1alpha1().CatalogSourceLister(), log),
		log:                    log,
	}
}

func (r *OperatorStepResolver) Expire(key registry.CatalogKey) {
	r.satResolver.cache.Expire(key)
}

func (r *OperatorStepResolver) ResolveSteps(namespace string) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
	// create a generation - a representation of the current set of installed operators and their provided/required apis
	allCSVs, err := r.csvLister.ClusterServiceVersions(namespace).List(labels.Everything())
	if err != nil {
		return nil, nil, nil, err
	}

	// TODO: build this index ahead of time
	// omit copied csvs from generation - they indicate that apis are provided to the namespace, not by the namespace
	var csvs []*v1alpha1.ClusterServiceVersion
	for i := range allCSVs {
		if !allCSVs[i].IsCopied() {
			csvs = append(csvs, allCSVs[i])
		}
	}

	subs, err := r.listSubscriptions(namespace)
	if err != nil {
		return nil, nil, nil, err
	}

	var operators cache.OperatorSet
	namespaces := []string{namespace, r.globalCatalogNamespace}
	operators, err = r.satResolver.SolveOperators(namespaces, csvs, subs)
	if err != nil {
		return nil, nil, nil, err
	}

	// if there's no error, we were able to satisfy all constraints in the subscription set, so we calculate what
	// changes to persist to the cluster and write them out as `steps`
	steps := []*v1alpha1.Step{}
	updatedSubs := []*v1alpha1.Subscription{}
	bundleLookups := []v1alpha1.BundleLookup{}
	for name, op := range operators {
		// Find any existing subscriptions that resolve to this operator.
		existingSubscriptions := make(map[*v1alpha1.Subscription]bool)
		sourceInfo := *op.SourceInfo
		for _, sub := range subs {
			if sub.Spec.Package != sourceInfo.Package {
				continue
			}
			if sub.Spec.Channel != "" && sub.Spec.Channel != sourceInfo.Channel {
				continue
			}
			subCatalogKey := registry.CatalogKey{
				Name:      sub.Spec.CatalogSource,
				Namespace: sub.Spec.CatalogSourceNamespace,
			}
			if !subCatalogKey.Empty() && !subCatalogKey.Equal(sourceInfo.Catalog) {
				continue
			}
			alreadyExists, err := r.hasExistingCurrentCSV(sub)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("unable to determine whether subscription %s has a preexisting CSV", sub.GetName())
			}
			existingSubscriptions[sub] = alreadyExists
		}

		if len(existingSubscriptions) > 0 {
			upToDate := true
			for sub, exists := range existingSubscriptions {
				if !exists || sub.Status.CurrentCSV != op.Name {
					upToDate = false
				}
			}
			// all matching subscriptions are up to date
			if upToDate {
				continue
			}
		}

		// add steps for any new bundle
		if op.Bundle != nil {
			if op.Inline() {
				bundleSteps, err := NewStepsFromBundle(op.Bundle, namespace, op.Replaces, op.SourceInfo.Catalog.Name, op.SourceInfo.Catalog.Namespace)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("failed to turn bundle into steps: %s", err.Error())
				}
				steps = append(steps, bundleSteps...)
			} else {
				lookup := v1alpha1.BundleLookup{
					Path:       op.Bundle.GetBundlePath(),
					Identifier: op.Name,
					Replaces:   op.Replaces,
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: op.SourceInfo.Catalog.Namespace,
						Name:      op.SourceInfo.Catalog.Name,
					},
					Conditions: []v1alpha1.BundleLookupCondition{
						{
							Type:    BundleLookupConditionPacked,
							Status:  corev1.ConditionTrue,
							Reason:  controllerbundle.NotUnpackedReason,
							Message: controllerbundle.NotUnpackedMessage,
						},
						{
							Type:    v1alpha1.BundleLookupPending,
							Status:  corev1.ConditionTrue,
							Reason:  controllerbundle.JobNotStartedReason,
							Message: controllerbundle.JobNotStartedMessage,
						},
					},
				}
				if anno, err := projection.PropertiesAnnotationFromPropertyList(op.Properties); err != nil {
					return nil, nil, nil, fmt.Errorf("failed to serialize operator properties for %q: %w", op.Name, err)
				} else {
					lookup.Properties = anno
				}
				bundleLookups = append(bundleLookups, lookup)
			}

			if len(existingSubscriptions) == 0 {
				// explicitly track the resolved CSV as the starting CSV on the resolved subscriptions
				op.SourceInfo.StartingCSV = op.Name
				subStep, err := NewSubscriptionStepResource(namespace, *op.SourceInfo)
				if err != nil {
					return nil, nil, nil, err
				}
				steps = append(steps, &v1alpha1.Step{
					Resolving: name,
					Resource:  subStep,
					Status:    v1alpha1.StepStatusUnknown,
				})
			}
		}

		// add steps for subscriptions for bundles that were added through resolution
		for sub := range existingSubscriptions {
			if sub.Status.CurrentCSV == op.Name {
				continue
			}
			// update existing subscription status
			sub.Status.CurrentCSV = op.Name
			updatedSubs = append(updatedSubs, sub)
		}
	}

	// Order Steps
	steps = v1alpha1.OrderSteps(steps)
	return steps, bundleLookups, updatedSubs, nil
}

func (r *OperatorStepResolver) hasExistingCurrentCSV(sub *v1alpha1.Subscription) (bool, error) {
	if sub.Status.CurrentCSV == "" {
		return false, nil
	}
	_, err := r.csvLister.ClusterServiceVersions(sub.GetNamespace()).Get(sub.Status.CurrentCSV)
	if err == nil {
		return true, nil
	}
	if errors.IsNotFound(err) {
		return false, nil
	}
	return false, err // Can't answer this question right now.
}

func (r *OperatorStepResolver) listSubscriptions(namespace string) ([]*v1alpha1.Subscription, error) {
	list, err := r.client.OperatorsV1alpha1().Subscriptions(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var subs []*v1alpha1.Subscription
	for i := range list.Items {
		subs = append(subs, &list.Items[i])
	}

	return subs, nil
}
