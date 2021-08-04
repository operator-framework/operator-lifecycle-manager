package olm

import (
	"context"
	"fmt"
	"hash/fnv"
	"reflect"
	"strings"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	utillabels "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/labels"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	hashutil "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubernetes/pkg/util/hash"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

const (
	AdminSuffix = "admin"
	EditSuffix  = "edit"
	ViewSuffix  = "view"
)

var (
	AdminVerbs     = []string{"*"}
	EditVerbs      = []string{"create", "update", "patch", "delete"}
	ViewVerbs      = []string{"get", "list", "watch"}
	Suffices       = []string{AdminSuffix, EditSuffix, ViewSuffix}
	VerbsForSuffix = map[string][]string{
		AdminSuffix: AdminVerbs,
		EditSuffix:  EditVerbs,
		ViewSuffix:  ViewVerbs,
	}
)

func aggregationLabelFromAPIKey(k opregistry.APIKey, suffix string) (string, error) {
	hash, err := cache.APIKeyToGVKHash(k)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("olm.opgroup.permissions/aggregate-to-%s-%s", hash, suffix), nil
}

func (a *Operator) syncOperatorGroups(obj interface{}) error {
	op, ok := obj.(*v1.OperatorGroup)
	if !ok {
		a.logger.Debugf("wrong type: %#v\n", obj)
		return fmt.Errorf("casting OperatorGroup failed")
	}

	logger := a.logger.WithFields(logrus.Fields{
		"operatorGroup": op.GetName(),
		"namespace":     op.GetNamespace(),
	})

	// Query OG in this namespace
	groups, err := a.lister.OperatorsV1().OperatorGroupLister().OperatorGroups(op.GetNamespace()).List(labels.Everything())
	if err != nil {
		logger.WithError(err).Warnf("failed to list OperatorGroups in the namespace")
	}

	// Check if there is a stale multiple OG condition and clear it if existed.
	if len(groups) == 1 {
		og := groups[0]
		if c := meta.FindStatusCondition(og.Status.Conditions, v1.MutlipleOperatorGroupCondition); c != nil {
			meta.RemoveStatusCondition(&og.Status.Conditions, v1.MutlipleOperatorGroupCondition)
			if og.GetName() == op.GetName() {
				meta.RemoveStatusCondition(&op.Status.Conditions, v1.MutlipleOperatorGroupCondition)
			}
			_, err = a.client.OperatorsV1().OperatorGroups(op.GetNamespace()).UpdateStatus(context.TODO(), og, metav1.UpdateOptions{})
			if err != nil {
				logger.Warnf("fail to upgrade operator group status og=%s with condition %+v: %s", og.GetName(), c, err.Error())
			}
		}
	} else if len(groups) > 1 {
		// Add to all OG's status conditions to indicate they're multiple OGs in the
		// same namespace which is not allowed.
		cond := metav1.Condition{
			Type:    v1.MutlipleOperatorGroupCondition,
			Status:  metav1.ConditionTrue,
			Reason:  v1.MultipleOperatorGroupsReason,
			Message: "Multiple OperatorGroup found in the same namespace",
		}
		for _, og := range groups {
			if c := meta.FindStatusCondition(og.Status.Conditions, v1.MutlipleOperatorGroupCondition); c != nil {
				continue
			}
			meta.SetStatusCondition(&og.Status.Conditions, cond)
			if og.GetName() == op.GetName() {
				meta.SetStatusCondition(&op.Status.Conditions, cond)
			}
			_, err = a.client.OperatorsV1().OperatorGroups(op.GetNamespace()).UpdateStatus(context.TODO(), og, metav1.UpdateOptions{})
			if err != nil {
				logger.Warnf("fail to upgrade operator group status og=%s with condition %+v: %s", og.GetName(), cond, err.Error())
			}
		}
	}

	previousRef := op.Status.ServiceAccountRef.DeepCopy()
	op, err = a.serviceAccountSyncer.SyncOperatorGroup(op)
	if err != nil {
		logger.Errorf("error updating service account - %v", err)
		return err
	}
	if op.Status.ServiceAccountRef != previousRef {
		crdList, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().List(labels.Everything())
		if err != nil {
			return err
		}
		for _, csv := range crdList {
			if group, ok := csv.GetAnnotations()[v1.OperatorGroupAnnotationKey]; !ok || group != op.GetName() {
				continue
			}
			if csv.Status.Reason == v1alpha1.CSVReasonComponentFailedNoRetry {
				csv.SetPhase(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonDetectedClusterChange, "Cluster resources changed state", a.now())
				_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).UpdateStatus(context.TODO(), csv, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
				if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
					return err
				}
				logger.Debug("Requeuing CSV due to detected service account change")
			}
		}
	}

	targetNamespaces, err := a.updateNamespaceList(op)
	if err != nil {
		logger.WithError(err).Warn("issue getting operatorgroup target namespaces")
		return err
	}
	logger.WithField("targetNamespaces", targetNamespaces).Debug("updated target namespaces")

	if namespacesChanged(targetNamespaces, op.Status.Namespaces) {
		logger.Debug("OperatorGroup namespaces change detected")
		outOfSyncNamespaces := namespacesAddedOrRemoved(op.Status.Namespaces, targetNamespaces)

		// Update operatorgroup target namespace selection
		logger.WithField("targets", targetNamespaces).Debug("namespace change detected")
		op.Status = v1.OperatorGroupStatus{
			Namespaces:  targetNamespaces,
			LastUpdated: a.now(),
			Conditions:  op.Status.Conditions,
		}

		if _, err = a.client.OperatorsV1().OperatorGroups(op.GetNamespace()).UpdateStatus(context.TODO(), op, metav1.UpdateOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			logger.WithError(err).Warn("operatorgroup update failed")
			return err
		}

		logger.Debug("operatorgroup status updated")

		// Requeueing out of sync namespaces
		logger.Debug("Requeueing out of sync namespaces")
		for _, ns := range outOfSyncNamespaces {
			logger.WithField("namespace", ns).Debug("requeueing")
			a.nsQueueSet.Add(ns)
		}

		// CSV requeue is handled by the succeeding sync in `annotateCSVs`
		return nil
	}

	logger.Debug("check that operatorgroup has updated CSV anotations")
	err = a.annotateCSVs(op, targetNamespaces, logger)
	if err != nil {
		logger.WithError(err).Warn("failed to annotate CSVs in operatorgroup after group change")
		return err
	}
	logger.Debug("OperatorGroup CSV annotation completed")

	// Requeue all CSVs that provide the same APIs (including those removed). This notifies conflicting CSVs in
	// intersecting groups that their conflict has possibly been resolved, either through resizing or through
	// deletion of the conflicting CSV.
	groupSurface := resolver.NewOperatorGroup(op)
	groupProvidedAPIs := groupSurface.ProvidedAPIs()
	providedAPIsForCSVs := a.providedAPIsFromCSVs(op, logger)
	providedAPIsForGroup := make(cache.APISet)
	for api := range providedAPIsForCSVs {
		providedAPIsForGroup[api] = struct{}{}
	}
	for api := range groupProvidedAPIs {
		providedAPIsForGroup[api] = struct{}{}
	}

	if err := a.ensureOpGroupClusterRoles(op, providedAPIsForGroup); err != nil {
		logger.WithError(err).Warn("failed to ensure operatorgroup clusterroles")
		return err
	}
	logger.Debug("operatorgroup clusterroles ensured")

	csvs, err := a.findCSVsThatProvideAnyOf(providedAPIsForGroup)
	if err != nil {
		logger.WithError(err).Warn("could not find csvs that provide group apis")
	}
	for _, csv := range csvs {
		logger.WithFields(logrus.Fields{
			"csv":       csv.GetName(),
			"namespace": csv.GetNamespace(),
		}).Debug("requeueing provider")
		if err := a.csvQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
			logger.WithError(err).Warn("could not requeue provider")
		}
	}

	a.pruneProvidedAPIs(op, groupProvidedAPIs, providedAPIsForCSVs, logger)
	return nil
}

func (a *Operator) operatorGroupDeleted(obj interface{}) {
	op, ok := obj.(*v1.OperatorGroup)
	if !ok {
		a.logger.Debugf("casting OperatorGroup failed, wrong type: %#v\n", obj)
		return
	}

	logger := a.logger.WithFields(logrus.Fields{
		"operatorGroup": op.GetName(),
		"namespace":     op.GetNamespace(),
	})

	clusterRoles, err := a.lister.RbacV1().ClusterRoleLister().List(labels.SelectorFromSet(ownerutil.OwnerLabel(op, "OperatorGroup")))
	if err != nil {
		logger.WithError(err).Error("failed to list ClusterRoles for garbage collection")
		return
	}
	for _, clusterRole := range clusterRoles {
		err = a.opClient.KubernetesInterface().RbacV1().ClusterRoles().Delete(context.TODO(), clusterRole.GetName(), metav1.DeleteOptions{})
		if err != nil {
			logger.WithError(err).Error("failed to delete ClusterRole during garbage collection")
		}
	}

	// Trigger a sync on namespaces
	logger.Debug("OperatorGroup deleted, requeueing out of sync namespaces")
	for _, ns := range op.Status.Namespaces {
		logger.WithField("namespace", ns).Debug("requeueing")
		a.nsQueueSet.Add(ns)
	}
}

func (a *Operator) annotateCSVs(group *v1.OperatorGroup, targetNamespaces []string, logger *logrus.Entry) error {
	updateErrs := []error{}
	targetNamespaceSet := resolver.NewNamespaceSet(targetNamespaces)

	for _, csv := range a.csvSet(group.GetNamespace(), v1alpha1.CSVPhaseAny) {
		if csv.IsCopied() {
			continue
		}
		logger := logger.WithField("csv", csv.GetName())

		originalNamespacesAnnotation, _ := a.copyOperatorGroupAnnotations(&csv.ObjectMeta)[v1.OperatorGroupTargetsAnnotationKey]
		originalNamespaceSet := resolver.NewNamespaceSetFromString(originalNamespacesAnnotation)

		if a.operatorGroupAnnotationsDiffer(&csv.ObjectMeta, group) {
			a.setOperatorGroupAnnotations(&csv.ObjectMeta, group, true)
			// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(context.TODO(), csv, metav1.UpdateOptions{}); err != nil && !k8serrors.IsNotFound(err) {
				logger.WithError(err).Warnf("error adding operatorgroup annotations")
				updateErrs = append(updateErrs, err)
				continue
			}
		}

		// requeue csvs in original namespaces or in new target namespaces (to capture removed/added namespaces)
		requeueNamespaces := originalNamespaceSet.Union(targetNamespaceSet)
		if !requeueNamespaces.IsAllNamespaces() {
			for ns := range requeueNamespaces {
				if err := a.csvQueueSet.Requeue(ns, csv.GetName()); err != nil {
					logger.WithError(err).Warn("could not requeue csv")
				}
			}
		}
		// have to requeue in all namespaces, previous or new targets were AllNamespaces
		if namespaces, err := a.lister.CoreV1().NamespaceLister().List(labels.Everything()); err != nil {
			for _, ns := range namespaces {
				if err := a.csvQueueSet.Requeue(ns.GetName(), csv.GetName()); err != nil {
					logger.WithError(err).Warn("could not requeue csv")
				}
			}
		}
	}
	return errors.NewAggregate(updateErrs)
}

func (a *Operator) providedAPIsFromCSVs(group *v1.OperatorGroup, logger *logrus.Entry) map[opregistry.APIKey]*v1alpha1.ClusterServiceVersion {
	set := a.csvSet(group.Namespace, v1alpha1.CSVPhaseAny)
	providedAPIsFromCSVs := make(map[opregistry.APIKey]*v1alpha1.ClusterServiceVersion)
	for _, csv := range set {
		// Don't union providedAPIsFromCSVs if the CSV is copied (member of another OperatorGroup)
		if csv.IsCopied() {
			logger.Debug("csv is copied. not updating annotations or including in operatorgroup's provided api set")
			continue
		}

		// TODO: Throw out CSVs that aren't members of the group due to group related failures?

		// Union the providedAPIsFromCSVs from existing members of the group
		operatorSurface, err := cache.NewOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			logger.WithError(err).Warn("could not create OperatorSurface from csv")
			continue
		}
		for providedAPI := range operatorSurface.GetProvidedAPIs().StripPlural() {
			providedAPIsFromCSVs[providedAPI] = csv
		}
	}
	return providedAPIsFromCSVs
}

func (a *Operator) pruneProvidedAPIs(group *v1.OperatorGroup, groupProvidedAPIs cache.APISet, providedAPIsFromCSVs map[opregistry.APIKey]*v1alpha1.ClusterServiceVersion, logger *logrus.Entry) {
	// Don't prune providedAPIsFromCSVs if static
	if group.Spec.StaticProvidedAPIs {
		a.logger.Debug("group has static provided apis. skipping provided api pruning")
		return
	}

	intersection := make(cache.APISet)
	for api := range providedAPIsFromCSVs {
		if _, ok := groupProvidedAPIs[api]; ok {
			intersection[api] = struct{}{}
		} else {
			csv := providedAPIsFromCSVs[api]
			_, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(csv.GetNamespace()).Get(csv.GetName())
			if k8serrors.IsNotFound(err) {
				continue
			}
			if csv.DeletionTimestamp == nil && (csv.Status.Phase == v1alpha1.CSVPhaseNone || csv.Status.Phase == v1alpha1.CSVPhasePending) {
				logger.Debugf("aborting operator group provided API update due to CSV %v in phase %v", csv.GetName(), csv.Status.Phase)
				return
			}
		}
	}

	// Prune providedAPIs annotation if the cluster has fewer providedAPIs (handles CSV deletion)
	//if intersection := groupProvidedAPIs.Intersection(providedAPIsFromCSVs); len(intersection) < len(groupProvidedAPIs) {
	if len(intersection) < len(groupProvidedAPIs) {
		difference := groupProvidedAPIs.Difference(intersection)
		logger := logger.WithFields(logrus.Fields{
			"providedAPIsOnCluster":  providedAPIsFromCSVs,
			"providedAPIsAnnotation": groupProvidedAPIs,
			"providedAPIDifference":  difference,
			"intersection":           intersection,
		})

		// Don't need to check for nil annotations since we already know |annotations| > 0
		annotations := group.GetAnnotations()
		annotations[v1.OperatorGroupProvidedAPIsAnnotationKey] = intersection.String()
		group.SetAnnotations(annotations)
		logger.Debug("removing provided apis from annotation to match cluster state")
		if _, err := a.client.OperatorsV1().OperatorGroups(group.GetNamespace()).Update(context.TODO(), group, metav1.UpdateOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			logger.WithError(err).Warn("could not update provided api annotations")
		}
	}
	return
}

// ensureProvidedAPIClusterRole ensures that a clusterrole exists (admin, edit, or view) for a single provided API Type
func (a *Operator) ensureProvidedAPIClusterRole(namePrefix, suffix string, verbs []string, group, resource string, resourceNames []string, api ownerutil.Owner, key opregistry.APIKey) error {
	aggregationLabel, err := aggregationLabelFromAPIKey(key, suffix)
	if err != nil {
		return err
	}
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namePrefix + suffix,
			Labels: map[string]string{
				// Matches aggregation rules on the bootstrap ClusterRoles.
				// https://github.com/kubernetes/kubernetes/blob/61847eab61788fb0543b4cf147773c9da646ed2f/plugin/pkg/auth/authorizer/rbac/bootstrappolicy/policy.go#L232
				fmt.Sprintf("rbac.authorization.k8s.io/aggregate-to-%s", suffix): "true",
				aggregationLabel: "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerutil.NonBlockingOwner(api)},
		},
		Rules: []rbacv1.PolicyRule{{Verbs: verbs, APIGroups: []string{group}, Resources: []string{resource}, ResourceNames: resourceNames}},
	}

	existingCR, err := a.lister.RbacV1().ClusterRoleLister().Get(clusterRole.Name)
	if existingCR == nil {
		existingCR, err = a.opClient.KubernetesInterface().RbacV1().ClusterRoles().Create(context.TODO(), clusterRole, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if !k8serrors.IsAlreadyExists(err) {
			a.logger.WithError(err).Errorf("Create cluster role failed: %v", clusterRole)
			return err
		}
	}

	if existingCR != nil && reflect.DeepEqual(existingCR.Rules, clusterRole.Rules) && ownerutil.IsOwnedBy(existingCR, api) && labels.Equals(existingCR.Labels, clusterRole.Labels) {
		return nil
	}

	if _, err := a.opClient.UpdateClusterRole(clusterRole); err != nil {
		a.logger.WithError(err).Errorf("Update existing cluster role failed: %v", clusterRole)
		return err
	}
	return nil
}

// ensureClusterRolesForCSV ensures that ClusterRoles for writing and reading provided APIs exist for each operator
func (a *Operator) ensureClusterRolesForCSV(csv *v1alpha1.ClusterServiceVersion) error {
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		crd, err := a.lister.APIExtensionsV1().CustomResourceDefinitionLister().Get(owned.Name)
		if err != nil {
			return fmt.Errorf("crd %q not found: %s", owned.Name, err.Error())
		}
		nameGroupPair := strings.SplitN(owned.Name, ".", 2) // -> etcdclusters etcd.database.coreos.com
		if len(nameGroupPair) != 2 {
			return fmt.Errorf("invalid parsing of name '%v', got %v", owned.Name, nameGroupPair)
		}
		plural := nameGroupPair[0]
		group := nameGroupPair[1]
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)
		key := opregistry.APIKey{Group: group, Version: owned.Version, Kind: owned.Kind, Plural: plural}
		for suffix, verbs := range VerbsForSuffix {
			if err := a.ensureProvidedAPIClusterRole(namePrefix, suffix, verbs, group, plural, nil, crd, key); err != nil {
				return err
			}
		}
		if err := a.ensureProvidedAPIClusterRole(namePrefix+"crd", ViewSuffix, []string{"get"}, "apiextensions.k8s.io", "customresourcedefinitions", []string{owned.Name}, crd, key); err != nil {
			return err
		}
	}
	for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
		svcName := strings.Join([]string{owned.Version, owned.Group}, ".")
		svc, err := a.lister.APIRegistrationV1().APIServiceLister().Get(svcName)
		if err != nil {
			return fmt.Errorf("apiservice %q not found: %s", svcName, err.Error())
		}
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)
		key := opregistry.APIKey{Group: owned.Group, Version: owned.Version, Kind: owned.Kind}
		for suffix, verbs := range VerbsForSuffix {
			if err := a.ensureProvidedAPIClusterRole(namePrefix, suffix, verbs, owned.Group, owned.Name, nil, svc, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Operator) ensureRBACInTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1.OperatorGroup) error {
	targetNamespaces := operatorGroup.Status.Namespaces
	if targetNamespaces == nil {
		return nil
	}

	strategyResolver := install.StrategyResolver{}
	strategy, err := strategyResolver.UnmarshalStrategy(csv.Spec.InstallStrategy)
	if err != nil {
		return err
	}
	strategyDetailsDeployment, ok := strategy.(*v1alpha1.StrategyDetailsDeployment)
	if !ok {
		return fmt.Errorf("could not cast install strategy as type %T", strategyDetailsDeployment)
	}
	ruleChecker := install.NewCSVRuleChecker(a.lister.RbacV1().RoleLister(), a.lister.RbacV1().RoleBindingLister(), a.lister.RbacV1().ClusterRoleLister(), a.lister.RbacV1().ClusterRoleBindingLister(), csv)

	logger := a.logger.WithField("opgroup", operatorGroup.GetName()).WithField("csv", csv.GetName())

	// if OperatorGroup is global (all namespaces) we generate cluster roles / cluster role bindings instead
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {
		logger.Debug("opgroup is global")

		// synthesize cluster permissions to verify rbac
		for _, p := range strategyDetailsDeployment.Permissions {
			strategyDetailsDeployment.ClusterPermissions = append(strategyDetailsDeployment.ClusterPermissions, p)
		}
		strategyDetailsDeployment.Permissions = nil
		permMet, _, err := a.permissionStatus(strategyDetailsDeployment, ruleChecker, corev1.NamespaceAll, csv)
		if err != nil {
			return err
		}

		// operator already has access at the cluster scope
		if permMet {
			logger.Debug("global operator has correct global permissions")
			return nil
		}
		logger.Debug("lift roles/rolebindings to clusterroles/rolebindings")
		if err := a.ensureSingletonRBAC(operatorGroup.GetNamespace(), csv); err != nil {
			return err
		}

		return nil
	}

	return nil
}

func (a *Operator) ensureSingletonRBAC(operatorNamespace string, csv *v1alpha1.ClusterServiceVersion) error {
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := a.lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}
	if len(ownedRoles) == 0 {
		return fmt.Errorf("no owned roles found")
	}

	for _, r := range ownedRoles {
		a.logger.Debug("processing role")
		_, err := a.lister.RbacV1().ClusterRoleLister().Get(r.GetName())
		if err != nil {
			clusterRole := &rbacv1.ClusterRole{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterRole",
					APIVersion: r.APIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:   r.GetName(),
					Labels: r.GetLabels(),
				},
				Rules: append(r.Rules, rbacv1.PolicyRule{
					Verbs:     ViewVerbs,
					APIGroups: []string{corev1.GroupName},
					Resources: []string{"namespaces"},
				}),
			}
			// TODO: this should do something smarter if the cluster role already exists
			if cr, err := a.opClient.CreateClusterRole(clusterRole); err != nil {
				// if the CR already exists, but the label is correct, the cache is just behind
				if k8serrors.IsAlreadyExists(err) && ownerutil.IsOwnedByLabel(cr, csv) {
					continue
				} else {
					return err
				}
			}
			a.logger.Debug("created cluster role")
		}
	}

	ownedRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}
	if len(ownedRoleBindings) == 0 {
		return fmt.Errorf("no owned rolebindings found")
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
					Labels: r.GetLabels(),
				},
				Subjects: r.Subjects,
				RoleRef: rbacv1.RoleRef{
					APIGroup: r.RoleRef.APIGroup,
					Kind:     "ClusterRole",
					Name:     r.RoleRef.Name,
				},
			}
			// TODO: this should do something smarter if the cluster role binding already exists
			if crb, err := a.opClient.CreateClusterRoleBinding(clusterRoleBinding); err != nil {
				// if the CR already exists, but the label is correct, the cache is just behind
				if k8serrors.IsAlreadyExists(err) && ownerutil.IsOwnedByLabel(crb, csv) {
					continue
				} else {
					return err
				}
			}
		}
	}
	return nil
}

func (a *Operator) ensureTenantRBAC(operatorNamespace, targetNamespace string, csv *v1alpha1.ClusterServiceVersion, targetCSV *v1alpha1.ClusterServiceVersion) error {
	if operatorNamespace == targetNamespace {
		return nil
	}

	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := a.lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	if len(ownedRoles) == 0 {
		return fmt.Errorf("owned roles not found in cache")
	}

	targetRoles, err := a.lister.RbacV1().RoleLister().Roles(targetNamespace).List(ownerutil.CSVOwnerSelector(targetCSV))
	if err != nil {
		return err
	}

	targetRolesByName := map[string]*rbacv1.Role{}
	for _, r := range targetRoles {
		targetRolesByName[r.GetName()] = r
	}

	for _, ownedRole := range ownedRoles {
		// don't trust the owner label
		// TODO: this can skip objects that have owner labels but different ownerreferences
		if !ownerutil.IsOwnedBy(ownedRole, csv) {
			continue
		}

		existing, ok := targetRolesByName[ownedRole.GetName()]

		// role already exists, update the rules
		if ok {
			existing.Rules = ownedRole.Rules
			if _, err := a.opClient.UpdateRole(existing); err != nil {
				return err
			}
			continue
		}

		// role doesn't exist, create it
		// TODO: we can work around error cases here; if there's an un-owned role with a matching name we should generate instead
		targetRole := ownedRole.DeepCopy()
		targetRole.SetResourceVersion("0")
		targetRole.SetNamespace(targetNamespace)
		targetRole.SetOwnerReferences([]metav1.OwnerReference{ownerutil.NonBlockingOwner(targetCSV)})
		if err := ownerutil.AddOwnerLabels(targetRole, targetCSV); err != nil {
			return err
		}
		targetRole.SetLabels(utillabels.AddLabel(targetRole.GetLabels(), v1alpha1.CopiedLabelKey, operatorNamespace))
		if _, err := a.opClient.CreateRole(targetRole); err != nil {
			return err
		}
	}

	ownedRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	targetRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(targetNamespace).List(ownerutil.CSVOwnerSelector(targetCSV))
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
		_, ok := targetRoleBindingsByName[ownedRoleBinding.GetName()]

		// role binding exists
		if ok {
			// TODO: we should check if SA/role has changed
			continue
		}

		// role binding doesn't exist
		// TODO: we can work around error cases here; if there's an un-owned role with a matching name we should generate instead
		ownedRoleBinding = ownedRoleBinding.DeepCopy()
		ownedRoleBinding.SetNamespace(targetNamespace)
		ownedRoleBinding.SetResourceVersion("0")
		ownedRoleBinding.SetOwnerReferences([]metav1.OwnerReference{ownerutil.NonBlockingOwner(targetCSV)})
		if err := ownerutil.AddOwnerLabels(ownedRoleBinding, targetCSV); err != nil {
			return err
		}
		ownedRoleBinding.SetLabels(utillabels.AddLabel(ownedRoleBinding.GetLabels(), v1alpha1.CopiedLabelKey, operatorNamespace))
		if _, err := a.opClient.CreateRoleBinding(ownedRoleBinding); err != nil {
			return err
		}
	}
	return nil
}

func (a *Operator) ensureCSVsInNamespaces(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1.OperatorGroup, targets resolver.NamespaceSet) error {
	namespaces, err := a.lister.CoreV1().NamespaceLister().List(labels.Everything())
	if err != nil {
		return err
	}

	strategyDetailsDeployment := &csv.Spec.InstallStrategy.StrategySpec
	ruleChecker := install.NewCSVRuleChecker(a.lister.RbacV1().RoleLister(), a.lister.RbacV1().RoleBindingLister(), a.lister.RbacV1().ClusterRoleLister(), a.lister.RbacV1().ClusterRoleBindingLister(), csv)

	logger := a.logger.WithField("opgroup", operatorGroup.GetName()).WithField("csv", csv.GetName())

	targetCSVs := make(map[string]*v1alpha1.ClusterServiceVersion)

	var copyPrototype v1alpha1.ClusterServiceVersion
	csvCopyPrototype(csv, &copyPrototype)
	nonstatus, status := copyableCSVHash(&copyPrototype)

	for _, ns := range namespaces {
		if ns.GetName() == operatorGroup.Namespace {
			continue
		}
		if targets.Contains(ns.GetName()) {
			var targetCSV *v1alpha1.ClusterServiceVersion
			if targetCSV, err = a.copyToNamespace(&copyPrototype, csv.GetNamespace(), ns.GetName(), nonstatus, status); err != nil {
				a.logger.WithError(err).Debug("error copying to target")
				continue
			}
			targetCSVs[ns.GetName()] = targetCSV
		} else {
			if err := a.pruneFromNamespace(operatorGroup.GetName(), ns.GetName()); err != nil {
				a.logger.WithError(err).Debug("error pruning from old target")
			}
		}
	}

	targetNamespaces := operatorGroup.Status.Namespaces
	if targetNamespaces == nil {
		a.logger.Errorf("operatorgroup '%v' should have non-nil status", operatorGroup.GetName())
		return nil
	}
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {
		// global operator group handled by ensureRBACInTargetNamespace
		return nil
	}
	for _, ns := range targetNamespaces {
		// create roles/rolebindings for each target namespace
		permMet, _, err := a.permissionStatus(strategyDetailsDeployment, ruleChecker, ns, csv)
		if err != nil {
			logger.WithError(err).Debug("permission status")
			return err
		}
		logger.WithField("target", ns).WithField("permMet", permMet).Debug("permission status")

		// operator already has access in the target namespace
		if permMet {
			logger.Debug("operator has access")
			continue
		} else {
			logger.Debug("operator needs access, going to create permissions")
		}

		targetCSV, ok := targetCSVs[ns]
		if !ok {
			return fmt.Errorf("bug: no target CSV for namespace %v", ns)
		}
		if err := a.ensureTenantRBAC(operatorGroup.GetNamespace(), ns, csv, targetCSV); err != nil {
			logger.WithError(err).Debug("ensuring tenant rbac")
			return err
		}
		logger.Debug("permissions created")
	}

	return nil
}

// copyableCSVHash returns a hash of the parts of the given CSV that
// are relevant to copied CSV projection.
func copyableCSVHash(original *v1alpha1.ClusterServiceVersion) (string, string) {
	shallow := v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:        original.Name,
			Labels:      original.Labels,
			Annotations: original.Annotations,
		},
		Spec: original.Spec,
	}

	hash := fnv.New64a()
	hashutil.DeepHashObject(hash, &shallow)
	nonstatus := string(hash.Sum(nil))

	hash.Reset()
	hashutil.DeepHashObject(hash, &original.Status)
	status := string(hash.Sum(nil))

	return nonstatus, status
}

// If returned error is not nil, the returned ClusterServiceVersion
// has only the Name, Namespace, and UID fields set.
func (a *Operator) copyToNamespace(prototype *v1alpha1.ClusterServiceVersion, nsFrom, nsTo, nonstatus, status string) (*v1alpha1.ClusterServiceVersion, error) {
	if nsFrom == nsTo {
		return nil, fmt.Errorf("bug: can not copy to active namespace %v", nsFrom)
	}

	prototype.Namespace = nsTo
	prototype.ResourceVersion = ""
	prototype.UID = ""

	existing, err := a.copiedCSVLister.ClusterServiceVersions(nsTo).Get(prototype.GetName())
	if k8serrors.IsNotFound(err) {

		created, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(nsTo).Create(context.TODO(), prototype, metav1.CreateOptions{})
		if err != nil {
			return nil, err
		}
		created.Status = prototype.Status
		if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(nsTo).UpdateStatus(context.TODO(), created, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
		return &v1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name:      created.Name,
				Namespace: created.Namespace,
				UID:       created.UID,
			},
		}, nil
	} else if err != nil {
		return nil, err
	}

	prototype.Namespace = existing.Namespace
	prototype.ResourceVersion = existing.ResourceVersion
	prototype.UID = existing.UID
	existingNonStatus := existing.Annotations["$copyhash-nonstatus"]
	existingStatus := existing.Annotations["$copyhash-status"]

	if existingNonStatus != nonstatus {
		if existing, err = a.client.OperatorsV1alpha1().ClusterServiceVersions(nsTo).Update(context.TODO(), prototype, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
	} else {
		// Avoid mutating cached copied CSV.
		existing = prototype
	}

	if existingStatus != status {
		existing.Status = prototype.Status
		if _, err = a.client.OperatorsV1alpha1().ClusterServiceVersions(nsTo).UpdateStatus(context.TODO(), existing, metav1.UpdateOptions{}); err != nil {
			return nil, err
		}
	}
	return &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      existing.Name,
			Namespace: existing.Namespace,
			UID:       existing.UID,
		},
	}, nil
}

func (a *Operator) pruneFromNamespace(operatorGroupName, namespace string) error {
	fetchedCSVs, err := a.copiedCSVLister.ClusterServiceVersions(namespace).List(labels.Everything())
	if err != nil {
		return err
	}

	for _, csv := range fetchedCSVs {
		if csv.IsCopied() && csv.GetAnnotations()[v1.OperatorGroupAnnotationKey] == operatorGroupName {
			a.logger.Debugf("Found CSV '%v' in namespace %v to delete", csv.GetName(), namespace)
			if err := a.copiedCSVGCQueueSet.Requeue(csv.GetNamespace(), csv.GetName()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Operator) setOperatorGroupAnnotations(obj *metav1.ObjectMeta, op *v1.OperatorGroup, addTargets bool) {
	metav1.SetMetaDataAnnotation(obj, v1.OperatorGroupNamespaceAnnotationKey, op.GetNamespace())
	metav1.SetMetaDataAnnotation(obj, v1.OperatorGroupAnnotationKey, op.GetName())

	if addTargets && op.Status.Namespaces != nil {
		metav1.SetMetaDataAnnotation(obj, v1.OperatorGroupTargetsAnnotationKey, op.BuildTargetNamespaces())
	}
}

func (a *Operator) operatorGroupAnnotationsDiffer(obj *metav1.ObjectMeta, op *v1.OperatorGroup) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return true
	}
	if operatorGroupNamespace, ok := annotations[v1.OperatorGroupNamespaceAnnotationKey]; !ok || operatorGroupNamespace != op.GetNamespace() {
		return true
	}
	if operatorGroup, ok := annotations[v1.OperatorGroupAnnotationKey]; !ok || operatorGroup != op.GetName() {
		return true
	}
	if targets, ok := annotations[v1.OperatorGroupTargetsAnnotationKey]; !ok || targets != op.BuildTargetNamespaces() {
		a.logger.WithFields(logrus.Fields{
			"annotationTargets": annotations[v1.OperatorGroupTargetsAnnotationKey],
			"opgroupTargets":    op.BuildTargetNamespaces(),
		}).Debug("annotations different")
		return true
	}

	a.logger.WithFields(logrus.Fields{
		"annotationTargets": annotations[v1.OperatorGroupTargetsAnnotationKey],
		"opgroupTargets":    op.BuildTargetNamespaces(),
	}).Debug("annotations correct")
	return false
}

func (a *Operator) copyOperatorGroupAnnotations(obj *metav1.ObjectMeta) map[string]string {
	copiedAnnotations := make(map[string]string)
	for k, v := range obj.GetAnnotations() {
		switch k {
		case v1.OperatorGroupNamespaceAnnotationKey:
			fallthrough
		case v1.OperatorGroupAnnotationKey:
			fallthrough
		case v1.OperatorGroupTargetsAnnotationKey:
			copiedAnnotations[k] = v
		}
	}
	return copiedAnnotations
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

func (a *Operator) getOperatorGroupTargets(op *v1.OperatorGroup) (map[string]struct{}, error) {
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
		} else if len(matchedNamespaces) == 0 {
			a.logger.Debugf("No matched TargetNamespaces are found for given selector: %#v\n", selector)
		}

		for _, ns := range matchedNamespaces {
			namespaceSet[ns.GetName()] = struct{}{}
		}
	}
	return namespaceSet, nil
}

func (a *Operator) updateNamespaceList(op *v1.OperatorGroup) ([]string, error) {
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

func (a *Operator) ensureOpGroupClusterRole(op *v1.OperatorGroup, suffix string, apis cache.APISet) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.Join([]string{op.GetName(), suffix}, "-"),
		},
	}
	var selectors []metav1.LabelSelector
	for api := range apis {
		aggregationLabel, err := aggregationLabelFromAPIKey(api, suffix)
		if err != nil {
			return err
		}
		selectors = append(selectors, metav1.LabelSelector{
			MatchLabels: map[string]string{
				aggregationLabel: "true",
			},
		})
	}
	if len(selectors) > 0 {
		clusterRole.AggregationRule = &rbacv1.AggregationRule{
			ClusterRoleSelectors: selectors,
		}
	}
	err := ownerutil.AddOwnerLabels(clusterRole, op)
	if err != nil {
		return err
	}

	existingRole, err := a.lister.RbacV1().ClusterRoleLister().Get(clusterRole.Name)
	if existingRole == nil {
		existingRole, err = a.opClient.KubernetesInterface().RbacV1().ClusterRoles().Create(context.TODO(), clusterRole, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if !k8serrors.IsAlreadyExists(err) {
			a.logger.WithError(err).Errorf("Create cluster role failed: %v", clusterRole)
			return err
		}
	}

	if existingRole != nil && labels.Equals(existingRole.Labels, clusterRole.Labels) && reflect.DeepEqual(existingRole.AggregationRule, clusterRole.AggregationRule) {
		return nil
	}

	if _, err := a.opClient.UpdateClusterRole(clusterRole); err != nil {
		a.logger.WithError(err).Errorf("Update existing cluster role failed: %v", clusterRole)
		return err
	}
	return nil
}

func (a *Operator) ensureOpGroupClusterRoles(op *v1.OperatorGroup, apis cache.APISet) error {
	for _, suffix := range Suffices {
		if err := a.ensureOpGroupClusterRole(op, suffix, apis); err != nil {
			return err
		}
	}
	return nil
}

func (a *Operator) findCSVsThatProvideAnyOf(provide cache.APISet) ([]*v1alpha1.ClusterServiceVersion, error) {
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

		operatorSurface, err := cache.NewOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			continue
		}

		if len(operatorSurface.GetProvidedAPIs().StripPlural().Intersection(provide)) > 0 {
			providers = append(providers, csv)
		}
	}

	return providers, nil
}

// namespacesAddedOrRemoved returns the union of:
// - the set of elements in A but not in B
// - the set of elements in B but not in A
func namespacesAddedOrRemoved(a, b []string) []string {
	check := make(map[string]struct{})

	for _, namespace := range a {
		check[namespace] = struct{}{}
	}

	for _, namespace := range b {
		if _, ok := check[namespace]; !ok {
			check[namespace] = struct{}{}
		} else {
			delete(check, namespace)
		}
	}

	// Remove global namespace name if added
	delete(check, "")

	var keys []string
	for key := range check {
		keys = append(keys, key)
	}

	return keys
}

func csvCopyPrototype(src, dst *v1alpha1.ClusterServiceVersion) {
	*dst = v1alpha1.ClusterServiceVersion{
		TypeMeta: src.TypeMeta,
		ObjectMeta: metav1.ObjectMeta{
			Name:        src.Name,
			Annotations: map[string]string{},
			Labels:      map[string]string{},
		},
		Spec:   src.Spec,
		Status: src.Status,
	}
	for k, v := range src.Annotations {
		if k == v1.OperatorGroupTargetsAnnotationKey {
			continue
		}
		if k == "kubectl.kubernetes.io/last-applied-configuration" {
			continue // big
		}
		dst.Annotations[k] = v
	}
	for k, v := range src.Labels {
		if strings.HasPrefix(k, decorators.ComponentLabelKeyPrefix) {
			continue
		}
		dst.Labels[k] = v
	}
	dst.Labels[v1alpha1.CopiedLabelKey] = src.Namespace
	dst.Status.Reason = v1alpha1.CSVReasonCopied
	dst.Status.Message = fmt.Sprintf("The operator is running in %s but is managing this namespace", src.GetNamespace())
}
