package olm

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/metadata/metadatalister"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ktesting "k8s.io/client-go/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
)

func TestCopyToNamespace(t *testing.T) {
	gvr := v1alpha1.SchemeGroupVersion.WithResource("clusterserviceversions")

	for _, tc := range []struct {
		Name            string
		FromNamespace   string
		ToNamespace     string
		Hash            string
		StatusHash      string
		Prototype       v1alpha1.ClusterServiceVersion
		ExistingCopy    *metav1.PartialObjectMetadata
		ExpectedResult  *v1alpha1.ClusterServiceVersion
		ExpectedError   error
		ExpectedActions []ktesting.Action
	}{
		{
			Name:          "copy to original namespace returns error",
			FromNamespace: "samesies",
			ToNamespace:   "samesies",
			ExpectedError: fmt.Errorf("bug: can not copy to active namespace samesies"),
		},
		{
			Name:          "copy created if does not exist",
			FromNamespace: "from",
			ToNamespace:   "to",
			Prototype: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Replaces: "replacee",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: "waxing gibbous",
				},
			},
			ExpectedActions: []ktesting.Action{
				ktesting.NewCreateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "to",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				ktesting.NewUpdateSubresourceAction(gvr, "status", "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "to",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
				},
			},
		},
		{
			Name:          "copy updated if hash differs",
			FromNamespace: "from",
			ToNamespace:   "to",
			Hash:          "hn-1",
			StatusHash:    "hs",
			Prototype: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Replaces: "replacee",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: "waxing gibbous",
				},
			},
			ExistingCopy: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "to",
					UID:             "uid",
					ResourceVersion: "42",
					Annotations: map[string]string{
						nonStatusCopyHashAnnotation: "hn-2",
						statusCopyHashAnnotation:    "hs",
					},
				},
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
					UID:       "uid",
				},
			},
			ExpectedActions: []ktesting.Action{
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
			},
		},
		{
			Name:          "copy status updated if status hash differs",
			FromNamespace: "from",
			ToNamespace:   "to",
			Hash:          "hn",
			StatusHash:    "hs-1",
			Prototype: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Replaces: "replacee",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: "waxing gibbous",
				},
			},
			ExistingCopy: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "to",
					UID:             "uid",
					ResourceVersion: "42",
					Annotations: map[string]string{
						nonStatusCopyHashAnnotation: "hn",
						statusCopyHashAnnotation:    "hs-2",
					},
				},
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
					UID:       "uid",
				},
			},
			ExpectedActions: []ktesting.Action{
				ktesting.NewUpdateSubresourceAction(gvr, "status", "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
			},
		},
		{
			Name:          "copy and copy status updated if both hashes differ",
			FromNamespace: "from",
			ToNamespace:   "to",
			Hash:          "hn-1",
			StatusHash:    "hs-1",
			Prototype: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Replaces: "replacee",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: "waxing gibbous",
				},
			},
			ExistingCopy: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "to",
					UID:             "uid",
					ResourceVersion: "42",
					Annotations: map[string]string{
						nonStatusCopyHashAnnotation: "hn-2",
						statusCopyHashAnnotation:    "hs-2",
					},
				},
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
					UID:       "uid",
				},
			},
			ExpectedActions: []ktesting.Action{
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				ktesting.NewUpdateSubresourceAction(gvr, "status", "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
			},
		},
		{
			Name:          "no action taken if neither hash differs",
			FromNamespace: "from",
			ToNamespace:   "to",
			Hash:          "hn",
			StatusHash:    "hs",
			Prototype: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "name",
				},
			},
			ExistingCopy: &metav1.PartialObjectMetadata{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
					UID:       "uid",
					Annotations: map[string]string{
						nonStatusCopyHashAnnotation: "hn",
						statusCopyHashAnnotation:    "hs",
					},
				},
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
					UID:       "uid",
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			var lister metadatalister.Lister
			if tc.ExistingCopy != nil {
				client = fake.NewSimpleClientset(&v1alpha1.ClusterServiceVersion{
					ObjectMeta: tc.ExistingCopy.ObjectMeta,
				})
				lister = FakeClusterServiceVersionLister{tc.ExistingCopy}
			} else {
				lister = FakeClusterServiceVersionLister{{}}
			}

			logger, _ := test.NewNullLogger()
			o := &Operator{
				copiedCSVLister: lister,
				client:          client,
				logger:          logger,
			}

			result, err := o.copyToNamespace(tc.Prototype.DeepCopy(), tc.FromNamespace, tc.ToNamespace, tc.Hash, tc.StatusHash)

			if tc.ExpectedError == nil {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.ExpectedError.Error())
			}
			if diff := cmp.Diff(tc.ExpectedResult, result); diff != "" {
				t.Errorf("incorrect result: %v", diff)
			}

			actions := client.Actions()
			if len(actions) == 0 {
				actions = nil
			}
			if diff := cmp.Diff(tc.ExpectedActions, actions); diff != "" {
				t.Errorf("incorrect actions: %v", diff)
			}
		})
	}
}

type FakeClusterServiceVersionLister []*metav1.PartialObjectMetadata

func (l FakeClusterServiceVersionLister) List(selector labels.Selector) ([]*metav1.PartialObjectMetadata, error) {
	var result []*metav1.PartialObjectMetadata
	for _, csv := range l {
		if !selector.Matches(labels.Set(csv.GetLabels())) {
			continue
		}
		result = append(result, csv)
	}
	return result, nil
}

func (l FakeClusterServiceVersionLister) Namespace(namespace string) metadatalister.NamespaceLister {
	var filtered []*metav1.PartialObjectMetadata
	for _, csv := range l {
		if csv.GetNamespace() != namespace {
			continue
		}
		filtered = append(filtered, csv)
	}
	return FakeClusterServiceVersionLister(filtered)
}

func (l FakeClusterServiceVersionLister) Get(name string) (*metav1.PartialObjectMetadata, error) {
	for _, csv := range l {
		if csv.GetName() == name {
			return csv, nil
		}
	}
	return nil, errors.NewNotFound(v1alpha1.Resource("clusterserviceversion"), name)
}

var (
	_ metadatalister.Lister          = FakeClusterServiceVersionLister{}
	_ metadatalister.NamespaceLister = FakeClusterServiceVersionLister{}
)

func TestCSVCopyPrototype(t *testing.T) {
	src := v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "name",
			Namespace: "foo",
			Annotations: map[string]string{
				"olm.targetNamespaces":                             "a,b,c",
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
				"preserved": "yes",
			},
			Labels: map[string]string{
				"operators.coreos.com/foo": "",
				"operators.coreos.com/bar": "",
				"untouched":                "fine",
			},
		},
	}
	var dst v1alpha1.ClusterServiceVersion
	csvCopyPrototype(&src, &dst)
	assert.Equal(t, v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "name",
			Annotations: map[string]string{
				"preserved": "yes",
			},
			Labels: map[string]string{
				"untouched":      "fine",
				"olm.copiedFrom": "foo",
			},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{
			Message: "The operator is running in foo but is managing this namespace",
			Reason:  v1alpha1.CSVReasonCopied,
		},
	}, dst)
}
