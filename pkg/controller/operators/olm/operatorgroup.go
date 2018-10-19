package olm

import (
	"fmt"
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

	err, targetedNamespaces := a.updateNamespaceList(op)
	log.Debugf("Got targetedNamespaces: '%v'", targetedNamespaces)
	if err != nil {
		return err
	}

	if err := a.ensureClusterRoles(op); err != nil {
		return err
	}

	var nsList []string
	for ix := range targetedNamespaces {
		nsList = append(nsList, targetedNamespaces[ix].GetName())
	}
	nsListJoined := strings.Join(nsList, ",")

	if err := a.annotateDeployments(nsList, nsListJoined); err != nil {
		return err
	}

	// annotate csvs
	csvsInNamespace := a.csvsInNamespace(op.Namespace)
	for _, csv := range csvsInNamespace {
		// create new CSV instead of DeepCopy as namespace and resource version (and status) will be different
		newCSV := v1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name: csv.Name,
			},
			Spec: *csv.Spec.DeepCopy(),
			Status: v1alpha1.ClusterServiceVersionStatus{
				Message:        "CSV copied to target namespace",
				Reason:         v1alpha1.CSVReasonCopied,
				LastUpdateTime: timeNow(),
			},
		}

		//TODO: listen to delete events on CSVS, delete all "target CSVs"
		metav1.SetMetaDataAnnotation(&newCSV.ObjectMeta, "olm.targetNamespaces", strings.Join(nsList, ","))
		metav1.SetMetaDataAnnotation(&newCSV.ObjectMeta, "olm.operatorNamespace", op.GetNamespace())
		metav1.SetMetaDataAnnotation(&newCSV.ObjectMeta, "olm.operatorGroup", op.GetName())

		for _, ns := range targetedNamespaces {
			newCSV.SetNamespace(ns.Name)
			if ns.Name != op.Namespace {
				_, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(newCSV.GetNamespace()).Create(&newCSV)
				if err != nil {
					return err
				}
			}
		}
	}

	//TODO: ensure RBAC on operator serviceaccount

	return nil
}

func namespacesChanged(clusterNamespaces []*corev1.Namespace, statusNamespaces []*corev1.Namespace) bool {
	if len(clusterNamespaces) != len(statusNamespaces) {
		return true
	}

	nsMap := map[string]struct{}{}
	for _, v := range clusterNamespaces {
		nsMap[v.Name] = struct{}{}
	}
	for _, v := range statusNamespaces {
		if _, ok := nsMap[v.Name]; !ok {
			return true
		}
	}
	return false
}

func (a *Operator) updateNamespaceList(op *v1alpha2.OperatorGroup) (error, []*corev1.Namespace) {
	selector, err := metav1.LabelSelectorAsSelector(&op.Spec.Selector)
	if err != nil {
		return err, nil
	}

	namespaceList, err := a.lister.CoreV1().NamespaceLister().List(selector)
	if err != nil {
		return err, nil
	}

	if !namespacesChanged(namespaceList, op.Status.Namespaces) {
		// status is current with correct namespaces, so no further updates required
		return nil, namespaceList
	}
	log.Debugf("Namespace change detected, found: %v", namespaceList)
	op.Status.Namespaces = make([]*corev1.Namespace, len(namespaceList))
	copy(op.Status.Namespaces, namespaceList)
	op.Status.LastUpdated = timeNow()
	_, err = a.client.OperatorsV1alpha2().OperatorGroups(op.Namespace).UpdateStatus(op)
	if err != nil {
		return err, namespaceList
	}
	return nil, namespaceList
}

func (a *Operator) ensureClusterRoles(op *v1alpha2.OperatorGroup) error {
	currentNamespace := op.GetNamespace()
	csvsInNamespace := a.csvsInNamespace(currentNamespace)
	for _, csv := range csvsInNamespace {
		managerPolicyRules := []rbacv1.PolicyRule{}
		apiEditPolicyRules := []rbacv1.PolicyRule{}
		apiViewPolicyRules := []rbacv1.PolicyRule{}
		for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
			resourceNames := []string{}
			for _, resource := range owned.Resources {
				resourceNames = append(resourceNames, resource.Name)
			}
			managerPolicyRules = append(managerPolicyRules, rbacv1.PolicyRule{Verbs: []string{"*"}, APIGroups: []string{owned.Name}, Resources: resourceNames})
			apiEditPolicyRules = append(apiEditPolicyRules, rbacv1.PolicyRule{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{owned.Name}, Resources: []string{owned.Kind}})
			apiViewPolicyRules = append(apiViewPolicyRules, rbacv1.PolicyRule{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{owned.Name}, Resources: []string{owned.Kind}})
		}
		for _, owned := range csv.Spec.APIServiceDefinitions.Owned {
			resourceNames := []string{}
			for _, resource := range owned.Resources {
				resourceNames = append(resourceNames, resource.Name)
			}
			managerPolicyRules = append(managerPolicyRules, rbacv1.PolicyRule{Verbs: []string{"*"}, APIGroups: []string{owned.Group}, Resources: resourceNames})
			apiEditPolicyRules = append(apiEditPolicyRules, rbacv1.PolicyRule{Verbs: []string{"create", "update", "patch", "delete"}, APIGroups: []string{owned.Group}, Resources: []string{owned.Kind}})
			apiViewPolicyRules = append(apiViewPolicyRules, rbacv1.PolicyRule{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{owned.Group}, Resources: []string{owned.Kind}})
		}

		clusterRole := &rbacv1.ClusterRole{
			Rules: managerPolicyRules,
		}
		ownerutil.AddNonBlockingOwner(clusterRole, csv)
		clusterRole.SetGenerateName(fmt.Sprintf("owned-crd-manager-%s-", csv.Spec.DisplayName))
		_, err := a.OpClient.KubernetesInterface().RbacV1().ClusterRoles().Create(clusterRole)
		if k8serrors.IsAlreadyExists(err) {
			if _, err = a.OpClient.UpdateClusterRole(clusterRole); err != nil {
				return err
			}
		} else if err != nil {
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
				return err
			}
		} else if err != nil {
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
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (a *Operator) annotateDeployments(targetNamespaces []string, targetNamespaceString string) error {
	// write above namespaces to watch in every deployment
	for _, ns := range targetNamespaces {
		deploymentList, err := a.deploymentLister[ns].List(labels.Everything())
		if err != nil {
			return err
		}

		for _, deploy := range deploymentList {
			// TODO: this will be incorrect if two operatorgroups own the same namespace
			// also - will be incorrect if a CSV is manually installed into a namespace
			if !ownerutil.IsOwnedByKind(deploy, "ClusterServiceVersion") {
				continue
			}

			if lastAnnotation, ok := deploy.Spec.Template.Annotations["olm.targetNamespaces"]; ok {
				if lastAnnotation == targetNamespaceString {
					continue
				}
			}

			originalDeploy := deploy.DeepCopy()
			metav1.SetMetaDataAnnotation(&deploy.Spec.Template.ObjectMeta, "olm.targetNamespaces", targetNamespaceString)
			if _, _, err := a.OpClient.PatchDeployment(originalDeploy, deploy); err != nil {
				return err
			}

		}
	}

	return nil
}
