package rh_operators

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	pv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators/v1"
	psVersioned "github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/clientset/versioned"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	pollInterval        = 1 * time.Second
	pollDuration        = 20 * time.Minute
	terminationDuration = 5 * time.Minute

	immediate             = int64(1)
	immediateDeleteOption = &metav1.DeleteOptions{GracePeriodSeconds: &immediate}
)

type operator struct {
	name             string
	namespace        string
	targetNamespaces []string
	operatorGroup    string
	subscription     string
	channel          string
	source           string
	sourceNamespace  string
	csv              string
	installMode      v1alpha1.InstallModeType

	timeoutDuration     time.Duration
	terminationDuration time.Duration

	coreClient     operatorclient.ClientInterface
	packageClient  psVersioned.Interface
	operatorClient versioned.Interface
}

// ListRHOperators lists the PackageManifest of all Red Hat Operators on the existing cluster.
func ListRHOperators(crc psVersioned.Interface) ([]pv1.PackageManifest, error) {
	plist, err := crc.OperatorsV1().PackageManifests("default").List(context.TODO(), metav1.ListOptions{
		LabelSelector: "catalog=redhat-operators",
	})
	if err != nil {
		return nil, fmt.Errorf("error listing Red Hat Operator Packagemanifests")
	}

	if len(plist.Items) == 0 {
		return nil, fmt.Errorf("no Red Hat Operator Packagemanifest is found")
	}
	return plist.Items, nil
}

// ListRHOperatorsByInstallModes lists the names of all Red Hat Operators from Packagemanifests on the existing cluster
// that supports at least one of the support modes.
func ListRHOperatorsByInstallModes(crc psVersioned.Interface, installModes ...v1alpha1.InstallModeType) (
	packageList []string, err error) {
	plist, err := ListRHOperators(crc)
	if err != nil {
		return nil, err
	}

	for _, pm := range plist {
		if packagemanifestSupportsInstallMode(pm, installModes...) {
			packageList = append(packageList, pm.Name)
		}
	}
	return packageList, nil
}

// ListRHOperatorsWithoutInstallModes lists the names of all Red Hat Operators from Packagemanifests on the existing cluster
// that supports at least one of the support modes.
func ListRHOperatorsWithoutInstallModes(crc psVersioned.Interface, installModes ...v1alpha1.InstallModeType) (
	packageList []string, err error) {
	plist, err := ListRHOperators(crc)
	if err != nil {
		return nil, err
	}

	for _, pm := range plist {
		if !packagemanifestSupportsInstallMode(pm, installModes...) {
			packageList = append(packageList, pm.Name)
		}
	}
	return packageList, nil
}

// packagemanifestSupportsInstallMode checks if the default channel of the packagemanifest supports at lest one of
// the install modes.
func packagemanifestSupportsInstallMode(pm pv1.PackageManifest, installModes ...v1alpha1.InstallModeType) bool {
	for _, ch := range pm.Status.Channels {
		if pm.GetDefaultChannel() == ch.Name {
			for _, cim := range ch.CurrentCSVDesc.InstallModes {
				if cim.Supported {
					for _, im := range installModes {
						if im == cim.Type {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// GetAllNamespace gets all namespaces and return a list of names.
func GetAllNamespace(c operatorclient.ClientInterface) (nsList []string, err error) {
	list, err := c.KubernetesInterface().CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, ns := range list.Items {
		nsList = append(nsList, ns.Name)
	}
	return nsList, nil
}

// CleanupOperatorNamespace is a backup clean up function of an operator.
// If regular way of uninstalling an operator has failed, this strategy cleans up all Subscriptions and OperatorGroups
// within the namespace and then deletes the namespace itself.
func CleanupOperatorNamespace(t *testing.T, o *operator) {
	err := o.operatorClient.OperatorsV1alpha1().Subscriptions(o.namespace).DeleteCollection(context.TODO(), *immediateDeleteOption,
		metav1.ListOptions{})
	if err != nil {
		t.Logf("failed to clean all Subscriptions in %s, %v", o.namespace, err)
	}

	err = o.operatorClient.OperatorsV1().OperatorGroups(o.namespace).DeleteCollection(context.TODO(), *immediateDeleteOption,
		metav1.ListOptions{})
	if err != nil {
		t.Logf("failed to clean up all OperatorGroups in %s, %v", o.namespace, err)
	}

	err = o.coreClient.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), o.namespace, *immediateDeleteOption)
	if err != nil {
		t.Logf("failed to delete Namespace %s, %v", o.namespace, err)
	}
}

// CleanupOperatorCSVs deletes the CSVs of an operator in all targetNamespaces and fails if listing targetNamespaces fails.
func CleanupOperatorCSVs(t *testing.T, o *operator) {
	targetNamespaces, err := o.listTargetNamespaces()
	if err != nil {
		t.Logf("failed to list TargetNamespaces, CleanupOperatorCSVs failed")
		return
	}

	for _, ns := range targetNamespaces {
		err = o.operatorClient.OperatorsV1alpha1().ClusterServiceVersions(ns).Delete(context.TODO(), o.csv, *immediateDeleteOption)
		if err != nil && !k8serrors.IsNotFound(err) {
			t.Logf("error deleting CSV: %s from ns:%s, %v", o.csv, ns, err)
		}
	}
}

// CleanupAll deletes all test created CSVs, subscriptions, and namespaces.
func CleanupAll(t *testing.T, c operatorclient.ClientInterface, vc versioned.Interface) {
	CleanupAllCSVs(t, c, vc)
	CleanupAllSubscriptions(t, c, vc)
	CleanupAllTestNamespace(t, c)
}

// CleanupAllCSVs deletes All CSVs other than the packageServer.
func CleanupAllCSVs(t *testing.T, c operatorclient.ClientInterface, vc versioned.Interface) {
	nsList, err := c.KubernetesInterface().CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		t.Logf("failed to list all Namespaces, CleanupAllCSVs failed")
		return
	}
	for _, ns := range nsList.Items {
		if ns.Name == "openshift-operator-lifecycle-manager" {
			csvList, err := vc.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				t.Logf("error listing CSVs in Namespace: %s, %v", ns.Name, err)
			}
			for _, csv := range csvList.Items {
				if csv.Name != "packageserver" {
					err = vc.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).Delete(context.TODO(), csv.Name, *immediateDeleteOption)
					if err != nil {
						t.Logf("error deleting %s in Namespace: %s, %v", csv.Name, ns.Name, err)
					}
				}
			}
		} else {
			err = vc.OperatorsV1alpha1().ClusterServiceVersions(ns.Name).DeleteCollection(context.TODO(), *immediateDeleteOption,
				metav1.ListOptions{})
			if err != nil {
				t.Logf("error deleting CSVs in Namespace: %s, %v", ns.Name, err)
			}
		}
	}
}

// CleanupAllTestNamespace deletes all namespaces that are starting with "test-".
func CleanupAllTestNamespace(t *testing.T, c operatorclient.ClientInterface) {
	nsList, err := GetAllNamespace(c)
	if err != nil {
		t.Logf("error getting namespaces, %v", err)
	}
	for _, ns := range nsList {
		if strings.HasPrefix(ns, "test-") {
			err = c.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), ns, *immediateDeleteOption)
			if err != nil {
				t.Logf("error deleting namespace, %v", err)
			}
		}
	}
}

// CleanupAllSubscriptions deletes all subscriptions from all namespaces.
func CleanupAllSubscriptions(t *testing.T, c operatorclient.ClientInterface, vc versioned.Interface) {
	nsList, err := GetAllNamespace(c)
	if err != nil {
		t.Logf("error getting namespaces, %v", err)
	}
	for _, ns := range nsList {
		err = vc.OperatorsV1alpha1().Subscriptions(ns).DeleteCollection(context.TODO(), *immediateDeleteOption, metav1.ListOptions{})
		if err != nil {
			t.Logf("error deleting all Subscriptions from Namespace: %s, %v", ns, err)
		}
	}
}

// NewOperator creates an operator struct based on operator name while fill in all default values based on
// packagemanifests. It returns error if package is not found on cluster or cluster is not connectable.
func NewOperator(c operatorclient.ClientInterface, crc psVersioned.Interface, vc versioned.Interface,
	operatorName string) (*operator, error) {
	op := operator{
		name:                operatorName,
		coreClient:          c,
		packageClient:       crc,
		operatorClient:      vc,
		timeoutDuration:     pollDuration,
		terminationDuration: terminationDuration,
	}

	err := op.loadOperatorManifest(crc)
	if err != nil {
		return nil, err
	}
	return &op, nil
}

// SetChannel changes the channel of the operator from the default one.
func (o *operator) SetChannel(channel string) {
	o.channel = channel
}

// SetSource changes the source of the operator from the default one.
func (o *operator) SetSource(sourceNamespace, source string) {
	o.sourceNamespace = sourceNamespace
	o.source = source
}

// SetNamespace changes the namespace of the operator. This namespace has to exist.
func (o *operator) SetNamespace(namespace string) {
	o.namespace = namespace
}

// SetOperatorGroup sets the operator group for the operator. The OperatorGroup has to exist.
func (o *operator) SetOperatorGroup(OperatorGroupName string) {
	o.operatorGroup = OperatorGroupName
}

// SetStartingCSV sets the starting CSV. Default CSV is the head of default channel.
func (o *operator) SetStartingCSV(csv string) {
	o.csv = csv
}

// SetInstallMode sets the installMode of the operator.
func (o *operator) SetInstallMode(installMode v1alpha1.InstallModeType) {
	o.installMode = installMode
}

// SetNamespace sets the target namespace of the operator. This namespace has to exist.
func (o *operator) SetTargetNamespace(targetNamespace []string) {
	o.targetNamespaces = targetNamespace
}

// SetTimeoutDuration sets the timeout duration for installing the operator.
func (o *operator) SetTimeoutDuration(timeoutDuration time.Duration) {
	o.timeoutDuration = timeoutDuration
}

func (o *operator) SetTerminationDuration(terminationDuration time.Duration) {
	o.terminationDuration = terminationDuration
}

// Subscribe installs the operator by creating a subscription. If Namespace and OperatorGroup is unset,
// it creates one that is dedicated to the subscription. It waits for the CSV install status to be successful, fail,
// or timeout.
func (o *operator) Subscribe() error {
	if o.namespace == "" {
		nsName, err := o.createNamespace()
		if err != nil {
			return fmt.Errorf("error creating namespace %s, %v", o.namespace, err)
		}
		o.namespace = nsName
	}

	if o.operatorGroup == "" {
		err := o.setTargetNamespacesByInstallMode()
		if err != nil {
			return fmt.Errorf("error setting TargetNamespaces, %v", err)
		}

		ogName, err := o.createOperatorGroup()
		if err != nil {
			return fmt.Errorf("error creating OperatorGroup %s, %v", o.operatorGroup, err)
		}
		o.operatorGroup = ogName
	}

	subName, err := o.createSubscription()
	if err != nil {
		return fmt.Errorf("error creating subscription, %v", err)
	}
	o.subscription = subName

	CSV, err := o.WaitForCSV(csvDoneChecker)
	if err != nil {
		if ipmsg, iperr := o.getInstallPlanMessage(); iperr != nil {
			err = errors.Wrapf(err, "error getting installPlan messages, %v", iperr)
		} else {
			err = errors.Wrap(err, ipmsg)
		}
		return fmt.Errorf("error waiting for CSV to become ready, %v", err)
	}

	if CSV.Status.Phase == v1alpha1.CSVPhaseFailed {
		return fmt.Errorf("operator %s failed to install, %s", o.name, CSV.Status.Message)
	}

	return nil
}

// Unsubscribe uninstalls an operator by deleting its subscription and CSVs from all targetNamespaces.
// Use `true` for the orphan option to delete a subscription without deleting the OperatorGroup and the Namespace
// used by it.
func (o *operator) Unsubscribe(orphan bool) error {
	err := o.removeSubscription()
	if err != nil {
		return fmt.Errorf("error removing Subscription %s, %v", o.subscription, err)
	}

	err = o.removeAllCSVs()
	if err != nil {
		return fmt.Errorf("error removing CSVs from all TargetNamespaces, %v", err)
	}

	if !orphan {
		err = o.removeOperatorGroup()
		if err != nil {
			return fmt.Errorf("error removing OperatorGroup %s, %v", o.operatorGroup, err)
		}

		err = o.removeNamespace()
		if err != nil {
			return fmt.Errorf("error removing Namespace %s, %v", o.namespace, err)
		}

	}
	return nil
}

// removeSubscription deletes the subscription.
func (o *operator) removeSubscription() error {
	return o.operatorClient.OperatorsV1alpha1().Subscriptions(o.namespace).Delete(context.TODO(), o.subscription, metav1.DeleteOptions{})
}

// removeAllCSVs removes CSVs from all target Namespaces.
func (o *operator) removeAllCSVs() error {
	targetNamespaces, err := o.listTargetNamespaces()
	if err != nil {
		return fmt.Errorf("error listing TargetNamespaces, %v", err)
	}

	for _, namespace := range targetNamespaces {
		err := o.operatorClient.OperatorsV1alpha1().ClusterServiceVersions(namespace).Delete(context.TODO(), o.csv,
			metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("error deleting CSV: %s from ns:%s, %v", o.csv, namespace, err)
		}
	}
	return nil
}

// removeOperatorGroup deletes the OperatorGroup.
func (o *operator) removeOperatorGroup() error {
	return o.operatorClient.OperatorsV1().OperatorGroups(o.namespace).Delete(context.TODO(), o.operatorGroup, metav1.DeleteOptions{})
}

// removeNamespace deletes the Namespace.
func (o *operator) removeNamespace() error {
	return o.coreClient.KubernetesInterface().CoreV1().Namespaces().Delete(context.TODO(), o.namespace, metav1.DeleteOptions{})
}

// GetRHOperatorManifest gets the package manifest of a rh-operator and fills default info into operator struct.
// It return error if the package is not found.
func (o *operator) loadOperatorManifest(crc psVersioned.Interface) error {
	pm, err := crc.OperatorsV1().PackageManifests("default").Get(context.TODO(), o.name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error retriving operator %s, %v", o.name, err)
	}

	o.channel = pm.GetDefaultChannel()

	err = o.setToFirstAvailableInstallMode(pm)
	if err != nil {
		return fmt.Errorf("error setting InstallMode, %v", err)
	}

	for _, ch := range pm.Status.Channels {
		if ch.Name == o.channel {
			o.csv = ch.CurrentCSV
		}
	}

	o.source = pm.Status.CatalogSource
	o.sourceNamespace = pm.Status.CatalogSourceNamespace
	return nil
}

// createNamespace creates a test namespace with a generated name that starts with "test-" and returns the name.
func (o *operator) createNamespace() (string, error) {
	ns, err := o.coreClient.KubernetesInterface().CoreV1().Namespaces().Create(context.TODO(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-",
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return ns.Name, nil
}

// createOperatorGroup create a test OperatorGroup with a target namespace and a generated name that starts with "og
// -" and returns the name.
func (o *operator) createOperatorGroup() (string, error) {
	og, err := o.operatorClient.OperatorsV1().OperatorGroups(o.namespace).Create(context.TODO(), &v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("og-%s-", o.name),
			Namespace:    o.namespace,
		},
		Spec: v1.OperatorGroupSpec{TargetNamespaces: o.targetNamespaces},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return og.Name, nil
}

// createSubscription create a test Subscription with a generated name that starts with "sub-" and returns the name.
func (o *operator) createSubscription() (string, error) {
	sub, err := o.operatorClient.OperatorsV1alpha1().Subscriptions(o.namespace).Create(context.TODO(), &v1alpha1.Subscription{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("sub-%s-", o.name),
			Namespace:    o.namespace,
		},
		Spec: &v1alpha1.SubscriptionSpec{
			CatalogSource:          o.source,
			CatalogSourceNamespace: o.sourceNamespace,
			Package:                o.name,
			Channel:                o.channel,
			StartingCSV:            o.csv,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return sub.Name, nil
}

// setToFirstAvailableInstallMode sets the default installMode to be the first available option and returns error if
// channels from packagemanifest does not include the default channel or no installMode is supported for that channel.
func (o *operator) setToFirstAvailableInstallMode(pm *pv1.PackageManifest) error {
	for _, ch := range pm.Status.Channels {

		if ch.Name == pm.GetDefaultChannel() {

			for _, installMode := range pm.Status.Channels[0].CurrentCSVDesc.InstallModes {
				if installMode.Supported {
					o.installMode = installMode.Type
					return nil
				}
			}

			return fmt.Errorf("no supported installMode found for channel %s", pm.GetDefaultChannel())
		}
	}
	return fmt.Errorf("channel %s is not found", pm.GetDefaultChannel())
}

// setTargetNamespacesByInstallMode sets the targetNamespace by the installMode if they are unset.
func (o *operator) setTargetNamespacesByInstallMode() error {
	if len(o.targetNamespaces) > 0 {
		return nil
	}

	if o.installMode == v1alpha1.InstallModeTypeSingleNamespace {
		o.targetNamespaces = []string{"default"}

	} else if o.installMode == v1alpha1.InstallModeTypeOwnNamespace {
		o.targetNamespaces = []string{o.namespace}

	} else if o.installMode == v1alpha1.InstallModeTypeMultiNamespace {
		o.targetNamespaces = []string{o.namespace, "default"}

	} else if o.installMode == v1alpha1.InstallModeTypeAllNamespaces {
		o.targetNamespaces = nil

	} else {
		return fmt.Errorf("%s is an unknown installMode", o.installMode)
	}
	return nil
}

// listTargetNamespaces lists all the target namespaces of an operator. It it is applied to all namespace,
// get all current namespaces.
func (o *operator) listTargetNamespaces() (nsList []string, err error) {
	if o.targetNamespaces == nil {
		return GetAllNamespace(o.coreClient)
	}

	for _, tns := range o.targetNamespaces {

		if tns == "" {
			nsList = append(nsList, "default")

		} else {
			nsList = append(nsList, tns)
		}
	}

	if len(nsList) == 0 {
		return GetAllNamespace(o.coreClient)
	}

	return nsList, nil
}

// getInstallPlanMessage gets the latest message from install plan else return error.
func (o *operator) getInstallPlanMessage() (string, error) {
	sub, err := o.operatorClient.OperatorsV1alpha1().Subscriptions(o.namespace).Get(context.TODO(), o.subscription, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if sub.Status.InstallPlanRef == nil {
		return "", fmt.Errorf("no install plan available for subscription %s", o.subscription)
	}

	installPlan := sub.Status.InstallPlanRef.Name
	ip, err := o.operatorClient.OperatorsV1alpha1().InstallPlans(o.namespace).Get(context.TODO(), installPlan, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(ip.Status.Conditions) > 0 {
		if ip.Status.Phase == v1alpha1.InstallPlanPhaseComplete {
			return fmt.Sprintf("InstallPlan %s Complete", ip.Name), nil
		}

		return fmt.Sprintf("operator %s install plan is in phase: %s, %s, %s", o.name, ip.Status.Phase,
			ip.Status.Conditions[0].Reason, ip.Status.Conditions[0].Message), nil
	}
	return "", fmt.Errorf("installplan has no message")
}

type csvConditionChecker func(csv *v1alpha1.ClusterServiceVersion) bool

// buildCSVConditionChecker checks the CSV condition to match one of the phase defined in the phase set.
func buildCSVConditionChecker(phases ...v1alpha1.ClusterServiceVersionPhase) csvConditionChecker {
	return func(csv *v1alpha1.ClusterServiceVersion) bool {
		conditionMet := false
		for _, phase := range phases {
			conditionMet = conditionMet || csv.Status.Phase == phase
		}
		return conditionMet
	}
}

// csvDoneChecker considers succeeded and failed to be the final CSV phases.
var csvDoneChecker = buildCSVConditionChecker(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVPhaseFailed)

// WaitForCSV waits for the CSV of an operator in all targetNamespaces to have phase updated to a certain states or
// timeout.
func (o *operator) WaitForCSV(checker csvConditionChecker) (fetchedCSV *v1alpha1.ClusterServiceVersion, err error) {
	targetNamespaces, err := o.listTargetNamespaces()
	if err != nil {
		return nil, fmt.Errorf("error listing TargetNamespaces, %v", err)
	}

	err = wait.Poll(pollInterval, o.timeoutDuration, func() (bool, error) {
		passed := true

		for _, namespace := range targetNamespaces {

			fetchedCSV, err = o.operatorClient.OperatorsV1alpha1().ClusterServiceVersions(namespace).Get(context.TODO(), o.csv,
				metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}

			passed = passed && checker(fetchedCSV)
		}
		return passed, nil
	})

	return fetchedCSV, err
}

// WaitToDeleteNamespace waits until namespace is deleted based on terminationDuration of an operator.
func (o *operator) WaitToDeleteNamespace() error {
	err := wait.Poll(pollInterval, o.terminationDuration, func() (bool, error) {
		_, err := o.coreClient.KubernetesInterface().CoreV1().Namespaces().Get(context.TODO(), o.namespace, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}

		return false, nil
	})
	return err
}
