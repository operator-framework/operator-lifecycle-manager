package olm

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

// The fixtures model one operator installed by an InstallPlan: the CSV, its
// Deployment, its ServiceAccount, and the RBAC the InstallPlan created. The
// plan status lists every object as a Present step with an inline manifest.

const (
	cleanupNamespace  = "ns"
	cleanupCSVName    = "csv1"
	cleanupCSVUID     = types.UID("csv1-uid")
	cleanupDeployment = "a1"
	cleanupSA         = "sa"
	cleanupPlanName   = "install-1"
)

var cleanupRBACNames = map[string]string{
	"Role":               "csv1-role",
	"RoleBinding":        "csv1-rolebinding",
	"ClusterRole":        "csv1-clusterrole",
	"ClusterRoleBinding": "csv1-clusterrolebinding",
}

// cleanupStepKinds are the step kinds the cleanup deletes. ServiceAccounts
// are plan steps too but are never deleted.
var cleanupStepKinds = []string{"Role", "RoleBinding", "ClusterRole", "ClusterRoleBinding"}

func cleanupOperatorGroup(static bool) *operatorsv1.OperatorGroup {
	return &operatorsv1.OperatorGroup{
		TypeMeta: metav1.TypeMeta{Kind: "OperatorGroup", APIVersion: operatorsv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: cleanupNamespace,
			// Already annotated, otherwise the first sync only annotates the group and returns.
			Annotations: map[string]string{operatorsv1.OperatorGroupProvidedAPIsAnnotationKey: "c1.v1.g1"},
		},
		Spec:   operatorsv1.OperatorGroupSpec{StaticProvidedAPIs: static},
		Status: operatorsv1.OperatorGroupStatus{Namespaces: []string{cleanupNamespace}},
	}
}

func cleanupAnnotations() map[string]string {
	return map[string]string{
		operatorsv1.OperatorGroupTargetsAnnotationKey:   cleanupNamespace,
		operatorsv1.OperatorGroupNamespaceAnnotationKey: cleanupNamespace,
		operatorsv1.OperatorGroupAnnotationKey:          "default",
	}
}

var unsupportedInstallModes = []v1alpha1.InstallMode{{Type: v1alpha1.InstallModeTypeSingleNamespace, Supported: false}}

// cleanupCSV needs a Role for its ServiceAccount, so the requirements stay
// unmet while the RBAC the InstallPlan created is missing.
func cleanupCSV(phase v1alpha1.ClusterServiceVersionPhase, reason v1alpha1.ConditionReason) *v1alpha1.ClusterServiceVersion {
	permissions := []v1alpha1.StrategyDeploymentPermissions{{
		ServiceAccountName: cleanupSA,
		Rules:              []rbacv1.PolicyRule{{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"configmaps"}}},
	}}
	out := withConditionReason(csvWithAnnotations(csv(cleanupCSVName,
		cleanupNamespace,
		"0.0.0",
		"",
		installStrategy(cleanupDeployment, permissions, nil),
		[]*apiextensionsv1.CustomResourceDefinition{crd("c1", "v1", "g1")},
		[]*apiextensionsv1.CustomResourceDefinition{},
		phase,
	), cleanupAnnotations()), reason)
	out.UID = cleanupCSVUID
	return out
}

// failedUnsupportedCSV is a Succeeded CSV that fails on the next sync with
// UnsupportedOperatorGroup.
func failedUnsupportedCSV() *v1alpha1.ClusterServiceVersion {
	return withInstallModes(cleanupCSV(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful), unsupportedInstallModes)
}

func ownedDeployment(csv *v1alpha1.ClusterServiceVersion) runtime.Object {
	d := deployment(cleanupDeployment, cleanupNamespace, cleanupSA, cleanupAnnotations())
	ownerutil.AddNonBlockingOwner(d, csv)
	return d
}

// ownedRBAC returns the ServiceAccount, Role, RoleBinding, ClusterRole and
// ClusterRoleBinding the InstallPlan created for the CSV.
func ownedRBAC(csv *v1alpha1.ClusterServiceVersion) []runtime.Object {
	l := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	l[install.OLMManagedLabelKey] = install.OLMManagedLabelValue
	namespaced := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: csv.GetNamespace(), Labels: l}
	}
	clusterScoped := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Labels: l}
	}
	return []runtime.Object{
		serviceAccount(cleanupSA, cleanupNamespace),
		&rbacv1.Role{ObjectMeta: namespaced(cleanupRBACNames["Role"])},
		&rbacv1.RoleBinding{ObjectMeta: namespaced(cleanupRBACNames["RoleBinding"])},
		&rbacv1.ClusterRole{ObjectMeta: clusterScoped(cleanupRBACNames["ClusterRole"])},
		&rbacv1.ClusterRoleBinding{ObjectMeta: clusterScoped(cleanupRBACNames["ClusterRoleBinding"])},
	}
}

func cleanupK8sObjs(csv *v1alpha1.ClusterServiceVersion) []runtime.Object {
	return append([]runtime.Object{ownedDeployment(csv)}, ownedRBAC(csv)...)
}

// completedInstallPlan returns the Complete InstallPlan that installed the
// CSV. Every step is Present with an inline manifest.
func completedInstallPlan() *v1alpha1.InstallPlan {
	return installPlanInPhase(v1alpha1.InstallPlanPhaseComplete)
}

func installPlanInPhase(phase v1alpha1.InstallPlanPhase) *v1alpha1.InstallPlan {
	start := metav1.Now()
	step := func(kind, name string) *v1alpha1.Step {
		return &v1alpha1.Step{
			Resolving: cleanupCSVName,
			Status:    v1alpha1.StepStatusPresent,
			Resource:  v1alpha1.StepResource{Kind: kind, Name: name, Manifest: `{"kind":"` + kind + `"}`},
		}
	}
	return &v1alpha1.InstallPlan{
		TypeMeta:   metav1.TypeMeta{Kind: v1alpha1.InstallPlanKind, APIVersion: v1alpha1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Name: cleanupPlanName, Namespace: cleanupNamespace},
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: []string{cleanupCSVName},
			Approval:                   v1alpha1.ApprovalAutomatic,
			Approved:                   true,
		},
		Status: v1alpha1.InstallPlanStatus{
			Phase:     phase,
			StartTime: &start,
			Plan: []*v1alpha1.Step{
				step(v1alpha1.ClusterServiceVersionKind, cleanupCSVName),
				step("CustomResourceDefinition", "c1"),
				step("ServiceAccount", cleanupSA),
				step("Role", cleanupRBACNames["Role"]),
				step("RoleBinding", cleanupRBACNames["RoleBinding"]),
				step("ClusterRole", cleanupRBACNames["ClusterRole"]),
				step("ClusterRoleBinding", cleanupRBACNames["ClusterRoleBinding"]),
			},
		},
	}
}

// cleanedUpInstallPlan is the plan as deletePlanSteps leaves it: Complete,
// the RBAC steps NotPresent.
func cleanedUpInstallPlan() *v1alpha1.InstallPlan {
	plan := completedInstallPlan()
	for _, step := range plan.Status.Plan {
		if slices.Contains(cleanupStepKinds, step.Resource.Kind) {
			step.Status = v1alpha1.StepStatusNotPresent
		}
	}
	return plan
}

func stepStatuses(plan *v1alpha1.InstallPlan) map[string]v1alpha1.StepStatus {
	statuses := map[string]v1alpha1.StepStatus{}
	for _, step := range plan.Status.Plan {
		statuses[step.Resource.Kind] = step.Status
	}
	return statuses
}

func stepObjectExists(t *testing.T, op *Operator, kind string) bool {
	t.Helper()
	var err error
	switch kind {
	case "ServiceAccount":
		_, err = op.opClient.GetServiceAccount(cleanupNamespace, cleanupSA)
	case "Role":
		_, err = op.opClient.GetRole(cleanupNamespace, cleanupRBACNames[kind])
	case "RoleBinding":
		_, err = op.opClient.GetRoleBinding(cleanupNamespace, cleanupRBACNames[kind])
	case "ClusterRole":
		_, err = op.opClient.GetClusterRole(cleanupRBACNames[kind])
	case "ClusterRoleBinding":
		_, err = op.opClient.GetClusterRoleBinding(cleanupRBACNames[kind])
	}
	if err == nil {
		return true
	}
	require.True(t, apierrors.IsNotFound(err), "%s: %v", kind, err)
	return false
}

func deploymentExists(t *testing.T, op *Operator) bool {
	t.Helper()
	_, err := op.opClient.GetDeployment(cleanupNamespace, cleanupDeployment)
	if err == nil {
		return true
	}
	require.True(t, apierrors.IsNotFound(err), "deployment: %v", err)
	return false
}

// requireStepObjects asserts for every RBAC step kind whether its object
// exists. The ServiceAccount must exist in every case.
func requireStepObjects(t *testing.T, op *Operator, exist bool) {
	t.Helper()
	for _, kind := range cleanupStepKinds {
		require.Equal(t, exist, stepObjectExists(t, op, kind), "%s exists", kind)
	}
	require.True(t, stepObjectExists(t, op, "ServiceAccount"), "ServiceAccount must never be deleted")
}

// requirePlan asserts the phase of the stored plan and the status of its
// RBAC steps. The ServiceAccount, CSV and CRD steps must stay Present.
func requirePlan(t *testing.T, ctx context.Context, op *Operator, phase v1alpha1.InstallPlanPhase, steps v1alpha1.StepStatus) *v1alpha1.InstallPlan {
	t.Helper()
	plan, err := op.client.OperatorsV1alpha1().InstallPlans(cleanupNamespace).Get(ctx, cleanupPlanName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, phase, plan.Status.Phase)
	statuses := stepStatuses(plan)
	for _, kind := range cleanupStepKinds {
		require.Equal(t, steps, statuses[kind], "step %s", kind)
	}
	require.Equal(t, v1alpha1.StepStatusPresent, statuses[v1alpha1.ClusterServiceVersionKind], "CSV step")
	require.Equal(t, v1alpha1.StepStatusPresent, statuses["CustomResourceDefinition"], "CRD step")
	require.Equal(t, v1alpha1.StepStatusPresent, statuses["ServiceAccount"], "ServiceAccount step")
	return plan
}

func getCSV(t *testing.T, ctx context.Context, op *Operator) *v1alpha1.ClusterServiceVersion {
	t.Helper()
	got, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(cleanupNamespace).Get(ctx, cleanupCSVName, metav1.GetOptions{})
	require.NoError(t, err)
	return got
}

func newCleanupOperator(t *testing.T, ctx context.Context, csv *v1alpha1.ClusterServiceVersion, clientObjs, k8sObjs []runtime.Object, reconciler APIIntersectionReconciler) *Operator {
	t.Helper()
	op, err := NewFakeOperator(
		ctx,
		withNamespaces(cleanupNamespace, "kube-system"),
		withClientObjs(append(clientObjs, csv)...),
		withK8sObjs(k8sObjs...),
		withExtObjs(crd("c1", "v1", "g1")),
		withPartialMetadata(&metav1.PartialObjectMetadata{ObjectMeta: csv.ObjectMeta}),
		withOperatorNamespace(cleanupNamespace),
		withAPIReconciler(reconciler),
	)
	require.NoError(t, err)
	return op
}

// failingReactor makes every call with the verb and resource fail.
func failingReactor(msg string) clienttesting.ReactionFunc {
	return func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New(msg)
	}
}

func operatorsFake(op *Operator) *fake.ReactionForwardingClientsetDecorator {
	return op.client.(*fake.ReactionForwardingClientsetDecorator)
}

func kubeFake(op *Operator) *k8sfake.Clientset {
	return op.opClient.KubernetesInterface().(*k8sfake.Clientset)
}

// recover syncs a Failed CSV once so it goes back to Pending, waits for the
// informer cache, and syncs the Pending CSV once more. The second sync is
// where the InstallPlan is reopened. It returns the error of the second sync.
func recover(t *testing.T, ctx context.Context, op *Operator, csv *v1alpha1.ClusterServiceVersion) error {
	t.Helper()
	require.NoError(t, op.syncClusterServiceVersion(csv))
	require.Eventually(t, func() bool {
		got, err := op.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(cleanupNamespace).Get(cleanupCSVName)
		return err == nil && got.Status.Phase == v1alpha1.CSVPhasePending
	}, 5*time.Second, 10*time.Millisecond, "CSV did not reach Pending")
	return op.syncClusterServiceVersion(getCSV(t, ctx, op))
}

// TestFailedCSVCleanup: a Succeeded operator whose CSV fails because of its
// OperatorGroup loses its Deployment. It loses the objects of its InstallPlan
// steps when the plan is Complete, and the plan records that as NotPresent
// steps. Otherwise the objects stay and the plan is untouched.
func TestFailedCSVCleanup(t *testing.T) {
	tests := []struct {
		name         string
		reason       v1alpha1.ConditionReason
		installModes []v1alpha1.InstallMode
		static       bool
		reconciler   APIIntersectionReconciler
		installPlan  *v1alpha1.InstallPlan
		wantDeleted  bool
	}{
		{
			name:         "UnsupportedOperatorGroup",
			reason:       v1alpha1.CSVReasonUnsupportedOperatorGroup,
			installModes: unsupportedInstallModes,
			installPlan:  completedInstallPlan(),
			wantDeleted:  true,
		},
		{
			name:         "UnsupportedOperatorGroup/NoInstallPlan",
			reason:       v1alpha1.CSVReasonUnsupportedOperatorGroup,
			installModes: unsupportedInstallModes,
			installPlan:  nil,
			wantDeleted:  false,
		},
		{
			name:         "UnsupportedOperatorGroup/InstallPlanStillInstalling",
			reason:       v1alpha1.CSVReasonUnsupportedOperatorGroup,
			installModes: unsupportedInstallModes,
			installPlan:  installPlanInPhase(v1alpha1.InstallPlanPhaseInstalling),
			wantDeleted:  false,
		},
		{
			name:         "UnsupportedOperatorGroup/InstallPlanFailed",
			reason:       v1alpha1.CSVReasonUnsupportedOperatorGroup,
			installModes: unsupportedInstallModes,
			installPlan:  installPlanInPhase(v1alpha1.InstallPlanPhaseFailed),
			wantDeleted:  false,
		},
		{
			name:         "UnsupportedOperatorGroup/InstallPlanRequiresApproval",
			reason:       v1alpha1.CSVReasonUnsupportedOperatorGroup,
			installModes: unsupportedInstallModes,
			installPlan:  installPlanInPhase(v1alpha1.InstallPlanPhaseRequiresApproval),
			wantDeleted:  false,
		},
		{
			name:        "InterOperatorGroupOwnerConflict",
			reason:      v1alpha1.CSVReasonInterOperatorGroupOwnerConflict,
			reconciler:  buildFakeAPIIntersectionReconcilerThatReturns(APIConflict),
			installPlan: completedInstallPlan(),
			wantDeleted: true,
		},
		{
			name:        "CannotModifyStaticOperatorGroupProvidedAPIs",
			reason:      v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs,
			static:      true,
			reconciler:  buildFakeAPIIntersectionReconcilerThatReturns(AddAPIs),
			installPlan: completedInstallPlan(),
			wantDeleted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			csv := cleanupCSV(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful)
			if tt.installModes != nil {
				csv = withInstallModes(csv, tt.installModes)
			}
			clientObjs := []runtime.Object{cleanupOperatorGroup(tt.static)}
			if tt.installPlan != nil {
				clientObjs = append(clientObjs, tt.installPlan)
			}
			op := newCleanupOperator(t, ctx, csv, clientObjs, cleanupK8sObjs(csv), tt.reconciler)

			require.NoError(t, op.syncClusterServiceVersion(csv))

			got := getCSV(t, ctx, op)
			require.Equal(t, v1alpha1.CSVPhaseFailed, got.Status.Phase)
			require.Equal(t, tt.reason, got.Status.Reason)
			require.False(t, deploymentExists(t, op), "deployment exists")
			requireStepObjects(t, op, !tt.wantDeleted)
			if tt.installPlan != nil {
				steps := v1alpha1.StepStatusPresent
				if tt.wantDeleted {
					steps = v1alpha1.StepStatusNotPresent
				}
				requirePlan(t, ctx, op, tt.installPlan.Status.Phase, steps)
			}
		})
	}
}

// TestFailedCSVCleanupThenRecovery: the whole cycle on one operator. Fail,
// clean up, fix the install modes, recover, and the plan is reopened for the
// steps the cleanup removed.
func TestFailedCSVCleanupThenRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv,
		[]runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()},
		cleanupK8sObjs(csv), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))
	requireStepObjects(t, op, false)
	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusNotPresent)

	// The user fixes the install modes.
	failed := getCSV(t, ctx, op)
	failed.Spec.InstallModes = cleanupCSV(v1alpha1.CSVPhaseFailed, "").Spec.InstallModes
	fixed, err := op.client.OperatorsV1alpha1().ClusterServiceVersions(cleanupNamespace).Update(ctx, failed, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		got, err := op.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(cleanupNamespace).Get(cleanupCSVName)
		return err == nil && len(got.Spec.InstallModes) == len(fixed.Spec.InstallModes)
	}, 5*time.Second, 10*time.Millisecond, "informer did not see the fixed install modes")

	require.ErrorIs(t, recover(t, ctx, op, fixed), ErrRequirementsNotMet)
	plan := requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseInstalling, v1alpha1.StepStatusNotPresent)
	require.Nil(t, plan.Status.StartTime, "startTime must be cleared so the install timeout starts again")
	require.Equal(t, corev1.ConditionUnknown, plan.Status.GetCondition(v1alpha1.InstallPlanInstalled).Status)
}

// TestFailedCSVCleanupIgnoresForgedInstallPlan: a newer InstallPlan that
// only names the CSV in its spec, without the status the catalog-operator
// writes, does not count. The real plan is used.
func TestFailedCSVCleanupIgnoresForgedInstallPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	forged := &v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "install-forged", Namespace: cleanupNamespace, CreationTimestamp: metav1.NewTime(time.Now().Add(time.Hour))},
		Spec:       v1alpha1.InstallPlanSpec{ClusterServiceVersionNames: []string{cleanupCSVName}},
	}
	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv,
		[]runtime.Object{cleanupOperatorGroup(false), completedInstallPlan(), forged},
		cleanupK8sObjs(csv), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	requireStepObjects(t, op, false)
	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusNotPresent)
}

// TestFailedCSVCleanupIgnoresOtherInstallPlans: the plan of another operator
// in the namespace does not count as the plan of this CSV.
func TestFailedCSVCleanupIgnoresOtherInstallPlans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	other := completedInstallPlan()
	other.Name = "install-other"
	other.Spec.ClusterServiceVersionNames = []string{"other-csv"}
	for _, step := range other.Status.Plan {
		step.Resolving = "other-csv"
	}
	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv,
		[]runtime.Object{cleanupOperatorGroup(false), other},
		cleanupK8sObjs(csv), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	require.False(t, deploymentExists(t, op), "deployment exists")
	requireStepObjects(t, op, true)
}

// TestFailedCSVCleanupUsesNewestInstallPlan: with two plans for the CSV the
// newer one decides. The newer one is Complete, so the objects are deleted.
func TestFailedCSVCleanupUsesNewestInstallPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	older := installPlanInPhase(v1alpha1.InstallPlanPhaseInstalling)
	older.Name = "install-0"
	older.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Hour))
	newer := completedInstallPlan()
	newer.CreationTimestamp = metav1.NewTime(time.Now())

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv,
		[]runtime.Object{cleanupOperatorGroup(false), older, newer},
		cleanupK8sObjs(csv), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	requireStepObjects(t, op, false)
}

// TestFailedCSVCleanupManifestReferences: steps of a bundle-image install
// refer to the ConfigMap of the unpacked bundle. The objects are deleted only
// while that ConfigMap exists, because the catalog-operator needs it to
// create them again.
func TestFailedCSVCleanupManifestReferences(t *testing.T) {
	ref := `{"kind":"ConfigMap","name":"bundle-cm","namespace":"ns","catalogSourceName":"cat","catalogSourceNamespace":"ns"}`
	tests := []struct {
		name        string
		configMap   bool
		wantDeleted bool
	}{
		{"ConfigMapMissing", false, false},
		{"ConfigMapPresent", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			plan := completedInstallPlan()
			for _, step := range plan.Status.Plan {
				step.Resource.Manifest = ref
			}
			csv := failedUnsupportedCSV()
			k8sObjs := cleanupK8sObjs(csv)
			if tt.configMap {
				k8sObjs = append(k8sObjs, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "bundle-cm", Namespace: cleanupNamespace}})
			}
			op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), plan}, k8sObjs, nil)

			require.NoError(t, op.syncClusterServiceVersion(csv))

			requireStepObjects(t, op, !tt.wantDeleted)
		})
	}
}

// TestFailedCSVCleanupEmptyManifest: a step without a manifest cannot be
// applied again, nothing is deleted. The step belongs to another CSV of the
// same plan: ExecutePlan resolves every step of the plan.
func TestFailedCSVCleanupEmptyManifest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	plan := completedInstallPlan()
	plan.Status.Plan = append(plan.Status.Plan, &v1alpha1.Step{Resolving: "other-csv", Status: v1alpha1.StepStatusPresent, Resource: v1alpha1.StepResource{Kind: "ClusterServiceVersion", Name: "other-csv"}})
	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), plan}, cleanupK8sObjs(csv), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	requireStepObjects(t, op, true)
}

// TestFailedCSVCleanupDeletesWebhooks: webhook configurations of the CSV are
// deleted with the Deployment, so no webhook points at a dead backend.
func TestFailedCSVCleanupDeletesWebhooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	labels := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
	webhooks := []runtime.Object{
		&admissionregistrationv1.ValidatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "csv1-validating", Labels: labels}},
		&admissionregistrationv1.MutatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "csv1-mutating", Labels: labels}},
		&admissionregistrationv1.ValidatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "other-validating"}},
	}
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false)}, append(cleanupK8sObjs(csv), webhooks...), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	admission := op.opClient.KubernetesInterface().AdmissionregistrationV1()
	_, err := admission.ValidatingWebhookConfigurations().Get(ctx, "csv1-validating", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err), "validating webhook of the CSV: %v", err)
	_, err = admission.MutatingWebhookConfigurations().Get(ctx, "csv1-mutating", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err), "mutating webhook of the CSV: %v", err)
	_, err = admission.ValidatingWebhookConfigurations().Get(ctx, "other-validating", metav1.GetOptions{})
	require.NoError(t, err, "webhook of another owner must stay")
}

// TestFailedCSVCleanupKeepsForeignDeployment: a Deployment with the name
// from the install strategy that another CSV owns is kept.
func TestFailedCSVCleanupKeepsForeignDeployment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	other := cleanupCSV(v1alpha1.CSVPhaseSucceeded, v1alpha1.CSVReasonInstallSuccessful)
	other.Name, other.UID = "other-csv", "other-uid"
	op := newCleanupOperator(t, ctx, csv,
		[]runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()},
		append([]runtime.Object{ownedDeployment(other)}, ownedRBAC(csv)...), nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	require.True(t, deploymentExists(t, op), "foreign deployment must stay")
	requireStepObjects(t, op, false)
}

// TestFailedCSVCleanupErrors: an API error during the cleanup is returned so
// the sync is retried, and the parts that did not fail still happen.
func TestFailedCSVCleanupErrors(t *testing.T) {
	tests := []struct {
		name           string
		breakOperators func(*fake.ReactionForwardingClientsetDecorator)
		breakKube      func(*k8sfake.Clientset)
		wantErr        string
		wantDeployment bool
		skipDeployment bool
		wantObjects    map[string]bool
		wantSteps      map[string]v1alpha1.StepStatus
	}{
		{
			name: "InstallPlanListFails",
			breakOperators: func(c *fake.ReactionForwardingClientsetDecorator) {
				c.PrependReactor("list", "installplans", failingReactor("list installplans"))
			},
			wantErr:     "list installplans",
			wantObjects: map[string]bool{"ServiceAccount": true, "Role": true, "RoleBinding": true, "ClusterRole": true, "ClusterRoleBinding": true},
		},
		{
			name: "RoleDeleteFails",
			breakKube: func(c *k8sfake.Clientset) {
				c.PrependReactor("delete", "roles", failingReactor("delete role"))
			},
			wantErr:     "delete role",
			wantObjects: map[string]bool{"ServiceAccount": true, "Role": true, "RoleBinding": false, "ClusterRole": false, "ClusterRoleBinding": false},
			wantSteps:   map[string]v1alpha1.StepStatus{"Role": v1alpha1.StepStatusNotPresent, "RoleBinding": v1alpha1.StepStatusNotPresent},
		},
		{
			name: "RoleGetFails",
			breakKube: func(c *k8sfake.Clientset) {
				c.PrependReactor("get", "roles", failingReactor("get role"))
			},
			wantErr:     "get role",
			wantObjects: map[string]bool{"ServiceAccount": true, "RoleBinding": false, "ClusterRole": false, "ClusterRoleBinding": false},
			wantSteps:   map[string]v1alpha1.StepStatus{"Role": v1alpha1.StepStatusPresent, "RoleBinding": v1alpha1.StepStatusNotPresent},
		},
		{
			name: "DeploymentDeleteFails",
			breakKube: func(c *k8sfake.Clientset) {
				c.PrependReactor("delete", "deployments", failingReactor("delete deployment"))
			},
			wantErr:        "delete deployment",
			wantDeployment: true,
			wantObjects:    map[string]bool{"ServiceAccount": true, "Role": false, "RoleBinding": false, "ClusterRole": false, "ClusterRoleBinding": false},
		},
		{
			name: "DeploymentGetFails",
			breakKube: func(c *k8sfake.Clientset) {
				c.PrependReactor("get", "deployments", failingReactor("get deployment"))
			},
			wantErr:        "get deployment",
			skipDeployment: true,
			wantObjects:    map[string]bool{"ServiceAccount": true, "Role": false, "RoleBinding": false, "ClusterRole": false, "ClusterRoleBinding": false},
		},
		{
			name: "WebhookListFails",
			breakKube: func(c *k8sfake.Clientset) {
				c.PrependReactor("list", "validatingwebhookconfigurations", failingReactor("list webhooks"))
			},
			wantErr:     "list webhooks",
			wantObjects: map[string]bool{"ServiceAccount": true, "Role": false, "RoleBinding": false, "ClusterRole": false, "ClusterRoleBinding": false},
		},
		{
			name: "InstallPlanUpdateStatusFails/NothingDeleted",
			breakOperators: func(c *fake.ReactionForwardingClientsetDecorator) {
				c.PrependReactor("update", "installplans", failingReactor("update installplans"))
			},
			wantErr:     "update installplans",
			wantObjects: map[string]bool{"ServiceAccount": true, "Role": true, "RoleBinding": true, "ClusterRole": true, "ClusterRoleBinding": true},
			wantSteps:   map[string]v1alpha1.StepStatus{"Role": v1alpha1.StepStatusPresent},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			csv := failedUnsupportedCSV()
			op := newCleanupOperator(t, ctx, csv,
				[]runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()},
				cleanupK8sObjs(csv), nil)
			if tt.breakOperators != nil {
				tt.breakOperators(operatorsFake(op))
			}
			if tt.breakKube != nil {
				tt.breakKube(kubeFake(op))
			}

			require.EqualError(t, op.syncClusterServiceVersion(csv), tt.wantErr)

			require.Equal(t, v1alpha1.CSVReasonUnsupportedOperatorGroup, getCSV(t, ctx, op).Status.Reason)
			if !tt.skipDeployment {
				require.Equal(t, tt.wantDeployment, deploymentExists(t, op), "deployment exists")
			}
			for kind, want := range tt.wantObjects {
				require.Equal(t, want, stepObjectExists(t, op, kind), "%s exists", kind)
			}
			if tt.wantSteps != nil {
				plan, err := op.client.OperatorsV1alpha1().InstallPlans(cleanupNamespace).Get(ctx, cleanupPlanName, metav1.GetOptions{})
				require.NoError(t, err)
				statuses := stepStatuses(plan)
				for kind, want := range tt.wantSteps {
					require.Equal(t, want, statuses[kind], "step %s", kind)
				}
			}
		})
	}
}

// TestFailedCSVCleanupRequeueFailure: when the CSV cannot be requeued while
// its InstallPlan still runs, the cleanup logs and returns. An empty queue
// set makes RequeueAfter fail.
func TestFailedCSVCleanupRequeueFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{installPlanInPhase(v1alpha1.InstallPlanPhaseInstalling)}, nil, nil)
	op.csvQueueSet = queueinformer.NewEmptyResourceQueueSet()

	logger, hook := logrustest.NewNullLogger()
	require.NoError(t, op.cleanupFailedCSV(logrus.NewEntry(logger), csv))

	messages := []string{}
	for _, entry := range hook.AllEntries() {
		messages = append(messages, entry.Message)
	}
	require.Contains(t, messages, "unable to requeue")
}

// TestPendingCSVReopensCleanedUpPlan: a Pending CSV with unmet requirements
// whose InstallPlan records NotPresent steps gets its plan reopened.
func TestPendingCSVReopensCleanedUpPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := cleanupCSV(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown)
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, nil, nil)

	require.ErrorIs(t, op.syncClusterServiceVersion(csv), ErrRequirementsNotMet)

	plan := requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseInstalling, v1alpha1.StepStatusNotPresent)
	require.Nil(t, plan.Status.StartTime)
}

// TestPendingCSVLeavesIntactPlanAlone: a Pending CSV whose RBAC is missing
// but whose InstallPlan has no NotPresent steps (someone else removed the
// RBAC, for example an administrator who revoked a binding) does not get
// its plan reopened. OLM only recreates what it removed itself.
func TestPendingCSVLeavesIntactPlanAlone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := cleanupCSV(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown)
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, nil, nil)
	operatorsFake(op).PrependReactor("update", "installplans", failingReactor("must not write the InstallPlan"))

	require.ErrorIs(t, op.syncClusterServiceVersion(csv), ErrRequirementsNotMet)

	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusPresent)
}

// TestFailedCSVRecoveryForEachReason: whatever reason the CSV failed with
// last, once it is back in Pending a cleaned-up plan is reopened. The reason
// can change while Failed, for example from UnsupportedOperatorGroup to
// NoOperatorGroup when the user replaces the OperatorGroup.
func TestFailedCSVRecoveryForEachReason(t *testing.T) {
	tests := []struct {
		name   string
		reason v1alpha1.ConditionReason
		static bool
	}{
		{"UnsupportedOperatorGroup", v1alpha1.CSVReasonUnsupportedOperatorGroup, false},
		{"InterOperatorGroupOwnerConflict", v1alpha1.CSVReasonInterOperatorGroupOwnerConflict, false},
		{"CannotModifyStaticOperatorGroupProvidedAPIs", v1alpha1.CSVReasonCannotModifyStaticOperatorGroupProvidedAPIs, true},
		{"NoOperatorGroup", v1alpha1.CSVReasonNoOperatorGroup, false},
		{"TooManyOperatorGroups", v1alpha1.CSVReasonTooManyOperatorGroups, false},
		{"NoTargetNamespaces", v1alpha1.CSVReasonNoTargetNamespaces, false},
		{"InvalidInstallModes", v1alpha1.CSVReasonInvalidInstallModes, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			csv := cleanupCSV(v1alpha1.CSVPhaseFailed, tt.reason)
			op := newCleanupOperator(t, ctx, csv,
				[]runtime.Object{cleanupOperatorGroup(tt.static), cleanedUpInstallPlan()},
				nil,
				buildFakeAPIIntersectionReconcilerThatReturns(NoAPIConflict))

			require.ErrorIs(t, recover(t, ctx, op, csv), ErrRequirementsNotMet)

			requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseInstalling, v1alpha1.StepStatusNotPresent)
		})
	}
}

// TestFailedCSVRecoveryWithoutInstallPlan: a CSV that no InstallPlan created
// recovers as before, nothing to reinstall.
func TestFailedCSVRecoveryWithoutInstallPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := cleanupCSV(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonUnsupportedOperatorGroup)
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false)}, nil, nil)

	require.ErrorIs(t, recover(t, ctx, op, csv), ErrRequirementsNotMet)
	require.Equal(t, v1alpha1.CSVPhasePending, getCSV(t, ctx, op).Status.Phase)
}

// TestPendingCSVReopenErrors: when the InstallPlan cannot be read or written
// the sync returns the error and the plan is left as it was, so the reopen
// is retried.
func TestPendingCSVReopenErrors(t *testing.T) {
	tests := []struct {
		name    string
		verb    string
		wantErr string
	}{
		{"InstallPlanListFails", "list", "list installplans"},
		{"InstallPlanUpdateStatusFails", "update", "update installplans"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			csv := cleanupCSV(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown)
			op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, nil, nil)
			operatorsFake(op).PrependReactor(tt.verb, "installplans", failingReactor(tt.wantErr))

			require.EqualError(t, op.syncClusterServiceVersion(csv), tt.wantErr)

			requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusNotPresent)
		})
	}
}

// TestCleanupCSVDeploymentsInvalidStrategy: a CSV whose install strategy
// cannot be parsed is skipped, nothing is deleted.
func TestCleanupCSVDeploymentsInvalidStrategy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	csv.Spec.InstallStrategy.StrategyName = "not-a-strategy"
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false)}, cleanupK8sObjs(csv), nil)

	require.NoError(t, op.cleanupCSVDeployments(logrus.NewEntry(logrus.New()), csv))
	require.True(t, deploymentExists(t, op), "deployment exists")
}

// otherStrategy is an install strategy the resolver can parse but that is
// not a deployment strategy.
type otherStrategy struct {
	install.StrategyResolverInterface
}

func (otherStrategy) GetStrategyName() string { return "other" }

func (o otherStrategy) UnmarshalStrategy(v1alpha1.NamedInstallStrategy) (install.Strategy, error) {
	return o, nil
}

// TestCleanupCSVDeploymentsNonDeploymentStrategy: a parsed strategy that is
// not a deployment strategy is skipped, nothing is deleted.
func TestCleanupCSVDeploymentsNonDeploymentStrategy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false)}, cleanupK8sObjs(csv), nil)
	op.resolver = otherStrategy{StrategyResolverInterface: op.resolver}

	require.NoError(t, op.cleanupCSVDeployments(logrus.NewEntry(logrus.New()), csv))
	require.True(t, deploymentExists(t, op), "deployment exists")
}

// TestDeleteStepObjectUnknownKind: a step of a kind the cleanup does not
// handle is an error, not a silent skip.
func TestDeleteStepObjectUnknownKind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, nil, nil, nil)

	step := &v1alpha1.Step{Resource: v1alpha1.StepResource{Kind: "Secret", Name: "s"}}
	_, err := op.deleteStepObject(cleanupNamespace, step, "uid")
	require.EqualError(t, err, "step kind Secret is not deleted on failure")
	_, err = op.getStepObject(cleanupNamespace, step)
	require.EqualError(t, err, "step kind Secret is not deleted on failure")
}

// TestPendingCSVReopensPlanEvenWhenRequirementsAreMet: the reopen is a pure
// check of the marks. A CSV whose requirements happen to be met (here it
// declares no permissions) still gets its plan reopened and moves on.
func TestPendingCSVReopensPlanEvenWhenRequirementsAreMet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := cleanupCSV(v1alpha1.CSVPhasePending, v1alpha1.CSVReasonRequirementsUnknown)
	csv.Spec.InstallStrategy = installStrategy(cleanupDeployment, nil, nil)
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, nil, nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseInstalling, v1alpha1.StepStatusNotPresent)
	require.Equal(t, v1alpha1.CSVPhaseInstallReady, getCSV(t, ctx, op).Status.Phase)
}

// TestFailedCSVCleanupAlreadyCleanedPlan: a second sync of a Failed CSV
// whose plan already records the cleanup writes nothing.
func TestFailedCSVCleanupAlreadyCleanedPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, nil, nil)
	operatorsFake(op).PrependReactor("update", "installplans", failingReactor("must not write the InstallPlan"))

	require.NoError(t, op.syncClusterServiceVersion(csv))
	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusNotPresent)
}

// TestFailedCSVCleanupMissingObjects: objects that are already gone were
// removed by someone else, for example an administrator who revoked a
// binding. Their steps stay Present, so recovery does not bring them back.
func TestFailedCSVCleanupMissingObjects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, []runtime.Object{serviceAccount(cleanupSA, cleanupNamespace)}, nil)
	operatorsFake(op).PrependReactor("update", "installplans", failingReactor("must not write the InstallPlan"))

	require.NoError(t, op.syncClusterServiceVersion(csv))
	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusPresent)
}

// TestFailedCSVCleanupRecreatedObjects: InstallPlan execution is at least
// once. When a stale catalog-operator sync creates the objects again after
// the cleanup marked their steps, the next sync deletes them again without
// another status write.
func TestFailedCSVCleanupRecreatedObjects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, cleanupK8sObjs(csv), nil)
	operatorsFake(op).PrependReactor("update", "installplans", failingReactor("must not write the InstallPlan"))

	require.NoError(t, op.syncClusterServiceVersion(csv))
	requireStepObjects(t, op, false)
	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseComplete, v1alpha1.StepStatusNotPresent)
}

// TestFailedCSVCleanupForeignStepObject: an object at a step name that
// another owner holds is kept and its step stays Present.
func TestFailedCSVCleanupForeignStepObject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	objs := cleanupK8sObjs(csv)
	for _, o := range objs {
		if role, ok := o.(*rbacv1.Role); ok {
			role.Labels = map[string]string{ownerutil.OwnerKey: "other-csv", ownerutil.OwnerNamespaceKey: cleanupNamespace, ownerutil.OwnerKind: v1alpha1.ClusterServiceVersionKind}
		}
	}
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, objs, nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	require.True(t, stepObjectExists(t, op, "Role"), "foreign Role must stay")
	for _, kind := range []string{"RoleBinding", "ClusterRole", "ClusterRoleBinding"} {
		require.False(t, stepObjectExists(t, op, kind), "%s exists", kind)
	}
	plan, err := op.client.OperatorsV1alpha1().InstallPlans(cleanupNamespace).Get(ctx, cleanupPlanName, metav1.GetOptions{})
	require.NoError(t, err)
	statuses := stepStatuses(plan)
	require.Equal(t, v1alpha1.StepStatusPresent, statuses["Role"])
	require.Equal(t, v1alpha1.StepStatusNotPresent, statuses["RoleBinding"])
}

// TestFailedCSVCleanupKeepsDeploymentOfAPIServer: a CSV that owns an
// APIService or a conversion webhook keeps its Deployment, the API would
// break without a backend. The RBAC is still cleaned up.
func TestFailedCSVCleanupKeepsDeploymentOfAPIServer(t *testing.T) {
	tests := []struct {
		name string
		set  func(*v1alpha1.ClusterServiceVersion)
	}{
		{"OwnedAPIService", func(c *v1alpha1.ClusterServiceVersion) {
			c.Spec.APIServiceDefinitions.Owned = []v1alpha1.APIServiceDescription{{Name: "v1.api.example.com", Group: "api.example.com", Version: "v1", Kind: "Thing", DeploymentName: cleanupDeployment}}
		}},
		{"ConversionWebhook", func(c *v1alpha1.ClusterServiceVersion) {
			c.Spec.WebhookDefinitions = []v1alpha1.WebhookDescription{{GenerateName: "conv", Type: v1alpha1.ConversionWebhook, DeploymentName: cleanupDeployment}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			csv := failedUnsupportedCSV()
			tt.set(csv)
			op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, cleanupK8sObjs(csv), nil)

			require.NoError(t, op.syncClusterServiceVersion(csv))

			require.True(t, deploymentExists(t, op), "deployment of an API server must stay")
			requireStepObjects(t, op, false)
		})
	}
}

// TestFailedCSVCleanupDeleteConflict: a delete that fails its UID
// precondition means the object was replaced in between. The error is
// returned so the next sync reads the replacement.
func TestFailedCSVCleanupDeleteConflict(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, cleanupK8sObjs(csv), nil)
	kubeFake(op).PrependReactor("delete", "roles", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(rbacv1.Resource("roles"), cleanupRBACNames["Role"], errors.New("uid changed"))
	})

	require.Error(t, op.syncClusterServiceVersion(csv))
	require.True(t, stepObjectExists(t, op, "Role"))
}

// TestFailedCSVCleanupForeignObjectClearsMark: a step marked NotPresent by
// an earlier cleanup whose name now belongs to another owner gets its mark
// removed, so the reopened plan does not overwrite the other owner's object.
func TestFailedCSVCleanupForeignObjectClearsMark(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	foreign := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: cleanupRBACNames["Role"], Namespace: cleanupNamespace, Labels: map[string]string{ownerutil.OwnerKey: "other-csv", ownerutil.OwnerNamespaceKey: cleanupNamespace}}}
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, []runtime.Object{serviceAccount(cleanupSA, cleanupNamespace), foreign}, nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	require.True(t, stepObjectExists(t, op, "Role"), "foreign Role must stay")
	plan, err := op.client.OperatorsV1alpha1().InstallPlans(cleanupNamespace).Get(ctx, cleanupPlanName, metav1.GetOptions{})
	require.NoError(t, err)
	statuses := stepStatuses(plan)
	require.Equal(t, v1alpha1.StepStatusPresent, statuses["Role"], "mark cleared for the foreign object")
	require.Equal(t, v1alpha1.StepStatusNotPresent, statuses["RoleBinding"], "other marks kept")
}

// TestFailedCSVReopensPlanFromFailedBranch: a Failed CSV whose reason is not
// an OperatorGroup reason can go to Succeeded without passing Pending. The
// cleaned-up plan is reopened from the Failed branch as well.
func TestFailedCSVReopensPlanFromFailedBranch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := cleanupCSV(v1alpha1.CSVPhaseFailed, v1alpha1.CSVReasonComponentFailed)
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), cleanedUpInstallPlan()}, []runtime.Object{serviceAccount(cleanupSA, cleanupNamespace)}, nil)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	requirePlan(t, ctx, op, v1alpha1.InstallPlanPhaseInstalling, v1alpha1.StepStatusNotPresent)
}

// TestFailedCSVCleanupKeepsDeploymentOfLiveAPIService: an APIService that
// carries the CSV owner labels keeps the Deployment even when the CSV spec
// no longer declares it.
func TestFailedCSVCleanupKeepsDeploymentOfLiveAPIService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	apiService := &apiregistrationv1.APIService{ObjectMeta: metav1.ObjectMeta{Name: "v1.api.example.com", Labels: ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)}}
	op, err := NewFakeOperator(ctx,
		withNamespaces(cleanupNamespace, "kube-system"),
		withClientObjs(cleanupOperatorGroup(false), completedInstallPlan(), csv),
		withK8sObjs(cleanupK8sObjs(csv)...),
		withExtObjs(crd("c1", "v1", "g1")),
		withRegObjs(apiService),
		withPartialMetadata(&metav1.PartialObjectMetadata{ObjectMeta: csv.ObjectMeta}),
		withOperatorNamespace(cleanupNamespace))
	require.NoError(t, err)

	require.NoError(t, op.syncClusterServiceVersion(csv))

	require.True(t, deploymentExists(t, op), "deployment of a live API server must stay")
	requireStepObjects(t, op, false)
}

// TestFailedCSVCleanupManifestForms: an inline manifest in any form can be
// applied again, an empty manifest cannot.
func TestFailedCSVCleanupManifestForms(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		wantDeleted bool
	}{
		{"Empty", "", false},
		{"NotJSON", "apiVersion: rbac.authorization.k8s.io/v1", true},
		{"JSONNotAReference", `{"kind":"ConfigMap","name":"x"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			plan := completedInstallPlan()
			plan.Status.Plan[3].Resource.Manifest = tt.manifest
			csv := failedUnsupportedCSV()
			op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), plan}, cleanupK8sObjs(csv), nil)

			require.NoError(t, op.syncClusterServiceVersion(csv))
			requireStepObjects(t, op, !tt.wantDeleted)
		})
	}
}

// TestFailedCSVCleanupConfigMapError: an error other than NotFound while
// checking the unpacked bundle ConfigMap is returned, nothing is deleted.
func TestFailedCSVCleanupConfigMapError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	plan := completedInstallPlan()
	for _, step := range plan.Status.Plan {
		step.Resource.Manifest = `{"kind":"ConfigMap","name":"bundle-cm","namespace":"ns","catalogSourceName":"cat","catalogSourceNamespace":"ns"}`
	}
	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), plan}, cleanupK8sObjs(csv), nil)
	kubeFake(op).PrependReactor("get", "configmaps", failingReactor("get configmap"))

	require.EqualError(t, op.syncClusterServiceVersion(csv), "get configmap")
	requireStepObjects(t, op, true)
}

// TestFailedCSVCleanupWebhookErrors: webhook API errors are returned, the
// rest of the cleanup still happens.
func TestFailedCSVCleanupWebhookErrors(t *testing.T) {
	tests := []struct {
		name     string
		verb     string
		resource string
	}{
		{"ValidatingDeleteFails", "delete", "validatingwebhookconfigurations"},
		{"MutatingListFails", "list", "mutatingwebhookconfigurations"},
		{"MutatingDeleteFails", "delete", "mutatingwebhookconfigurations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			csv := failedUnsupportedCSV()
			labels := ownerutil.OwnerLabel(csv, v1alpha1.ClusterServiceVersionKind)
			webhooks := []runtime.Object{
				&admissionregistrationv1.ValidatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "csv1-validating", Labels: labels}},
				&admissionregistrationv1.MutatingWebhookConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "csv1-mutating", Labels: labels}},
			}
			op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, append(cleanupK8sObjs(csv), webhooks...), nil)
			kubeFake(op).PrependReactor(tt.verb, tt.resource, failingReactor(tt.name))

			require.EqualError(t, op.syncClusterServiceVersion(csv), tt.name)
			require.False(t, deploymentExists(t, op), "deployment exists")
			requireStepObjects(t, op, false)
		})
	}
}

// TestFailedCSVCleanupObjectGoneBeforeDelete: an object that someone else
// deleted between the read and the delete was their decision. The mark is
// taken back so the reopened plan does not create it again. The other
// steps keep their marks.
func TestFailedCSVCleanupObjectGoneBeforeDelete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, cleanupK8sObjs(csv), nil)
	kubeFake(op).PrependReactor("delete", "roles", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(rbacv1.Resource("roles"), cleanupRBACNames["Role"])
	})

	require.NoError(t, op.syncClusterServiceVersion(csv))

	plan, err := op.client.OperatorsV1alpha1().InstallPlans(cleanupNamespace).Get(ctx, cleanupPlanName, metav1.GetOptions{})
	require.NoError(t, err)
	statuses := stepStatuses(plan)
	require.Equal(t, v1alpha1.StepStatusPresent, statuses["Role"], "mark taken back")
	require.Equal(t, v1alpha1.StepStatusNotPresent, statuses["RoleBinding"], "other marks kept")
}

// TestFailedCSVCleanupObjectGoneBeforeDeleteStatusWriteFails: taking a
// mark back is a status write too, its failure is returned for retry.
func TestFailedCSVCleanupObjectGoneBeforeDeleteStatusWriteFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()

	csv := failedUnsupportedCSV()
	op := newCleanupOperator(t, ctx, csv, []runtime.Object{cleanupOperatorGroup(false), completedInstallPlan()}, cleanupK8sObjs(csv), nil)
	kubeFake(op).PrependReactor("delete", "roles", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(rbacv1.Resource("roles"), cleanupRBACNames["Role"])
	})
	updates := 0
	operatorsFake(op).PrependReactor("update", "installplans", func(clienttesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 2 {
			return true, nil, errors.New("second status write fails")
		}
		return false, nil, nil
	})

	require.EqualError(t, op.syncClusterServiceVersion(csv), "second status write fails")
}
