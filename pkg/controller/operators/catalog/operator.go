package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	catsrcv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/catalogsource/v1alpha1"
	csvv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/installplan/v1alpha1"
	subscriptionv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/subscription/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"k8s.io/client-go/util/workqueue"
)

const (
	crdKind    = "CustomResourceDefinition"
	secretKind = "Secret"
)

//for test stubbing and for ensuring standardization of timezones to UTC
var timeNow = func() metav1.Time { return metav1.NewTime(time.Now().UTC()) }

// Operator represents a Kubernetes operator that executes InstallPlans by
// resolving dependencies in a catalog.
type Operator struct {
	*queueinformer.Operator
	client             versioned.Interface
	namespace          string
	sources            map[registry.SourceKey]registry.Source
	sourcesLock        sync.RWMutex
	sourcesLastUpdate  metav1.Time
	subscriptions      map[registry.SubscriptionKey]subscriptionv1alpha1.Subscription
	subscriptionsLock  sync.RWMutex
	dependencyResolver resolver.DependencyResolver
}

// NewOperator creates a new Catalog Operator.
func NewOperator(kubeconfigPath string, wakeupInterval time.Duration, operatorNamespace string, watchedNamespaces ...string) (*Operator, error) {
	// Default to watching all namespaces.
	if watchedNamespaces == nil {
		watchedNamespaces = []string{metav1.NamespaceAll}
	}

	// Create a new client for ALM types (CRs)
	crClient, err := client.NewClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create an informer for each watched namespace.
	ipSharedIndexInformers := []cache.SharedIndexInformer{}
	subSharedIndexInformers := []cache.SharedIndexInformer{}
	for _, namespace := range watchedNamespaces {
		nsInformerFactory := externalversions.NewFilteredSharedInformerFactory(crClient, wakeupInterval, namespace, nil)
		ipSharedIndexInformers = append(ipSharedIndexInformers, nsInformerFactory.Installplan().V1alpha1().InstallPlans().Informer())
		subSharedIndexInformers = append(subSharedIndexInformers, nsInformerFactory.Subscription().V1alpha1().Subscriptions().Informer())
	}

	// Create an informer for each catalog namespace
	catsrcSharedIndexInformers := []cache.SharedIndexInformer{}
	for _, namespace := range []string{operatorNamespace} {
		nsInformerFactory := externalversions.NewFilteredSharedInformerFactory(crClient, wakeupInterval, namespace, nil)
		catsrcSharedIndexInformers = append(catsrcSharedIndexInformers, nsInformerFactory.Catalogsource().V1alpha1().CatalogSources().Informer())
	}

	// Create a new queueinformer-based operator.
	queueOperator, err := queueinformer.NewOperator(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Allocate the new instance of an Operator.
	op := &Operator{
		Operator:           queueOperator,
		client:             crClient,
		namespace:          operatorNamespace,
		sources:            make(map[registry.SourceKey]registry.Source),
		subscriptions:      make(map[registry.SubscriptionKey]subscriptionv1alpha1.Subscription),
		dependencyResolver: &resolver.MultiSourceResolver{},
	}

	// Register CatalogSource informers.
	catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogsources")
	catsrcQueueInformer := queueinformer.New(
		catsrcQueue,
		catsrcSharedIndexInformers,
		op.syncCatalogSources,
		nil,
	)
	for _, informer := range catsrcQueueInformer {
		op.RegisterQueueInformer(informer)
	}

	// Register InstallPlan informers.
	ipQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "installplans")
	ipQueueInformers := queueinformer.New(
		ipQueue,
		ipSharedIndexInformers,
		op.syncInstallPlans,
		nil,
	)
	for _, informer := range ipQueueInformers {
		op.RegisterQueueInformer(informer)
	}

	// Register Subscription informers.
	subscriptionQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "subscriptions")
	subscriptionQueueInformers := queueinformer.New(
		subscriptionQueue,
		subSharedIndexInformers,
		op.syncSubscriptions,
		nil,
	)
	for _, informer := range subscriptionQueueInformers {
		op.RegisterQueueInformer(informer)
	}

	return op, nil
}

func (o *Operator) syncCatalogSources(obj interface{}) (syncError error) {
	catsrc, ok := obj.(*catsrcv1alpha1.CatalogSource)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting CatalogSource failed")
	}

	src, err := registry.NewInMemoryFromConfigMap(o.OpClient, o.namespace, catsrc.Spec.ConfigMap)
	if err != nil {
		return fmt.Errorf("failed to create catalog source from ConfigMap %s: %s", catsrc.Spec.ConfigMap, err)
	}

	o.sourcesLock.Lock()
	defer o.sourcesLock.Unlock()
	o.sources[registry.SourceKey{Name: catsrc.GetName(), Namespace: catsrc.GetNamespace()}] = src
	o.sourcesLastUpdate = timeNow()
	return nil
}

func (o *Operator) syncSubscriptions(obj interface{}) (syncError error) {
	sub, ok := obj.(*subscriptionv1alpha1.Subscription)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Subscription failed")
	}

	log.Infof("syncing Subscription with catalog %s: %s on channel %s",
		sub.Spec.CatalogSource, sub.Spec.Package, sub.Spec.Channel)

	var updatedSub *subscriptionv1alpha1.Subscription
	updatedSub, syncError = o.syncSubscription(sub)

	if updatedSub != nil {
		updatedSub.Status.LastUpdated = timeNow()
		// Update Subscription with status of transition. Log errors if we can't write them to the status.
		if _, err := o.client.SubscriptionV1alpha1().Subscriptions(updatedSub.GetNamespace()).Update(updatedSub); err != nil {
			updateErr := errors.New("error updating Subscription status: " + err.Error())
			if syncError == nil {
				log.Info(updateErr)
				return updateErr
			}
			syncError = fmt.Errorf("error transitioning Subscription: %s and error updating Subscription status: %s", syncError, updateErr)
			log.Info(syncError)
		} else {
			// map subcription
			o.subscriptionsLock.Lock()
			defer o.subscriptionsLock.Unlock()
			o.subscriptions[registry.SubscriptionKey{Name: sub.GetName(), Namespace: sub.GetNamespace()}] = *updatedSub
		}
	}
	return
}

func (o *Operator) syncInstallPlans(obj interface{}) (syncError error) {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting InstallPlan failed")
	}

	log.Infof("syncing InstallPlan: %s", plan.SelfLink)

	syncError = transitionInstallPlanState(o, plan)

	// Update InstallPlan with status of transition. Log errors if we can't write them to the status.
	if _, err := o.client.InstallplanV1alpha1().InstallPlans(plan.GetNamespace()).Update(plan); err != nil {
		updateErr := errors.New("error updating InstallPlan status: " + err.Error())
		if syncError == nil {
			log.Info(updateErr)
			return updateErr
		}
		syncError = fmt.Errorf("error transitioning InstallPlan: %s and error updating InstallPlan status: %s", syncError, updateErr)
		log.Info(syncError)
	}
	return
}

type installPlanTransitioner interface {
	ResolvePlan(*v1alpha1.InstallPlan) error
	ExecutePlan(*v1alpha1.InstallPlan) error
}

var _ installPlanTransitioner = &Operator{}

func transitionInstallPlanState(transitioner installPlanTransitioner, plan *v1alpha1.InstallPlan) error {
	switch plan.Status.Phase {
	case v1alpha1.InstallPlanPhaseNone:
		log.Debugf("plan %s phase unrecognized, setting to Planning", plan.SelfLink)
		plan.Status.Phase = v1alpha1.InstallPlanPhasePlanning
		return nil

	case v1alpha1.InstallPlanPhasePlanning:
		log.Debugf("plan %s phase Planning, attempting to resolve", plan.SelfLink)
		if err := transitioner.ResolvePlan(plan); err != nil {
			plan.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanResolved,
				v1alpha1.InstallPlanReasonInstallCheckFailed, err))
			plan.Status.Phase = v1alpha1.InstallPlanPhaseFailed
			return err
		}
		plan.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanResolved))

		if plan.Spec.Approval == v1alpha1.ApprovalManual && plan.Spec.Approved != true {
			plan.Status.Phase = v1alpha1.InstallPlanPhaseRequiresApproval
		} else {
			plan.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		}
		return nil

	case v1alpha1.InstallPlanPhaseRequiresApproval:
		if plan.Spec.Approved {
			log.Debugf("plan %s approved, setting to Planning", plan.SelfLink)
			plan.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		} else {
			log.Debugf("plan %s is not approved, skipping sync", plan.SelfLink)
		}
		return nil

	case v1alpha1.InstallPlanPhaseInstalling:
		log.Debugf("plan %s phase Installing, attempting to install", plan.SelfLink)
		if err := transitioner.ExecutePlan(plan); err != nil {
			plan.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanInstalled,
				v1alpha1.InstallPlanReasonComponentFailed, err))
			plan.Status.Phase = v1alpha1.InstallPlanPhaseFailed
			return err
		}
		plan.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanInstalled))
		plan.Status.Phase = v1alpha1.InstallPlanPhaseComplete
		return nil

	default:
		return nil
	}
}

// ResolvePlan modifies an InstallPlan to contain a Plan in its Status field.
func (o *Operator) ResolvePlan(plan *v1alpha1.InstallPlan) error {
	if plan.Status.Phase != v1alpha1.InstallPlanPhasePlanning {
		panic("attempted to create a plan that wasn't in the planning phase")
	}

	if len(o.sources) == 0 {
		return fmt.Errorf("cannot resolve InstallPlan without any Catalog Sources")
	}

	// Copy the sources for resolution from the included namespaces
	includedNamespaces := map[string]struct{}{
		o.namespace:    struct{}{},
		plan.Namespace: struct{}{},
	}
	sourcesSnapshot := o.getSourcesSnapshot(plan, includedNamespaces)

	// Attempt to resolve the InstallPlan
	steps, usedSources, notFoundErr := o.dependencyResolver.ResolveInstallPlan(sourcesSnapshot, CatalogLabel, plan)
	if notFoundErr != nil {
		return notFoundErr
	}

	// Set the resolved steps
	plan.Status.Plan = steps
	plan.Status.CatalogSources = usedSources

	// Add secrets for each used catalog source
	for _, sourceKey := range plan.Status.CatalogSources {
		catsrc, err := o.client.CatalogsourceV1alpha1().CatalogSources(sourceKey.Namespace).Get(sourceKey.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		for _, secretName := range catsrc.Spec.Secrets {
			// Attempt to look up the secret.
			_, err := o.OpClient.KubernetesInterface().CoreV1().Secrets(sourceKey.Namespace).Get(secretName, metav1.GetOptions{})
			status := v1alpha1.StepStatusUnknown
			if k8serrors.IsNotFound(err) {
				status = v1alpha1.StepStatusNotPresent
			} else if err == nil {
				status = v1alpha1.StepStatusPresent
			} else {
				return err
			}

			// Prepend any required secrets to the plan for that Catalog Source.
			plan.Status.Plan = append([]v1alpha1.Step{{
				Resolving: "",
				Resource: v1alpha1.StepResource{
					Name:    secretName,
					Kind:    "Secret",
					Group:   "",
					Version: "v1",
				},
				Status: status,
			}}, plan.Status.Plan...)
		}
	}

	return nil
}

// ExecutePlan applies a planned InstallPlan to a namespace.
func (o *Operator) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	if plan.Status.Phase != v1alpha1.InstallPlanPhaseInstalling {
		panic("attempted to install a plan that wasn't in the installing phase")
	}

	for i, step := range plan.Status.Plan {
		switch step.Status {
		case v1alpha1.StepStatusPresent, v1alpha1.StepStatusCreated:
			continue

		case v1alpha1.StepStatusUnknown, v1alpha1.StepStatusNotPresent:
			log.Debugf("resource kind: %s", step.Resource.Kind)
			switch step.Resource.Kind {
			case crdKind:
				// Marshal the manifest into a CRD instance.
				var crd v1beta1ext.CustomResourceDefinition
				err := json.Unmarshal([]byte(step.Resource.Manifest), &crd)
				if err != nil {
					return err
				}

				// TODO: check that names are accepted
				// Attempt to create the CRD.
				_, err = o.OpClient.ApiextensionsV1beta1Interface().ApiextensionsV1beta1().CustomResourceDefinitions().Create(&crd)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
					continue
				} else if err != nil {
					return err
				} else {
					// If no error occured, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
					continue
				}

			case csvv1alpha1.ClusterServiceVersionKind:
				// Marshal the manifest into a CRD instance.
				var csv csvv1alpha1.ClusterServiceVersion
				err := json.Unmarshal([]byte(step.Resource.Manifest), &csv)
				if err != nil {
					return err
				}

				// Attempt to create the CSV.
				_, err = o.client.ClusterserviceversionV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Create(&csv)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occurred, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			case secretKind:
				// Get the pre-existing secret.
				secret, err := o.OpClient.KubernetesInterface().CoreV1().Secrets(o.namespace).Get(step.Resource.Name, metav1.GetOptions{})
				if k8serrors.IsNotFound(err) {
					return fmt.Errorf("secret %s does not exist", step.Resource.Name)
				} else if err != nil {
					return err
				}

				// Set the namespace to the InstallPlan's namespace and attempt to
				// create a new secret.
				secret.Namespace = plan.Namespace
				_, err = o.OpClient.KubernetesInterface().CoreV1().Secrets(plan.Namespace).Create(&v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secret.Name,
						Namespace: plan.Namespace,
					},
					Data: secret.Data,
					Type: secret.Type,
				})
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If no error occured, mark the step as Created.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusCreated
				}

			default:
				return v1alpha1.ErrInvalidInstallPlan
			}

		default:
			return v1alpha1.ErrInvalidInstallPlan
		}
	}

	// Loop over one final time to check and see if everything is good.
	for _, step := range plan.Status.Plan {
		switch step.Status {
		case v1alpha1.StepStatusCreated, v1alpha1.StepStatusPresent:
		default:
			return nil
		}
	}

	return nil
}

func (o *Operator) getSourcesSnapshot(plan *v1alpha1.InstallPlan, includedNamespaces map[string]struct{}) []registry.SourceRef {
	o.sourcesLock.RLock()
	defer o.sourcesLock.RUnlock()
	sourcesSnapshot := []registry.SourceRef{}

	for key, source := range o.sources {
		// Only copy catalog sources in included namespaces
		if _, ok := includedNamespaces[key.Namespace]; ok {
			ref := registry.SourceRef{
				Source:    source,
				SourceKey: key,
			}
			if key.Name == plan.Spec.CatalogSource && key.Namespace == plan.Spec.CatalogSourceNamespace {
				// Prepend preffered catalog source
				sourcesSnapshot = append([]registry.SourceRef{ref}, sourcesSnapshot...)
			} else {
				// Append the catalog source
				sourcesSnapshot = append(sourcesSnapshot, ref)
			}
		}
	}

	return sourcesSnapshot
}
