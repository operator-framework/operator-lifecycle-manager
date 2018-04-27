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

	catsrcv1alpha1 "github.com/coreos/alm/pkg/api/apis/catalogsource/v1alpha1"
	csvv1alpha1 "github.com/coreos/alm/pkg/api/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos/alm/pkg/api/apis/installplan/v1alpha1"
	subscriptionv1alpha1 "github.com/coreos/alm/pkg/api/apis/subscription/v1alpha1"
	"github.com/coreos/alm/pkg/api/client"
	"github.com/coreos/alm/pkg/api/client/clientset/versioned"
	"github.com/coreos/alm/pkg/api/client/informers/externalversions"
	"github.com/coreos/alm/pkg/controller/registry"
	"github.com/coreos/alm/pkg/lib/queueinformer"
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
	client            versioned.Interface
	namespace         string
	sources           map[string]registry.Source
	sourcesLock       sync.Mutex
	sourcesLastUpdate metav1.Time
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

	sharedInformerFactory := externalversions.NewSharedInformerFactory(crClient, wakeupInterval)

	// Create an informer for each namespace.
	ipSharedIndexInformers := []cache.SharedIndexInformer{}
	subSharedIndexInformers := []cache.SharedIndexInformer{}
	for _, namespace := range watchedNamespaces {
		nsInformerFactory := externalversions.NewFilteredSharedInformerFactory(crClient, wakeupInterval, namespace, nil)
		ipSharedIndexInformers = append(ipSharedIndexInformers, nsInformerFactory.Installplan().V1alpha1().InstallPlans().Informer())
		subSharedIndexInformers = append(subSharedIndexInformers, nsInformerFactory.Subscription().V1alpha1().Subscriptions().Informer())
	}

	// Create a new queueinformer-based operator.
	queueOperator, err := queueinformer.NewOperator(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Allocate the new instance of an Operator.
	op := &Operator{
		Operator:  queueOperator,
		client:    crClient,
		namespace: operatorNamespace,
		sources:   make(map[string]registry.Source),
	}
	// Register CatalogSource informers.
	catsrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogsources")
	catsrcQueueInformer := queueinformer.New(
		catsrcQueue,
		[]cache.SharedIndexInformer{
			sharedInformerFactory.Catalogsource().V1alpha1().CatalogSources().Informer(),
		},
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
	o.sources[catsrc.GetName()] = src
	o.sourcesLastUpdate = timeNow()
	return err
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
		log.Debug("plan phase unrecognized, setting to Planning")
		plan.Status.Phase = v1alpha1.InstallPlanPhasePlanning
		return nil

	case v1alpha1.InstallPlanPhasePlanning:
		log.Debug("plan phase Planning, attempting to resolve")
		if err := transitioner.ResolvePlan(plan); err != nil {
			plan.Status.SetCondition(v1alpha1.ConditionFailed(v1alpha1.InstallPlanResolved,
				v1alpha1.InstallPlanReasonDependencyConflict, err))
			plan.Status.Phase = v1alpha1.InstallPlanPhaseFailed
			return err
		}
		plan.Status.SetCondition(v1alpha1.ConditionMet(v1alpha1.InstallPlanResolved))
		plan.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		return nil

	case v1alpha1.InstallPlanPhaseInstalling:
		log.Debug("plan phase Installing, attempting to install")
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
	o.sourcesLock.Lock()
	defer o.sourcesLock.Unlock()

	var notFoundErr error
	for sourceName, source := range o.sources {
		log.Debugf("resolving against source %v", sourceName)
		plan.EnsureCatalogSource(sourceName)
		notFoundErr = resolveInstallPlan(sourceName, source, plan)
		if notFoundErr != nil {
			continue
		}

		// Look up the CatalogSource.
		catsrc, err := o.client.CatalogsourceV1alpha1().CatalogSources(o.namespace).Get(sourceName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		for _, secretName := range catsrc.Spec.Secrets {
			// Attempt to look up the secret.
			_, err := o.OpClient.KubernetesInterface().CoreV1().Secrets(plan.Namespace).Get(secretName, metav1.GetOptions{})
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
		return nil
	}

	return notFoundErr
}

func resolveCRDDescription(crdDesc csvv1alpha1.CRDDescription, sourceName string, source registry.Source, owned bool) (v1alpha1.StepResource, string, error) {
	log.Debugf("resolving %#v", crdDesc)

	crdKey := registry.CRDKey{
		Kind:    crdDesc.Kind,
		Name:    crdDesc.Name,
		Version: crdDesc.Version,
	}

	crd, err := source.FindCRDByKey(crdKey)
	if err != nil {
		return v1alpha1.StepResource{}, "", err
	}
	log.Debugf("found %#v", crd)

	if owned {
		// Label CRD with catalog source
		labels := crd.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[CatalogLabel] = sourceName
		crd.SetLabels(labels)

		// Add CRD Step
		step, err := v1alpha1.NewStepResourceFromCRD(crd)
		return step, "", err
	}

	csvs, err := source.ListLatestCSVsForCRD(crdKey)
	if err != nil {
		return v1alpha1.StepResource{}, "", err
	}
	if len(csvs) == 0 {
		return v1alpha1.StepResource{}, "", fmt.Errorf("Unknown CRD %s", crdKey)
	}

	// TODO: Change to lookup the CSV from the preferred or default channel.
	log.Infof("found %v owner %s", crdKey, csvs[0].CSV.Name)
	return v1alpha1.StepResource{}, csvs[0].CSV.Name, nil
}

type stepResourceMap map[string][]v1alpha1.StepResource

func (srm stepResourceMap) Plan() []v1alpha1.Step {
	steps := make([]v1alpha1.Step, 0)
	for csvName, stepResSlice := range srm {
		for _, stepRes := range stepResSlice {
			steps = append(steps, v1alpha1.Step{
				Resolving: csvName,
				Resource:  stepRes,
				Status:    v1alpha1.StepStatusUnknown,
			})
		}
	}

	return steps
}

func (srm stepResourceMap) Combine(y stepResourceMap) {
	for csvName, stepResSlice := range y {
		// Skip any redundant steps.
		if _, alreadyExists := srm[csvName]; alreadyExists {
			continue
		}

		srm[csvName] = stepResSlice
	}
}

func resolveCSV(csvName, namespace, sourceName string, source registry.Source) (stepResourceMap, error) {
	log.Debugf("resolving CSV with name: %s", csvName)

	steps := make(stepResourceMap)
	csvNamesToBeResolved := []string{csvName}

	for len(csvNamesToBeResolved) != 0 {
		// Pop off a CSV name.
		currentName := csvNamesToBeResolved[0]
		csvNamesToBeResolved = csvNamesToBeResolved[1:]

		// If this CSV is already resolved, continue.
		if _, exists := steps[currentName]; exists {
			continue
		}

		// Get the full CSV object for the name.
		csv, err := source.FindCSVByName(currentName)
		if err != nil {
			return nil, err
		}
		log.Debugf("found %#v", csv)

		// Resolve each owned or required CRD for the CSV.
		for _, crdDesc := range csv.GetAllCRDDescriptions() {
			step, owner, err := resolveCRDDescription(crdDesc, sourceName, source, csv.OwnsCRD(crdDesc.Name))
			if err != nil {
				return nil, err
			}

			// If a different owner was resolved, add it to the list.
			if owner != "" && owner != currentName {
				csvNamesToBeResolved = append(csvNamesToBeResolved, owner)
				continue
			}

			// Add the resolved step to the plan.
			steps[currentName] = append(steps[currentName], step)
		}

		// Manually override the namespace and create the final step for the CSV,
		// which is for the CSV itself.
		csv.SetNamespace(namespace)

		// Add the sourcename as a label on the CSV, so that we know where it came from
		labels := csv.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[CatalogLabel] = sourceName
		csv.SetLabels(labels)

		step, err := v1alpha1.NewStepResourceFromCSV(csv)
		if err != nil {
			return nil, err
		}

		// Add the final step for the CSV to the plan.
		log.Infof("finished step: %v", step)
		steps[currentName] = append(steps[currentName], step)
	}

	return steps, nil
}

func resolveInstallPlan(sourceName string, source registry.Source, plan *v1alpha1.InstallPlan) error {
	srm := make(stepResourceMap)
	for _, csvName := range plan.Spec.ClusterServiceVersionNames {
		csvSRM, err := resolveCSV(csvName, plan.Namespace, sourceName, source)
		if err != nil {
			return err
		}

		srm.Combine(csvSRM)
	}

	plan.Status.Plan = srm.Plan()
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

				// Attempt to create the CRD.
				err = o.OpClient.CreateCustomResourceDefinition(&crd)
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
