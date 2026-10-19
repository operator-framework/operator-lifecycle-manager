package olm

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// A CSV that fails because of its OperatorGroup loses what OLM created for it:
// the Deployments and webhook configurations of the installer, and the RBAC
// steps of its InstallPlan. A step whose object the cleanup found, verified
// as owned by the CSV and deleted is marked NotPresent in the InstallPlan
// status while the plan stays Complete. When the CSV is Pending again, a
// plan with NotPresent steps goes back to Installing and the catalog-operator
// creates those objects again. The InstallPlan status is the only record of
// the cleanup, and only the controllers write it.
//
// InstallPlan execution is at least once: a catalog-operator sync that works
// from a stale Installing copy of a plan that is already Complete creates the
// objects again. The cleanup therefore deletes on every sync of the failed
// CSV, marks or not, and never skips a step because it is already marked.

// reinstalledStepKinds are the InstallPlan steps the cleanup deletes and the
// recovery creates again. ServiceAccounts are kept: the OperatorGroup scoped
// ServiceAccount can be one of them and the catalog-operator needs it to
// execute the plan.
var reinstalledStepKinds = []string{"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"}

// installPlanRecheckDelay is how long cleanupFailedCSV waits before it looks
// at an InstallPlan that is still running.
const installPlanRecheckDelay = 10 * time.Second

// cleanupFailedCSV removes what OLM created for a CSV that failed because of
// its OperatorGroup. Deployments and webhook configurations go always. The
// objects of the InstallPlan steps go when the plan is Complete and its
// manifests can be applied again, otherwise nothing could put them back.
// Errors are returned so the sync is retried.
func (a *Operator) cleanupFailedCSV(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) error {
	var errs []error
	if err := a.cleanupCSVDeployments(logger, csv); err != nil {
		errs = append(errs, err)
	}
	if err := a.deleteOwnedWebhooks(logger, csv); err != nil {
		errs = append(errs, err)
	}

	plan, err := a.installPlanFor(csv)
	switch {
	case err != nil:
		errs = append(errs, err)
	case plan == nil:
		logger.Debug("no InstallPlan for the CSV, keeping its RBAC")
	case plan.Status.Phase == v1alpha1.InstallPlanPhaseComplete:
		if err := a.deletePlanSteps(logger, csv, plan); err != nil {
			errs = append(errs, err)
		}
	case plan.Status.Phase == v1alpha1.InstallPlanPhaseInstalling:
		// The InstallPlan is still creating resources. Look again once it is done.
		logger.WithField("installplan", plan.GetName()).Debug("InstallPlan of the CSV still running, checking again later")
		if err := a.csvQueueSet.RequeueAfter(csv.GetNamespace(), csv.GetName(), installPlanRecheckDelay); err != nil {
			logger.WithError(err).Warn("unable to requeue")
		}
	default:
		// Failed, RequiresApproval or not started: nothing will create the RBAC again.
		logger.WithFields(logrus.Fields{"installplan": plan.GetName(), "planPhase": plan.Status.Phase}).Debug("InstallPlan of the CSV cannot run, keeping its RBAC")
	}
	return utilerrors.NewAggregate(errs)
}

// installPlanFor returns the newest InstallPlan that installed the CSV, or
// nil when there is none. A plan installed the CSV when its status, which
// only the catalog-operator writes, holds the step that created the CSV.
func (a *Operator) installPlanFor(csv *v1alpha1.ClusterServiceVersion) (*v1alpha1.InstallPlan, error) {
	plans, err := a.client.OperatorsV1alpha1().InstallPlans(csv.GetNamespace()).List(a.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var newest *v1alpha1.InstallPlan
	for i := range plans.Items {
		plan := &plans.Items[i]
		if !planInstalledCSV(plan, csv) {
			continue
		}
		if newest == nil || newest.CreationTimestamp.Before(&plan.CreationTimestamp) {
			newest = plan
		}
	}
	return newest, nil
}

func planInstalledCSV(plan *v1alpha1.InstallPlan, csv *v1alpha1.ClusterServiceVersion) bool {
	for _, step := range plan.Status.Plan {
		if step.Resolving == csv.GetName() && step.Resource.Kind == v1alpha1.ClusterServiceVersionKind {
			return true
		}
	}
	return false
}

// csvSteps returns the RBAC steps of the plan that belong to the CSV.
func csvSteps(plan *v1alpha1.InstallPlan, csv *v1alpha1.ClusterServiceVersion) []*v1alpha1.Step {
	var steps []*v1alpha1.Step
	for _, step := range plan.Status.Plan {
		if step.Resolving == csv.GetName() && slices.Contains(reinstalledStepKinds, step.Resource.Kind) {
			steps = append(steps, step)
		}
	}
	return steps
}

// deletePlanSteps deletes the objects of the CSV's RBAC steps that exist and
// that the CSV owns, and marks those steps NotPresent. The marks are written
// before the objects are deleted: a failed delete after the mark is harmless,
// the reopened plan finds the object and marks it Present again, while a
// delete without a mark would leave nothing to recover from. An object that
// is already gone, or that another owner holds, is left alone and its step
// keeps its status. The plan stays Complete. Nothing is deleted when a step
// manifest of the CSV cannot be applied again.
func (a *Operator) deletePlanSteps(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion, plan *v1alpha1.InstallPlan) error {
	logger = logger.WithField("installplan", plan.GetName())

	// ExecutePlan resolves the manifests of every step of the plan, also the
	// steps of other CSVs and the steps that are already Present.
	resolvable, err := a.stepManifestsResolvable(plan.Status.Plan)
	if err != nil {
		return err
	}
	if !resolvable {
		logger.Debug("InstallPlan manifests cannot be applied again, keeping the RBAC of the CSV")
		return nil
	}

	type victim struct {
		step *v1alpha1.Step
		uid  types.UID
	}
	var victims []victim
	var errs []error
	changed := false
	for _, step := range csvSteps(plan, csv) {
		object, err := a.getStepObject(plan.GetNamespace(), step)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ownedByCSV(object, csv) {
			logger.WithFields(logrus.Fields{"kind": step.Resource.Kind, "name": step.Resource.Name}).Debug("InstallPlan step object is not owned by this CSV, keeping it")
			// Someone else holds that name now. Drop a mark from an earlier
			// cleanup so the reopened plan does not overwrite their object.
			if step.Status == v1alpha1.StepStatusNotPresent {
				step.Status = v1alpha1.StepStatusPresent
				changed = true
			}
			continue
		}
		victims = append(victims, victim{step: step, uid: object.GetUID()})
		if step.Status != v1alpha1.StepStatusNotPresent {
			step.Status = v1alpha1.StepStatusNotPresent
			changed = true
		}
	}
	if changed {
		if _, err := a.client.OperatorsV1alpha1().InstallPlans(plan.GetNamespace()).UpdateStatus(a.ctx, plan, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
			return utilerrors.NewAggregate(errs)
		}
	}
	unconfirmed := false
	for _, v := range victims {
		stepLogger := logger.WithFields(logrus.Fields{"kind": v.step.Resource.Kind, "name": v.step.Resource.Name})
		deleted, err := a.deleteStepObject(plan.GetNamespace(), v.step, v.uid)
		if err != nil {
			stepLogger.WithError(err).Warn("error cleaning up InstallPlan step object")
			errs = append(errs, err)
			continue
		}
		if !deleted {
			// Someone else deleted the object between the read and the delete.
			// That was their decision, drop the mark so the plan does not undo it.
			stepLogger.Info("InstallPlan step object was deleted by someone else, not marked for reinstall")
			v.step.Status = v1alpha1.StepStatusPresent
			unconfirmed = true
			continue
		}
		stepLogger.Info("cleaned up InstallPlan step object")
	}
	if unconfirmed {
		if _, err := a.client.OperatorsV1alpha1().InstallPlans(plan.GetNamespace()).UpdateStatus(a.ctx, plan, metav1.UpdateOptions{}); err != nil {
			errs = append(errs, err)
		}
	}
	return utilerrors.NewAggregate(errs)
}

// ownedByCSV reports whether the object carries the CSV owner labels or an
// owner reference to the CSV.
func ownedByCSV(object metav1.Object, csv *v1alpha1.ClusterServiceVersion) bool {
	labels := object.GetLabels()
	if labels[ownerutil.OwnerKey] == csv.GetName() {
		namespace, ok := labels[ownerutil.OwnerNamespaceKey]
		if !ok || namespace == csv.GetNamespace() {
			return true
		}
	}
	return ownerutil.IsOwnedBy(object, csv)
}

// getStepObject reads the object of an RBAC step.
func (a *Operator) getStepObject(namespace string, step *v1alpha1.Step) (metav1.Object, error) {
	kube := a.opClient.KubernetesInterface().RbacV1()
	name := step.Resource.Name
	options := metav1.GetOptions{}
	switch step.Resource.Kind {
	case "Role":
		return kube.Roles(namespace).Get(a.ctx, name, options)
	case "RoleBinding":
		return kube.RoleBindings(namespace).Get(a.ctx, name, options)
	case "ClusterRole":
		return kube.ClusterRoles().Get(a.ctx, name, options)
	case "ClusterRoleBinding":
		return kube.ClusterRoleBindings().Get(a.ctx, name, options)
	}
	return nil, fmt.Errorf("step kind %s is not deleted on failure", step.Resource.Kind)
}

// unpackedBundleReference is the manifest of a step whose real manifest
// lives in the ConfigMap of an unpacked bundle. It mirrors the catalog
// package type of the same name.
type unpackedBundleReference struct {
	Kind                   string `json:"kind"`
	Name                   string `json:"name"`
	Namespace              string `json:"namespace"`
	CatalogSourceName      string `json:"catalogSourceName"`
	CatalogSourceNamespace string `json:"catalogSourceNamespace"`
}

// stepManifestsResolvable reports whether the catalog-operator can apply
// every step again: the manifest is inline, or it refers to an unpacked
// bundle ConfigMap that still exists.
func (a *Operator) stepManifestsResolvable(steps []*v1alpha1.Step) (bool, error) {
	for _, step := range steps {
		if step.Resource.Manifest == "" {
			return false, nil
		}
		var ref unpackedBundleReference
		if err := json.Unmarshal([]byte(step.Resource.Manifest), &ref); err != nil {
			continue
		}
		if ref.Kind != "ConfigMap" || ref.Name == "" || ref.Namespace == "" || ref.CatalogSourceName == "" || ref.CatalogSourceNamespace == "" {
			continue
		}
		if _, err := a.opClient.KubernetesInterface().CoreV1().ConfigMaps(ref.Namespace).Get(a.ctx, ref.Name, metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

// deleteStepObject deletes the object of an RBAC step, and only the object
// with the given UID. It reports whether this call deleted the object: a
// missing object is not an error but was not deleted by us. A UID conflict
// means the object was replaced between the read and the delete, the error
// is returned so the next sync reads the replacement and fixes the mark.
func (a *Operator) deleteStepObject(namespace string, step *v1alpha1.Step, uid types.UID) (bool, error) {
	kube := a.opClient.KubernetesInterface().RbacV1()
	name := step.Resource.Name
	options := metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}
	var err error
	switch step.Resource.Kind {
	case "Role":
		err = kube.Roles(namespace).Delete(a.ctx, name, options)
	case "RoleBinding":
		err = kube.RoleBindings(namespace).Delete(a.ctx, name, options)
	case "ClusterRole":
		err = kube.ClusterRoles().Delete(a.ctx, name, options)
	case "ClusterRoleBinding":
		err = kube.ClusterRoleBindings().Delete(a.ctx, name, options)
	default:
		return false, fmt.Errorf("step kind %s is not deleted on failure", step.Resource.Kind)
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// deleteOwnedWebhooks deletes the webhook configurations the installer
// created for the CSV. A webhook that points at a deleted Deployment would
// block the API calls it covers. The installer creates them again when the
// CSV is installed again.
func (a *Operator) deleteOwnedWebhooks(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) error {
	admission := a.opClient.KubernetesInterface().AdmissionregistrationV1()
	listOptions := metav1.ListOptions{LabelSelector: ownerutil.CSVOwnerSelector(csv).String()}
	var errs []error

	validating, err := admission.ValidatingWebhookConfigurations().List(a.ctx, listOptions)
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, webhook := range validating.Items {
			uid := webhook.GetUID()
			if err := admission.ValidatingWebhookConfigurations().Delete(a.ctx, webhook.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, err)
				continue
			}
			logger.WithField("validatingwebhookconfiguration", webhook.GetName()).Info("cleaned up CSV webhook")
		}
	}
	mutating, err := admission.MutatingWebhookConfigurations().List(a.ctx, listOptions)
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, webhook := range mutating.Items {
			uid := webhook.GetUID()
			if err := admission.MutatingWebhookConfigurations().Delete(a.ctx, webhook.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, err)
				continue
			}
			logger.WithField("mutatingwebhookconfiguration", webhook.GetName()).Info("cleaned up CSV webhook")
		}
	}
	return utilerrors.NewAggregate(errs)
}

// reinstallPlanSteps makes the catalog-operator create again what
// deletePlanSteps removed. When the CSV's Complete InstallPlan has steps that
// are NotPresent, the plan goes back to Installing with a fresh start time.
// The catalog-operator executes those steps with its usual logic and client
// and the plan returns to Complete. A plan without NotPresent steps is left
// alone, so objects that someone else removed are not created again. The
// check is a pure read of the marks and runs on every sync of a Pending CSV,
// whether or not its requirements are met.
func (a *Operator) reinstallPlanSteps(logger *logrus.Entry, csv *v1alpha1.ClusterServiceVersion) error {
	plan, err := a.installPlanFor(csv)
	if err != nil || plan == nil || plan.Status.Phase != v1alpha1.InstallPlanPhaseComplete {
		return err
	}
	pending := false
	for _, step := range csvSteps(plan, csv) {
		if step.Status == v1alpha1.StepStatusNotPresent {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}

	plan.Status.Phase = v1alpha1.InstallPlanPhaseInstalling
	plan.Status.StartTime = nil
	plan.Status.Message = "reinstalling resources removed while the ClusterServiceVersion was failed"
	plan.Status.SetCondition(v1alpha1.InstallPlanCondition{
		Type:               v1alpha1.InstallPlanInstalled,
		Status:             corev1.ConditionUnknown,
		Reason:             v1alpha1.InstallPlanReasonPlanUnknown,
		Message:            plan.Status.Message,
		LastTransitionTime: a.now(),
	})
	logger.WithField("installplan", plan.GetName()).Info("reinstalling InstallPlan steps for recovered CSV")
	_, err = a.client.OperatorsV1alpha1().InstallPlans(plan.GetNamespace()).UpdateStatus(a.ctx, plan, metav1.UpdateOptions{})
	return err
}
