package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
)

func TestSetInstalledAlongsideAnnotation(t *testing.T) {
	for _, tc := range []struct {
		Name         string
		NewNamespace string
		NewName      string
		CSVs         []v1alpha1.ClusterServiceVersion
		Before       []alongside.NamespacedName
		After        []alongside.NamespacedName
	}{
		{
			Name:         "object annotated with bundle name",
			NewNamespace: "test-namespace",
			NewName:      "test-name",
			After: []alongside.NamespacedName{
				{Namespace: "test-namespace", Name: "test-name"},
			},
		},
		{
			Name:         "annotations referencing missing bundles removed",
			NewNamespace: "test-namespace",
			NewName:      "test-name",
			Before: []alongside.NamespacedName{
				{Namespace: "missing-namespace", Name: "missing-name"},
			},
			After: []alongside.NamespacedName{
				{Namespace: "test-namespace", Name: "test-name"},
			},
		},
		{
			Name:         "annotations referencing copied csv removed",
			NewNamespace: "test-namespace",
			NewName:      "test-name",
			CSVs: []v1alpha1.ClusterServiceVersion{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "copied-namespace",
						Name:      "copied-name",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Reason: v1alpha1.CSVReasonCopied,
					},
				},
			},
			Before: []alongside.NamespacedName{
				{Namespace: "copied-namespace", Name: "copied-name"},
			},
			After: []alongside.NamespacedName{
				{Namespace: "test-namespace", Name: "test-name"},
			},
		},
		{
			Name:         "annotations referencing found bundles preserved",
			NewNamespace: "test-namespace",
			NewName:      "test-name",
			CSVs: []v1alpha1.ClusterServiceVersion{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "found-namespace",
						Name:      "found-name",
					},
				},
			},
			Before: []alongside.NamespacedName{
				{Namespace: "found-namespace", Name: "found-name"},
			},
			After: []alongside.NamespacedName{
				{Namespace: "found-namespace", Name: "found-name"},
				{Namespace: "test-namespace", Name: "test-name"},
			},
		},
		{
			Name:         "nothing added if namespace empty",
			NewNamespace: "",
			NewName:      "test-name",
			CSVs: []v1alpha1.ClusterServiceVersion{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "found-namespace",
						Name:      "found-name",
					},
				},
			},
			Before: []alongside.NamespacedName{
				{Namespace: "found-namespace", Name: "found-name"},
			},
			After: []alongside.NamespacedName{
				{Namespace: "found-namespace", Name: "found-name"},
			},
		},
		{
			Name:         "nothing added if name empty",
			NewNamespace: "test-namespace",
			NewName:      "",
			CSVs: []v1alpha1.ClusterServiceVersion{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "found-namespace",
						Name:      "found-name",
					},
				},
			},
			Before: []alongside.NamespacedName{
				{Namespace: "found-namespace", Name: "found-name"},
			},
			After: []alongside.NamespacedName{
				{Namespace: "found-namespace", Name: "found-name"},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			csvsByNamespace := make(map[string][]*v1alpha1.ClusterServiceVersion)
			for _, csv := range tc.CSVs {
				csvsByNamespace[csv.GetNamespace()] = append(csvsByNamespace[csv.GetNamespace()], csv.DeepCopy())
			}

			nsListers := make(map[string]v1alpha1listers.ClusterServiceVersionNamespaceLister)
			for ns, csvs := range csvsByNamespace {
				ns := ns
				csvs := csvs
				nslister := &operatorlisterfakes.FakeClusterServiceVersionNamespaceLister{}
				nslister.GetCalls(func(name string) (*v1alpha1.ClusterServiceVersion, error) {
					for _, csv := range csvs {
						if csv.GetName() == name {
							return csv, nil
						}
					}
					return nil, errors.NewNotFound(schema.GroupResource{}, name)
				})
				nsListers[ns] = nslister
			}

			emptyLister := &operatorlisterfakes.FakeClusterServiceVersionNamespaceLister{}
			emptyLister.GetCalls(func(name string) (*v1alpha1.ClusterServiceVersion, error) {
				return nil, errors.NewNotFound(schema.GroupResource{}, name)
			})

			csvLister := &operatorlisterfakes.FakeClusterServiceVersionLister{}
			csvLister.ClusterServiceVersionsCalls(func(namespace string) v1alpha1listers.ClusterServiceVersionNamespaceLister {
				if lister, ok := nsListers[namespace]; ok {
					return lister
				}
				return emptyLister
			})

			var (
				dst, src metav1.ObjectMeta
				a        alongside.Annotator
			)
			a.ToObject(&src, tc.Before)
			setInstalledAlongsideAnnotation(a, &dst, tc.NewNamespace, tc.NewName, csvLister, &src)
			after := a.FromObject(&dst)
			assert.ElementsMatch(t, tc.After, after)
		})
	}
}
