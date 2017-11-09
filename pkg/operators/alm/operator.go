package alm

import (
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	"github.com/coreos-inc/alm/pkg/annotator"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client"
	"github.com/coreos-inc/alm/pkg/install"
	"github.com/coreos-inc/alm/pkg/queueinformer"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")

const (
	FallbackWakeupInterval  = 30 * time.Second
	ALMManagedAnnotationKey = "alm-manager"
)

type ALMOperator struct {
	*queueinformer.Operator
	csvClient client.ClusterServiceVersionInterface
	resolver  install.StrategyResolverInterface
	annotator *annotator.Annotator
}

func NewALMOperator(kubeconfig string, wakeupInterval time.Duration, podNamespace, podName string, namespaces ...string) (*ALMOperator, error) {
	if wakeupInterval < 0 {
		wakeupInterval = FallbackWakeupInterval
	}
	if namespaces == nil {
		namespaces = []string{metav1.NamespaceAll}
	}

	csvClient, err := client.NewClusterServiceVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	csvSharedIndexInformers := []cache.SharedIndexInformer{}
	namespaceSharedIndexInformers := []cache.SharedIndexInformer{}

	for _, namespace := range namespaces {
		csvWatcher := cache.NewListWatchFromClient(
			csvClient,
			"clusterserviceversion-v1s",
			namespace,
			fields.Everything(),
		)
		csvInformer := cache.NewSharedIndexInformer(
			csvWatcher,
			&v1alpha1.ClusterServiceVersion{},
			wakeupInterval,
			cache.Indexers{},
		)
		csvSharedIndexInformers = append(csvSharedIndexInformers, csvInformer)

		namespaceWatcher := cache.NewListWatchFromClient(
			csvClient,
			"namespaces",
			podNamespace,
			fields.OneTermEqualSelector("metadata.namespace", namespace),
		)
		namespaceInformer := cache.NewSharedIndexInformer(
			namespaceWatcher,
			&corev1.Namespace{},
			wakeupInterval,
			cache.Indexers{},
		)
		namespaceSharedIndexInformers = append(namespaceSharedIndexInformers, namespaceInformer)
	}

	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}
	annotations := map[string]string{
		ALMManagedAnnotationKey: fmt.Sprintf("%s.%s", podNamespace, podName),
	}
	namespaceAnnotator := annotator.NewAnnotator(queueOperator.OpClient, annotations)
	if err := namespaceAnnotator.AnnotateNamespaces(namespaces); err != nil {
		return nil, err
	}

	op := &ALMOperator{
		queueOperator,
		csvClient,
		&install.StrategyResolver{},
		namespaceAnnotator,
	}
	csvQueueInformers := queueinformer.New(
		"clusterserviceversions",
		csvSharedIndexInformers,
		op.syncClusterServiceVersion,
		nil,
	)
	namespaceInformers := queueinformer.New(
		"namespaces",
		namespaceSharedIndexInformers,
		op.annotateNamespace,
		nil,
	)
	queueInformers := append(csvQueueInformers, namespaceInformers...)
	for _, opVerQueueInformer := range queueInformers {
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
		met, statuses := a.requirementStatus(csv)

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
		strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
		if err != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err))
			return
		}
		installer := a.resolver.InstallerForStrategy(strategy.GetStrategyName(), a.OpClient, csv.ObjectMeta)
		installed, err := installer.CheckInstalled(strategy)
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
		syncError = installer.Install(strategy)
		if syncError != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", err))
			return
		} else {
			csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors")
			return
		}
	case v1alpha1.CSVPhaseSucceeded:
		strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
		if err != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err))
			return
		}
		installer := a.resolver.InstallerForStrategy(strategy.GetStrategyName(), a.OpClient, csv.ObjectMeta)
		installed, err := installer.CheckInstalled(strategy)

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

func (a *ALMOperator) requirementStatus(csv *v1alpha1.ClusterServiceVersion) (met bool, statuses []v1alpha1.RequirementStatus) {
	met = true
	for _, r := range csv.GetAllCRDDescriptions() {
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

// annotateNamespace is the method that gets called when we see a namespace event in the cluster
func (a *ALMOperator) annotateNamespace(obj interface{}) (syncError error) {
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		log.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting Namespace failed")
	}

	log.Infof("syncing Namespace: %s", namespace.GetName())
	if err := a.annotator.AnnotateNamespace(namespace); err != nil {
		log.Infof("error annotating namespace '%s'", namespace.GetName())
		return err
	}
	return nil
}
