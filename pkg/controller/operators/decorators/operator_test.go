package decorators

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func TestOperatorNames(t *testing.T) {
	type args struct {
		labels map[string]string
	}
	type results struct {
		names []types.NamespacedName
	}

	tests := []struct {
		description string
		args        args
		results     results
	}{
		{
			description: "SingleOperator",
			args: args{
				labels: map[string]string{
					ComponentLabelKeyPrefix + "lobster": "",
				},
			},
			results: results{
				names: []types.NamespacedName{
					{Name: "lobster"},
				},
			},
		},
		{
			description: "MultipleOperators",
			args: args{
				labels: map[string]string{
					ComponentLabelKeyPrefix + "lobster": "",
					ComponentLabelKeyPrefix + "cod":     "",
				},
			},
			results: results{
				names: []types.NamespacedName{
					{Name: "lobster"},
					{Name: "cod"},
				},
			},
		},
		{
			description: "NoOperators",
			args: args{
				labels: map[string]string{
					"robot": "whirs_and_clicks",
				},
			},
			results: results{
				names: nil,
			},
		},
		{
			description: "NoLabels",
			args: args{
				labels: nil,
			},
			results: results{
				names: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			require.ElementsMatch(t, tt.results.names, OperatorNames(tt.args.labels))
		})
	}
}

func TestAddComponents(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, k8sscheme.AddToScheme(scheme))
	require.NoError(t, operatorsv1alpha1.AddToScheme(scheme))
	require.NoError(t, metav1.AddMetaToScheme(scheme))

	type fields struct {
		operator *operatorsv1.Operator
	}
	type args struct {
		components []runtime.Object
	}
	type results struct {
		operator *operatorsv1.Operator
		err      error
	}

	tests := []struct {
		description string
		fields      fields
		args        args
		results     results
	}{
		{
			description: "Empty/ComponentsAdded",
			fields: fields{
				operator: func() *operatorsv1.Operator {
					operator := &operatorsv1.Operator{}
					operator.SetName("puffin")

					return operator
				}(),
			},
			args: args{
				components: []runtime.Object{
					func() runtime.Object {
						namespace := &corev1.Namespace{}
						namespace.SetName("atlantic")
						namespace.SetLabels(map[string]string{
							ComponentLabelKeyPrefix + "puffin": "",
						})

						return namespace
					}(),
					func() runtime.Object {
						pod := &corev1.Pod{}
						pod.SetNamespace("atlantic")
						pod.SetName("puffin")
						pod.SetLabels(map[string]string{
							ComponentLabelKeyPrefix + "puffin": "",
						})
						pod.Status.Conditions = []corev1.PodCondition{
							{
								Type:   corev1.PodReady,
								Status: corev1.ConditionTrue,
							},
						}

						return pod
					}(),
					func() runtime.Object {
						csv := &operatorsv1alpha1.ClusterServiceVersion{}
						csv.SetNamespace("atlantic")
						csv.SetName("puffin")
						csv.SetLabels(map[string]string{
							ComponentLabelKeyPrefix + "puffin": "",
						})
						csv.Status.Phase = operatorsv1alpha1.CSVPhaseSucceeded
						csv.Status.Reason = operatorsv1alpha1.CSVReasonInstallSuccessful
						csv.Status.Message = "this puffin is happy"

						return csv
					}(),
				},
			},
			results: results{
				operator: func() *operatorsv1.Operator {
					operator := &operatorsv1.Operator{}
					operator.SetName("puffin")
					operator.Status.Components = &operatorsv1.Components{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      ComponentLabelKeyPrefix + operator.GetName(),
									Operator: metav1.LabelSelectorOpExists,
								},
							},
						},
					}
					operator.Status.Components.Refs = []operatorsv1.RichReference{
						{
							ObjectReference: &corev1.ObjectReference{
								APIVersion: operatorsv1alpha1.SchemeGroupVersion.String(),
								Kind:       operatorsv1alpha1.ClusterServiceVersionKind,
								Namespace:  "atlantic",
								Name:       "puffin",
							},
							Conditions: []operatorsv1.Condition{
								{
									Type:    operatorsv1.ConditionType(operatorsv1alpha1.CSVPhaseSucceeded),
									Status:  corev1.ConditionTrue,
									Reason:  string(operatorsv1alpha1.CSVReasonInstallSuccessful),
									Message: "this puffin is happy",
								},
							},
						},
						{
							ObjectReference: &corev1.ObjectReference{
								APIVersion: "v1",
								Kind:       "Namespace",
								Name:       "atlantic",
							},
						},
						{
							ObjectReference: &corev1.ObjectReference{
								APIVersion: "v1",
								Kind:       "Pod",
								Namespace:  "atlantic",
								Name:       "puffin",
							},
							Conditions: []operatorsv1.Condition{
								{
									Type:   operatorsv1.ConditionType(corev1.PodReady),
									Status: corev1.ConditionTrue,
								},
							},
						},
					}

					return operator
				}(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			operator := &Operator{
				Operator: tt.fields.operator,
				scheme:   scheme,
			}
			err := operator.AddComponents(tt.args.components...)
			require.Equal(t, tt.results.err, err)
			require.Equal(t, tt.results.operator, operator.Operator)
		})
	}
}

func TestComponentLabelKey(t *testing.T) {
	tests := []struct {
		description       string
		componentLabelKey string
		name              string
		expectedLabel     string
		returnsError      bool
	}{
		{
			description:       "when componentLabelKey is set then return it",
			componentLabelKey: "my-component-label",
			name:              "my-operator",
			expectedLabel:     "my-component-label",
			returnsError:      false,
		},
		{
			description:       "when componentLabelKey is not set and operator name is less than 63 characters then do not truncate",
			componentLabelKey: "",
			name:              "my-operator",
			expectedLabel:     ComponentLabelKeyPrefix + "my-operator",
			returnsError:      false,
		},
		{
			description:   "when componentLabelKey is not set and operator name is more than 63 characters truncate",
			name:          "this-is-my-operator-its-the-coolest-you-got-a-problem-with-that-come-at-me-bro",
			expectedLabel: ComponentLabelKeyPrefix + "this-is-my-operator-its-the-coolest-you-got-a-problem-with-that",
			returnsError:  false,
		},
		{
			description:   "when componentLabelKey is not set and operator name is more than 63 characters truncate and drop trailing illegal characters",
			name:          "this-is-my-operator-its-the-coolest-you-got-a-problem-with----...---___...---",
			expectedLabel: ComponentLabelKeyPrefix + "this-is-my-operator-its-the-coolest-you-got-a-problem-with",
			returnsError:  false,
		},
		{
			description:   "when componentLabelKey is not set and operator name is more than 63 characters and is made up of illegal characters then return error",
			name:          "----...---___...-------...---___...-------...---___...-------...---___...---",
			expectedLabel: "",
			returnsError:  true,
		},
	}

	for _, tt := range tests {
		operator := &Operator{
			Operator: &operatorsv1.Operator{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.name,
				},
			},
			componentLabelKey: tt.componentLabelKey,
		}

		actualLabel, actualErr := operator.ComponentLabelKey()
		require.Equal(t, tt.returnsError, actualErr != nil, actualErr)
		require.Equal(t, tt.expectedLabel, actualLabel)
	}
}
