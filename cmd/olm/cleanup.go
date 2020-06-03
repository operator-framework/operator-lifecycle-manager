package main

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	apiregv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute
)

type checkResourceFunc func() error
type deleteResourceFunc func() error
type mutateMeta func(obj metav1.Object) (mutated bool)

func cleanup(logger *logrus.Logger, c operatorclient.ClientInterface, crc versioned.Interface) {
	if err := waitForDelete(checkCatalogSource(crc, "olm-operators"), deleteCatalogSource(crc, "olm-operators")); err != nil {
		logger.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkConfigMap(c, "olm-operators"), deleteConfigMap(c, "olm-operators")); err != nil {
		logger.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkSubscription(crc, "packageserver"), deleteSubscription(crc, "packageserver")); err != nil {
		logger.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkClusterServiceVersion(crc, "packageserver.v0.10.0"), deleteClusterServiceVersion(crc, "packageserver.v0.10.0")); err != nil {
		logger.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkClusterServiceVersion(crc, "packageserver.v0.10.1"), deleteClusterServiceVersion(crc, "packageserver.v0.10.0")); err != nil {
		logger.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkClusterServiceVersion(crc, "packageserver.v0.9.0"), deleteClusterServiceVersion(crc, "packageserver.v0.9.0")); err != nil {
		logger.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := cleanupOwnerReferences(c, crc); err != nil {
		logger.WithError(err).Fatal("couldn't cleanup cross-namespace ownerreferences")
	}

	if err := ensureAPIServiceLabels(c.ApiregistrationV1Interface()); err != nil {
		logger.WithError(err).Fatal("couldn't ensure APIService labels")
	}
}

func waitForDelete(checkResource checkResourceFunc, deleteResource deleteResourceFunc) error {
	if err := checkResource(); err != nil && k8serrors.IsNotFound(err) {
		return nil
	}
	if err := deleteResource(); err != nil {
		return err
	}
	err := wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		err := checkResource()
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})

	return err
}

func checkClusterServiceVersion(crc versioned.Interface, name string) checkResourceFunc {
	return func() error {
		_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(*namespace).Get(context.TODO(), name, metav1.GetOptions{})
		return err
	}
}

func deleteClusterServiceVersion(crc versioned.Interface, name string) deleteResourceFunc {
	return func() error {
		return crc.OperatorsV1alpha1().ClusterServiceVersions(*namespace).Delete(context.TODO(), name, *metav1.NewDeleteOptions(0))
	}
}

func checkSubscription(crc versioned.Interface, name string) checkResourceFunc {
	return func() error {
		_, err := crc.OperatorsV1alpha1().Subscriptions(*namespace).Get(context.TODO(), name, metav1.GetOptions{})
		return err
	}
}

func deleteSubscription(crc versioned.Interface, name string) deleteResourceFunc {
	return func() error {
		return crc.OperatorsV1alpha1().Subscriptions(*namespace).Delete(context.TODO(), name, *metav1.NewDeleteOptions(0))
	}
}

func checkConfigMap(c operatorclient.ClientInterface, name string) checkResourceFunc {
	return func() error {
		_, err := c.KubernetesInterface().CoreV1().ConfigMaps(*namespace).Get(context.TODO(), name, metav1.GetOptions{})
		return err
	}
}

func deleteConfigMap(c operatorclient.ClientInterface, name string) deleteResourceFunc {
	return func() error {
		return c.KubernetesInterface().CoreV1().ConfigMaps(*namespace).Delete(context.TODO(), name, *metav1.NewDeleteOptions(0))
	}
}

func checkCatalogSource(crc versioned.Interface, name string) checkResourceFunc {
	return func() error {
		_, err := crc.OperatorsV1alpha1().CatalogSources(*namespace).Get(context.TODO(), name, metav1.GetOptions{})
		return err
	}
}

func deleteCatalogSource(crc versioned.Interface, name string) deleteResourceFunc {
	return func() error {
		return crc.OperatorsV1alpha1().CatalogSources(*namespace).Delete(context.TODO(), name, *metav1.NewDeleteOptions(0))
	}
}

// cleanupOwnerReferences cleans up inter-namespace and cluster-to-namespace scoped OwnerReferences to ClusterServiceVersions.
//
// Cross-namespace and cluster-to-namespace scoped OwnerReferences may cause sibling resources in the owner namespace to be
// deleted sporadically (see CVE-2019-3884 https://access.redhat.com/security/cve/cve-2019-3884). Older versions of OLM use both types of
// OwnerReference, and in cases where OLM is updated, they must be removed to prevent erroneous deletion of OLM's self-hosted components.
func cleanupOwnerReferences(c operatorclient.ClientInterface, crc versioned.Interface) error {
	listOpts := metav1.ListOptions{}
	csvs, err := crc.OperatorsV1alpha1().ClusterServiceVersions(metav1.NamespaceAll).List(context.TODO(), listOpts)
	if err != nil {
		return err
	}

	uidNamespaces := map[types.UID]string{}
	for _, csv := range csvs.Items {
		uidNamespaces[csv.GetUID()] = csv.GetNamespace()
	}
	removeBadRefs := crossNamespaceOwnerReferenceRemoval(v1alpha1.ClusterServiceVersionKind, uidNamespaces)

	// Cleanup cross-namespace OwnerReferences on CSVs, ClusterRoles/Bindings, and Roles/Bindings
	var objs []metav1.Object
	for _, obj := range csvs.Items {
		objs = append(objs, &obj)
	}

	clusterRoles, _ := c.KubernetesInterface().RbacV1().ClusterRoles().List(context.TODO(), listOpts)
	for _, obj := range clusterRoles.Items {
		objs = append(objs, &obj)
	}

	clusterRoleBindings, _ := c.KubernetesInterface().RbacV1().ClusterRoleBindings().List(context.TODO(), listOpts)
	for _, obj := range clusterRoleBindings.Items {
		objs = append(objs, &obj)
	}

	roles, _ := c.KubernetesInterface().RbacV1().Roles(metav1.NamespaceAll).List(context.TODO(), listOpts)
	for _, obj := range roles.Items {
		objs = append(objs, &obj)
	}
	roleBindings, _ := c.KubernetesInterface().RbacV1().RoleBindings(metav1.NamespaceAll).List(context.TODO(), listOpts)
	for _, obj := range roleBindings.Items {
		objs = append(objs, &obj)
	}

	apiServices, _ := c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().List(context.TODO(), listOpts)
	for _, obj := range apiServices.Items {
		objs = append(objs, &obj)
	}

	for _, obj := range objs {
		if !removeBadRefs(obj) {
			continue
		}

		update := func() error {
			// If this is not a type we care about, do nothing
			return nil
		}
		switch v := obj.(type) {
		case *v1alpha1.ClusterServiceVersion:
			update = func() error {
				_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(v.GetNamespace()).Update(context.TODO(), v, metav1.UpdateOptions{})
				return err
			}
		case *rbacv1.ClusterRole:
			update = func() error {
				_, err = c.KubernetesInterface().RbacV1().ClusterRoles().Update(context.TODO(), v, metav1.UpdateOptions{})
				return err
			}
		case *rbacv1.ClusterRoleBinding:
			update = func() error {
				_, err = c.KubernetesInterface().RbacV1().ClusterRoleBindings().Update(context.TODO(), v, metav1.UpdateOptions{})
				return err
			}
		case *rbacv1.Role:
			update = func() error {
				_, err = c.KubernetesInterface().RbacV1().Roles(v.GetNamespace()).Update(context.TODO(), v, metav1.UpdateOptions{})
				return err
			}
		case *rbacv1.RoleBinding:
			update = func() error {
				_, err = c.KubernetesInterface().RbacV1().RoleBindings(v.GetNamespace()).Update(context.TODO(), v, metav1.UpdateOptions{})
				return err
			}
		case *apiregv1.APIService:
			update = func() error {
				_, err = c.ApiregistrationV1Interface().ApiregistrationV1().APIServices().Update(context.TODO(), v, metav1.UpdateOptions{})
				return err
			}
		}

		if err := retry.RetryOnConflict(retry.DefaultBackoff, update); err != nil {
			return err
		}
	}

	return nil
}

func crossNamespaceOwnerReferenceRemoval(kind string, uidNamespaces map[types.UID]string) mutateMeta {
	return func(obj metav1.Object) (mutated bool) {
		var cleanRefs []metav1.OwnerReference
		objNamespace := obj.GetNamespace()
		for _, ref := range obj.GetOwnerReferences() {
			if ref.Kind == kind {
				refNamespace, ok := uidNamespaces[ref.UID]
				if !ok || (refNamespace != metav1.NamespaceAll && refNamespace != objNamespace) {
					mutated = true
					continue
				}
			}

			cleanRefs = append(cleanRefs, ref)
		}

		if mutated {
			obj.SetOwnerReferences(cleanRefs)
		}

		return
	}
}

// ensureAPIServiceLabels checks the labels of existing APIService objects to ensure ownership is set correctly.
// If the APIService label does not correspond to the expected packageserver owner the APIService labels are updated
func ensureAPIServiceLabels(c clientset.Interface) error {
	apiService, err := c.ApiregistrationV1().APIServices().Get(context.TODO(), olm.PackageserverName, metav1.GetOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		logrus.WithField("apiService", "packageserver").Debugf("get error: %s", err)
		return err
	}
	if apiService == nil || k8serrors.IsNotFound(err) || apiService.Name == "" {
		logrus.WithField("apiService", "packageserver").Debugf("not found")
		return nil
	}

	if l := apiService.GetLabels(); l == nil {
		apiService.Labels = make(map[string]string)
	}

	if apiService.Labels[ownerutil.OwnerKey] != ownerutil.OwnerPackageServer {
		apiService.Labels[ownerutil.OwnerKey] = ownerutil.OwnerPackageServer
		update := func() error {
			_, err := c.ApiregistrationV1().APIServices().Update(context.TODO(), apiService, metav1.UpdateOptions{})
			return err
		}
		if err := retry.RetryOnConflict(retry.DefaultBackoff, update); err != nil {
			logrus.WithField("apiService", apiService.Name).Warnf("update error: %s", err)
			return err
		}
	}

	return nil
}
