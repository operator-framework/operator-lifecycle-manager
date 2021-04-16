package alongside

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestAnnotatable map[string]string

func (a TestAnnotatable) GetAnnotations() map[string]string {
	return a
}

func (a *TestAnnotatable) SetAnnotations(v map[string]string) {
	*a = v
}

func TestAnnotatorFromObject(t *testing.T) {
	for _, tc := range []struct {
		Name            string
		Object          TestAnnotatable
		NamespacedNames []NamespacedName
	}{
		{
			Name: "annotation without prefix ignored",
			Object: TestAnnotatable{
				"foo": "namespace/name",
			},
		},
		{
			Name: "annotation with malformed value ignored",
			Object: TestAnnotatable{
				"operatorframework.io/installed-alongside-0": "namespace/name/color",
			},
		},
		{
			Name: "multiple valid annotations returned",
			Object: TestAnnotatable{
				"operatorframework.io/installed-alongside-0": "namespace-0/name-0",
				"operatorframework.io/installed-alongside-1": "namespace-1/name-1",
			},
			NamespacedNames: []NamespacedName{
				{Namespace: "namespace-0", Name: "name-0"},
				{Namespace: "namespace-1", Name: "name-1"},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.NamespacedNames, Annotator{}.FromObject(&tc.Object))
		})
	}
}

func TestAnnotatorToObject(t *testing.T) {
	for _, tc := range []struct {
		Name            string
		Object          TestAnnotatable
		NamespacedNames []NamespacedName
		Expected        TestAnnotatable
	}{
		{
			Name: "existing annotation removed",
			Object: TestAnnotatable{
				"operatorframework.io/installed-alongside-0": "namespace/name",
			},
		},
		{
			Name: "annotation without prefix ignored",
			Object: TestAnnotatable{
				"operatorframework.io/something-else": "",
			},
			Expected: TestAnnotatable{
				"operatorframework.io/something-else": "",
			},
		},
		{
			Name: "annotation added",
			NamespacedNames: []NamespacedName{
				{Namespace: "namespace-0", Name: "name-0"},
			},
			Expected: TestAnnotatable{
				key(NamespacedName{"namespace-0", "name-0"}): "namespace-0/name-0",
			},
		},
		{
			Name: "replace multiple annotations",
			Object: TestAnnotatable{
				key(NamespacedName{"namespace-0", "name-0"}): "namespace-0/name-0",
				key(NamespacedName{"namespace-1", "name-1"}): "namespace-1/name-1",
			},
			NamespacedNames: []NamespacedName{
				{Namespace: "namespace-2", Name: "name-2"},
				{Namespace: "namespace-3", Name: "name-3"},
			},
			Expected: TestAnnotatable{
				key(NamespacedName{"namespace-2", "name-2"}): "namespace-2/name-2",
				key(NamespacedName{"namespace-3", "name-3"}): "namespace-3/name-3",
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			Annotator{}.ToObject(&tc.Object, tc.NamespacedNames)
			assert.Equal(t, &tc.Expected, &tc.Object)
		})
	}
}
