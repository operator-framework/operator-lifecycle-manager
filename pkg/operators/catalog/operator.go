package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coreos-inc/alm/pkg/queueinformer"
	log "github.com/sirupsen/logrus"
	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/installplan/v1alpha1"
	catlib "github.com/coreos-inc/alm/pkg/catalog"
	"github.com/coreos-inc/alm/pkg/client"
)

const crdKind = "CustomResourceDefinition"

// Operator represents a Kubernetes operator that executes InstallPlans by
// resolving dependencies in a catalog.
type Operator struct {
	*queueinformer.Operator
	ipClient  client.InstallPlanInterface
	csvClient client.ClusterServiceVersionInterface
	sources   []catlib.Source
}

// NewOperator creates a new Catalog Operator.
func NewOperator(kubeconfigPath string, wakeupInterval time.Duration, sources []catlib.Source, namespaces ...string) (*Operator, error) {
	if namespaces == nil {
		namespaces = []string{metav1.NamespaceAll}
	}

	// Create an instance of an InstallPlan client.
	ipClient, err := client.NewInstallPlanClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create an instance of a CSV client.
	csvClient, err := client.NewClusterServiceVersionClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create a watch for each namespace.
	ipWatchers := []*cache.ListWatch{}
	for _, namespace := range namespaces {
		ipWatchers = append(ipWatchers, cache.NewListWatchFromClient(
			ipClient,
			"installplan-v1s",
			namespace,
			fields.Everything(),
		))
	}

	// Create an informer for each watch.
	sharedIndexInformers := []cache.SharedIndexInformer{}
	for _, ipWatcher := range ipWatchers {
		sharedIndexInformers = append(sharedIndexInformers, cache.NewSharedIndexInformer(
			ipWatcher,
			&v1alpha1.InstallPlan{},
			wakeupInterval,
			cache.Indexers{},
		))
	}

	// Create a new queueinformer-based operator.
	queueOperator, err := queueinformer.NewOperator(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	op := &Operator{
		queueOperator,
		ipClient,
		csvClient,
		sources,
	}

	ipQueueInformers := queueinformer.New(
		"installplans",
		sharedIndexInformers,
		op.syncInstallPlans,
		nil,
	)

	for _, opVerQueueInformer := range ipQueueInformers {
		op.RegisterQueueInformer(opVerQueueInformer)
	}

	return op, nil
}

func (o *Operator) syncInstallPlans(obj interface{}) (syncError error) {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting InstallPlan failed")
	}

	log.Infof("syncing InstallPlan: %s", plan.SelfLink)

	syncError = transitionInstallPlanState(o, plan)

	// Update CSV with status of transition. Log errors if we can't write them to the status.
	if _, err := o.ipClient.UpdateInstallPlan(plan); err != nil {
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
			cond := v1alpha1.ConditionFailed(v1alpha1.InstallPlanResolved,
				v1alpha1.InstallPlanReasonDependencyConflict, err)
			plan.Status.Conditions = v1alpha1.UpdateConditionIn(plan.Status.Conditions, cond)
			return err
		}
		cond := v1alpha1.ConditionMet(v1alpha1.InstallPlanResolved)
		plan.Status.Conditions = v1alpha1.UpdateConditionIn(plan.Status.Conditions, cond)
		plan.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
		return nil
	case v1alpha1.InstallPlanPhaseInstalling:
		log.Debug("plan phase Installing, attempting to install")
		if err := transitioner.ExecutePlan(plan); err != nil {
			cond := v1alpha1.ConditionFailed(v1alpha1.InstallPlanInstalled,
				v1alpha1.InstallPlanReasonComponentFailed, err)
			plan.Status.Conditions = v1alpha1.UpdateConditionIn(plan.Status.Conditions, cond)
			return err
		}
		cond := v1alpha1.ConditionMet(v1alpha1.InstallPlanInstalled)
		plan.Status.Conditions = v1alpha1.UpdateConditionIn(plan.Status.Conditions, cond)
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

	for _, source := range o.sources {
		log.Debugf("resolving against source %v", source)
		err := resolveInstallPlan(source, plan)
		// Intentionally return after the first source only.
		// TODO(jzelinskie): update to check all sources.
		return err
	}

	return nil
}

func resolveCRDDescription(crdDesc csvv1alpha1.CRDDescription, source catlib.Source, owned bool) (v1alpha1.StepResource, string, error) {
	log.Debugf("resolving %#v", crdDesc)

	crdKey := catlib.CRDKey{
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
		step, err := v1alpha1.NewStepResourceFromCRD(crd)
		return step, "", err
	}

	csv, err := source.FindLatestCSVForCRD(crdKey)
	if err != nil {
		return v1alpha1.StepResource{}, "", err
	}
	log.Infof("found %v owner %s", crdKey, csv.Name)

	return v1alpha1.StepResource{}, csv.Name, nil
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

func resolveCSV(csvName, namespace string, source catlib.Source) (stepResourceMap, error) {
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
		csv, err := source.FindLatestCSVByServiceName(currentName)
		if err != nil {
			return nil, err
		}
		log.Debugf("found %#v", csv)

		// Resolve each owned or required CRD for the CSV.
		for _, crdDesc := range csv.GetAllCRDDescriptions() {
			step, owner, err := resolveCRDDescription(crdDesc, source, csv.OwnsCRD(crdDesc.Name))
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

func resolveInstallPlan(source catlib.Source, plan *v1alpha1.InstallPlan) error {
	srm := make(stepResourceMap)
	for _, csvName := range plan.Spec.ClusterServiceVersionNames {
		csvSRM, err := resolveCSV(csvName, plan.Namespace, source)
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
				err = o.OpClient.CreateCustomResourceDefinitionKind(&crd)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
					continue
				} else if err != nil {
					return err
				} else {
					// If it no error occured, mark the step as Created.
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
				err = o.csvClient.CreateCSV(&csv)
				if k8serrors.IsAlreadyExists(err) {
					// If it already existed, mark the step as Present.
					plan.Status.Plan[i].Status = v1alpha1.StepStatusPresent
				} else if err != nil {
					return err
				} else {
					// If it no error occured, mark the step as Created.
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
