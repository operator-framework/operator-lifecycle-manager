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
	"github.com/coreos-inc/alm/pkg/apis"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/client"
	"github.com/coreos-inc/alm/pkg/install"
	"github.com/coreos-inc/alm/pkg/queueinformer"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
)

var ErrRequirementsNotMet = errors.New("requirements were not met")

const (
	FallbackWakeupInterval = 30 * time.Second
)

type ALMOperator struct {
	*queueinformer.Operator
	csvClient client.ClusterServiceVersionInterface
	resolver  install.StrategyResolverInterface
	annotator *annotator.Annotator
}

func NewALMOperator(kubeconfig string, wakeupInterval time.Duration, annotations map[string]string, namespaces []string) (*ALMOperator, error) {
	if wakeupInterval < 0 {
		wakeupInterval = FallbackWakeupInterval
	}
	if len(namespaces) < 1 {
		namespaces = []string{metav1.NamespaceAll}
	}
	csvClient, err := client.NewClusterServiceVersionClient(kubeconfig)
	if err != nil {
		return nil, err
	}
	queueOperator, err := queueinformer.NewOperator(kubeconfig)
	if err != nil {
		return nil, err
	}
	namespaceAnnotator := annotator.NewAnnotator(queueOperator.OpClient, annotations)

	op := &ALMOperator{
		queueOperator,
		csvClient,
		&install.StrategyResolver{},
		namespaceAnnotator,
	}

	// if watching all namespaces, set up a watch to annotate new namespaces
	if len(namespaces) == 1 && namespaces[0] == metav1.NamespaceAll {
		log.Debug("watching all namespaces, setting up queue")
		namespaceWatcher := cache.NewListWatchFromClient(
			queueOperator.OpClient.KubernetesInterface().CoreV1().RESTClient(),
			"namespaces",
			metav1.NamespaceAll,
			fields.Everything(),
		)
		namespaceInformer := cache.NewSharedIndexInformer(
			namespaceWatcher,
			&corev1.Namespace{},
			wakeupInterval,
			cache.Indexers{},
		)
		queueInformer := queueinformer.NewInformer(
			"namespaces",
			namespaceInformer,
			op.annotateNamespace,
			nil,
		)
		op.RegisterQueueInformer(queueInformer)
	}

	// annotate namespaces that ALM operator manages
	if err := namespaceAnnotator.AnnotateNamespaces(namespaces); err != nil {
		return nil, err
	}

	// set up watch on CSVs
	for _, namespace := range namespaces {
		log.Debugf("watching for CSVs in namespace %s", namespace)
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
		queueInformer := queueinformer.NewInformer(
			"clusterserviceversions",
			csvInformer,
			op.syncClusterServiceVersion,
			nil,
		)
		op.RegisterQueueInformer(queueInformer)
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

	// check if we should transition to Replacing first, because it short-circuits all other state transitions
	if csv.Status.Phase != v1alpha1.CSVPhaseReplacing && csv.Status.Phase != v1alpha1.CSVPhaseDeleting {
		if replacement := a.isBeingReplaced(csv); replacement != nil {
			log.Infof("newer ClusterServiceVersion replacing %s, no-op", csv.SelfLink)
			msg := fmt.Sprintf("being replaced by csv: %s", replacement.SelfLink)
			csv.SetPhase(v1alpha1.CSVPhaseReplacing, v1alpha1.CSVReasonBeingReplaced, msg)
			return
		}
	}

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
		csv.SetPhase(v1alpha1.CSVPhaseInstallReady, v1alpha1.CSVReasonRequirementsMet, "all requirements found, attempting install")
		csv.SetRequirementStatus(statuses)
	case v1alpha1.CSVPhaseInstallReady:
		strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
		if err != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err))
			return
		}

		previousCSV := a.isReplacing(csv)
		var previousStrategy install.Strategy
		if previousCSV != nil {
			previousStrategy, err = a.resolver.UnmarshalStrategy(previousCSV.Spec.InstallStrategy)
			if err != nil {
				previousStrategy = nil
			}
		}

		strName := strategy.GetStrategyName()
		installer := a.resolver.InstallerForStrategy(strName, a.OpClient, csv.ObjectMeta, previousStrategy)
		syncError = installer.Install(strategy)
		if syncError != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed, fmt.Sprintf("install strategy failed: %s", syncError))
			return
		} else {
			csv.SetPhase(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonInstallSuccessful, "waiting for install components to report healthy")
			// TODO: setting an error requeus the object, won't be necessary if we can update based on the deployment events directly
			syncError = fmt.Errorf("installing, requeue for another check")
			return
		}
	case v1alpha1.CSVPhaseInstalling:
		strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
		if err != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err))
			return
		}

		previousCSV := a.isReplacing(csv)
		var previousStrategy install.Strategy
		if previousCSV != nil {
			previousStrategy, err = a.resolver.UnmarshalStrategy(previousCSV.Spec.InstallStrategy)
			if err != nil {
				previousStrategy = nil
			}
		}

		strName := strategy.GetStrategyName()
		installer := a.resolver.InstallerForStrategy(strName, a.OpClient, csv.ObjectMeta, previousStrategy)

		strategyErr := installer.CheckInstalled(strategy)

		// installcheck determined we can't progress (e.g. deployment failed to come up in time)
		if install.IsErrorUnrecoverable(strategyErr) {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install failed: %s", err))
			return
		}

		// if there's an error checking install that shouldn't fail the strategy requeue with message
		if strategyErr != nil {
			csv.SetPhase(v1alpha1.CSVPhaseInstalling, v1alpha1.CSVReasonInstallSuccessful, fmt.Sprintf("waiting for install to complete: %s", err))
			// TODO: setting an error requeues the object, won't be necessary if we can update based on the deployment events directly
			syncError = fmt.Errorf("installing, requeue for another check: %s", err.Error())
			syncError = fmt.Errorf("installing, requeue for another check: %s", strategyErr.Error())
			return
		}

		log.Infof(
			"%s install strategy successful for %s",
			csv.Spec.InstallStrategy.StrategyName,
			csv.SelfLink,
		)
		csv.SetPhase(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful, "install strategy completed with no errors")
		return
	case v1alpha1.CSVPhaseSucceeded:
		strategy, err := a.resolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
		if err != nil {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInvalidStrategy, fmt.Sprintf("install strategy invalid: %s", err))
			return
		}

		previousCSV := a.isReplacing(csv)
		var previousStrategy install.Strategy
		if previousCSV != nil {
			previousStrategy, err = a.resolver.UnmarshalStrategy(previousCSV.Spec.InstallStrategy)
			if err != nil {
				previousStrategy = nil
			}
		}
		strName := strategy.GetStrategyName()
		installer := a.resolver.InstallerForStrategy(strName, a.OpClient, csv.ObjectMeta, previousStrategy)
		strategyErr := installer.CheckInstalled(strategy)

		// installcheck determined we can't progress (e.g. deployment failed to come up in time)
		if install.IsErrorUnrecoverable(strategyErr) {
			csv.SetPhase(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonInstallCheckFailed, fmt.Sprintf("install failed: %s", err))
			return
		}
		// transition back to pending
		if strategyErr != nil {
			csv.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonComponentUnhealthy, "component unhealthy, rechecking and re-installing")
			return
		}
	case v1alpha1.CSVPhaseReplacing:
		// if we're the oldest version being replaced (not replacing anything)
		// and there exists a newer version in a replacement chain that installed successfully
		// then we're safe to delete

		// if there is a previous csv still in the cluster, not safe to delete csv yet
		if prev := a.isReplacing(csv); prev != nil {
			log.Debugf("%s is being replaced, but is not a leaf so we don't replace it for now.", csv.GetName())
			return
		}

		// if we can find a newer version that's successfully installed, we're safe to delete
		current := csv
		next := a.isBeingReplaced(current)
		for next != nil {
			log.Debugf("checking to see if %s is running so we can delete %s", next.GetName(), csv.GetName())
			nextStrategy, err := a.resolver.UnmarshalStrategy(next.Spec.InstallStrategy)
			if err != nil {
				log.Debugf("couldn't unmarshal strategy for %s", next.GetName())
				continue
			}

			currentStrategy, err := a.resolver.UnmarshalStrategy(current.Spec.InstallStrategy)
			if err != nil {
				log.Debugf("couldn't unmarshal strategy for %s", current.GetName())
				currentStrategy = nil
			}

			installer := a.resolver.InstallerForStrategy(nextStrategy.GetStrategyName(), a.OpClient, csv.ObjectMeta, currentStrategy)
			strategyErr := installer.CheckInstalled(nextStrategy)

			// found newer, installed CSV - safe to delete the current csv
			if strategyErr == nil {
				csv.SetPhase(v1alpha1.CSVPhaseDeleting, v1alpha1.CSVReasonReplaced, "has been replaced by a newer ClusterServiceVersion that has successfully installed.")
				return
			}

			log.Debugf("install strategy for %s is in an error state: %s, checking next", next.GetName(), err.Error())
			current = next
			next = a.isBeingReplaced(current)
		}
	case v1alpha1.CSVPhaseDeleting:
		syncError := a.OpClient.DeleteCustomResource(apis.GroupName, v1alpha1.GroupVersion, csv.GetNamespace(), v1alpha1.ClusterServiceVersionKind, csv.GetName())
		if syncError != nil {
			log.Debugf("unable to get delete csv marked for deletion: %s", syncError.Error())
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
		crd, err := a.OpClient.GetCustomResourceDefinition(r.Name)
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

func (a *ALMOperator) isBeingReplaced(in *v1alpha1.ClusterServiceVersion) (replacedBy *v1alpha1.ClusterServiceVersion) {
	csvsInNamespace, err := a.OpClient.ListCustomResource(apis.GroupName, v1alpha1.GroupVersion, in.GetNamespace(), v1alpha1.ClusterServiceVersionKind)
	if err != nil {
		return nil
	}
	unstructuredConverter := conversion.NewConverter(true)

	for _, csvUnst := range csvsInNamespace.Items {
		csv := v1alpha1.ClusterServiceVersion{}
		if err := unstructuredConverter.FromUnstructured(csvUnst.UnstructuredContent(), &csv); err != nil {
			continue
		}
		if csv.Spec.Replaces == in.GetName() {
			return &csv
		}
	}
	return nil
}

func (a *ALMOperator) isReplacing(in *v1alpha1.ClusterServiceVersion) (previous *v1alpha1.ClusterServiceVersion) {
	log.Debugf("checking if csv is replacing an older version")
	if in.Spec.Replaces == "" {
		return nil
	}
	oldCSVUnst, err := a.OpClient.GetCustomResource(apis.GroupName, v1alpha1.GroupVersion, in.GetNamespace(), v1alpha1.ClusterServiceVersionKind, in.Spec.Replaces)
	if err != nil {
		log.Debugf("unable to get previous csv: %s", err.Error())
		return nil
	}
	unstructuredConverter := conversion.NewConverter(true)
	p := v1alpha1.ClusterServiceVersion{}
	if err := unstructuredConverter.FromUnstructured(oldCSVUnst.UnstructuredContent(), &p); err != nil {
		log.Debugf("unable to parse previous csv: %s", err.Error())
		return nil
	}
	return &p
}
