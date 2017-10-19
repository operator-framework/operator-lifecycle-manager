package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coreos-inc/alm/queueinformer"
	log "github.com/sirupsen/logrus"
	v1beta1ext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/tools/cache"

	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	v1alpha1csv "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/apis/installplan/v1alpha1"
	catlib "github.com/coreos-inc/alm/catalog"
	"github.com/coreos-inc/alm/client"
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

	syncError = o.transitionInstallPlanState(plan)

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

func (o *Operator) transitionInstallPlanState(plan *v1alpha1.InstallPlan) error {
	switch plan.Status.Phase {
	case v1alpha1.InstallPlanPhaseNone:
		log.Info("plan phase unrecognized, setting to Planning")
		plan.Status.Phase = v1alpha1.InstallPlanPhasePlanning
	case v1alpha1.InstallPlanPhasePlanning:
		log.Info("plan phase Planning, attempting to resolve")
		for _, source := range o.sources {
			log.Infof("resolving against source %v", source)
			err := createInstallPlan(source, plan)
			// Intentionally return after the first source only.
			// TODO(jzelinskie): update to check all sources.
			return err
		}
	case v1alpha1.InstallPlanPhaseInstalling:
		log.Info("plan phase Installing, attempting to install")
		if err := o.installInstallPlan(plan); err != nil {
			return err
		}
	}
	return nil
}

func createInstallPlan(source catlib.Source, installPlan *v1alpha1.InstallPlan) error {
	steps := installPlan.Status.Plan
	names := installPlan.Spec.ClusterServiceVersionNames

	crdSerializer := k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme.Scheme, scheme.Scheme, true)
	scheme := runtime.NewScheme()
	if err := v1alpha1csv.AddToScheme(scheme); err != nil {
		return err
	}
	csvSerializer := k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme, scheme, true)

	log.Info("resolving names")

	for len(names) > 0 {
		// looping here like this because we are adding names to the list from dependencies
		name := names[0]
		names = names[1:]

		log.Infof("resolving %s", name)

		csv, err := source.FindLatestCSVByServiceName(name)
		if err != nil {
			return err
		}

		log.Info("resolving CRDs for CSV: %v", csv)
		for _, crdDescription := range csv.Spec.CustomResourceDefinitions.GetAllCrds() {
			log.Info("resolving crd: %v", crdDescription)

			crd, err := source.FindCRDByName(crdDescription.Name)
			if err != nil {
				return err
			}

			log.Info("found crd: %v", crd)

			if checkIfOwned(*csv, crd.Name) {
				log.Infof("crd is owned: %s", crd.Name)

				var manifest bytes.Buffer
				if err := crdSerializer.Encode(crd, &manifest); err != nil {
					return err
				}

				log.Infof("encoded crd as manifest: %s", manifest.String())
				step := v1alpha1.Step{
					Resolving: name,
					Resource: v1alpha1.StepResource{
						Group:    crd.Spec.Group,
						Version:  crd.Spec.Version,
						Kind:     crd.Kind,
						Name:     crd.Name,
						Manifest: manifest.String(),
					},
					Status: v1alpha1.StepStatusUnknown,
				}

				log.Infof("finished step: %v", step)
				steps = append(steps, step)
			} else {
				log.Infof("crd is not owned: %s", crd.Name)
				csvForCRD, err := source.FindLatestCSVForCRD(crdDescription.Name)
				if err != nil {
					return err
				}
				log.Infof("found csv for crd: %s", csv.Name)
				names = append(names, csvForCRD.Name)
			}

		}

		csv.SetNamespace(installPlan.Namespace)
		var manifestCSV bytes.Buffer
		if err := csvSerializer.Encode(csv, &manifestCSV); err != nil {
			return err
		}

		log.Infof("encoded crd as manifest: %s", manifestCSV.String())

		stepCSV := v1alpha1.Step{
			Resolving: name,
			Resource: v1alpha1.StepResource{
				Group:    csv.GroupVersionKind().Group,
				Version:  csv.GroupVersionKind().Group,
				Kind:     csv.Kind,
				Name:     csv.Name,
				Manifest: manifestCSV.String(),
			},
			Status: v1alpha1.StepStatusUnknown,
		}
		log.Infof("finished step: %v", stepCSV)
		steps = append(steps, stepCSV)
	}
	log.Infof("finished install plan resolution")
	installPlan.Status.Plan = steps
	installPlan.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
	return nil
}

func checkIfOwned(csv v1alpha1csv.ClusterServiceVersion, crdName string) bool {
	for _, crdDescription := range csv.Spec.CustomResourceDefinitions.Owned {
		if crdDescription.Name == crdName {
			return true
		}
	}
	return false
}

func (o *Operator) installInstallPlan(plan *v1alpha1.InstallPlan) error {
	if plan.Status.Phase != v1alpha1.InstallPlanPhaseInstalling {
		panic("attempted to install a plan that wasn't in the installing phase")
	}

	for i, step := range plan.Status.Plan {
		switch step.Status {
		case v1alpha1.StepStatusPresent, v1alpha1.StepStatusCreated:
			continue
		case v1alpha1.StepStatusUnknown, v1alpha1.StepStatusNotPresent:
			log.Infof("resource kind: %s", step.Resource.Kind)
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
	plan.Status.Phase = v1alpha1.InstallPlanPhaseComplete
	return nil
}
