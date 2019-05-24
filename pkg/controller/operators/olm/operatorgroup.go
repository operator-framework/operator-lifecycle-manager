package olm

import (
	"fmt"
	"reflect"
	"strings"

	v1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/cache"
	utillabels "k8s.io/kubernetes/pkg/util/labels"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
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
	Suffices       = []string{AdminSuffix, EditSuffix, ViewSuffix}
	VerbsForSuffix = map[string][]string{
		AdminSuffix: AdminVerbs,
		EditSuffix:  EditVerbs,
		ViewSuffix:  ViewVerbs,
	}
)

func (o *Operator) syncOperatorGroups(obj interface{}) error {
	og, ok := obj.(*v1.OperatorGroup)
	if !ok {
		o.Log.Debugf("wrong type: %#v\n", obj)
		return fmt.Errorf("casting OperatorGroup failed")
	}

	logger := o.Log.WithFields(logrus.Fields{
		"operatorgroup": og.GetName(),
		"namespace":     og.GetNamespace(),
	})

	targetNamespaces, err := o.updateNamespaceList(og)
	if err != nil {
		logger.WithError(err).Warn("issue getting operatorgroup target namespaces")
		return err
	}
	logger.WithField("targetnamespaces", targetNamespaces).Debug("updated target namespaces")

	if namespacesChanged(targetNamespaces, og.Status.Namespaces) {
		// Update operatorgroup target namespace selection
		logger.WithField("targets", targetNamespaces).Debug("namespace change detected")
		og.Status = v1.OperatorGroupStatus{
			Namespaces:  targetNamespaces,
			LastUpdated: o.Now(),
		}

		if _, err = o.Client.OperatorsV1().OperatorGroups(og.GetNamespace()).UpdateStatus(og); err != nil && !k8serrors.IsNotFound(err) {
			logger.WithError(err).Warn("operatorgroup update failed")
			return err
		}
		logger.Debug("namespace change detected and operatorgroup status updated")

		// CSV requeue is handled by the succeeding sync in `annotateCSVs`
		return nil
	}

	logger.Debug("check that operatorgroup has updated CSV anotations")
	err = o.annotateCSVs(og, targetNamespaces, logger)
	if err != nil {
		logger.WithError(err).Warn("failed to annotate CSVs in operatorgroup after group change")
		return err
	}
	logger.Debug("OperatorGroup CSV annotation completed")

	if err := o.ensureOpGroupClusterRoles(og); err != nil {
		logger.WithError(err).Warn("failed to ensure operatorgroup clusterroles")
		return err
	}
	logger.Debug("operatorgroup clusterroles ensured")

	// Requeue all CSVs that provide the same APIs (including those removed). This notifies conflicting CSVs in
	// intersecting groups that their conflict has possibly been resolved, either through resizing or through
	// deletion of the conflicting CSV.
	groupSurface := resolver.NewOperatorGroup(og)
	groupProvidedAPIs := groupSurface.ProvidedAPIs()
	providedAPIsForCSVs := o.providedAPIsFromCSVs(og, logger)
	providedAPIsForGroup := providedAPIsForCSVs.Union(groupProvidedAPIs)

	csvs, err := o.findCSVsThatProvideAnyOf(providedAPIsForGroup)
	if err != nil {
		logger.WithError(err).Warn("could not find csvs that provide group apis")
	}
	for _, csv := range csvs {
		logger := logger.WithFields(logrus.Fields{
			"csv":       csv.GetName(),
			"namespace": csv.GetNamespace(),
		})
		if key, err := cache.MetaNamespaceKeyFunc(csv); err == nil {
			o.csvQueue.Add(key)
			logger.Debug("provider requeued")
		} else {
			logger.WithError(err).Warn("failed to requeue provider")
		}
	}

	o.pruneProvidedAPIs(og, groupProvidedAPIs, providedAPIsForCSVs, logger)

	return nil
}

func (o *Operator) operatorGroupDeleted(obj interface{}) {
	og, ok := obj.(*v1.OperatorGroup)
	if !ok {
		o.Log.Debugf("casting operatorgroup failed, wrong type: %#v\n", obj)
		return
	}

	logger := o.Log.WithFields(logrus.Fields{
		"operatorgroup": og.GetName(),
		"namespace":     og.GetNamespace(),
	})

	clusterRoles, err := o.Lister.RbacV1().ClusterRoleLister().List(labels.SelectorFromSet(ownerutil.OwnerLabel(og, "OperatorGroup")))
	if err != nil {
		logger.WithError(err).Error("failed to list clusterroles for garbage collection")
		return
	}
	for _, clusterRole := range clusterRoles {
		err = o.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Delete(clusterRole.GetName(), &metav1.DeleteOptions{})
		if err != nil {
			logger.WithError(err).Error("failed to delete clusterrole during garbage collection")
		}
	}
}

func (o *Operator) annotateCSVs(group *v1.OperatorGroup, targetNamespaces []string, logger *logrus.Entry) error {
	updateErrs := []error{}
	targetNamespaceSet := resolver.NewNamespaceSet(targetNamespaces)

	for _, csv := range o.csvSet(group.GetNamespace(), v1alpha1.CSVPhaseAny) {
		logger := logger.WithField("csv", csv.GetName())

		originalNamespacesAnnotation, _ := o.copyOperatorGroupAnnotations(&csv.ObjectMeta)[v1.OperatorGroupTargetsAnnotationKey]
		originalNamespaceSet := resolver.NewNamespaceSetFromString(originalNamespacesAnnotation)

		if o.operatorGroupAnnotationsDiffer(&csv.ObjectMeta, group) {
			o.setOperatorGroupAnnotations(&csv.ObjectMeta, group, true)
			// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
			if _, err := o.Client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(csv); err != nil && !k8serrors.IsNotFound(err) {
				logger.WithError(err).Warnf("error adding operatorgroup annotations")
				updateErrs = append(updateErrs, err)
				continue
			}
		}

		// Requeue csvs in original namespaces or in new target namespaces (to capture removed/added namespaces)
		requeueNamespaces := originalNamespaceSet.Union(targetNamespaceSet)
		if !requeueNamespaces.IsAllNamespaces() {
			for ns := range requeueNamespaces {
				o.csvQueue.Add(defaultKey(ns, csv.GetName()))
			}
		}

		// Must requeue in all namespaces, previous or new targets were AllNamespaces
		if namespaces, err := o.Lister.CoreV1().NamespaceLister().List(labels.Everything()); err != nil {
			for _, ns := range namespaces {
				o.csvQueue.Add(defaultKey(ns.GetName(), csv.GetName()))
			}
		}
	}
	return errors.NewAggregate(updateErrs)
}

func (o *Operator) providedAPIsFromCSVs(group *v1.OperatorGroup, logger *logrus.Entry) resolver.APISet {
	set := o.csvSet(group.Namespace, v1alpha1.CSVPhaseAny)
	providedAPIsFromCSVs := make(resolver.APISet)
	for _, csv := range set {
		// Don't union providedAPIsFromCSVs if the CSV is copied (member of another OperatorGroup)
		if csv.IsCopied() {
			logger.Debug("csv is copied. not updating annotations or including in operatorgroup's provided api set")
			continue
		}

		// TODO: Throw out CSVs that aren't members of the group due to group related failures?

		// Union the providedAPIsFromCSVs from existing members of the group
		operatorSurface, err := resolver.NewOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			logger.WithError(err).Warn("could not create OperatorSurface from csv")
			continue
		}
		providedAPIsFromCSVs = providedAPIsFromCSVs.Union(operatorSurface.ProvidedAPIs().StripPlural())
	}
	return providedAPIsFromCSVs
}

func (o *Operator) pruneProvidedAPIs(group *v1.OperatorGroup, groupProvidedAPIs, providedAPIsFromCSVs resolver.APISet, logger *logrus.Entry) {
	// Don't prune providedAPIsFromCSVs if static
	if group.Spec.StaticProvidedAPIs {
		o.Log.Debug("group has static provided apis. skipping provided api pruning")
		return
	}

	// Prune providedAPIs annotation if the cluster has fewer providedAPIs (handles CSV deletion)
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
		annotations[v1.OperatorGroupProvidedAPIsAnnotationKey] = intersection.String()
		group.SetAnnotations(annotations)
		logger.Debug("removing provided apis from annotation to match cluster state")
		if _, err := o.Client.OperatorsV1().OperatorGroups(group.GetNamespace()).Update(group); err != nil && !k8serrors.IsNotFound(err) {
			logger.WithError(err).Warn("could not update provided api annotations")
		}
	}
	return
}

// ensureProvidedAPIClusterRole ensures that a clusterrole exists (admin, edit, or view) for a single provided API Type
func (o *Operator) ensureProvidedAPIClusterRole(operatorGroup *v1.OperatorGroup, csv *v1alpha1.ClusterServiceVersion, namePrefix, suffix string, verbs []string, group, resource string, resourceNames []string) error {
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
	err := ownerutil.AddOwnerLabels(clusterRole, operatorGroup)
	if err != nil {
		return err
	}
	existingCR, err := o.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(clusterRole)
	if k8serrors.IsAlreadyExists(err) {
		if existingCR != nil && reflect.DeepEqual(existingCR.Labels, clusterRole.Labels) && reflect.DeepEqual(existingCR.Rules, clusterRole.Rules) {
			return nil
		}
		if _, err = o.OpClient.UpdateClusterRole(clusterRole); err != nil {
			o.Log.WithError(err).Errorf("Update existing cluster role failed: %v", clusterRole)
			return err
		}
	} else if err != nil {
		o.Log.WithError(err).Errorf("Create cluster role failed: %v", clusterRole)
		return err
	}
	return nil
}

// ensureClusterRolesForCSV ensures that ClusterRoles for writing and reading provided APIs exist for each operator
func (o *Operator) ensureClusterRolesForCSV(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1.OperatorGroup) error {
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		nameGroupPair := strings.SplitN(owned.Name, ".", 2) // -> etcdclusters etcd.database.coreos.com
		if len(nameGroupPair) != 2 {
			return fmt.Errorf("invalid parsing of name '%v', got %v", owned.Name, nameGroupPair)
		}
		plural := nameGroupPair[0]
		group := nameGroupPair[1]
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)

		for suffix, verbs := range VerbsForSuffix {
			if err := o.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, suffix, verbs, group, plural, nil); err != nil {
				return err
			}
		}
		if err := o.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix+"crd", ViewSuffix, []string{"get"}, "apiextensions.k8s.io", "customresourcedefinitions", []string{owned.Name}); err != nil {
			return err
		}
	}
	for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
		namePrefix := fmt.Sprintf("%s-%s-", owned.Name, owned.Version)
		for suffix, verbs := range VerbsForSuffix {
			if err := o.ensureProvidedAPIClusterRole(operatorGroup, csv, namePrefix, suffix, verbs, owned.Group, owned.Name, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *Operator) ensureRBACInTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1.OperatorGroup) error {
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
	ruleChecker := install.NewCSVRuleChecker(o.Lister.RbacV1().RoleLister(), o.Lister.RbacV1().RoleBindingLister(), o.Lister.RbacV1().ClusterRoleLister(), o.Lister.RbacV1().ClusterRoleBindingLister(), csv)

	logger := o.Log.WithField("opgroup", operatorGroup.GetName()).WithField("csv", csv.GetName())

	// if OperatorGroup is global (all namespaces) we generate cluster roles / cluster role bindings instead
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {
		logger.Debug("opgroup is global")

		// synthesize cluster permissions to verify rbac
		for _, p := range strategyDetailsDeployment.Permissions {
			strategyDetailsDeployment.ClusterPermissions = append(strategyDetailsDeployment.ClusterPermissions, p)
		}
		strategyDetailsDeployment.Permissions = nil
		permMet, _, err := o.permissionStatus(strategyDetailsDeployment, ruleChecker, corev1.NamespaceAll, csv.GetNamespace())
		if err != nil {
			return err
		}

		// operator already has access at the cluster scope
		if permMet {
			logger.Debug("global operator has correct global permissions")
			return nil
		}
		logger.Debug("lift roles/rolebindings to clusterroles/rolebindings")
		if err := o.ensureSingletonRBAC(operatorGroup.GetNamespace(), csv); err != nil {
			return err
		}

		return nil
	}

	return nil
}

func (o *Operator) ensureSingletonRBAC(operatorNamespace string, csv *v1alpha1.ClusterServiceVersion) error {
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := o.Lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}
	if len(ownedRoles) == 0 {
		return fmt.Errorf("no owned roles found")
	}

	for _, r := range ownedRoles {
		o.Log.Debug("processing role")
		_, err := o.Lister.RbacV1().ClusterRoleLister().Get(r.GetName())
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
			if _, err := o.OpClient.CreateClusterRole(clusterRole); err != nil {
				return err
			}
			o.Log.Debug("created cluster role")
		}
	}

	ownedRoleBindings, err := o.Lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}
	if len(ownedRoleBindings) == 0 {
		return fmt.Errorf("no owned rolebindings found")
	}

	for _, r := range ownedRoleBindings {
		_, err := o.Lister.RbacV1().ClusterRoleBindingLister().Get(r.GetName())
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
			if _, err := o.OpClient.CreateClusterRoleBinding(clusterRoleBinding); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *Operator) ensureTenantRBAC(operatorNamespace, targetNamespace string, csv *v1alpha1.ClusterServiceVersion, targetCSV *v1alpha1.ClusterServiceVersion) error {
	if operatorNamespace == targetNamespace {
		return nil
	}

	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := o.Lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	if len(ownedRoles) == 0 {
		return fmt.Errorf("owned roles not found in cache")
	}

	targetRoles, err := o.Lister.RbacV1().RoleLister().Roles(targetNamespace).List(ownerutil.CSVOwnerSelector(targetCSV))
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
			if _, err := o.OpClient.UpdateRole(existing); err != nil {
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
		if _, err := o.OpClient.CreateRole(targetRole); err != nil {
			return err
		}
	}

	ownedRoleBindings, err := o.Lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	targetRoleBindings, err := o.Lister.RbacV1().RoleBindingLister().RoleBindings(targetNamespace).List(ownerutil.CSVOwnerSelector(targetCSV))
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
		if _, err := o.OpClient.CreateRoleBinding(ownedRoleBinding); err != nil {
			return err
		}
	}
	return nil
}

func (o *Operator) ensureCSVsInNamespaces(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1.OperatorGroup, targets resolver.NamespaceSet) error {
	namespaces, err := o.Lister.CoreV1().NamespaceLister().List(labels.Everything())
	if err != nil {
		return err
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
	ruleChecker := install.NewCSVRuleChecker(o.Lister.RbacV1().RoleLister(), o.Lister.RbacV1().RoleBindingLister(), o.Lister.RbacV1().ClusterRoleLister(), o.Lister.RbacV1().ClusterRoleBindingLister(), csv)

	logger := o.Log.WithField("opgroup", operatorGroup.GetName()).WithField("csv", csv.GetName())

	targetCSVs := make(map[string]*v1alpha1.ClusterServiceVersion)
	for _, ns := range namespaces {
		if ns.GetName() == operatorGroup.Namespace {
			continue
		}
		if targets.Contains(ns.GetName()) {
			var targetCSV *v1alpha1.ClusterServiceVersion
			if targetCSV, err = o.copyToNamespace(csv, ns.GetName()); err != nil {
				o.Log.WithError(err).Debug("error copying to target")
				continue
			}
			targetCSVs[ns.GetName()] = targetCSV
		} else {
			if err := o.pruneFromNamespace(operatorGroup.GetName(), ns.GetName()); err != nil {
				o.Log.WithError(err).Debug("error pruning from old target")
			}
		}
	}

	targetNamespaces := operatorGroup.Status.Namespaces
	if targetNamespaces == nil {
		o.Log.Errorf("operatorgroup '%v' should have non-nil status", operatorGroup.GetName())
		return nil
	}
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {
		// global operator group handled by ensureRBACInTargetNamespace
		return nil
	}
	for _, ns := range targetNamespaces {
		// create roles/rolebindings for each target namespace
		permMet, _, err := o.permissionStatus(strategyDetailsDeployment, ruleChecker, ns, csv.GetNamespace())
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
		if err := o.ensureTenantRBAC(operatorGroup.GetNamespace(), ns, csv, targetCSV); err != nil {
			logger.WithError(err).Debug("ensuring tenant rbac")
			return err
		}
		logger.Debug("permissions created")
	}

	return nil
}

func (o *Operator) copyToNamespace(csv *v1alpha1.ClusterServiceVersion, namespace string) (*v1alpha1.ClusterServiceVersion, error) {
	if csv.GetNamespace() == namespace {
		return nil, fmt.Errorf("bug: can not copy to active namespace %v", csv.GetNamespace())
	}

	logger := o.Log.WithField("operator-ns", csv.GetNamespace()).WithField("target-ns", namespace)
	newCSV := csv.DeepCopy()
	delete(newCSV.Annotations, v1.OperatorGroupTargetsAnnotationKey)

	fetchedCSV, err := o.Lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(namespace).Get(newCSV.GetName())

	logger = logger.WithField("csv", csv.GetName())
	if fetchedCSV != nil {
		logger.Debug("checking annotations")

		if !reflect.DeepEqual(o.copyOperatorGroupAnnotations(&fetchedCSV.ObjectMeta), o.copyOperatorGroupAnnotations(&newCSV.ObjectMeta)) {
			// TODO: only copy over the opgroup annotations, not _all_ annotations
			fetchedCSV.Annotations = newCSV.Annotations
			fetchedCSV.SetLabels(utillabels.AddLabel(fetchedCSV.GetLabels(), v1alpha1.CopiedLabelKey, csv.GetNamespace()))
			// CRs don't support strategic merge patching, but in the future if they do this should be updated to patch
			logger.Debug("updating target CSV")
			if fetchedCSV, err = o.Client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Update(fetchedCSV); err != nil {
				logger.WithError(err).Error("update target CSV failed")
				return nil, err
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
			fetchedCSV.Status.LastUpdateTime = o.Now()
			if fetchedCSV, err = o.Client.OperatorsV1alpha1().ClusterServiceVersions(namespace).UpdateStatus(fetchedCSV); err != nil {
				logger.WithError(err).Error("status update for target CSV failed")
				return nil, err
			}
		}

		return fetchedCSV, nil

	} else if k8serrors.IsNotFound(err) {
		newCSV.SetNamespace(namespace)
		newCSV.SetResourceVersion("")
		newCSV.SetLabels(utillabels.AddLabel(newCSV.GetLabels(), v1alpha1.CopiedLabelKey, csv.GetNamespace()))

		logger.Debug("copying CSV to target")
		createdCSV, err := o.Client.OperatorsV1alpha1().ClusterServiceVersions(namespace).Create(newCSV)
		if err != nil {
			o.Log.Errorf("Create for new CSV failed: %v", err)
			return nil, err
		}
		createdCSV.Status.Reason = v1alpha1.CSVReasonCopied
		createdCSV.Status.Message = fmt.Sprintf("The operator is running in %s but is managing this namespace", csv.GetNamespace())
		createdCSV.Status.LastUpdateTime = o.Now()
		if _, err := o.Client.OperatorsV1alpha1().ClusterServiceVersions(namespace).UpdateStatus(createdCSV); err != nil {
			o.Log.Errorf("Status update for CSV failed: %v", err)
			return nil, err
		}

		return createdCSV, nil

	} else if err != nil {
		logger.WithError(err).Error("couldn't get CSV")
		return nil, err
	}

	// this return shouldn't be hit
	return nil, fmt.Errorf("unhandled code path")
}

func (o *Operator) pruneFromNamespace(operatorGroupName, namespace string) error {
	fetchedCSVs, err := o.Lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(namespace).List(labels.Everything())
	if err != nil {
		return err
	}

	for _, csv := range fetchedCSVs {
		if csv.IsCopied() && csv.GetAnnotations()[v1.OperatorGroupAnnotationKey] == operatorGroupName {
			o.Log.Debugf("Found CSV '%v' in namespace %v to delete", csv.GetName(), namespace)
			o.gcQueueIndexer.Enqueue(csv)
		}
	}
	return nil
}

func (o *Operator) setOperatorGroupAnnotations(obj *metav1.ObjectMeta, og *v1.OperatorGroup, addTargets bool) {
	metav1.SetMetaDataAnnotation(obj, v1.OperatorGroupNamespaceAnnotationKey, og.GetNamespace())
	metav1.SetMetaDataAnnotation(obj, v1.OperatorGroupAnnotationKey, og.GetName())

	if addTargets && og.Status.Namespaces != nil {
		metav1.SetMetaDataAnnotation(obj, v1.OperatorGroupTargetsAnnotationKey, og.BuildTargetNamespaces())
	}
}

func (o *Operator) operatorGroupAnnotationsDiffer(obj *metav1.ObjectMeta, og *v1.OperatorGroup) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return true
	}
	if operatorGroupNamespace, ok := annotations[v1.OperatorGroupNamespaceAnnotationKey]; !ok || operatorGroupNamespace != og.GetNamespace() {
		return true
	}
	if operatorGroup, ok := annotations[v1.OperatorGroupAnnotationKey]; !ok || operatorGroup != og.GetName() {
		return true
	}
	if targets, ok := annotations[v1.OperatorGroupTargetsAnnotationKey]; !ok || targets != og.BuildTargetNamespaces() {
		o.Log.WithFields(logrus.Fields{
			"annotationTargets": annotations[v1.OperatorGroupTargetsAnnotationKey],
			"opgroupTargets":    og.BuildTargetNamespaces(),
		}).Debug("annotations different")
		return true
	}

	o.Log.WithFields(logrus.Fields{
		"annotationTargets": annotations[v1.OperatorGroupTargetsAnnotationKey],
		"opgroupTargets":    og.BuildTargetNamespaces(),
	}).Debug("annotations correct")
	return false
}

func (o *Operator) copyOperatorGroupAnnotations(obj *metav1.ObjectMeta) map[string]string {
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

func (o *Operator) getOperatorGroupTargets(og *v1.OperatorGroup) (map[string]struct{}, error) {
	selector, err := metav1.LabelSelectorAsSelector(og.Spec.Selector)

	if err != nil {
		return nil, err
	}

	namespaceSet := make(map[string]struct{})
	if og.Spec.TargetNamespaces != nil && len(og.Spec.TargetNamespaces) > 0 {
		for _, ns := range og.Spec.TargetNamespaces {
			if ns == corev1.NamespaceAll {
				return nil, fmt.Errorf("TargetNamespaces cannot contain NamespaceAll: %v", og.Spec.TargetNamespaces)
			}
			namespaceSet[ns] = struct{}{}
		}
	} else if selector == nil || selector.Empty() || selector == labels.Nothing() {
		namespaceSet[corev1.NamespaceAll] = struct{}{}
	} else {
		matchedNamespaces, err := o.Lister.CoreV1().NamespaceLister().List(selector)
		if err != nil {
			return nil, err
		} else if len(matchedNamespaces) == 0 {
			o.Log.Debugf("No matched TargetNamespaces are found for given selector: %#v\n", selector)
		}

		for _, ns := range matchedNamespaces {
			namespaceSet[ns.GetName()] = struct{}{}
		}
	}
	return namespaceSet, nil
}

func (o *Operator) updateNamespaceList(og *v1.OperatorGroup) ([]string, error) {
	namespaceSet, err := o.getOperatorGroupTargets(og)
	if err != nil {
		return nil, err
	}
	namespaceList := []string{}
	for ns := range namespaceSet {
		namespaceList = append(namespaceList, ns)
	}

	return namespaceList, nil
}

func (o *Operator) ensureOpGroupClusterRole(og *v1.OperatorGroup, suffix string) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: strings.Join([]string{og.GetName(), suffix}, "-"),
		},
		AggregationRule: &rbacv1.AggregationRule{
			ClusterRoleSelectors: []metav1.LabelSelector{
				{
					MatchLabels: map[string]string{
						operatorGroupAggregrationKeyPrefix + suffix: og.GetName(),
					},
				},
			},
		},
	}
	err := ownerutil.AddOwnerLabels(clusterRole, og)
	if err != nil {
		return err
	}
	_, err = o.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(clusterRole)
	if k8serrors.IsAlreadyExists(err) {
		return nil
	} else if err != nil {
		o.Log.WithError(err).Errorf("Create cluster role failed: %v", clusterRole)
		return err
	}
	return nil
}

func (o *Operator) ensureOpGroupClusterRoles(og *v1.OperatorGroup) error {
	for _, suffix := range Suffices {
		if err := o.ensureOpGroupClusterRole(og, suffix); err != nil {
			return err
		}
	}
	return nil
}

func (o *Operator) findCSVsThatProvideAnyOf(provide resolver.APISet) ([]*v1alpha1.ClusterServiceVersion, error) {
	csvs, err := o.Lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	providers := []*v1alpha1.ClusterServiceVersion{}
	for i := 0; i < len(csvs); i++ {
		csv := csvs[i]
		if csv.IsCopied() {
			continue
		}

		operatorSurface, err := resolver.NewOperatorFromV1Alpha1CSV(csv)
		if err != nil {
			continue
		}

		if len(operatorSurface.ProvidedAPIs().StripPlural().Intersection(provide)) > 0 {
			providers = append(providers, csv)
		}
	}

	return providers, nil
}
