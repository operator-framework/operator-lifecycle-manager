package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/blang/semver"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	watch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"

	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
	porcelainv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain/v1alpha1"
)

// TestInstalledOperator ensures that an InstalledOperator resource is synthesized for a CSV
// and is accessible from all target namespaces.
//
// Steps:
// 1. Create namespaces ns-a, ns-b, ns-c
// 2. Create OperatorGroup, og, in ns-a that targets all namespaces
// 3. Create CatalogSource, cs, in ns-a that contains an any-scoped CSV with no dependencies
// 4. Create Subscription, sub, in ns-a that installs the any-scoped CSV, csv
// 5. Wait for csv to be installed with status Successful
// 6. Ensure an InstalledOperator resource exists in ns-a, with:
//    - uid: ns-a/<csv-uid>
// 	  - namespace: ns-a
//	  - name: csv
// 	  - ClusterServiceVersionRef referencing csv
//    - SubscriptionRef referencing sub
//    - projected CSV fields matching csv
// 	  - projected Subscription fields matching sub
// 7. Ensure InstalledOperator resource exists in ns-b with namespace ns-b, uid ns-b/<csv-uid>, and the additional checks from step 6
// 8. Ensure InstalledOperator resource exists in ns-c with namespace ns-c, uid ns-c/<csv-uid>, and the additional checks from step 6
// 9. Ensure an error is returned when a delete request is issued any of the InstalledOperator resource
// 10. Store the ResourceVersions of the InstalledOperators resources
// 11. Delete sub and ensure the SubscriptionRef field is (eventually) removed from all InstalledOperator resources
// 12. Ensure the ResourceVersion of each InstalledOperator resource has changed
// 13. Update og to target only ns-a
// 14. Ensure that the InstalledOperator resources in ns-b and ns-c no longer exist
// 15. Ensure the InstalledOperator resource in ns-a still exists
// 16. Delete csv and ensure the InstalledOperator resource in ns-a no longer exists
func TestInstalledOperator(t *testing.T) {
	// Create namespaces ns-a, ns-b, and ns-c
	c := newKubeClient(t)
	nsA, nsB, nsC := genName("ns-a-"), genName("ns-b-"), genName("ns-c-")
	for _, ns := range []string{nsA, nsB, nsC} {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(namespace)
		require.NoError(t, err)
		defer func(name string) {
			require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}))
		}(ns)
	}

	// Create OperatorGroup, og, in ns-a that targets all namespaces
	crc := newCRClient(t)
	og := newOperatorGroup(nsA, genName("og-"), nil, nil, nil, false)
	_, err := crc.OperatorsV1().OperatorGroups(og.GetNamespace()).Create(og)
	require.NoError(t, err)

	// Create CatalogSource, cs, in ns-a that contains an any-scoped CSV with no dependencies
	pkg := genName("pkg-")
	csv := newCSV(genName("csv-"), nsA, "", semver.MustParse("0.0.0"), nil, nil, newNginxInstallStrategy(pkg, nil, nil))
	pkgManfiest := registry.PackageManifest{
		PackageName: pkg,
		Channels: []registry.PackageChannel{
			{Name: "stable", CurrentCSVName: csv.GetName()},
		},
	}
	catalog, cleanupCatalog := createInternalCatalogSource(t, c, crc, genName("cs-"), nsA, []registry.PackageManifest{pkgManfiest}, nil, []operatorsv1alpha1.ClusterServiceVersion{csv})
	defer cleanupCatalog()
	_, err = fetchCatalogSource(t, crc, catalog.GetName(), nsA, catalogSourceRegistryPodSynced)
	require.NoError(t, err)

	// Create Subscription, sub, in ns-a that installs the any-scoped CSV, csv
	pc := newPClient(t)
	pw, err := pc.PorcelainV1alpha1().InstalledOperators(metav1.NamespaceAll).Watch(metav1.ListOptions{})
	require.NoError(t, err)
	defer pw.Stop()

	sub := genName("sub-")
	cleanupSubA := createSubscriptionForCatalog(t, crc, nsA, sub, catalog.GetName(), pkg, stableChannel, "", operatorsv1alpha1.ApprovalAutomatic)
	defer cleanupSubA()

	fetchedSub, err := fetchSubscription(t, crc, nsA, sub, subscriptionHasInstallPlanChecker)
	require.NoError(t, err)

	// Wait for csv to be installed with status Successful
	fetchedCSV, err := awaitCSV(t, crc, nsA, csv.GetName(), csvSucceededChecker)
	require.NoError(t, err)

	// Ensure an InstalledOperator resource exists in ns-a, ns-b, and ns-c
	builder := porcelain.NewInstalledOperatorBuilder()
	builder.SetClusterServiceVersion(fetchedCSV)
	builder.SetSubscription(fetchedSub)
	operator, err := builder.Build()
	require.NoError(t, err)

	conditions := conditionCheckers{
		installedOperatorExists(t, operator, nsA),
		installedOperatorExists(t, operator, nsB),
		installedOperatorExists(t, operator, nsC),
	}
	awaitCondition(context.Background(), t, pw, conditions)

	// Ensure an error is returned when a delete request is issued any of the InstalledOperator resource
	deleteOpts := new(metav1.DeleteOptions)
	expected := apierrors.NewForbidden(porcelainv1alpha1.Resource("installedoperators"), operator.GetName(), fmt.Errorf("synthetic resource deletion forbidden")).Error()
	require.Error(t, pc.PorcelainV1alpha1().InstalledOperators(nsA).Delete(operator.GetName(), deleteOpts), expected)
	require.Error(t, pc.PorcelainV1alpha1().InstalledOperators(nsB).Delete(operator.GetName(), deleteOpts), expected)
	require.Error(t, pc.PorcelainV1alpha1().InstalledOperators(nsC).Delete(operator.GetName(), deleteOpts), expected)

	// Store the ResourceVersions of the InstalledOperators resources
	getOpts := metav1.GetOptions{}
	operatorA, err := pc.PorcelainV1alpha1().InstalledOperators(nsA).Get(operator.GetName(), getOpts)
	require.NoError(t, err)
	operatorB, err := pc.PorcelainV1alpha1().InstalledOperators(nsB).Get(operator.GetName(), getOpts)
	require.NoError(t, err)
	operatorC, err := pc.PorcelainV1alpha1().InstalledOperators(nsC).Get(operator.GetName(), getOpts)
	require.NoError(t, err)

	rvA, rvB, rvC := operatorA.GetResourceVersion(), operatorB.GetResourceVersion(), operatorC.GetResourceVersion()
	require.Equal(t, rvB, rvA, "resource version of installedoperator in ns-b doesn't match ns-a")
	require.Equal(t, rvC, rvB, "resource version of installedoperator in ns-c doesn't match ns-b (and ns-a transitively)")

	// Delete sub and ensure the SubscriptionRef field is (eventually) removed and the resource version is updated in InstalledOperator resources
	require.NoError(t, crc.OperatorsV1alpha1().Subscriptions(nsA).Delete(sub, deleteOpts))
	conditions = conditionCheckers{
		installedOperatorMissingSubReference(t, nsA, operator.GetName()),
		installedOperatorMissingSubReference(t, nsB, operator.GetName()),
		installedOperatorMissingSubReference(t, nsC, operator.GetName()),
		metaResourceVersionChanged(t, nsA, operator.GetName(), rvA),
		metaResourceVersionChanged(t, nsB, operator.GetName(), rvB),
		metaResourceVersionChanged(t, nsC, operator.GetName(), rvC),
	}
	awaitCondition(context.Background(), t, pw, conditions)

	// Update og to target only ns-a
	og, err = crc.OperatorsV1().OperatorGroups(nsA).Get(og.GetName(), getOpts)
	require.NoError(t, err)

	og.Spec.TargetNamespaces = []string{nsA}
	_, err = crc.OperatorsV1().OperatorGroups(nsA).Update(og)
	require.NoError(t, err)

	// Ensure that the InstalledOperator resources in ns-b and ns-c no longer exist.
	// Use polling since OperatorGroup resizing doesn't generate delete events for synthetic InstalledOperators
	listOpts := metav1.ListOptions{}
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		operators, err := pc.PorcelainV1alpha1().InstalledOperators(metav1.NamespaceAll).List(listOpts)
		if err != nil || len(operators.Items) == 0 {
			return false, err
		}

		for _, op := range operators.Items {
			if op.GetNamespace() != nsA && op.GetName() == operator.GetName() {
				return false, nil
			}
		}
		return true, nil
	})
	require.NoError(t, err)

	// Ensure the InstalledOperator resource in ns-a still exists
	operatorA, err = pc.PorcelainV1alpha1().InstalledOperators(nsA).Get(operator.GetName(), getOpts)
	require.NoError(t, err)

	// Delete csv and ensure the InstalledOperator resource in ns-a no longer exists
	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(csv.GetName(), deleteOpts))
	awaitCondition(context.Background(), t, pw, metaDeleted(t, nsA, operator.GetName()))
}

// TestInstalledOperatorGC ensures that an InstalledOperator resource can be used for garbage collection.
//
// Steps:
// 1. Create namespaces ns-a and ns-b
// 2. Create OperatorGroup, og, in ns-a that targets all namespaces
// 3. Create an any-scoped CSV, csv, in ns-a
// 4. Wait for csv to be installed with status Successful
// 5. Ensure an InstalledOperator resource exists in ns-a, with:
//	  - uid: ns-a/<csv-uid>
// 	  - namespace: ns-a
//	  - name: csv
// 6. Ensure an InstalledOperator resource exists in ns-b with namespace ns-b, uid ns-b/<csv-uid>, and the additional checks from step 5
// 7. Create a RoleBinding, rb, in ns-b:
//    - granting the admin role to the default ServiceAccount in ns-a
//    - with an OwnerReference to the InstalledOperator resource in ns-b
// 8. Add a label to csv
// 9. Wait for the label to be present on the InstalledOperator resource in ns-a and ns-b
// 10. Ensure rb still exists and is not marked for deletion
// 11. Delete csv and ensure the InstalledOperator resources in ns-a and ns-b no longer exist
// 12. Ensure that rb is (eventually) deleted
func TestInstalledOperatorGC(t *testing.T) {
	// Create namespaces ns-a, ns-b
	c := newKubeClient(t)
	nsA, nsB := genName("ns-a-"), genName("ns-b-")
	for _, ns := range []string{nsA, nsB} {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		}
		_, err := c.KubernetesInterface().CoreV1().Namespaces().Create(namespace)
		require.NoError(t, err)
		defer func(name string) {
			require.NoError(t, c.KubernetesInterface().CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}))
		}(ns)
	}

	// Create OperatorGroup, og, in ns-a that targets all namespaces
	crc := newCRClient(t)
	og := newOperatorGroup(nsA, genName("og-"), nil, nil, nil, false)
	_, err := crc.OperatorsV1().OperatorGroups(og.GetNamespace()).Create(og)
	require.NoError(t, err)

	// Create an any-scoped CSV, csv, in ns-a
	pc := newPClient(t)
	pw, err := pc.PorcelainV1alpha1().InstalledOperators(metav1.NamespaceAll).Watch(metav1.ListOptions{})
	require.NoError(t, err)
	defer pw.Stop()

	csv := newCSV(genName("csv-"), nsA, "", semver.MustParse("0.0.0"), nil, nil, newNginxInstallStrategy("pkg", nil, nil))

	//  Wait for csv to be installed with status Successful
	fetchedCSV, err := crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Create(&csv)
	require.NoError(t, err)

	// Ensure an InstalledOperator resource exists in ns-a and ns-b for csv
	builder := porcelain.NewInstalledOperatorBuilder()
	builder.SetClusterServiceVersion(fetchedCSV)
	operator, err := builder.Build()
	require.NoError(t, err)

	conditions := conditionCheckers{
		installedOperatorExists(t, operator, nsA),
		installedOperatorExists(t, operator, nsB),
	}
	awaitCondition(context.Background(), t, pw, conditions)

	operatorB := operator.DeepCopy()
	m, err := porcelain.InstalledOperatorMetaAccessor(operatorB)
	require.NoError(t, err)
	m.WithNamespace(nsB)

	// Create a RoleBinding, rb, in ns-b
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: nsB,
			Name:      genName("rb-"),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: porcelainv1alpha1.SchemeGroupVersion.String(),
					Kind:       porcelain.InstalledOperatorKind,
					Name:       operator.GetName(),
					UID:        operatorB.GetUID(),
				},
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      "default",
				Namespace: nsA,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "admin",
		},
	}
	rb, err = c.KubernetesInterface().RbacV1().RoleBindings(nsB).Create(rb)
	require.NoError(t, err)

	// Add a label to csv.
	// This lets us check if an update causes premature garbage collection of the dependent.
	key, value := "k", "v"
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() (retryErr error) {
		fetchedCSV, retryErr = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Get(csv.GetName(), metav1.GetOptions{})
		if retryErr != nil {
			return
		}
		if fetchedCSV.GetLabels() == nil {
			fetchedCSV.SetLabels(map[string]string{})
		}
		fetchedCSV.GetLabels()[key] = value
		t.Log("attempting update...")
		fetchedCSV, retryErr = crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Update(fetchedCSV)
		if retryErr != nil {
			t.Log("update failed")
		}
		return
	})
	require.NoError(t, err)

	// Wait for the label to be present on the InstalledOperator resources in ns-a and ns-b
	t.Log("checking for labels")
	conditions = conditionCheckers{
		metaHasLabel(t, nsA, operator.GetName(), key, value),
		metaHasLabel(t, nsB, operator.GetName(), key, value),
	}
	awaitCondition(context.Background(), t, pw, conditions)
	t.Log("labels found")

	// Ensure rb still exists and is not marked for deletion
	rb, err = c.KubernetesInterface().RbacV1().RoleBindings(nsB).Get(rb.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	require.Nil(t, rb.DeletionTimestamp, "rolebinding prematurely marked for deletion")

	// Delete csv and ensure the InstalledOperator resource in ns-a and ns-b no longer exist
	rw, err := c.KubernetesInterface().RbacV1().RoleBindings(nsB).Watch(metav1.ListOptions{})
	require.NoError(t, err)
	defer rw.Stop()

	require.NoError(t, crc.OperatorsV1alpha1().ClusterServiceVersions(nsA).Delete(csv.GetName(), new(metav1.DeleteOptions)))

	conditions = conditionCheckers{
		metaDeleted(t, nsA, operator.GetName()),
		metaDeleted(t, nsB, operator.GetName()),
	}
	awaitCondition(context.Background(), t, pw, conditions)

	// Ensure that rb is (eventually) deleted
	t.Log("checking for rb deleted event")
	awaitCondition(context.Background(), t, rw, metaDeleted(t, nsB, rb.GetName()))
	t.Log("rb deleted")
}

type conditionChecker interface {
	conditionMet(t *testing.T, event watch.Event) (met bool)
}

type conditionMetFunc func(t *testing.T, event watch.Event) (met bool)

func (c conditionMetFunc) conditionMet(t *testing.T, event watch.Event) bool {
	return c(t, event)
}

type conditionCheckers []conditionChecker

func (c conditionCheckers) conditionMet(t *testing.T, event watch.Event) bool {
	met := true
	for _, checker := range c {
		met = checker.conditionMet(t, event) && met
	}

	return met
}

func awaitCondition(ctx context.Context, t *testing.T, w watch.Interface, checker conditionChecker) {
	met := false
	for !met {
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
			return
		case event, ok := <-w.ResultChan():
			if !ok {
				return
			}
			met = checker.conditionMet(t, event)
		}
	}
}

type metaConditionMetFunc func(t *testing.T, action watch.EventType, found metav1.Object) (met bool)

func (fn metaConditionMetFunc) conditionMet(t *testing.T, event watch.Event) bool {
	m, err := meta.Accessor(event.Object)
	require.NoError(t, err)
	return fn(t, event.Type, m)
}

func metaResourceVersionChanged(t *testing.T, namespace, name, resourceVersion string) metaConditionMetFunc {
	met := false
	return func(t *testing.T, action watch.EventType, found metav1.Object) bool {
		if met || found.GetNamespace() != namespace || found.GetName() != name {
			return met
		}

		switch action {
		case watch.Added, watch.Modified:
			met = found.GetResourceVersion() != resourceVersion
		default:
			require.FailNow(t, "unexpected event type: (%s, %v)", action, found)
		}

		return met
	}
}

func metaDeleted(t *testing.T, namespace, name string) metaConditionMetFunc {
	met := false
	return func(t *testing.T, action watch.EventType, found metav1.Object) bool {
		if met || found.GetNamespace() != namespace || found.GetName() != name {
			return met
		}

		switch action {
		case watch.Deleted:
			met = true
		case watch.Added, watch.Modified:
			// Make sure it's not re-added
			met = false
		default:
			require.FailNow(t, "unexpected event type: (%s, %v)", action, found)
		}

		return met
	}
}

func metaHasLabel(t *testing.T, namespace, name, key, value string) metaConditionMetFunc {
	met := false
	return func(t *testing.T, action watch.EventType, found metav1.Object) bool {
		if met || found.GetNamespace() != namespace || found.GetName() != name {
			return met
		}

		switch action {
		case watch.Added, watch.Modified:
			if labels := found.GetLabels(); labels != nil {
				met = labels[key] == value
			}
		default:
			require.FailNow(t, "unexpected event type: (%s, %v)", action, found)
		}

		return met
	}
}

type installedOperatorConditionMetFunc func(t *testing.T, action watch.EventType, found *porcelain.InstalledOperator) (met bool)

func (fn installedOperatorConditionMetFunc) conditionMet(t *testing.T, event watch.Event) bool {
	operator := new(porcelain.InstalledOperator)
	require.NoError(t, scheme.Scheme.Convert(event.Object, operator, nil))
	return fn(t, event.Type, operator)
}

func installedOperatorExists(t *testing.T, want *porcelain.InstalledOperator, namespace string) installedOperatorConditionMetFunc {
	// Update the installed operator to match the given target namespace
	want = want.DeepCopy()
	m, err := porcelain.InstalledOperatorMetaAccessor(want)
	require.NoError(t, err)
	m.WithNamespace(namespace)
	m.Sanitize()

	// Unset resource versions since they may differ from expected
	if want.SubscriptionRef != nil {
		want.SubscriptionRef.ResourceVersion = ""
	}
	if want.ClusterServiceVersionRef != nil {
		want.ClusterServiceVersionRef.ResourceVersion = ""
	}

	met := false
	return func(t *testing.T, action watch.EventType, found *porcelain.InstalledOperator) bool {
		if met || found.GetNamespace() != want.GetNamespace() || found.GetName() != want.GetName() {
			return met
		}

		switch action {
		case watch.Added, watch.Modified:
			met = equality.Semantic.DeepDerivative(want, found)
		default:
			require.FailNow(t, "unexpected event type: (%s, %v)", action, found)
		}

		return met
	}
}

func installedOperatorMissingSubReference(t *testing.T, namespace, name string) installedOperatorConditionMetFunc {
	met := false
	return func(t *testing.T, action watch.EventType, found *porcelain.InstalledOperator) bool {
		if met || found.GetNamespace() != namespace || found.GetName() != name {
			return met
		}

		switch action {
		case watch.Added, watch.Modified:
			met = found.SubscriptionRef == nil
		default:
			require.FailNow(t, "unexpected event type: (%s, %v)", action, found)
		}

		return met
	}
}
