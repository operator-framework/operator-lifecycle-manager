package catalogsource

import (
	"context"
	"reflect"
	"sync"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// mu is a package scoped mutex for synchronizing catalog source updates
var mu sync.Mutex

/* UpdateStatus can be used to safely update the status of the provided catalog source. Note that the
status values are updated to the values from catsrc in their entirety when using this function.

• logger: used to log errors only

• client: used to fetch / update catalog source status

• catsrc: the CatalogSource to use as a source for status updates. Callers are
responsible for updating the catalog source status values as necessary.
*/
func UpdateStatus(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource) error {
	mu.Lock()
	defer mu.Unlock()

	// get the absolute latest update of this catalog source in case it changed
	latest, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Get(context.TODO(), catsrc.GetName(), metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Error("UpdateStatus - error getting latest CatalogSource... cannot update image reference")
		return err
	}

	// make copy (even though we're not making changes and using the status values as-is)
	out := latest.DeepCopy()
	out.Status = catsrc.Status

	// make the status update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(out.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("UpdateStatus - error while setting CatalogSource status")
		return err
	}

	return nil
}

/* UpdateStatusCondition can be used to safely update the status conditions for the provided catalog source.
This function will make no other changes to the status.

• logger: used to log errors only

• client: used to fetch / update catalog source status

• catsrc: the CatalogSource to use as a source for status updates.

• conditions: condition values to be updated
*/
func UpdateStatusCondition(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource, conditions ...metav1.Condition) error {
	mu.Lock()
	defer mu.Unlock()

	// get the absolute latest update of this catalog source in case it changed
	latest, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Get(context.TODO(), catsrc.GetName(), metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Error("UpdateStatusCondition - error getting latest CatalogSource... cannot update image reference")
		return err
	}

	// make copy and update the image and conditions only
	out := latest.DeepCopy()

	for _, condition := range conditions {
		meta.SetStatusCondition(&out.Status.Conditions, condition)
	}

	// don't bother updating if no changes were made
	if reflect.DeepEqual(out.Status.Conditions, latest.Status.Conditions) {
		logger.Debug("UpdateStatusCondition - request to update status conditions did not result in any changes, so updates were not made")
		return nil
	}

	// make the update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("UpdateStatusCondition - unable to update CatalogSource image reference")
		return err
	}
	return nil
}

/* UpdateImageReferenceAndStatusCondition can be used to safely update the image reference and status conditions for the provided catalog source.
This function will make no other changes to the catalog source.

• logger: used to log errors only

• client: used to fetch / update catalog source

• catsrc: the CatalogSource to use as a source for image and status condition updates.

• conditions: condition values to be updated
*/
func UpdateImageReferenceAndStatusCondition(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource, conditions ...metav1.Condition) error {
	mu.Lock()
	defer mu.Unlock()

	// get the absolute latest update of this catalog source in case it changed
	latest, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Get(context.TODO(), catsrc.GetName(), metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Error("UpdateImageReferenceAndStatusCondition - error getting latest CatalogSource... cannot update image reference")
		return err
	}

	// make copy and update the image and conditions only
	out := latest.DeepCopy()
	out.Spec.Image = catsrc.Spec.Image

	for _, condition := range conditions {
		meta.SetStatusCondition(&out.Status.Conditions, condition)
	}

	// make the update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Update(context.TODO(), out, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("UpdateImageReferenceAndStatusCondition - unable to update CatalogSource image reference")
		return err
	}
	return nil
}

/* RemoveStatusConditions can be used to safely remove the status conditions for the provided catalog source.
This function will make no other changes to the status.

• logger: used to log errors only

• client: used to fetch / update catalog source status

• catsrc: the CatalogSource to use as a source for status condition removal.

• conditionTypes: condition types to be removed
*/
func RemoveStatusConditions(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource, conditionTypes ...string) error {
	mu.Lock()
	defer mu.Unlock()

	// get the absolute latest update of this catalog source in case it changed
	latest, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Get(context.TODO(), catsrc.GetName(), metav1.GetOptions{})
	if err != nil {
		logger.WithError(err).Error("RemoveStatusConditions - error getting latest CatalogSource... cannot update image reference")
		return err
	}

	// make copy and update the conditions only
	out := latest.DeepCopy()
	for _, conditionType := range conditionTypes {
		meta.RemoveStatusCondition(&out.Status.Conditions, conditionType)
	}

	// don't bother updating if no changes were made
	if reflect.DeepEqual(out.Status.Conditions, latest.Status.Conditions) {
		logger.Debug("RemoveStatusConditions - request to remove status conditions did not result in any changes, so updates were not made")
		return nil
	}

	// make the update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).UpdateStatus(context.TODO(), out, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("RemoveStatusConditions - unable to update CatalogSource image reference")
		return err
	}
	return nil
}
