package olm

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"reflect"
	"strings"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (a *Operator) syncOperatorGroups(obj interface{}) error {
	op, ok := obj.(*v1alpha2.OperatorGroup)
	if !ok {
		log.Debugf("wrong type: %#v\n", obj)
		return fmt.Errorf("casting OperatorGroup failed")
	}
	log.Infof("syncing operator group %v", op)

	targetedNamespaces, err := a.updateNamespaceList(op)
	log.Debugf("Got targetedNamespaces: '%v'", targetedNamespaces)
	if err != nil {
		log.Errorf("updateNamespaceList error: %v", err)
		return err
	}

	if err := a.ensureClusterRoles(op); err != nil {
		log.Errorf("ensureClusterRoles error: %v", err)
		return err
	}
	log.Debug("Cluster roles completed")

	nsListJoined := strings.Join(targetedNamespaces, ",")

	if err := a.annotateDeployments(op, nsListJoined); err != nil {
		log.Errorf("annotateDeployments error: %v", err)
		return err
	}
	log.Debug("Deployment annotation completed")

	// annotate csvs
	csvsInNamespace := a.csvSet(op.Namespace)
	for _, csv := range csvsInNamespace {
		origCSVannotations := csv.GetAnnotations()
		a.addAnnotationsToObjectMeta(&csv.ObjectMeta, op, nsListJoined)
		if reflect.DeepEqual(origCSVannotations, csv.GetAnnotations()) == false {
			// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(csv); err != nil {
				log.Errorf("Update for existing CSV failed: %v", err)
			}
		}
	}

	for _, csv := range csvsInNamespace {
		if err := a.copyCsvToTargetNamespace(csv, op, targetedNamespaces); err != nil {
			return err
		}
	}

	for _, csv := range csvsInNamespace {
		if err := a.ensureRBACInTargetNamespace(csv, op, targetedNamespaces); err != nil {
			return err
		}
	}
	log.Debug("CSV annotation completed")

	return nil
}

func (a *Operator) ensureRBACInTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1alpha2.OperatorGroup, targetNamespaces []string) error {
	opPerms, err := resolver.RBACForClusterServiceVersion(csv)
	if err != nil {
		return err
	}

	if targetNamespaces == nil {
		return nil
	}

	// if OperatorGroup is global (all namespaces) we generate cluster roles / cluster role bindings instead
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {
		for _, p := range opPerms {
			if err := a.ensureSingletonRBAC(operatorGroup.GetNamespace(), csv, *p); err != nil {
				return err
			}
		}
		return nil
	}

	// otherwise, create roles/rolebindings for each target namespace
	for _, ns := range targetNamespaces {
		for _, p := range opPerms {
			if err := a.ensureTenantRBAC(operatorGroup.GetNamespace(), ns, csv, *p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *Operator) ensureSingletonRBAC(operatorNamespace string, csv *v1alpha1.ClusterServiceVersion, permissions resolver.OperatorPermissions) error {
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := a.lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	for _, r := range ownedRoles {
		// don't trust the owner label
		if !ownerutil.IsOwnedBy(r, csv) {
			continue
		}
		_, err := a.lister.RbacV1().ClusterRoleLister().Get(r.GetName())
		if err != nil {
			clusterRole := &rbacv1.ClusterRole{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterRole",
					APIVersion: r.APIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: r.GetName(),
				},
				Rules: r.Rules,
			}
			if _, err := a.OpClient.CreateClusterRole(clusterRole); err != nil {
				return err
			}
			// TODO check rules
		}
	}

	ownedRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	for _, r := range ownedRoleBindings {
		// don't trust the owner label
		if !ownerutil.IsOwnedBy(r, csv) {
			continue
		}
		_, err := a.lister.RbacV1().ClusterRoleBindingLister().Get(r.GetName())
		if err != nil {
			clusterRoleBinding := &rbacv1.ClusterRoleBinding{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ClusterRoleBinding",
					APIVersion: r.APIVersion,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: r.GetName(),
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
			// TODO check rules
		}
	}
	return nil
}

func (a *Operator) ensureTenantRBAC(operatorNamespace, targetNamespace string, csv *v1alpha1.ClusterServiceVersion, permissions resolver.OperatorPermissions) error {
	ownerSelector := ownerutil.CSVOwnerSelector(csv)
	ownedRoles, err := a.lister.RbacV1().RoleLister().Roles(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	for _, r := range ownedRoles {
		// don't trust the owner label
		if !ownerutil.IsOwnedBy(r, csv) {
			continue
		}
		_, err := a.lister.RbacV1().RoleLister().Roles(targetNamespace).Get(r.GetName())
		if err != nil {
			r.SetNamespace(targetNamespace)
			if _, err := a.OpClient.CreateRole(r); err != nil {
				return err
			}
		}
		// TODO check rules
	}

	ownedRoleBindings, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(operatorNamespace).List(ownerSelector)
	if err != nil {
		return err
	}

	// role bindings
	for _, r := range ownedRoleBindings {
		// don't trust the owner label
		if !ownerutil.IsOwnedBy(r, csv) {
			continue
		}
		_, err := a.lister.RbacV1().RoleBindingLister().RoleBindings(targetNamespace).Get(r.GetName())
		if err != nil {
			r.SetNamespace(targetNamespace)

			if _, err := a.OpClient.CreateRoleBinding(r); err != nil {
				return err
			}
			// TODO check  rules
		}
	}
	return nil
}

func (a *Operator) copyCsvToTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1alpha2.OperatorGroup, targetNamespaces []string) error {
	namespaces := targetNamespaces
	if len(targetNamespaces) == 1 && targetNamespaces[0] == corev1.NamespaceAll {
		namespaceObjs, err := a.lister.CoreV1().NamespaceLister().List(labels.Everything())
		if err != nil {
			return err
		}
		namespaces = []string{}
		for _, ns := range namespaceObjs {
			namespaces = append(namespaces, ns.GetName())
		}
	}

	for _, ns := range namespaces {
		if ns == operatorGroup.GetNamespace() {
			continue
		}
		fetchedCSV, err := a.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(ns).Get(csv.GetName())
		if k8serrors.IsAlreadyExists(err) {
			log.Debugf("Found existing CSV (%v), checking annotations", fetchedCSV.GetName())
			if reflect.DeepEqual(fetchedCSV.Annotations, csv.Annotations) == false {
				fetchedCSV.Annotations = csv.Annotations
				// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
				log.Debugf("Updating CSV %v in namespace %v", fetchedCSV.GetName(), ns)
				if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).Update(fetchedCSV); err != nil {
					log.Errorf("Update CSV in target namespace failed: %v", err)
					return err
				}
			}
			continue
		} else if k8serrors.IsNotFound(err) {
			// create new CSV instead of DeepCopy as namespace and resource version (and status) will be different
			newCSV := v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:        csv.Name,
					Annotations: csv.Annotations,
				},
				Spec: *csv.Spec.DeepCopy(),
			}
			newCSV.SetNamespace(ns)

			log.Debugf("Copying CSV %v to namespace %v", csv.GetName(), ns)
			createdCSV, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).Create(&newCSV)
			if err != nil {
				log.Errorf("Create for new CSV failed: %v", err)
				return err
			}
			createdCSV.Status = v1alpha1.ClusterServiceVersionStatus{
				Message:        "CSV copied to target namespace",
				Reason:         v1alpha1.CSVReasonCopied,
				LastUpdateTime: timeNow(),
			}
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns).UpdateStatus(createdCSV); err != nil {
				log.Errorf("Status update for CSV failed: %v", err)
				return err
			}
		} else if err != nil {
			log.Errorf("CSV fetch for %v failed: %v", csv.GetName(), err)
			return err
		}
	}
	return nil
}

func (a *Operator) addAnnotationsToObjectMeta(obj *metav1.ObjectMeta, op *v1alpha2.OperatorGroup, targetNamespaces string) {
	metav1.SetMetaDataAnnotation(obj, "olm.targetNamespaces", targetNamespaces)
	metav1.SetMetaDataAnnotation(obj, "olm.operatorNamespace", op.GetNamespace())
	metav1.SetMetaDataAnnotation(obj, "olm.operatorGroup", op.GetName())
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

func (a *Operator) updateNamespaceList(op *v1alpha2.OperatorGroup) ([]string, error) {
	selector, err := metav1.LabelSelectorAsSelector(&op.Spec.Selector)
	if err != nil {
		return nil, err
	}

	namespaceList := []string{}
	if selector.Empty() || selector == nil {
		namespaceList = append(namespaceList, corev1.NamespaceAll)
	} else {
		matchedNamespaces, err := a.lister.CoreV1().NamespaceLister().List(selector)
		if err != nil {
			return nil, err
		}

		operatorGroupNamespaceSelected := false
		for _, ns := range matchedNamespaces {
			namespaceList = append(namespaceList, ns.GetName())
			if ns.GetName() == op.GetNamespace() {
				operatorGroupNamespaceSelected = true
			}
		}

		// always include the operatorgroup namespace as a target namespace
		if !operatorGroupNamespaceSelected {
			namespaceList = append(namespaceList, op.GetNamespace())
		}
	}

	if !namespacesChanged(namespaceList, op.Status.Namespaces) {
		// status is current with correct namespaces, so no further updates required
		return namespaceList, nil
	}
	log.Debugf("Namespace change detected, found: %v", namespaceList)
	op.Status = v1alpha2.OperatorGroupStatus{
		Namespaces:  namespaceList,
		LastUpdated: timeNow(),
	}
	_, err = a.client.OperatorsV1alpha2().OperatorGroups(op.GetNamespace()).UpdateStatus(op)
	if err != nil {
		return namespaceList, err
	}
	return namespaceList, nil
}

func (a *Operator) ensureClusterRoles(op *v1alpha2.OperatorGroup) error {
	currentNamespace := op.GetNamespace()
	csvsInNamespace := a.csvSet(currentNamespace)
	for _, csv := range csvsInNamespace {
		managerPolicyRules := []rbacv1.PolicyRule{}
		apiEditPolicyRules := []rbacv1.PolicyRule{}
		apiViewPolicyRules := []rbacv1.PolicyRule{}
		for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
			nameGroupPair := strings.SplitN(owned.Name, ".", 2) // -> etcdclusters etcd.database.coreos.com
			if len(nameGroupPair) != 2 {
				return fmt.Errorf("Invalid parsing of name '%v', got %v", owned.Name, nameGroupPair)
			}
			managerPolicyRules = append(managerPolicyRules, rbacv1.PolicyRule{Verbs: []string{"*"}, APIGroups: []string{nameGroupPair[1]}, Resources: []string{nameGroupPair[0]}})
			apiEditPolicyRules = append(apiEditPolicyRules, rbacv1.PolicyRule{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{nameGroupPair[1]}, Resources: []string{nameGroupPair[0]}})
			apiViewPolicyRules = append(apiViewPolicyRules, rbacv1.PolicyRule{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{nameGroupPair[1]}, Resources: []string{nameGroupPair[0]}})
		}
		for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
			managerPolicyRules = append(managerPolicyRules, rbacv1.PolicyRule{Verbs: []string{"*"}, APIGroups: []string{owned.Group}, Resources: []string{owned.Name}})
			apiEditPolicyRules = append(apiEditPolicyRules, rbacv1.PolicyRule{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{owned.Group}, Resources: []string{owned.Name}})
			apiViewPolicyRules = append(apiViewPolicyRules, rbacv1.PolicyRule{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{owned.Group}, Resources: []string{owned.Name}})
		}

		clusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("owned-crd-manager-%s", csv.GetName()),
			},
			Rules: managerPolicyRules,
		}
		ownerutil.AddNonBlockingOwner(clusterRole, csv)
		_, err := a.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(clusterRole)
		if k8serrors.IsAlreadyExists(err) {
			if _, err = a.OpClient.UpdateClusterRole(clusterRole); err != nil {
				log.Errorf("Update CRD existing cluster role failed: %v", err)
				return err
			}
		} else if err != nil {
			log.Errorf("Update CRD cluster role failed: %v", err)
			return err
		}

		// operator group specific roles
		operatorGroupEditClusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-edit", op.Name),
			},
			Rules: apiEditPolicyRules,
		}
		_, err = a.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(operatorGroupEditClusterRole)
		if k8serrors.IsAlreadyExists(err) {
			if _, err = a.OpClient.UpdateClusterRole(operatorGroupEditClusterRole); err != nil {
				log.Errorf("Update existing edit cluster role failed: %v", err)
				return err
			}
		} else if err != nil {
			log.Errorf("Update edit cluster role failed: %v", err)
			return err
		}
		operatorGroupViewClusterRole := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-view", op.GetName()),
			},
			Rules: apiViewPolicyRules,
		}
		_, err = a.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(operatorGroupViewClusterRole)
		if k8serrors.IsAlreadyExists(err) {
			if _, err = a.OpClient.UpdateClusterRole(operatorGroupViewClusterRole); err != nil {
				log.Errorf("Update existing view cluster role failed: %v", err)
				return err
			}
		} else if err != nil {
			log.Errorf("Update view cluster role failed: %v", err)
			return err
		}
	}
	return nil
}

func (a *Operator) annotateDeployments(op *v1alpha2.OperatorGroup, targetNamespaceString string) error {
	// write above namespaces to watch in every deployment in operator namespace
	deploymentList, err := a.lister.AppsV1().DeploymentLister().Deployments(op.GetNamespace()).List(labels.Everything())
	if err != nil {
		log.Errorf("deployment list failed: %v\n", err)
		return err
	}

	for _, deploy := range deploymentList {
		// TODO: this will be incorrect if two operatorgroups own the same namespace
		// also - will be incorrect if a CSV is manually installed into a namespace
		if !ownerutil.IsOwnedByKind(deploy, "ClusterServiceVersion") {
			log.Debugf("deployment '%v' not owned by CSV, skipping", deploy.GetName())
			continue
		}

		if lastAnnotation, ok := deploy.Spec.Template.Annotations["olm.targetNamespaces"]; ok {
			if lastAnnotation == targetNamespaceString {
				log.Debugf("deployment '%v' already has annotation, skipping", deploy)
				continue
			}
		}

		originalDeploy := deploy.DeepCopy()
		a.addAnnotationsToObjectMeta(&deploy.Spec.Template.ObjectMeta, op, targetNamespaceString)
		if _, _, err := a.OpClient.PatchDeployment(originalDeploy, deploy); err != nil {
			log.Errorf("patch deployment failed: %v\n", err)
			return err
		}
	}

	return nil
}
