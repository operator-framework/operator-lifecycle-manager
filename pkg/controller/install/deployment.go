package install

import (
	"fmt"
	"hash/fnv"

	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/utils/pointer"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm/overrides/inject"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const DeploymentSpecHashLabelKey = "olm.deployment-spec-hash"

type StrategyDeploymentInstaller struct {
	strategyClient         wrappers.InstallStrategyDeploymentInterface
	owner                  ownerutil.Owner
	previousStrategy       Strategy
	templateAnnotations    map[string]string
	initializers           DeploymentInitializerFuncChain
	apiServiceDescriptions []certResource
	webhookDescriptions    []certResource
}

var _ Strategy = &v1alpha1.StrategyDetailsDeployment{}
var _ StrategyInstaller = &StrategyDeploymentInstaller{}

// DeploymentInitializerFunc takes a deployment object and appropriately
// initializes it for install.
//
// Before a deployment is created on the cluster, we can run a series of
// overrides functions that will properly initialize the deployment object.
type DeploymentInitializerFunc func(deployment *appsv1.Deployment) error

// DeploymentInitializerFuncChain defines a chain of DeploymentInitializerFunc.
type DeploymentInitializerFuncChain []DeploymentInitializerFunc

// Apply runs series of overrides functions that will properly initialize
// the deployment object.
func (c DeploymentInitializerFuncChain) Apply(deployment *appsv1.Deployment) (err error) {
	for _, initializer := range c {
		if initializer == nil {
			continue
		}

		if initializationErr := initializer(deployment); initializationErr != nil {
			err = initializationErr
			break
		}
	}

	return
}

// DeploymentInitializerBuilderFunc returns a DeploymentInitializerFunc based on
// the given context.
type DeploymentInitializerBuilderFunc func(owner ownerutil.Owner) DeploymentInitializerFunc

func NewStrategyDeploymentInstaller(strategyClient wrappers.InstallStrategyDeploymentInterface, templateAnnotations map[string]string, owner ownerutil.Owner, previousStrategy Strategy, initializers DeploymentInitializerFuncChain, apiServiceDescriptions []v1alpha1.APIServiceDescription, webhookDescriptions []v1alpha1.WebhookDescription) StrategyInstaller {
	apiDescs := make([]certResource, len(apiServiceDescriptions))
	for i := range apiServiceDescriptions {
		apiDescs[i] = &apiServiceDescriptionsWithCAPEM{apiServiceDescriptions[i], []byte{}}
	}

	webhookDescs := make([]certResource, len(webhookDescriptions))
	for i := range webhookDescs {
		webhookDescs[i] = &webhookDescriptionWithCAPEM{webhookDescriptions[i], []byte{}}
	}

	return &StrategyDeploymentInstaller{
		strategyClient:         strategyClient,
		owner:                  owner,
		previousStrategy:       previousStrategy,
		templateAnnotations:    templateAnnotations,
		initializers:           initializers,
		apiServiceDescriptions: apiDescs,
		webhookDescriptions:    webhookDescs,
	}
}

func (i *StrategyDeploymentInstaller) installDeployments(deps []v1alpha1.StrategyDeploymentSpec) error {
	for _, d := range deps {
		deployment, _, err := i.deploymentForSpec(d.Name, d.Spec, d.Label)
		if err != nil {
			return err
		}

		if _, err := i.strategyClient.CreateOrUpdateDeployment(deployment); err != nil {
			return err
		}

		if err := i.createOrUpdateCertResourcesForDeployment(); err != nil {
			return err
		}
	}
	return nil
}

func (i *StrategyDeploymentInstaller) createOrUpdateCertResourcesForDeployment() error {
	for _, desc := range i.getCertResources() {
		switch d := desc.(type) {
		case *apiServiceDescriptionsWithCAPEM:
			err := i.createOrUpdateAPIService(d.caPEM, d.apiServiceDescription)
			if err != nil {
				return err
			}

			// Cleanup legacy APIService resources
			err = i.deleteLegacyAPIServiceResources(*d)
			if err != nil {
				return err
			}
		case *webhookDescriptionWithCAPEM:
			err := i.createOrUpdateWebhook(d.caPEM, d.webhookDescription)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported CA Resource")
		}
	}
	return nil
}

func (i *StrategyDeploymentInstaller) deploymentForSpec(name string, spec appsv1.DeploymentSpec, specLabels k8slabels.Set) (deployment *appsv1.Deployment, hash string, err error) {
	dep := &appsv1.Deployment{Spec: spec}
	dep.SetName(name)
	dep.SetNamespace(i.owner.GetNamespace())

	// Merge annotations (to avoid losing info from pod template)
	annotations := map[string]string{}
	for k, v := range dep.Spec.Template.GetAnnotations() {
		annotations[k] = v
	}
	for k, v := range i.templateAnnotations {
		annotations[k] = v
	}
	dep.Spec.Template.SetAnnotations(annotations)

	// Set custom labels before CSV owner labels
	dep.SetLabels(specLabels)

	ownerutil.AddNonBlockingOwner(dep, i.owner)
	ownerutil.AddOwnerLabelsForKind(dep, i.owner, v1alpha1.ClusterServiceVersionKind)

	if applyErr := i.initializers.Apply(dep); applyErr != nil {
		err = applyErr
		return
	}

	podSpec := &dep.Spec.Template.Spec
	if injectErr := inject.InjectEnvIntoDeployment(podSpec, []corev1.EnvVar{{
		Name:  "OPERATOR_CONDITION_NAME",
		Value: i.owner.GetName(),
	}}); injectErr != nil {
		err = injectErr
		return
	}

	// OLM does not support Rollbacks.
	// By default, each deployment created by OLM could spawn up to 10 replicaSets.
	// By setting the deployments revisionHistoryLimit to 1, OLM will only create up
	// to 2 ReplicaSets per deployment it manages, saving memory.
	dep.Spec.RevisionHistoryLimit = pointer.Int32Ptr(1)

	hash = HashDeploymentSpec(dep.Spec)
	dep.Labels[DeploymentSpecHashLabelKey] = hash

	deployment = dep
	return
}

func (i *StrategyDeploymentInstaller) cleanupPrevious(current *v1alpha1.StrategyDetailsDeployment, previous *v1alpha1.StrategyDetailsDeployment) error {
	previousDeploymentsMap := map[string]struct{}{}
	for _, d := range previous.DeploymentSpecs {
		previousDeploymentsMap[d.Name] = struct{}{}
	}
	for _, d := range current.DeploymentSpecs {
		delete(previousDeploymentsMap, d.Name)
	}
	log.Debugf("preparing to cleanup: %s", previousDeploymentsMap)
	// delete deployments in old strategy but not new
	var err error = nil
	for name := range previousDeploymentsMap {
		err = i.strategyClient.DeleteDeployment(name)
	}
	return err
}

func (i *StrategyDeploymentInstaller) Install(s Strategy) error {
	strategy, ok := s.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return fmt.Errorf("attempted to install %s strategy with deployment installer", strategy.GetStrategyName())
	}

	// Install owned APIServices and update strategy with serving cert data
	updatedStrategy, err := i.installCertRequirements(strategy)
	if err != nil {
		return err
	}

	if err := i.installDeployments(updatedStrategy.DeploymentSpecs); err != nil {
		if k8serrors.IsForbidden(err) {
			return StrategyError{Reason: StrategyErrInsufficientPermissions, Message: fmt.Sprintf("install strategy failed: %s", err)}
		}
		return err
	}

	// Clean up orphaned deployments
	return i.cleanupOrphanedDeployments(updatedStrategy.DeploymentSpecs)
}

// CheckInstalled can return nil (installed), or errors
// Errors can indicate: some component missing (keep installing), unable to query (check again later), or unrecoverable (failed in a way we know we can't recover from)
func (i *StrategyDeploymentInstaller) CheckInstalled(s Strategy) (installed bool, err error) {
	strategy, ok := s.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return false, StrategyError{Reason: StrategyErrReasonInvalidStrategy, Message: fmt.Sprintf("attempted to check %s strategy with deployment installer", strategy.GetStrategyName())}
	}

	// Check deployments
	if err := i.checkForDeployments(strategy.DeploymentSpecs); err != nil {
		return false, err
	}
	return true, nil
}

func (i *StrategyDeploymentInstaller) checkForDeployments(deploymentSpecs []v1alpha1.StrategyDeploymentSpec) error {
	var depNames []string
	for _, dep := range deploymentSpecs {
		depNames = append(depNames, dep.Name)
	}

	// Check the owner is a CSV
	csv, ok := i.owner.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("owner %s is not a CSV", i.owner.GetName())}
	}

	existingDeployments, err := i.strategyClient.FindAnyDeploymentsMatchingLabels(ownerutil.CSVOwnerSelector(csv))
	if err != nil {
		return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("error querying existing deployments for CSV %s: %s", csv.GetName(), err)}
	}

	// compare deployments to see if any need to be created/updated
	existingMap := map[string]*appsv1.Deployment{}
	for _, d := range existingDeployments {
		existingMap[d.GetName()] = d
	}
	for _, spec := range deploymentSpecs {
		dep, exists := existingMap[spec.Name]
		if !exists {
			log.Debugf("missing deployment with name=%s", spec.Name)
			return StrategyError{Reason: StrategyErrReasonComponentMissing, Message: fmt.Sprintf("missing deployment with name=%s", spec.Name)}
		}
		reason, ready, err := DeploymentStatus(dep)
		if err != nil {
			log.Debugf("deployment %s not ready before timeout: %s", dep.Name, err.Error())
			return StrategyError{Reason: StrategyErrReasonTimeout, Message: fmt.Sprintf("deployment %s not ready before timeout: %s", dep.Name, err.Error())}
		}
		if !ready {
			return StrategyError{Reason: StrategyErrReasonWaiting, Message: fmt.Sprintf("waiting for deployment %s to become ready: %s", dep.Name, reason)}
		}

		// check annotations
		if len(i.templateAnnotations) > 0 && dep.Spec.Template.Annotations == nil {
			return StrategyError{Reason: StrategyErrReasonAnnotationsMissing, Message: fmt.Sprintf("no annotations found on deployment")}
		}
		for key, value := range i.templateAnnotations {
			if actualValue, ok := dep.Spec.Template.Annotations[key]; !ok {
				return StrategyError{Reason: StrategyErrReasonAnnotationsMissing, Message: fmt.Sprintf("annotations on deployment does not contain expected key: %s", key)}
			} else if dep.Spec.Template.Annotations[key] != value {
				return StrategyError{Reason: StrategyErrReasonAnnotationsMissing, Message: fmt.Sprintf("unexpected annotation on deployment. Expected %s:%s, found %s:%s", key, value, key, actualValue)}
			}
		}

		// check that the deployment spec hasn't changed since it was created
		labels := dep.GetLabels()
		if len(labels) == 0 {
			return StrategyError{Reason: StrategyErrDeploymentUpdated, Message: fmt.Sprintf("deployment doesn't have a spec hash, update it")}
		}
		existingDeploymentSpecHash, ok := labels[DeploymentSpecHashLabelKey]
		if !ok {
			return StrategyError{Reason: StrategyErrDeploymentUpdated, Message: fmt.Sprintf("deployment doesn't have a spec hash, update it")}
		}

		_, calculatedDeploymentHash, err := i.deploymentForSpec(spec.Name, spec.Spec, labels)
		if err != nil {
			return StrategyError{Reason: StrategyErrDeploymentUpdated, Message: fmt.Sprintf("couldn't calculate deployment spec hash: %v", err)}
		}

		if existingDeploymentSpecHash != calculatedDeploymentHash {
			return StrategyError{Reason: StrategyErrDeploymentUpdated, Message: fmt.Sprintf("deployment changed old hash=%s, new hash=%s", existingDeploymentSpecHash, calculatedDeploymentHash)}
		}
	}
	return nil
}

// Clean up orphaned deployments after reinstalling deployments process
func (i *StrategyDeploymentInstaller) cleanupOrphanedDeployments(deploymentSpecs []v1alpha1.StrategyDeploymentSpec) error {
	// Map of deployments
	depNames := map[string]string{}
	for _, dep := range deploymentSpecs {
		depNames[dep.Name] = dep.Name
	}

	// Check the owner is a CSV
	csv, ok := i.owner.(*v1alpha1.ClusterServiceVersion)
	if !ok {
		return fmt.Errorf("owner %s is not a CSV", i.owner.GetName())
	}

	// Get existing deployments in CSV's namespace and owned by CSV
	existingDeployments, err := i.strategyClient.FindAnyDeploymentsMatchingLabels(ownerutil.CSVOwnerSelector(csv))
	if err != nil {
		return err
	}

	// compare existing deployments to deployments in CSV's spec to see if any need to be deleted
	for _, d := range existingDeployments {
		if _, exists := depNames[d.GetName()]; !exists {
			if ownerutil.IsOwnedBy(d, i.owner) {
				log.Infof("found an orphaned deployment %s in namespace %s", d.GetName(), i.owner.GetNamespace())
				if err := i.strategyClient.DeleteDeployment(d.GetName()); err != nil {
					log.Warnf("error cleaning up deployment %s", d.GetName())
					return err
				}
			}
		}
	}

	return nil
}

// HashDeploymentSpec calculates a hash given a copy of the deployment spec from a CSV, stripping any
// operatorgroup annotations.
func HashDeploymentSpec(spec appsv1.DeploymentSpec) string {
	hasher := fnv.New32a()
	hashutil.DeepHashObject(hasher, &spec)
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}
