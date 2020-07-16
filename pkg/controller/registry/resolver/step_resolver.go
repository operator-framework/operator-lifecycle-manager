//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o fakes/fake_registry_interface.go ../../../../vendor/github.com/operator-framework/operator-registry/pkg/client/client.go Interface
//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -o ../../../fakes/fake_resolver.go . StepResolver
package resolver

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	controllerbundle "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/bundle"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

const (
	BundleLookupConditionPacked v1alpha1.BundleLookupConditionType = "BundleLookupNotPersisted"
)

var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

type StepResolver interface {
	ResolveSteps(namespace string, sourceQuerier SourceQuerier) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error)
}


type OperatorStepResolver struct {
	subLister              v1alpha1listers.SubscriptionLister
	csvLister              v1alpha1listers.ClusterServiceVersionLister
	ipLister               v1alpha1listers.InstallPlanLister
	client                 versioned.Interface
	kubeclient             kubernetes.Interface
	globalCatalogNamespace string
	satResolver            *SatResolver
}

var _ StepResolver = &OperatorStepResolver{}

func NewOperatorStepResolver(lister operatorlister.OperatorLister, client versioned.Interface, kubeclient kubernetes.Interface, globalCatalogNamespace string, log logrus.FieldLogger) *OperatorStepResolver {
	return &OperatorStepResolver{
		subLister:              lister.OperatorsV1alpha1().SubscriptionLister(),
		csvLister:              lister.OperatorsV1alpha1().ClusterServiceVersionLister(),
		ipLister:               lister.OperatorsV1alpha1().InstallPlanLister(),
		client:                 client,
		kubeclient:             kubeclient,
		globalCatalogNamespace: globalCatalogNamespace,
		satResolver:            NewDefaultSatResolver(NewDefaultRegistryClientProvider(client), log),
	}
}

func (r *OperatorStepResolver) ResolveSteps(namespace string, _ SourceQuerier) ([]*v1alpha1.Step, []v1alpha1.BundleLookup, []*v1alpha1.Subscription, error) {
	// create a generation - a representation of the current set of installed operators and their provided/required apis
	allCSVs, err := r.csvLister.ClusterServiceVersions(namespace).List(labels.Everything())
	if err != nil {
		return nil, nil, nil, err
	}

	// TODO: build this index ahead of time
	// omit copied csvs from generation - they indicate that apis are provided to the namespace, not by the namespace
	var csvs []*v1alpha1.ClusterServiceVersion
	for _, c := range allCSVs {
		if !c.IsCopied() {
			csvs = append(csvs, c)
		}
	}

	subs, err := r.listSubscriptions(namespace)
	if err != nil {
		return nil, nil, nil, err
	}

	// create a map of operatorsourceinfo (subscription+catalogsource data) to the original subscriptions
	subMap := r.sourceInfoToSubscriptions(subs)
	// get a list of new operators to add to the generation
	add := r.sourceInfoForNewSubscriptions(namespace, subMap)

	var operators OperatorSet
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
		_, isAdded := add[*op.SourceInfo()]
		existingSubscription, subExists := subMap[*op.SourceInfo()]

		// subscription exists and is up to date
		if subExists && existingSubscription.Status.CurrentCSV == op.Identifier() && !isAdded {
			continue
		}

		// add steps for any new bundle
		if op.Bundle() != nil {
			if op.Inline() {
				bundleSteps, err := NewStepsFromBundle(op.Bundle(), namespace, op.Replaces(), op.SourceInfo().Catalog.Name, op.SourceInfo().Catalog.Namespace)
				if err != nil {
					return nil, nil, nil, fmt.Errorf("failed to turn bundle into steps: %s", err.Error())
				}
				steps = append(steps, bundleSteps...)
			} else {
				bundleLookups = append(bundleLookups, v1alpha1.BundleLookup{
					Path:       op.Bundle().GetBundlePath(),
					Identifier: op.Identifier(),
					Replaces:   op.Replaces(),
					CatalogSourceRef: &corev1.ObjectReference{
						Namespace: op.SourceInfo().Catalog.Namespace,
						Name:      op.SourceInfo().Catalog.Name,
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
				})
			}

			if !subExists {
				// explicitly track the resolved CSV as the starting CSV on the resolved subscriptions
				op.SourceInfo().StartingCSV = op.Identifier()
				subStep, err := NewSubscriptionStepResource(namespace, *op.SourceInfo())
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
		if subExists && existingSubscription.Status.CurrentCSV != op.Identifier() {
			// update existing subscription status
			existingSubscription.Status.CurrentCSV = op.Identifier()
			updatedSubs = append(updatedSubs, existingSubscription)
		}
	}

	// Order Steps
	steps = v1alpha1.OrderSteps(steps)
	return steps, bundleLookups, updatedSubs, nil
}

func (r *OperatorStepResolver) sourceInfoForNewSubscriptions(namespace string, subs map[OperatorSourceInfo]*v1alpha1.Subscription) (add map[OperatorSourceInfo]struct{}) {
	add = make(map[OperatorSourceInfo]struct{})
	for key, sub := range subs {
		if sub.Status.CurrentCSV == "" {
			add[key] = struct{}{}
			continue
		}
		csv, err := r.csvLister.ClusterServiceVersions(namespace).Get(sub.Status.CurrentCSV)
		if csv == nil || errors.IsNotFound(err) {
			add[key] = struct{}{}
		}
	}
	return
}

func (r *OperatorStepResolver) sourceInfoToSubscriptions(subs []*v1alpha1.Subscription) (add map[OperatorSourceInfo]*v1alpha1.Subscription) {
	add = make(map[OperatorSourceInfo]*v1alpha1.Subscription)
	var sourceNamespace string
	for _, s := range subs {
		startingCSV := s.Spec.StartingCSV
		if s.Status.CurrentCSV != "" {
			// If a csv has previously been resolved for the operator, don't enable
			// a starting csv search.
			startingCSV = ""
		}
		if s.Spec.CatalogSourceNamespace == "" {
			sourceNamespace = s.GetNamespace()
		} else {
			sourceNamespace = s.Spec.CatalogSourceNamespace
		}
		add[OperatorSourceInfo{
			Package:     s.Spec.Package,
			Channel:     s.Spec.Channel,
			StartingCSV: startingCSV,
			Catalog:     CatalogKey{Name: s.Spec.CatalogSource, Namespace: sourceNamespace},
		}] = s.DeepCopy()
	}
	return
}

func (r *OperatorStepResolver) listSubscriptions(namespace string) (subs []*v1alpha1.Subscription, err error) {
	list, err := r.client.OperatorsV1alpha1().Subscriptions(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return
	}

	subs = make([]*v1alpha1.Subscription, 0)
	for i := range list.Items {
		subs = append(subs, &list.Items[i])
	}

	return
}