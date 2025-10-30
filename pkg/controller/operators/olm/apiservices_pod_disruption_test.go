package olm

// These focused tests make sure our lightweight disruption heuristics stay aligned with the
// contract for ClusterOperator conditions. They simulate the handful of pod states we expect to
// see during planned reboots and ensure we still fail fast on genuine outages.

import (
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationlisters "k8s.io/kube-aggregator/pkg/client/listers/apiregistration/v1"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
)

func newDisruptionTestOperator(t *testing.T, namespace string) (*Operator, cache.Indexer, cache.Indexer, cache.Indexer) {
	t.Helper()

	deploymentIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	podIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	apiServiceIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})

	fakeClock := clocktesting.NewFakeClock(time.Now())

	op := &Operator{
		logger:   logrus.New(),
		resolver: &install.StrategyResolver{},
		lister:   operatorlister.NewLister(),
		clock:    fakeClock,
	}
	op.logger.SetOutput(io.Discard)

	op.lister.AppsV1().RegisterDeploymentLister(namespace, appslisters.NewDeploymentLister(deploymentIndexer))
	op.lister.CoreV1().RegisterPodLister(namespace, corelisters.NewPodLister(podIndexer))
	op.lister.APIRegistrationV1().RegisterAPIServiceLister(apiregistrationlisters.NewAPIServiceLister(apiServiceIndexer))

	return op, deploymentIndexer, podIndexer, apiServiceIndexer
}

func csvWithAPIService(namespace, deploymentName, apiServiceName string) *v1alpha1.ClusterServiceVersion {
	replicas := int32(1)

	return &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-csv",
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName: v1alpha1.InstallStrategyNameDeployment,
				StrategySpec: v1alpha1.StrategyDetailsDeployment{
					DeploymentSpecs: []v1alpha1.StrategyDeploymentSpec{
						{
							Name: deploymentName,
							Spec: appsv1.DeploymentSpec{
								Replicas: &replicas,
								Selector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"app": deploymentName},
								},
								Template: corev1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										Labels: map[string]string{"app": deploymentName},
									},
								},
							},
						},
					},
				},
			},
			APIServiceDefinitions: v1alpha1.APIServiceDefinitions{
				Owned: []v1alpha1.APIServiceDescription{
					{
						Group:          "example.com",
						Version:        "v1",
						Kind:           "Example",
						DeploymentName: deploymentName,
						Resources:      []v1alpha1.APIResourceReference{},
					},
				},
			},
		},
	}
}

func addDeployment(t *testing.T, indexer cache.Indexer, deployment *appsv1.Deployment) {
	t.Helper()
	require.NoError(t, indexer.Add(deployment))
}

func addPod(t *testing.T, indexer cache.Indexer, pod *corev1.Pod) {
	t.Helper()
	require.NoError(t, indexer.Add(pod))
}

func addAPIService(t *testing.T, indexer cache.Indexer, service *apiregistrationv1.APIService) {
	t.Helper()
	require.NoError(t, indexer.Add(service))
}

// TestIsPodExpectedDisruption covers the pod lifecycle states we treat as expected churn.
func TestIsPodExpectedDisruption(t *testing.T) {
	now := time.Now()
	creation := metav1.NewTime(now.Add(-30 * time.Second))
	oldTime := metav1.NewTime(now.Add(-(expectedDisruptionGracePeriod + time.Minute)))

	testCases := []struct {
		name        string
		pod         *corev1.Pod
		expected    bool
		description string
	}{
		{
			name: "pod with DeletionTimestamp should indicate disruption",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &metav1.Time{Time: now},
				},
			},
			expected:    true,
			description: "Pod is draining, so we treat it as transient.",
		},
		{
			name: "terminating pod beyond grace should not be disruption",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &oldTime,
				},
			},
			expected:    false,
			description: "Deletion timestamp is outside the grace period, so it is a real outage.",
		},
		{
			name: "container creating should indicate disruption",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: creation,
				},
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "ContainerCreating",
								},
							},
						},
					},
				},
			},
			expected:    true,
			description: "Container is still creating, which matches the normal startup sequence.",
		},
		{
			name: "init container creating should indicate disruption",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: creation,
				},
				Status: corev1.PodStatus{
					InitContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "PodInitializing",
								},
							},
						},
					},
				},
			},
			expected:    true,
			description: "Init container is still creating, so the pod is still coming up.",
		},
		{
			name: "node shutdown reason should indicate disruption",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Reason: "NodeShutdown",
					StartTime: &metav1.Time{
						Time: now.Add(-time.Minute),
					},
				},
			},
			expected:    true,
			description: "NodeShutdown indicates a planned drain, so we treat it as expected.",
		},
		{
			name: "terminating condition within grace should indicate disruption",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:               corev1.PodReady,
							Status:             corev1.ConditionFalse,
							Reason:             "Terminating",
							LastTransitionTime: metav1.NewTime(now.Add(-time.Minute)),
						},
					},
				},
			},
			expected:    true,
			description: "PodReady condition shows Terminating within grace, still expected disruption.",
		},
		{
			name: "terminating condition beyond grace should not be disruption",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:               corev1.PodReady,
							Status:             corev1.ConditionFalse,
							Reason:             "Terminating",
							LastTransitionTime: metav1.NewTime(now.Add(-(expectedDisruptionGracePeriod + time.Minute))),
						},
					},
				},
			},
			expected:    false,
			description: "Terminating condition persisted too long, so it should surface as a failure.",
		},
		{
			name: "crash loop should not be disruption",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					ContainerStatuses: []corev1.ContainerStatus{
						{
							State: corev1.ContainerState{
								Waiting: &corev1.ContainerStateWaiting{
									Reason: "CrashLoopBackOff",
								},
							},
						},
					},
				},
			},
			expected:    false,
			description: "CrashLoopBackOff is a real failure and should not be considered transient.",
		},
		{
			name: "pending unschedulable should not be disruption",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodScheduled,
							Status: corev1.ConditionFalse,
							Reason: "Unschedulable",
						},
					},
				},
			},
			expected:    false,
			description: "Unschedulable pending pods require admin action, so they are not transient.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, isPodExpectedDisruption(tc.pod, now, expectedDisruptionGracePeriod), tc.description)
		})
	}
}

// TestIsAPIServiceBackendDisrupted_TerminatingPod ensures draining pods stay classified as transient.
func TestIsAPIServiceBackendDisrupted_TerminatingPod(t *testing.T) {
	// Scenario: deployment pods are draining because the node is rebooting.
	// We expect the disruption helper to treat this as an expected blip.
	const (
		namespace      = "test"
		deploymentName = "apisvc-dep"
		apiServiceName = "v1.example.com"
	)

	op, deploymentIndexer, podIndexer, _ := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, deploymentName, apiServiceName)
	now := op.clock.Now()
	nowMeta := metav1.NewTime(now)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
		},
	}
	addDeployment(t, deploymentIndexer, deployment)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-1",
			Namespace:         namespace,
			Labels:            map[string]string{"app": deploymentName},
			DeletionTimestamp: &nowMeta,
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	addPod(t, podIndexer, pod)

	require.True(t, op.isAPIServiceBackendDisrupted(csv, apiServiceName))
}

// TestIsAPIServiceBackendDisrupted_PendingWithoutEvidence surfaces genuine pending failures.
func TestIsAPIServiceBackendDisrupted_PendingWithoutEvidence(t *testing.T) {
	// Scenario: pod is stuck pending for an unschedulable reason. That should be treated
	// as a real outage, not an expected disruption.
	const (
		namespace      = "test"
		deploymentName = "apisvc-dep"
		apiServiceName = "v1.example.com"
	)

	op, deploymentIndexer, podIndexer, _ := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, deploymentName, apiServiceName)
	now := op.clock.Now()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
		},
	}
	addDeployment(t, deploymentIndexer, deployment)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-1",
			Namespace: namespace,
			Labels:    map[string]string{"app": deploymentName},
			CreationTimestamp: metav1.NewTime(now.Add(
				-(expectedDisruptionGracePeriod + time.Minute))),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionFalse,
					Reason: "Unschedulable",
				},
			},
		},
	}
	addPod(t, podIndexer, pod)

	require.False(t, op.isAPIServiceBackendDisrupted(csv, apiServiceName))
}

// TestIsAPIServiceBackendDisrupted_IgnoresUnrelatedDeployment ignores rollouts outside the APIService.
func TestIsAPIServiceBackendDisrupted_IgnoresUnrelatedDeployment(t *testing.T) {
	// Scenario: another deployment owned by the CSV is rolling. The APIService deployment
	// is healthy, so we should not hide the outage behind the unrelated rollout.
	const (
		namespace        = "test"
		targetDeployment = "apisvc-dep"
		otherDeployment  = "metrics-dep"
		apiServiceName   = "v1.example.com"
	)

	op, deploymentIndexer, podIndexer, _ := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, targetDeployment, apiServiceName)
	now := op.clock.Now()

	// Add second deployment to the strategy that should not influence the APIService result.
	replicas := int32(1)
	csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs = append(csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs,
		v1alpha1.StrategyDeploymentSpec{
			Name: otherDeployment,
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": otherDeployment},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": otherDeployment},
					},
				},
			},
		},
	)

	target := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      targetDeployment,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": targetDeployment},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 0,
		},
	}
	addDeployment(t, deploymentIndexer, target)

	other := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      otherDeployment,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": otherDeployment},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
		},
	}
	addDeployment(t, deploymentIndexer, other)

	nowMeta := metav1.NewTime(now)
	addPod(t, podIndexer, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "other-pod",
			Namespace:         namespace,
			Labels:            map[string]string{"app": otherDeployment},
			DeletionTimestamp: &nowMeta,
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		},
	})

	require.False(t, op.isAPIServiceBackendDisrupted(csv, apiServiceName))
}

// TestIsAPIServiceBackendDisrupted_NoPodsButRecentProgress trusts fresh rollout progress.
func TestIsAPIServiceBackendDisrupted_NoPodsButRecentProgress(t *testing.T) {
	// Scenario: the APIService deployment has unavailable replicas and no pods yet,
	// but it just reported progress. Treat this as transient so we give it time to settle.
	const (
		namespace      = "test"
		deploymentName = "apisvc-dep"
		apiServiceName = "v1.example.com"
	)

	op, deploymentIndexer, _, _ := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, deploymentName, apiServiceName)
	now := op.clock.Now()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:           appsv1.DeploymentProgressing,
					Status:         corev1.ConditionTrue,
					Reason:         "ReplicaSetUpdated",
					LastUpdateTime: metav1.NewTime(now.Add(-time.Minute)),
				},
			},
		},
	}
	addDeployment(t, deploymentIndexer, deployment)

	require.True(t, op.isAPIServiceBackendDisrupted(csv, apiServiceName))
}

// TestIsAPIServiceBackendDisrupted_NoPodsAndStaleProgress reports stuck deployments as failures.
func TestIsAPIServiceBackendDisrupted_NoPodsAndStaleProgress(t *testing.T) {
	// Scenario: the deployment has been stuck without pods for longer than our grace window.
	// We should surface this as a real failure.
	const (
		namespace      = "test"
		deploymentName = "apisvc-dep"
		apiServiceName = "v1.example.com"
	)

	op, deploymentIndexer, _, _ := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, deploymentName, apiServiceName)
	now := op.clock.Now()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:           appsv1.DeploymentProgressing,
					Status:         corev1.ConditionTrue,
					Reason:         "ReplicaSetUpdated",
					LastUpdateTime: metav1.NewTime(now.Add(-(expectedDisruptionGracePeriod + time.Minute))),
				},
			},
		},
	}
	addDeployment(t, deploymentIndexer, deployment)

	require.False(t, op.isAPIServiceBackendDisrupted(csv, apiServiceName))
}

// TestAreAPIServicesAvailable_ReturnsRetryableErrorForExpectedDisruption exercises the retryable path.
func TestAreAPIServicesAvailable_ReturnsRetryableErrorForExpectedDisruption(t *testing.T) {
	// Scenario: the APIService is unavailable while pods are terminating during a drain.
	// We should surface a retryable error so the CSV stays steady.
	const (
		namespace      = "test"
		deploymentName = "apisvc-dep"
		apiServiceName = "v1.example.com"
	)

	op, deploymentIndexer, podIndexer, apiServiceIndexer := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, deploymentName, apiServiceName)
	now := op.clock.Now()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
		},
	}
	addDeployment(t, deploymentIndexer, deployment)

	nowMeta := metav1.NewTime(now)
	addPod(t, podIndexer, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-1",
			Namespace:         namespace,
			Labels:            map[string]string{"app": deploymentName},
			DeletionTimestamp: &nowMeta,
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		},
	})

	apiService := &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: apiServiceName,
		},
		Status: apiregistrationv1.APIServiceStatus{
			Conditions: []apiregistrationv1.APIServiceCondition{
				{
					Type:   apiregistrationv1.Available,
					Status: apiregistrationv1.ConditionFalse,
					Reason: "ServiceUnavailable",
				},
			},
		},
	}
	addAPIService(t, apiServiceIndexer, apiService)

	available, err := op.areAPIServicesAvailable(csv)
	require.False(t, available)
	require.Error(t, err)
	require.True(t, olmerrors.IsRetryable(err), "expected retryable error")
}

// TestAreAPIServicesAvailable_NoRetryableErrorForUnschedulablePod keeps real outages non-retryable.
func TestAreAPIServicesAvailable_NoRetryableErrorForUnschedulablePod(t *testing.T) {
	// Scenario: a pod is pending because it cannot be scheduled. That requires admin action,
	// so the APIService check should return a normal error (not retryable).
	const (
		namespace      = "test"
		deploymentName = "apisvc-dep"
		apiServiceName = "v1.example.com"
	)

	op, deploymentIndexer, podIndexer, apiServiceIndexer := newDisruptionTestOperator(t, namespace)
	csv := csvWithAPIService(namespace, deploymentName, apiServiceName)
	now := op.clock.Now()

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
		},
		Status: appsv1.DeploymentStatus{
			UnavailableReplicas: 1,
		},
	}
	addDeployment(t, deploymentIndexer, deployment)

	addPod(t, podIndexer, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-1",
			Namespace:         namespace,
			Labels:            map[string]string{"app": deploymentName},
			CreationTimestamp: metav1.NewTime(now.Add(-(expectedDisruptionGracePeriod + time.Minute))),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionFalse,
					Reason: "Unschedulable",
				},
			},
		},
	})

	apiService := &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: apiServiceName,
		},
		Status: apiregistrationv1.APIServiceStatus{
			Conditions: []apiregistrationv1.APIServiceCondition{
				{
					Type:   apiregistrationv1.Available,
					Status: apiregistrationv1.ConditionFalse,
					Reason: "ServiceUnavailable",
				},
			},
		},
	}
	addAPIService(t, apiServiceIndexer, apiService)

	available, err := op.areAPIServicesAvailable(csv)
	require.False(t, available)
	require.NoError(t, err)
}
