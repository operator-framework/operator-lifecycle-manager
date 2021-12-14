package reconciler

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func TestPodNodeSelector(t *testing.T) {
	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
	}

	key := "kubernetes.io/os"
	value := "linux"

	gotCatSrcPod := Pod(catsrc, "hello", "busybox", "", map[string]string{}, map[string]string{}, int32(0), int32(0))
	gotCatSrcPodSelector := gotCatSrcPod.Spec.NodeSelector

	if gotCatSrcPodSelector[key] != value {
		t.Errorf("expected %s value for node selector key %s, received %s value instead", value, key,
			gotCatSrcPodSelector[key])
	}
}

func TestPullPolicy(t *testing.T) {
	var table = []struct {
		image  string
		policy corev1.PullPolicy
	}{
		{
			image:  "quay.io/operator-framework/olm@sha256:b9d011c0fbfb65b387904f8fafc47ee1a9479d28d395473341288ee126ed993b",
			policy: corev1.PullIfNotPresent,
		},
		{
			image:  "gcc@sha256:06a6f170d7fff592e44b089c0d2e68d870573eb9a23d9c66d4b6ea11f8fad18b",
			policy: corev1.PullIfNotPresent,
		},
		{
			image:  "myimage:1.0",
			policy: corev1.PullAlways,
		},
		{
			image:  "busybox",
			policy: corev1.PullAlways,
		},
		{
			image:  "gcc@sha256:06a6f170d7fff592e44b089c0d2e68",
			policy: corev1.PullIfNotPresent,
		},
		{
			image:  "hello@md5:b1946ac92492d2347c6235b4d2611184",
			policy: corev1.PullIfNotPresent,
		},
	}

	source := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
	}

	for _, tt := range table {
		p := Pod(source, "catalog", tt.image, "", nil, nil, int32(0), int32(0))
		policy := p.Spec.Containers[0].ImagePullPolicy
		if policy != tt.policy {
			t.Fatalf("expected pull policy %s for image  %s", tt.policy, tt.image)
		}
	}
}

func TestPodContainerSecurityContext(t *testing.T) {
	expectedReadOnlyRootFilesystem := false
	expectedContainerSecCtx := &corev1.SecurityContext{
		ReadOnlyRootFilesystem: &expectedReadOnlyRootFilesystem,
	}

	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
	}

	gotPod := Pod(catsrc, "hello", "busybox", "", map[string]string{}, map[string]string{}, int32(0), int32(0))
	gotContainerSecCtx := gotPod.Spec.Containers[0].SecurityContext
	require.Equal(t, expectedContainerSecCtx, gotContainerSecCtx)
}

func TestPodSchedulingOverrides(t *testing.T) {
	// This test ensures that any overriding pod scheduling configuration elements
	// defined in spec.grpcPodConfig are applied to the catalog source pod created
	// when spec.sourceType = 'grpc' and spec.image is set.
	var tolerationSeconds int64 = 120
	var overriddenPriorityClassName = "some-prio-class"
	var overriddenNodeSelectors = map[string]string{
		"label":  "value",
		"label2": "value2",
	}
	var defaultNodeSelectors = map[string]string{
		"kubernetes.io/os": "linux",
	}
	var defaultPriorityClassName = ""

	var overriddenTolerations = []corev1.Toleration{
		{
			Key:               "some/key",
			Operator:          corev1.TolerationOpExists,
			Effect:            corev1.TaintEffectNoExecute,
			TolerationSeconds: &tolerationSeconds,
		},
		{
			Key:      "someother/key",
			Operator: corev1.TolerationOpEqual,
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}

	testCases := []struct {
		title                     string
		catalogSource             *v1alpha1.CatalogSource
		expectedNodeSelectors     map[string]string
		expectedTolerations       []corev1.Toleration
		expectedPriorityClassName string
		annotations               map[string]string
	}{
		{
			title: "no overrides",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
				},
			},
			expectedTolerations:       nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "override node selectors",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						NodeSelector: overriddenNodeSelectors,
					},
				},
			},
			expectedTolerations:       nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     overriddenNodeSelectors,
		}, {
			title: "override priority class name",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						PriorityClassName: &overriddenPriorityClassName,
					},
				},
			},
			expectedTolerations:       nil,
			expectedPriorityClassName: overriddenPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "doesn't override priority class name when its nil",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						PriorityClassName: nil,
					},
				},
			},
			expectedTolerations:       nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "Override node tolerations",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						Tolerations: overriddenTolerations,
					},
				},
			},
			expectedTolerations:       overriddenTolerations,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "Override all the things",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						NodeSelector:      overriddenNodeSelectors,
						PriorityClassName: &overriddenPriorityClassName,
						Tolerations:       overriddenTolerations,
					},
				},
			},
			expectedTolerations:       overriddenTolerations,
			expectedPriorityClassName: overriddenPriorityClassName,
			expectedNodeSelectors:     overriddenNodeSelectors,
		}, {
			title: "priorityClassName annotation takes precedence",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						PriorityClassName: &overriddenPriorityClassName,
					},
				},
			},
			expectedTolerations: nil,
			annotations: map[string]string{
				CatalogPriorityClassKey: "some-OTHER-prio-class",
			},
			expectedPriorityClassName: "some-OTHER-prio-class",
			expectedNodeSelectors:     defaultNodeSelectors,
		},
	}

	for _, testCase := range testCases {
		pod := Pod(testCase.catalogSource, "hello", "busybox", "", map[string]string{}, testCase.annotations, int32(0), int32(0))
		require.Equal(t, testCase.expectedNodeSelectors, pod.Spec.NodeSelector)
		require.Equal(t, testCase.expectedPriorityClassName, pod.Spec.PriorityClassName)
		require.Equal(t, testCase.expectedTolerations, pod.Spec.Tolerations)
	}
}
