package catalog

import (
	"errors"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	"bytes"

	v1alpha1csv "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/apis/installplan/v1alpha1"
	catlib "github.com/coreos-inc/alm/catalog"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/queueinformer"
	log "github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
)

// Operator represents a Kubernetes operator that executes InstallPlans by
// resolving dependencies in a catalog.
type Operator struct {
	*queueinformer.Operator
	ipClient client.InstallPlanInterface
	sources  []catlib.Source
}

// NewOperator creates a new Catalog Operator.
func NewOperator(kubeconfigPath string, wakeupInterval time.Duration, sources []catlib.Source, namespaces ...string) (*Operator, error) {
	if namespaces == nil {
		namespaces = []string{metav1.NamespaceAll}
	}

	// Create an instance of the client.
	ipClient, err := client.NewInstallPlanClient(kubeconfigPath)
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

func (o *Operator) syncInstallPlans(obj interface{}) error {
	plan, ok := obj.(*v1alpha1.InstallPlan)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return errors.New("casting InstallPlan failed")
	}

	log.Infof("syncing InstallPlan: %s", plan.SelfLink)

	if err := o.transitionInstallPlanState(plan); err != nil {
		return err
	}

	_, err := o.ipClient.UpdateInstallPlan(plan)
	return err
}

func (o *Operator) transitionInstallPlanState(plan *v1alpha1.InstallPlan) error {
	for _, source := range o.sources {
		if err := createInstallPlan(source, plan); err != nil {
			return err
		}
	}
	return nil
}

func createInstallPlan(source catlib.Source, installPlan *v1alpha1.InstallPlan) error {
	steps := installPlan.Status.Plan
	names := installPlan.Spec.ClusterServiceVersionNames

	crdSerializer := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)
	scheme := runtime.NewScheme()
	if err := v1alpha1csv.AddToScheme(scheme); err != nil {
		return err
	}
	csvSerializer := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme, scheme)

	for len(names) > 0 {
		// looping here like this because we are adding names to the list from dependencies
		name := names[0]
		names = names[1:]
		csv, err := source.FindLatestCSVByServiceName(name)
		if err != nil {
			return err
		}

		for _, crdDescription := range csv.Spec.CustomResourceDefinitions.GetAllCrds() {
			crd, err := source.FindCRDByName(crdDescription.Name)
			if err != nil {
				return err
			}

			if checkIfOwned(*csv, crd.OwnerReferences) {
				var manifest bytes.Buffer
				if err := crdSerializer.Encode(crd, &manifest); err != nil {
					return err
				}
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
				steps = append(steps, step)
			} else {
				csvForCRD, err := source.FindLatestCSVForCRD(crdDescription.Name)
				if err != nil {
					return err
				}
				names = append(names, csvForCRD.Name)
			}

		}

		var manifestCSV bytes.Buffer
		if err := csvSerializer.Encode(csv, &manifestCSV); err != nil {
			return err
		}
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
		steps = append(steps, stepCSV)
	}
	installPlan.Status.Plan = steps
	installPlan.Status.InstallPlanPhase = v1alpha1.InstallPlanPhaseInstalling
	return nil
}

func checkIfOwned(csv v1alpha1csv.ClusterServiceVersion, ownerRefs []v1.OwnerReference) bool {
	for _, ownerRef := range ownerRefs {
		if csv.Name != "" && csv.Name == ownerRef.Name && csv.Kind != "" && csv.Kind == ownerRef.Kind {
			return true
		}
	}
	return false
}
