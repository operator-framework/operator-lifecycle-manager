package alm

import (
	"errors"
	"fmt"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	"github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/config"
	"github.com/coreos-inc/alm/install"
	"github.com/coreos-inc/alm/queueinformer"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")

type ALMOperator struct {
	*queueinformer.Operator
	csvClient client.ClusterServiceVersionInterface
}

func NewALMOperator(kubeconfig string, cfg *config.Config) (*ALMOperator, error) {
	csvClient, err := client.NewClusterServiceVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}

	csvWatchers := []*cache.ListWatch{}
	for _, namespace := range cfg.Namespaces {
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
			cfg.Interval,
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

func (a *ALMOperator) syncClusterServiceVersion(obj interface{}) error {
	clusterServiceVersion, ok := obj.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting ClusterServiceVersion failed")
	}

	log.Infof("syncing ClusterServiceVersion: %s", clusterServiceVersion.SelfLink)

	switch clusterServiceVersion.Status.Phase {
	case v1alpha1.CSVPhaseNone:
		log.Infof("scheduling ClusterServiceVersion for requirement verification: %s", clusterServiceVersion.SelfLink)
		if _, err := a.csvClient.TransitionPhase(clusterServiceVersion, v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnkown, "requirements not yet checked"); err != nil {
			return err
		}
		return nil
	case v1alpha1.CSVPhasePending:
		met, statuses := a.requirementStatus(clusterServiceVersion.Spec.CustomResourceDefinitions)

		if !met {
			log.Info("requirements were not met")
			if _, err := a.csvClient.UpdateRequirementStatus(clusterServiceVersion, v1alpha1.CSVPhasePending, statuses, v1alpha1.CSVReasonRequirementsNotMet, "one or more requirements couldn't be found"); err != nil {
				return err
			}
			return ErrRequirementsNotMet
		}

		log.Infof("scheduling ClusterServiceVersion for install: %s", clusterServiceVersion.SelfLink)
		if _, err := a.csvClient.UpdateRequirementStatus(clusterServiceVersion, v1alpha1.CSVPhaseInstalling, statuses, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting intstall"); err != nil {
			return err
		}
		return nil
	case v1alpha1.CSVPhaseInstalling:
		resolver := install.NewStrategyResolver(a.OpClient, clusterServiceVersion.ObjectMeta)
		installed, err := resolver.CheckInstalled(&clusterServiceVersion.Spec.InstallStrategy)
		if err != nil {
			if _, err := a.csvClient.TransitionPhase(clusterServiceVersion, v1alpha1.CSVPhaseUnknown, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install check failed: %s", err)); err != nil {
				return err
			}
		}
		if installed {
			log.Infof(
				"%s install strategy successful for %s",
				clusterServiceVersion.Spec.InstallStrategy.StrategyName,
				clusterServiceVersion.SelfLink,
			)
			if _, err := a.csvClient.TransitionPhase(clusterServiceVersion, v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors"); err != nil {
				return err
			}
			return nil
		}
		err = resolver.ApplyStrategy(&clusterServiceVersion.Spec.InstallStrategy)
		if err != nil {
			if _, transitionErr := a.csvClient.TransitionPhase(clusterServiceVersion, v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", err)); err != nil {
				return transitionErr
			}
			return err
		}
	}

	return nil
}

func (a *ALMOperator) requirementStatus(crds v1alpha1.CustomResourceDefinitions) (met bool, statuses []v1alpha1.RequirementStatus) {
	met = true
	requirements := append(crds.Owned, crds.Required...)
	for _, r := range requirements {
		status := v1alpha1.RequirementStatus{
			Group:   "apiextensions.k8s.io",
			Version: "v1beta1",
			Kind:    "CustomResourceDefinition",
			Name:    r,
		}
		crd, err := a.OpClient.GetCustomResourceDefinitionKind(r)
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
