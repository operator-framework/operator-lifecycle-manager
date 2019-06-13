package installedoperator

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/request"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/apis/porcelain"
)

func TestSynthesizingWatch(t *testing.T) {
	type input struct {
		targetNamespace string
		getNamespaces   func() []string
		events          []watch.Event
	}
	type expected struct {
		err    error
		events []watch.Event
	}
	tests := []struct {
		description string
		input       input
		expected    expected
	}{
		{
			description: "Targeted/OneOperator/ClusterScoped",
			input: input{
				targetNamespace: "z",
				getNamespaces: func() []string {
					return []string{"z"}
				},
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "0", metav1.NamespaceAll),
					},
				},
			},
			expected: expected{
				err: nil,
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("z", "butter", "z/0"),
					},
				},
			},
		},
		{
			description: "Targeted/TwoOperators/MixedScopes",
			input: input{
				targetNamespace: "z",
				getNamespaces: func() []string {
					return []string{"z"}
				},
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "0", metav1.NamespaceAll),
					},
					{
						Type:   watch.Deleted,
						Object: newInstalledOperator("b", "margerine", "1", "a", "b", "c", "z"),
					},
				},
			},
			expected: expected{
				err: nil,
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("z", "butter", "z/0"),
					},
					{
						Type:   watch.Deleted,
						Object: newInstalledOperator("z", "margerine", "z/1"),
					},
				},
			},
		},
		{
			description: "AllNamespaces/OneOperator/ClusterScoped",
			input: input{
				targetNamespace: metav1.NamespaceAll,
				getNamespaces: func() []string {
					return []string{"a", "b", "c", "d"}
				},
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "0", metav1.NamespaceAll),
					},
				},
			},
			expected: expected{
				err: nil,
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "a/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "butter", "b/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("c", "butter", "c/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("d", "butter", "d/0"),
					},
				},
			},
		},
		{
			description: "AllNamespaces/TwoOperators/MixedScopes",
			input: input{
				targetNamespace: metav1.NamespaceAll,
				getNamespaces: func() []string {
					return []string{"a", "b", "c", "d"}
				},
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "0", metav1.NamespaceAll),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "margerine", "1", "b"),
					},
				},
			},
			expected: expected{
				err: nil,
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "a/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "butter", "b/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("c", "butter", "c/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("d", "butter", "d/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "margerine", "b/1"),
					},
				},
			},
		},
		{
			description: "AllNamespaces/ThreeOperators/MixedScopes",
			input: input{
				targetNamespace: metav1.NamespaceAll,
				getNamespaces: func() []string {
					return []string{"a", "b", "c", "d"}
				},
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "0", metav1.NamespaceAll),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "margerine", "1", "b"),
					},
					{
						Type:   watch.Modified,
						Object: newInstalledOperator("c", "oliveoil", "2", "b", "d"),
					},
				},
			},
			expected: expected{
				err: nil,
				events: []watch.Event{
					{
						Type:   watch.Added,
						Object: newInstalledOperator("a", "butter", "a/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "butter", "b/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("c", "butter", "c/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("d", "butter", "d/0"),
					},
					{
						Type:   watch.Added,
						Object: newInstalledOperator("b", "margerine", "b/1"),
					},
					{
						Type:   watch.Modified,
						Object: newInstalledOperator("b", "oliveoil", "b/2"),
					},
					{
						Type:   watch.Modified,
						Object: newInstalledOperator("d", "oliveoil", "d/2"),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			base := watch.NewFake()
			defer base.Stop()

			nctx := request.WithNamespace(ctx, tt.input.targetNamespace)
			sw, err := synthesizingWatch(nctx, base, tt.input.getNamespaces)
			require.Equal(t, tt.expected.err, err)
			defer sw.Stop()

			go func() {
				defer base.Stop()
				for _, event := range tt.input.events {
					base.Action(event.Type, event.Object)
				}
			}()

			received := []watch.Event{}
			for event := range sw.ResultChan() {
				received = append(received, event)
			}
			require.Equal(t, tt.expected.events, received)
			// require.Len(t, received, len(tt.expected.events))

			// for i := 0; i < len(tt.expected.events); i++ {
			// 	expected := tt.expected.events[i]
			// 	actual := received[i]
			// 	require.Equal(t, expected.Type, actual.Type)
			// 	require.Equal(t, expected.Object, actual.Object)
			// }
		})
	}
}

func newInstalledOperator(namespace, name, uid string, targetNamespaces ...string) *porcelain.InstalledOperator {
	io := &porcelain.InstalledOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			UID:         types.UID(uid),
			Annotations: map[string]string{},
		},
	}

	if len(targetNamespaces) > 0 {
		io.GetAnnotations()[operators.OperatorGroupTargetsAnnotationKey] = strings.Join(targetNamespaces, ",")
	}

	return io
}
