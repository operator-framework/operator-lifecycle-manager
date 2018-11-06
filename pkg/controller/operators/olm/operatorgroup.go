package olm

import (
	"fmt"
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

	var nsList []string
	for ix := range targetedNamespaces {
		nsList = append(nsList, targetedNamespaces[ix].GetName())
	}
	nsListJoined := strings.Join(nsList, ",")

	if err := a.annotateDeployments(op, nsListJoined); err != nil {
		log.Errorf("annotateDeployments error: %v", err)
		return err
	}
	log.Debug("Deployment annotation completed")

	// annotate csvs
	csvsInNamespace := a.csvsInNamespace(op.Namespace)
	for _, csv := range csvsInNamespace {
		origCSVannotations := csv.GetAnnotations()
		a.addAnnotationsToObjectMeta(&csv.ObjectMeta, op, nsListJoined)
		if reflect.DeepEqual(origCSVannotations, csv.GetAnnotations()) == false {
			// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(csv.GetNamespace()).Update(csv); err != nil {
				log.Errorf("Update for existing CSV failed: %v", err)
			}
		}

		if err := a.copyCsvToTargetNamespace(csv, op, targetedNamespaces); err != nil {
			return err
		}
	}
	log.Debug("CSV annotation completed")
	//TODO: ensure RBAC on operator serviceaccount

	return nil
}

func (a *Operator) copyCsvToTargetNamespace(csv *v1alpha1.ClusterServiceVersion, operatorGroup *v1alpha2.OperatorGroup, targetNamespaces []*corev1.Namespace) error {
	for _, ns := range targetNamespaces {
		if ns.Name == operatorGroup.GetNamespace() {
			continue
		}
		// create new CSV instead of DeepCopy as namespace and resource version (and status) will be different
		newCSV := v1alpha1.ClusterServiceVersion{
			ObjectMeta: metav1.ObjectMeta{
				Name:        csv.Name,
				Annotations: csv.Annotations,
			},
			Spec: *csv.Spec.DeepCopy(),
		}
		newCSV.SetNamespace(ns.Name)

		log.Debugf("Copying/updating CSV %v to/in namespace %v", csv.GetName(), ns.Name)
		createdCSV, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).Create(&newCSV)
		if err == nil {
			createdCSV.Status = v1alpha1.ClusterServiceVersionStatus{
				Message:        "CSV copied to target namespace",
				Reason:         v1alpha1.CSVReasonCopied,
				LastUpdateTime: timeNow(),
			}
			if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).UpdateStatus(createdCSV); err != nil {
				log.Errorf("Status update for CSV failed: %v", err)
				return err
			}
		}
		if k8serrors.IsAlreadyExists(err) {
			fetchedCSV, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).Get(csv.GetName(), metav1.GetOptions{})
			if err != nil {
				log.Errorf("Create failed, yet get failed: %v", err)
			}
			if reflect.DeepEqual(fetchedCSV.Annotations, csv.Annotations) == false {
				// CRDs don't support strategic merge patching, but in the future if they do this should be updated to patch
				if _, err := a.client.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).Update(fetchedCSV); err != nil {
					log.Errorf("Update CSV in target namespace failed: %v", err)
					return err
				}
			}
		} else if err != nil {
			log.Errorf("Create for new CSV failed: %v", err)
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

func (a *Operator) updateNamespaceList(op *v1alpha2.OperatorGroup) ([]*corev1.Namespace, error) {
	selector, err := metav1.LabelSelectorAsSelector(&op.Spec.Selector)
	if err != nil {
		return nil, err
	}

	namespaceList, err := a.lister.CoreV1().NamespaceLister().List(selector)
	if err != nil {
		return nil, err
	}

	if !namespacesChanged(namespaceList, op.Status.Namespaces) {
		// status is current with correct namespaces, so no further updates required
		return namespaceList, nil
	}
	log.Debugf("Namespace change detected, found: %v", namespaceList)
	op.Status.Namespaces = make([]*corev1.Namespace, len(namespaceList))
	copy(op.Status.Namespaces, namespaceList)
	op.Status.LastUpdated = timeNow()
	_, err = a.client.OperatorsV1alpha2().OperatorGroups(op.Namespace).UpdateStatus(op)
	if err != nil {
		return namespaceList, err
	}
	return namespaceList, nil
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
