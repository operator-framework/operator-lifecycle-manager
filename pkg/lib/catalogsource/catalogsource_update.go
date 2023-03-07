package catalogsource

import (
	"context"
	"reflect"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
)

/*
UpdateStatus can be used to update the status of the provided catalog source. Note that
the caller is responsible for ensuring accurate status values in the catsrc argument (i.e.
the status is used as-is).

• logger: used to log errors only

• client: used to fetch / update catalog source status

• catsrc: the CatalogSource to use as a source for status updates. Callers are
responsible for updating the catalog source status values as necessary.
*/
func UpdateStatus(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource) error {

	// make the status update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).UpdateStatus(context.TODO(), catsrc, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("UpdateStatus - error while setting CatalogSource status")
		return err
	}

	return nil
}

/*
UpdateStatusWithConditions can be used to update the status conditions for the provided catalog source.
This function will make no changes to the other status fields (those fields will be used as-is).
If the provided conditions do not result in any status condition changes, then the API server will not be updated.
Note that the caller is responsible for ensuring accurate status values for all other fields.

• logger: used to log errors only

• client: used to fetch / update catalog source status

• catsrc: the CatalogSource to use as a source for status updates.

• conditions: condition values to be updated
*/
func UpdateStatusWithConditions(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource, conditions ...metav1.Condition) error {

	// make a copy of the status before we make the change
	statusBefore := catsrc.Status.DeepCopy()

	// update the conditions
	for _, condition := range conditions {
		meta.SetStatusCondition(&catsrc.Status.Conditions, condition)
	}

	// don't bother updating if no changes were made
	if reflect.DeepEqual(catsrc.Status.Conditions, statusBefore.Conditions) {
		logger.Debug("UpdateStatusWithConditions - request to update status conditions did not result in any changes, so updates were not made")
		return nil
	}

	// make the update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).UpdateStatus(context.TODO(), catsrc, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("UpdateStatusWithConditions - unable to update CatalogSource image reference")
		return err
	}
	return nil
}

/*
UpdateSpecAndStatusConditions can be used to update the catalog source with the provided status conditions.
This will update the spec and status portions of the catalog source. Calls to the API server will occur
even if the provided conditions result in no changes.

• logger: used to log errors only

• client: used to fetch / update catalog source

• catsrc: the CatalogSource to use as a source for image and status condition updates.

• conditions: condition values to be updated
*/
func UpdateSpecAndStatusConditions(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource, conditions ...metav1.Condition) error {

	// update the conditions
	for _, condition := range conditions {
		meta.SetStatusCondition(&catsrc.Status.Conditions, condition)
	}

	// make the update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).Update(context.TODO(), catsrc, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("UpdateSpecAndStatusConditions - unable to update CatalogSource image reference")
		return err
	}
	return nil
}

/*
RemoveStatusConditions can be used to remove the status conditions for the provided catalog source.
This function will make no changes to the other status fields (those fields will be used as-is).
If the provided conditions do not result in any status condition changes, then the API server will not be updated.
Note that the caller is responsible for ensuring accurate status values for all other fields.

• logger: used to log errors only

• client: used to fetch / update catalog source status

• catsrc: the CatalogSource to use as a source for status condition removal.

• conditionTypes: condition types to be removed
*/
func RemoveStatusConditions(logger *logrus.Entry, client versioned.Interface, catsrc *v1alpha1.CatalogSource, conditionTypes ...string) error {

	// make a copy of the status before we make the change
	statusBefore := catsrc.Status.DeepCopy()

	// remove the conditions
	for _, conditionType := range conditionTypes {
		meta.RemoveStatusCondition(&catsrc.Status.Conditions, conditionType)
	}

	// don't bother updating if no changes were made
	if reflect.DeepEqual(catsrc.Status.Conditions, statusBefore.Conditions) {
		logger.Debug("RemoveStatusConditions - request to remove status conditions did not result in any changes, so updates were not made")
		return nil
	}

	// make the update if possible
	if _, err := client.OperatorsV1alpha1().CatalogSources(catsrc.GetNamespace()).UpdateStatus(context.TODO(), catsrc, metav1.UpdateOptions{}); err != nil {
		logger.WithError(err).Error("RemoveStatusConditions - unable to update CatalogSource image reference")
		return err
	}
	return nil
}
