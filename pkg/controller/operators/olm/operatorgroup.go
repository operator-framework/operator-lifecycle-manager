package olm

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	operatorGroupAggregrationKeyPrefix = "olm.opgroup.permissions/aggregate-to-"
	kubeRBACAggregationKeyPrefix       = "rbac.authorization.k8s.io/aggregate-to-"
	AdminSuffix                        = "admin"
	EditSuffix                         = "edit"
	ViewSuffix                         = "view"
)

var (
	AdminVerbs     = []string{"*"}
	EditVerbs      = []string{"create", "update", "patch", "delete"}
	ViewVerbs      = []string{"get", "list", "watch"}
	VerbsForSuffix = map[string][]string{
		AdminSuffix: AdminVerbs,
		EditSuffix:  EditVerbs,
		ViewSuffix:  ViewVerbs,
	}
)

func (a *Operator) syncOperatorGroups(obj interface{}) error {
	op, ok := obj.(*v1alpha2.OperatorGroup)
	if !ok {
		a.Log.Debugf("wrong type: %#v\n", obj)
		return fmt.Errorf("casting OperatorGroup failed")
	}

	logger := a.Log.WithFields(logrus.Fields{
		"operatorGroup": op.GetName(),
		"namespace":     op.GetNamespace(),
	})

	targetNamespaces, err := a.updateNamespaceList(op)
	if err != nil {
		logger.WithError(err).Warn("issue getting operatorgroup target namespaces")
		return err
	}

	if namespacesChanged(targetNamespaces, op.Status.Namespaces) {
		// Update operatorgroup target namespace selection
		logger.WithField("targets", targetNamespaces).Debug("namespace change detected")
		op.Status = v1alpha2.OperatorGroupStatus{
			Namespaces:  targetNamespaces,
			LastUpdated: timeNow(),
		}

		if _, err = a.client.OperatorsV1alpha2().OperatorGroups(op.GetNamespace()).UpdateStatus(op); err != nil && !k8serrors.IsNotFound(err) {
			logger.WithError(err).Warn("operatorgroup update failed")
			return err
		}

		// CSV requeue is handled by the succeeding sync
		return nil
	}

	a.annotateCSVs(op, targetNamespaces, logger)
	logger.Debug("OperatorGroup CSV annotation completed")

	if err := a.ensureOpGroupClusterRoles(op); err != nil {
		logger.WithError(err).Warn("failed to ensure operatorgroup clusterroles")
		return err
	}
	logger.Debug("operatorgroup clusterroles ensured")

	// Requeue all CSVs that provide the same APIs (including those removed). This notifies conflicting CSVs in
	// intersecting groups that their conflict has possibly been resolved, either through resizing or through
	// deletion of the conflicting CSV.
	csvs, err := a.findCSVsThatProvideAnyOf(a.providedAPIsForGroup(op, logger))
	if err != nil {
		logger.WithError(err).Warn("could not find csvs that provide group apis")
	}
	for _, csv := range csvs {
		logger.WithFields(logrus.Fields{
			"csv":       csv.GetName(),
			"namespace": csv.GetNamespace(),
		}).Debug("requeueing provider")
		if err := a.csvQueueSet.Requeue(csv.GetName(), csv.GetNamespace()); err != nil {
			logger.WithError(err).Warn("could not requeue provider")
		}
	}

	return nil
}

func (a *Operator) annotateCSVs(group *v1alpha2.OperatorGroup, targetNamespaces []string, logger *logrus.Entry) {
	for _, csv := range a.csvSet(group.GetNamespace(), v1alpha1.CSVPhaseAny) {
		logger := logger.WithField("csv", csv.GetName())
		origCSVannotations := a.copyOperatorGroupAnnotations(&csv.ObjectMeta)
		a.setOperatorGroupAnnotations(&csv.ObjectMeta, op, !csv.IsCopied())
		if !reflect.DeepEqual(origCSVannotations, a.copyOperatorGroupAnnotations(&csv.ObjectMeta)) {
			// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(csv); err != nil {
				// TODO: return an error and requeue the OperatorGroup here? Can this cause an update to never happen if there's resource contention?
				logger.WithError(err).Warn("update to existing csv failed")
				continue
			} else {
				if _, ok := origCSVannotations[v1alpha2.OperatorGroupAnnotationKey]; ok {
					if err := a.csvQueueSet.Requeue(csv.GetName(), csv.GetNamespace()); err != nil {
						logger.WithError(err).Warn("error requeuing")
					}
					logger.Debugf("Successfully changed annotation on CSV: %v -> %v", origCSVannotations, csv.GetAnnotations())
				} else {
					logger.Debugf("Successfully added annotation on CSV: %v", csv.GetAnnotations())
				}
			}
		}
		for _, ns := range targetNamespaces {
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(csv.GetName())
			if k8serrors.IsNotFound(err) {
				if err := a.csvQueueSet.Requeue(csv.GetName(), csv.GetNamespace()); err != nil {
					logger.WithError(err).Warn("could not requeue provider")
				}
			}
		}
	}
}

func (a *Operator) providedAPIsForGroup(group *v1alpha2.OperatorGroup, logger *logrus.Entry) resolver.APISet {
	set := a.csvSet(group.Namespace, v1alpha1.CSVPhaseAny)
	providedAPIsFromCSVs := make(resolver.APISet)
	for _, csv := range set {
		// Don't union providedAPIsFromCSVs if the CSV is copied (member of another OperatorGroup)
		if csv.IsCopied() {
			logger.Debug("csv is copied. not updating annotations or including in operatorgroup's provided api set")
			continue
		}

		if a.operatorGroupAnnotationsDiffer(&csv.ObjectMeta, op) {
			a.setOperatorGroupAnnotations(&csv.ObjectMeta, op, true)
			// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(csv); err != nil && !k8serrors.IsNotFound(err) {
				// TODO: return an error and requeue the OperatorGroup here? Can this cause an update to never happen if there's resource contention?
				logger.WithError(err).Warnf("error adding operatorgroup annotations")
				return err
			}
		}

		// TODO: Throw out CSVs that aren't members of the group due to group related failures?

		// Union the providedAPIsFromCSVs from existing members of the group
		operatorSurface, err := resolver.NewOperatorFromCSV(csv)
		if err != nil {
			logger.WithError(err).Warn("could not create OperatorSurface from csv")
			continue
		}
		providedAPIsFromCSVs = providedAPIsFromCSVs.Union(operatorSurface.ProvidedAPIs().StripPlural())
	}

	groupSurface := resolver.NewOperatorGroup(*group)
	groupProvidedAPIs := groupSurface.ProvidedAPIs()

	// Don't prune providedAPIsFromCSVs if static
	if group.Spec.StaticProvidedAPIs {
		a.Log.Debug("group has static provided apis. skipping provided api pruning")
		return providedAPIsFromCSVs.Union(groupProvidedAPIs)
	}

	// Prune providedAPIsFromCSVs annotation if the cluster has less providedAPIsFromCSVs (handles CSV deletion)
	if intersection := groupProvidedAPIs.Intersection(providedAPIsFromCSVs); len(intersection) < len(groupProvidedAPIs) {
		difference := groupProvidedAPIs.Difference(intersection)
		logger := logger.WithFields(logrus.Fields{
			"providedAPIsOnCluster":  providedAPIsFromCSVs,
			"providedAPIsAnnotation": groupProvidedAPIs,
			"providedAPIDifference":  difference,
			"intersection":           intersection,
		})

		// Don't need to check for nil annotations since we already know |annotations| > 0
		annotations := group.GetAnnotations()
		annotations[v1alpha2.OperatorGroupProvidedAPIsAnnotationKey] = intersection.String()
		group.SetAnnotations(annotations)
		logger.Debug("removing provided apis from annotation to match cluster state")
		if updated, err := a.client.OperatorsV1alpha2().OperatorGroups(group.GetNamespace()).Update(group); err != nil && !k8serrors.IsNotFound(err) {
			logger.WithError(err).Warn("could not update provided api annotations")
		} else {
			group = updated
		}
	}

	return providedAPIsFromCSVs.Union(groupProvidedAPIs)
}

// ensureProvidedAPIClusterRole ensures that a clusterrole exists (admin, edit, or view) for a single provided API Type
func (a *Operator) ensureProvidedAPIClusterRole(operatorGroup *v1alpha2.OperatorGroup, csv *v1alpha1.ClusterServiceVersion, namePrefix, suffix string, verbs []string, group, resource string, resourceNames []string) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namePrefix + suffix,
			Labels: map[string]string{
				kubeRBACAggregationKeyPrefix + suffix:       "true",
				operatorGroupAggregrationKeyPrefix + suffix: operatorGroup.GetName(),
			},
		},
		Rules: []rbacv1.PolicyRule{{Verbs: verbs, APIGroups: []string{group}, Resources: []string{resource}, ResourceNames: resourceNames}},
	}
	existingCR, err := a.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(clusterRole)
	if k8serrors.IsAlreadyExists(err) {
		if existingCR != nil && reflect.DeepEqual(existingCR.Labels, clusterRole.Labels) && reflect.DeepEqual(existingCR.Rules, clusterRole.Rules) {
			return nil
		}
		if _, err = a.OpClient.UpdateClusterRole(clusterRole); err != nil {
			a.Log.WithError(err).Errorf("Update existing cluster role failed: %v", clusterRole)
			return err
		}
	} else if err != nil {
		a.Log.WithError(err).Errorf("Create cluster role failed: %v", clusterRole)
		return err
	}
	return nil
}

// ensureClusterRolesForCSV ensures that ClusterRoles for writing and reading provided APIs exist for each operator
func (a *Operator) ensureClusterRolesForCSV(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1alpha2.OperatorGroup) error {
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		nameGroupPair := strings.SplitN(owned.Name, ".", 2) // -> etcdclusters etcd.database.coreos.com
		if len(nameGroupPair) != 2 {
			return fmt.Errorf("invalid parsing of name '%v', got %v", owned.Name, nameGroupPair)
		}
		plural := nameGroupPair[0]
		group := nameGroupPair[1]
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)

		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, AdminSuffix, VerbsForSuffix[AdminSuffix], group, plural, nil); err != nil {
			return err
		}
		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, EditSuffix, VerbsForSuffix[EditSuffix], group, plural, nil); err != nil {
			return err
		}
		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, ViewSuffix, VerbsForSuffix[ViewSuffix], group, plural, nil); err != nil {
			return err
		}

		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix+"-crd", ViewSuffix, []string{"get"}, "apiextensions.k8s.io", "customresourcedefinitions", []string{owned.Name}); err != nil {
			return err
		}
	}
	for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)

		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, AdminSuffix, VerbsForSuffix[AdminSuffix], owned.Group, owned.Name, nil); err != nil {
			return err
		}
		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, EditSuffix, VerbsForSuffix[EditSuffix], owned.Group, owned.Name, nil); err != nil {
			return err
		}
		if err := a.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, ViewSuffix, VerbsForSuffix[ViewSuffix], owned.Group, owned.Name, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a *Operator) ensureRBACInTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1alpha2.OperatorGroup) error {
	targetNamespaces := operatorGroup.Status.Namespaces
	if targetNamespaces == nil {
		return nil
	}

	strategyResolver := install.StrategyResolver{}
	strategy, err := strategyResolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return err
	}
	strategyDetailsDeployment, ok := strategy.(*install.StrategyDetailsDeployment)
	if !ok {
		return fmt.Errorf("could not cast install strategy as type %T", strategyDetailsDeployment)
	}
	ruleChecker := install.NewCSVRuleChecker(a.lister.RbacV1().RoleLister(), a.lister.RbacV1().RoleBindingLister(), a.lister.RbacV1().ClusterRoleLister(), a.lister.RbacV1().ClusterRoleBindingLister(), csv)

	// if OperatorGroup is global (all namespaces) we generate cluster roles / cluster role bindings instead
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {

		// synthesize cluster permissions to verify rbac
		for _, p := range strategyDetailsDeployment.Permissions {
			strategyDetailsDeployment.ClusterPermissions = append(strategyDetailsDeployment.ClusterPermissions, p)
		}
		strategyDetailsDeployment.Permissions = nil
		permMet, _, err := a.permissionStatus(strategyDetailsDeployment, ruleChecker, corev1.NamespaceAll)
		if err != nil {
			return err
		}

		// operator already has access at the cluster scope
		if permMet {
			return nil
		}
		if err := a.ensureSingletonRBAC(operatorGroup.GetNamespace(), csv); err != nil {
			return err
		}

		return nil
	}

	// otherwise, create roles/rolebindings for each target namespace
	for _, ns := range targetNamespaces {
		if ns == operatorGroup.GetNamespace() {
			continue
		}

		permMet, _, err := a.permissionStatus(strategyDetailsDeployment, ruleChecker, ns)
		if err != nil {
			return err
		}
		// operator already has access in the target namespace
		if permMet {
			return nil
		}
		if err := a.ensureTenantRBAC(operatorGroup.GetNamespace(), ns, csv); err != nil {
			return err
		}
	}
	return nil
}

func (a *Operator) ensureSingletonRBAC(operatorNamespace string, csv *v1alpha1.ClusterServiceVersion) error {
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := a.lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	for _, r := range ownedRoles {
		_, err := a.lister.RbacV1().ClusterRoleLister().Get(r.GetName())
		if err != nil {
			clusterRole := &rbacv1.ClusterRole{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterRole",
					APIVersion: r.APIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:   r.GetName(),
					Labels: ownerutil.OwnerLabel(csv),
				},
				Rules: r.Rules,
			}
			if _, err := a.OpClient.CreateClusterRole(clusterRole); err != nil {
				return err
			}
		}
	}

	ownedRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	for _, r := range ownedRoleBindings {
		_, err := a.lister.RbacV1().ClusterRoleBindingLister().Get(r.GetName())
		if err != nil {
			clusterRoleBinding := &rbacv1.ClusterRoleBinding{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterRoleBinding",
					APIVersion: r.APIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:   r.GetName(),
					Labels: ownerutil.OwnerLabel(csv),
				},
				Subjects: r.Subjects,
				RoleRef: rbacv1.RoleRef{
					APIGroup: r.RoleRef.APIGroup,
					Kind:     "ClusterRole",
					Name:     r.RoleRef.Name,
				},
			}
			if _, err := a.OpClient.CreateClusterRoleBinding(clusterRoleBinding); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Operator) ensureTenantRBAC(operatorNamespace, targetNamespace string, csv *v1alpha1.ClusterServiceVersion) error {
	targetCSV, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(targetNamespace).Get(csv.GetName())
	if err != nil {
		return err
	}
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := a.lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	targetRoles, err := a.lister.RbacV1().RoleLister().List(ownerutil.CSVOwnerSelector(targetCSV))
	if err != nil {
		return err
	}

	targetRolesByName := map[string]*rbacv1.Role{}
	for _, r := range targetRoles {
		targetRolesByName[r.GetName()] = r
	}

	for _, ownedRole := range ownedRoles {
		// don't trust the owner label
		if !ownerutil.IsOwnedBy(ownedRole, csv) {
			continue
		}

		existing, ok := targetRolesByName[ownedRole.GetName()]

		// role already exists, update the rules
		if ok {
			existing.Rules = ownedRole.Rules
			if _, err := a.OpClient.UpdateRole(existing); err != nil {
				return err
			}
			continue
		}

		// role doesn't exist, create it
		ownedRole.SetNamespace(targetNamespace)
		ownedRole.SetOwnerReferences([]metav1.OwnerReference{ownerutil.NonBlockingOwner(targetCSV)})
		if _, err := a.OpClient.CreateRole(ownedRole); err != nil {
			return err
		}
	}

	ownedRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	targetRoleBindings, err := a.lister.RbacV1().RoleBindingLister().List(ownerutil.CSVOwnerSelector(targetCSV))
	if err != nil {
		return err
	}

	targetRoleBindingsByName := map[string]*rbacv1.RoleBinding{}
	for _, r := range targetRoleBindings {
		targetRoleBindingsByName[r.GetName()] = r
	}

	// role bindings
	for _, ownedRoleBinding := range ownedRoleBindings {
		// don't trust the owner label
		if !ownerutil.IsOwnedBy(ownedRoleBinding, csv) {
			continue
		}
		_, ok := targetRolesByName[ownedRoleBinding.GetName()]

		// role binding exists
		if ok {
			// TODO: should we check if SA/role has changed?
			continue
		}

		// role binding doesn't exist
		ownedRoleBinding.SetNamespace(targetNamespace)
		ownedRoleBinding.SetOwnerReferences([]metav1.OwnerReference{ownerutil.NonBlockingOwner(targetCSV)})
		if _, err := a.OpClient.CreateRoleBinding(ownedRoleBinding); err != nil {
			return err
		}
	}
	return nil
}

func (a *Operator) copyCsvToTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1alpha2.OperatorGroup, namespaces []string, pruneNamespaces []string) error {
	// check for stale CSVs from a different previous operator group configuration
	for _, ns := range pruneNamespaces {
		fetchedCSVs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).List(labels.Everything())
		if err != nil {
			return err
		}
		for _, csv := range fetchedCSVs {
			if csv.IsCopied() && csv.GetAnnotations()[v1alpha2.OperatorGroupAnnotationKey] == operatorGroup.GetName() {
				a.Log.Debugf("Found CSV '%v' in namespace %v to delete", csv.GetName(), ns)
				err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).Delete(csv.GetName(), &metav1.DeleteOptions{})
				if err != nil {
					return err
				}
			}
		}
	}

	logger := a.Log.WithField("operator-ns", operatorGroup.GetNamespace())
	newCSV := csv.DeepCopy()
	delete(newCSV.Annotations, v1alpha2.OperatorGroupTargetsAnnotationKey)
	for _, ns := range namespaces {
		if ns == operatorGroup.GetNamespace() {
			continue
		}
		logger = logger.WithField("target-ns", ns)

		fetchedCSV, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(newCSV.GetName())

		logger = logger.WithField("csv", csv.GetName())
		if fetchedCSV != nil {
			logger.Debug("checking annotations")
			if !reflect.DeepEqual(fetchedCSV.Annotations, newCSV.Annotations) {
				fetchedCSV.Annotations = newCSV.Annotations
				// CRs don't support strategic merge patching, but in the future if they do this should be updated to patch
				logger.Debug("updating target CSV")
				if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).Update(fetchedCSV); err != nil {
					logger.WithError(err).Error("update target CSV failed")
					return err
				}
			}

			logger.Debug("checking status")
			newCSV.Status = csv.Status
			newCSV.Status.Reason = v1alpha1.CSVReasonCopied
			newCSV.Status.Message = fmt.Sprintf("The operator is running in %s but is managing this namespace", csv.GetNamespace())

			if !reflect.DeepEqual(fetchedCSV.Status, newCSV.Status) {
				logger.Debug("updating status")
				// Must use fetchedCSV because UpdateStatus(...) checks resource UID.
				fetchedCSV.Status = newCSV.Status
				fetchedCSV.Status.LastUpdateTime = timeNow()
				if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).UpdateStatus(fetchedCSV); err != nil {
					logger.WithError(err).Error("status update for target CSV failed")
					return err
				}
			}

		} else if k8serrors.IsNotFound(err) {
			newCSV.SetNamespace(ns)
			newCSV.SetResourceVersion("")

			logger.Debug("copying CSV")
			createdCSV, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).Create(newCSV)
			if err != nil {
				a.Log.Errorf("Create for new CSV failed: %v", err)
				return err
			}
			createdCSV.Status.Reason = v1alpha1.CSVReasonCopied
			createdCSV.Status.Message = fmt.Sprintf("The operator is running in %s but is managing this namespace", csv.GetNamespace())
			createdCSV.Status.LastUpdateTime = timeNow()
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).UpdateStatus(createdCSV); err != nil {
				a.Log.Errorf("Status update for CSV failed: %v", err)
				return err
			}

		} else if err != nil {
			logger.WithError(err).Error("couldn't get CSV")
			return err
		}
	}
	return nil
}

func (a *Operator) setOperatorGroupAnnotations(obj *metav1.ObjectMeta, op *v1alpha2.OperatorGroup, addTargets bool) {
	metav1.SetMetaDataAnnotation(obj, v1alpha2.OperatorGroupNamespaceAnnotationKey, op.GetNamespace())
	metav1.SetMetaDataAnnotation(obj, v1alpha2.OperatorGroupAnnotationKey, op.GetName())

	if addTargets && op.Status.Namespaces != nil {
		metav1.SetMetaDataAnnotation(obj, v1alpha2.OperatorGroupTargetsAnnotationKey, strings.Join(op.Status.Namespaces, ","))
	}
}

func (a *Operator) operatorGroupAnnotationsDiffer(obj *metav1.ObjectMeta, op *v1alpha2.OperatorGroup) bool {
	annotations := obj.GetAnnotations()
	if operatorGroupNamespace, ok := annotations[v1alpha2.OperatorGroupNamespaceAnnotationKey]; !ok || operatorGroupNamespace != op.GetNamespace() {
		return true
	}
	if operatorGroup, ok := annotations[v1alpha2.OperatorGroupAnnotationKey]; !ok || operatorGroup != op.GetName() {
		return true
	}
	if targets, ok := annotations[v1alpha2.OperatorGroupTargetsAnnotationKey]; !ok || targets != strings.Join(op.Status.Namespaces, ",") {
		return true
	}

	return false
}

func (a *Operator) copyOperatorGroupAnnotations(obj *metav1.ObjectMeta) map[string]string {
	copiedAnnotations := make(map[string]string)
	for k, v := range obj.GetAnnotations() {
		switch k {
		case v1alpha2.OperatorGroupNamespaceAnnotationKey:
			fallthrough
		case v1alpha2.OperatorGroupAnnotationKey:
			fallthrough
		case v1alpha2.OperatorGroupTargetsAnnotationKey:
			copiedAnnotations[k] = v
		}
	}
	return copiedAnnotations
}

// returns items in a that are not in b
func sliceCompare(a, b []string) []string {
	mb := make(map[string]struct{})
	for _, x := range b {
		mb[x] = struct{}{}
	}
	ab := []string{}
	for _, x := range a {
		if _, ok := mb[x]; !ok {
			ab = append(ab, x)
		}
	}
	return ab
}

func namespacesChanged(clusterNamespaces []string, statusNamespaces []string) bool {
	if len(clusterNamespaces) != len(statusNamespaces) {
		return true
	}

	nsMap := map[string]struct{}{}
	for _, v := range clusterNamespaces {
		nsMap[v] = struct{}{}
	}
	for _, v := range statusNamespaces {
		if _, ok := nsMap[v]; !ok {
			return true
		}
	}
	return false
}

func (a *Operator) getOperatorGroupTargets(op *v1alpha2.OperatorGroup) (map[string]struct, error) {
	selector, err := metav1.LabelSelectorAsSelector(op.Spec.Selector)

	if err != nil {
		return nil, err
	}

	namespaceSet := make(map[string]struct{})
	if op.Spec.TargetNamespaces != nil && len(op.Spec.TargetNamespaces) > 0 {
		for _, ns := range op.Spec.TargetNamespaces {
			if ns == corev1.NamespaceAll {
				return nil, fmt.Errorf("TargetNamespaces cannot contain NamespaceAll: %v", op.Spec.TargetNamespaces)
			}
			namespaceSet[ns] = struct{}{}
		}
	} else if selector == nil || selector.Empty() || selector == labels.Nothing() {
		namespaceSet[corev1.NamespaceAll] = struct{}{}
	} else {
		matchedNamespaces, err := a.lister.CoreV1().NamespaceLister().List(selector)
		if err != nil {
			return nil, err
		}

		for _, ns := range matchedNamespaces {
			namespaceSet[ns.GetName()] = struct{}{}
		}
	}
	return namespaceSet, nil
}

func (a *Operator) updateNamespaceList(op *v1alpha2.OperatorGroup) ([]string, error) {
	namespaceSet, err := a.getOperatorGroupTargets(op)
	if err != nil {
		return nil, err
	}
	namespaceList := []string{}
	for ns := range namespaceSet {
		namespaceList = append(namespaceList, ns)
	}

	return namespaceList, nil
}

func (a *Operator) ensureOpGroupClusterRole(op *v1alpha2.OperatorGroup, suffix string) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.Join([]string{op.GetName(), suffix}, "-"),
		},
		AggregationRule: &rbacv1.AggregationRule{
			ClusterRoleSelectors: []metav1.LabelSelector{
				{
					MatchLabels: map[string]string{
						operatorGroupAggregrationKeyPrefix + suffix: op.GetName(),
					},
				},
			},
		},
	}
	_, err := a.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(clusterRole)
	if k8serrors.IsAlreadyExists(err) {
		return nil
	} else if err != nil {
		a.Log.WithError(err).Errorf("Create cluster role failed: %v", clusterRole)
		return err
	}
	return nil
}

func (a *Operator) ensureOpGroupClusterRoles(op *v1alpha2.OperatorGroup) error {
	if err := a.ensureOpGroupClusterRole(op, AdminSuffix); err != nil {
		return err
	}
	if err := a.ensureOpGroupClusterRole(op, EditSuffix); err != nil {
		return err
	}
	if err := a.ensureOpGroupClusterRole(op, ViewSuffix); err != nil {
		return err
	}
	return nil
}

func (a *Operator) findCSVsThatProvideAnyOf(provide resolver.APISet) ([]*v1alpha1.ClusterServiceVersion, error) {
	csvs, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	providers := []*v1alpha1.ClusterServiceVersion{}
	for i := 0; i < len(csvs); i++ {
		csv := csvs[i]
		if csv.IsCopied() {
			continue
		}

		operatorSurface, err := resolver.NewOperatorFromCSV(csv)
		if err != nil {
			continue
		}

		if len(operatorSurface.ProvidedAPIs().StripPlural().Intersection(provide)) > 0 {
			providers = append(providers, csv)
		}
	}

	return providers, nil
}
