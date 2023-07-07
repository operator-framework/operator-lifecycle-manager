package reconciler

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
)

const workloadUserID = 1001

func TestPodNodeSelector(t *testing.T) {
	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
	}

	key := "kubernetes.io/os"
	value := "linux"

	gotCatSrcPod := Pod(catsrc, "hello", "busybox", "", map[string]string{}, map[string]string{}, int32(0), int32(0), int64(workloadUserID))
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
		p := Pod(source, "catalog", tt.image, "", nil, nil, int32(0), int32(0), int64(workloadUserID))
		policy := p.Spec.Containers[0].ImagePullPolicy
		if policy != tt.policy {
			t.Fatalf("expected pull policy %s for image  %s", tt.policy, tt.image)
		}
	}
}

func TestPodContainerSecurityContext(t *testing.T) {
	testcases := []struct {
		title                            string
		inputCatsrc                      *v1alpha1.CatalogSource
		expectedSecurityContext          *corev1.PodSecurityContext
		expectedContainerSecurityContext *corev1.SecurityContext
	}{
		{
			title: "NoSpecDefined/PodContainsSecurityConfigForPSALegacy",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
			},
			expectedContainerSecurityContext: nil,
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/NoGRPCPodConfig/PodContainsSecurityConfigForPSALegacy",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{},
			},
			expectedContainerSecurityContext: nil,
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/GRPCPodConfigDefined/PodContainsSecurityConfigForPSALegacy",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{},
				},
			},
			expectedContainerSecurityContext: nil,
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/SecurityContextConfig:Legacy/PodContainsSecurityConfigForPSALegacy",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Legacy,
					},
				},
			},
			expectedContainerSecurityContext: nil,
			expectedSecurityContext:          nil,
		},
		{
			title: "SpecDefined/SecurityContextConfig:Restricted/PodContainsSecurityConfigForPSARestricted",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Restricted,
					},
				},
			},
			expectedContainerSecurityContext: &corev1.SecurityContext{
				ReadOnlyRootFilesystem:   pointer.Bool(false),
				AllowPrivilegeEscalation: pointer.Bool(false),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
				},
			},
			expectedSecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				RunAsUser:      pointer.Int64(workloadUserID),
				RunAsNonRoot:   pointer.Bool(true),
			},
		},
		{
			title: "SpecDefined/SecurityContextConfig:Legacy/PodDoesNotContainsSecurityConfig",
			inputCatsrc: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						SecurityContextConfig: v1alpha1.Legacy,
					},
				},
			},
			expectedContainerSecurityContext: nil,
			expectedSecurityContext:          nil,
		},
	}
	for _, testcase := range testcases {
		outputPod := Pod(testcase.inputCatsrc, "hello", "busybox", "", map[string]string{}, map[string]string{}, int32(0), int32(0), int64(workloadUserID))
		if testcase.expectedSecurityContext != nil {
			require.Equal(t, testcase.expectedSecurityContext, outputPod.Spec.SecurityContext)
		}
		if testcase.expectedContainerSecurityContext != nil {
			require.Equal(t, testcase.expectedContainerSecurityContext, outputPod.Spec.Containers[0].SecurityContext)
		}
	}
}

// TestPodAvoidsConcurrentWrite is a regression test for
// https://bugzilla.redhat.com/show_bug.cgi?id=2101357
// we were mutating the input annotations and labels parameters causing
// concurrent write issues
func TestPodAvoidsConcurrentWrite(t *testing.T) {
	catsrc := &v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "testns",
		},
	}

	labels := map[string]string{
		"label": "something",
	}

	annotations := map[string]string{
		"annotation": "somethingelse",
	}

	gotPod := Pod(catsrc, "hello", "busybox", "", labels, annotations, int32(0), int32(0), int64(workloadUserID))

	// check labels and annotations point to different addresses between parameters and what's in the pod
	require.NotEqual(t, &labels, &gotPod.Labels)
	require.NotEqual(t, &annotations, &gotPod.Annotations)

	// check that labels and annotations from the parameters were copied down to the pod's
	require.Equal(t, labels["label"], gotPod.Labels["label"])
	require.Equal(t, annotations["annotation"], gotPod.Annotations["annotation"])
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

	var overriddenAffinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      "kubernetes.io/arch",
								Operator: corev1.NodeSelectorOpIn,
								Values: []string{
									"amd64",
									"arm",
								},
							},
						},
					},
				},
			},
		},
	}

	testCases := []struct {
		title                     string
		catalogSource             *v1alpha1.CatalogSource
		expectedNodeSelectors     map[string]string
		expectedTolerations       []corev1.Toleration
		expectedAffinity          *corev1.Affinity
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
			expectedAffinity:          nil,
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
			expectedAffinity:          nil,
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
			expectedAffinity:          nil,
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
			expectedAffinity:          nil,
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
			expectedAffinity:          nil,
			expectedPriorityClassName: defaultPriorityClassName,
			expectedNodeSelectors:     defaultNodeSelectors,
		}, {
			title: "Override affinity",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "testns",
				},
				Spec: v1alpha1.CatalogSourceSpec{
					SourceType: v1alpha1.SourceTypeGrpc,
					Image:      "repo/image:tag",
					GrpcPodConfig: &v1alpha1.GrpcPodConfig{
						Affinity: overriddenAffinity,
					},
				},
			},
			expectedTolerations:       nil,
			expectedAffinity:          overriddenAffinity,
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
						Affinity:          overriddenAffinity,
					},
				},
			},
			expectedTolerations:       overriddenTolerations,
			expectedAffinity:          overriddenAffinity,
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
			expectedAffinity:    nil,
			annotations: map[string]string{
				CatalogPriorityClassKey: "some-OTHER-prio-class",
			},
			expectedPriorityClassName: "some-OTHER-prio-class",
			expectedNodeSelectors:     defaultNodeSelectors,
		},
	}

	for _, testCase := range testCases {
		pod := Pod(testCase.catalogSource, "hello", "busybox", "", map[string]string{}, testCase.annotations, int32(0), int32(0), int64(workloadUserID))
		require.Equal(t, testCase.expectedNodeSelectors, pod.Spec.NodeSelector)
		require.Equal(t, testCase.expectedPriorityClassName, pod.Spec.PriorityClassName)
		require.Equal(t, testCase.expectedTolerations, pod.Spec.Tolerations)
		require.Equal(t, testCase.expectedAffinity, pod.Spec.Affinity)
	}
}
