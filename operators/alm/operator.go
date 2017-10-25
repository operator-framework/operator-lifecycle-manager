package alm

import (
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/install"
	"github.com/coreos-inc/alm/queueinformer"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")

const FallbackWakeupInterval = 30 * time.Second

type ALMOperator struct {
	*queueinformer.Operator
	csvClient client.ClusterServiceVersionInterface
	resolver  install.Resolver
}

func NewALMOperator(kubeconfig string, wakeupInterval time.Duration, namespaces ...string) (*ALMOperator, error) {
	if wakeupInterval < 0 {
		wakeupInterval = FallbackWakeupInterval
	}

	csvClient, err := client.NewClusterServiceVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}

	csvWatchers := []*cache.ListWatch{}
	for _, namespace := range namespaces {
		csvWatcher := cache.NewListWatchFromClient(
			csvClient,
			"clusterserviceversion-v1s",
			namespace,
			fields.Everything(),
		)
		csvWatchers = append(csvWatchers, csvWatcher)
	}

	sharedIndexInformers := []cache.SharedIndexInformer{}
	for _, csvWatcher := range csvWatchers {
		csvInformer := cache.NewSharedIndexInformer(
			csvWatcher,
			&v1alpha1.ClusterServiceVersion{},
			wakeupInterval,
			cache.Indexers{},
		)
		sharedIndexInformers = append(sharedIndexInformers, csvInformer)
	}

	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}

	op := &ALMOperator{
		queueOperator,
		csvClient,
		install.NewStrategyResolver(queueOperator.OpClient),
	}
	csvQueueInformers := queueinformer.New(
		"clusterserviceversions",
		sharedIndexInformers,
		op.syncClusterServiceVersion,
		nil,
	)
	for _, opVerQueueInformer := range csvQueueInformers {
		op.RegisterQueueInformer(opVerQueueInformer)
	}

	return op, nil
}

// syncClusterServiceVersion is the method that gets called when we see a CSV event in the cluster
func (a *ALMOperator) syncClusterServiceVersion(obj interface{}) (syncError error) {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	log.Infof("syncing ClusterServiceVersion: %s", clusterServiceVersion.SelfLink)

	syncError = a.transitionCSVState(clusterServiceVersion)

	// Update CSV with status of transition. Log errors if we can't write them to the status.
	if _, err := a.csvClient.UpdateCSV(clusterServiceVersion); err != nil {
		updateErr := errors.New("error updating ClusterServiceVersion status: " + err.Error())
		if syncError == nil {
			log.Info(updateErr)
			return updateErr
		}
		syncError = fmt.Errorf("error transitioning ClusterServiceVersion: %s and error updating CSV status: %s", syncError, updateErr)
		log.Info(syncError)
	}
	return
}

// transitionCSVState moves the CSV status state machine along based on the current value and the current cluster
// state.
func (a *ALMOperator) transitionCSVState(csv *v1alpha1.ClusterServiceVersion) (syncError error) {
	switch csv.Status.Phase {
	case v1alpha1.CSVPhaseNone:
		log.Infof("scheduling ClusterServiceVersion for requirement verification: %s", csv.SelfLink)
		csv.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown, "requirements not yet checked")
	case v1alpha1.CSVPhasePending:
		met, statuses := a.requirementStatus(csv.Spec.CustomResourceDefinitions)

		if !met {
			log.Info("requirements were not met")
			csv.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsNotMet, "one or more requirements couldn't be found")
			csv.SetRequirementStatus(statuses)
			syncError = ErrRequirementsNotMet
			return
		}

		log.Infof("scheduling ClusterServiceVersion for install: %s", csv.SelfLink)
		csv.SetPhase(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting install")
		csv.SetRequirementStatus(statuses)
	case v1alpha1.CSVPhaseInstalling:
		installed, err := a.resolver.CheckInstalled(csv.Spec.InstallStrategy, csv.ObjectMeta, csv.TypeMeta)
		if err != nil {
			// TODO: add a retry count, don't give up on first failure
			csv.SetPhase(v1alpha1.CSVPhaseUnknown, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install check failed: %s", err))
			return
		}
		if installed {
			log.Infof(
				"%s install strategy successful for %s",
				csv.Spec.InstallStrategy.StrategyName,
				csv.SelfLink,
			)
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors")
			return
		}
		// We transition to ComponentFailed if install failed, but we don't transition to succeeded here. Instead we let
		// this queue pick the object back up, and transition to Succeeded once we verify the install
		// with the install strategy's `CheckInstall`
		syncError = a.resolver.ApplyStrategy(csv.Spec.InstallStrategy, csv.ObjectMeta, csv.TypeMeta)
		if syncError != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", err))
			return
		} else {
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors")
			return
		}
	case v1alpha1.CSVPhaseSucceeded:
		installed, err := a.resolver.CheckInstalled(csv.Spec.InstallStrategy, csv.ObjectMeta, csv.TypeMeta)

		// if already installed, don't transition to pending if we can't query
		if err != nil {
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install check failed: %s", err))
			return
		}
		// transition back to pending
		if !installed {
			csv.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonComponentUnhealthy, "component unhealthy, rechecking and re-installing")
			return
		}
	}
	return
}

func (a *ALMOperator) requirementStatus(crds v1alpha1.CustomResourceDefinitions) (met bool, statuses []v1alpha1.RequirementStatus) {
	met = true
	requirements := crds.GetAllCrds()
	for _, r := range requirements {
		status := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1beta1",
			Kind:    "CustomResourceDefinition",
			Name:    r.Name,
		}
		crd, err := a.OpClient.GetCustomResourceDefinitionKind(r.Name)
		if err != nil {
			status.Status = "NotPresent"
			met = false
		} else {
			status.Status = "Present"
			status.UUID = string(crd.GetUID())
		}
		statuses = append(statuses, status)
	}
	return
}
