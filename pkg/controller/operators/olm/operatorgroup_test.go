package olm

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/metadata/metadatalister"
	ktesting "k8s.io/client-go/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

// fakeCSVNamespaceLister implements metadatalister.NamespaceLister
type fakeCSVNamespaceLister struct {
	namespace string
	items     []*metav1.PartialObjectMetadata
}

func (n *fakeCSVNamespaceLister) List(selector labels.Selector) ([]*metav1.PartialObjectMetadata, error) {
	var result []*metav1.PartialObjectMetadata
	for _, item := range n.items {
		if item != nil && item.Namespace == n.namespace {
			result = append(result, item)
		}
	}
	return result, nil
}

func (n *fakeCSVNamespaceLister) Get(name string) (*metav1.PartialObjectMetadata, error) {
	for _, item := range n.items {
		if item != nil && item.Namespace == n.namespace && item.Name == name {
			return item, nil
		}
	}
	return nil, errors.NewNotFound(v1alpha1.Resource("clusterserviceversion"), name)
}

// fakeCSVLister implements the full metadatalister.Lister interface
// so that Operator.copiedCSVLister = &fakeCSVLister{...} works.
type fakeCSVLister struct {
	items []*metav1.PartialObjectMetadata
}

// List returns all CSV metadata items, ignoring namespaces.
func (f *fakeCSVLister) List(selector labels.Selector) ([]*metav1.PartialObjectMetadata, error) {
	return f.items, nil
}

// Get returns the CSV by name, ignoring namespaces.
func (f *fakeCSVLister) Get(name string) (*metav1.PartialObjectMetadata, error) {
	for _, item := range f.items {
		if item != nil && item.Name == name {
			return item, nil
		}
	}
	return nil, errors.NewNotFound(v1alpha1.Resource("clusterserviceversion"), name)
}

// Namespace returns a namespace-scoped lister wrapper.
func (f *fakeCSVLister) Namespace(ns string) metadatalister.NamespaceLister {
	return &fakeCSVNamespaceLister{
		namespace: ns,
		items:     f.items,
	}
}

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
			Hash:          "hn-1",
			StatusHash:    "hs",
			Prototype: v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "name",
					Annotations: map[string]string{},
				},
				Spec: v1alpha1.ClusterServiceVersionSpec{
					Replaces: "replacee",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					Phase: "waxing gibbous",
				},
			},
			// No ExistingCopy: means there's nothing in "to" namespace yet.
			ExpectedActions: []ktesting.Action{
				// Create the new CSV with nonStatusCopyHashAnnotation
				ktesting.NewCreateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "to",
						Annotations: map[string]string{
							"olm.operatorframework.io/nonStatusCopyHash": "hn-1",
						},
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				// UpdateStatus: note that name/namespace remain "name"/"to",
				//    and we still have nonStatusCopyHashAnnotation: "hn-1".
				ktesting.NewUpdateSubresourceAction(gvr, "status", "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "to",
						Annotations: map[string]string{
							"olm.operatorframework.io/nonStatusCopyHash": "hn-1",
						},
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				// Normal Update for the statusCopyHashAnnotation = "hs"
				//    We still keep the "hn-1" annotation as well, plus Name/Namespace as is.
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "to",
						Annotations: map[string]string{
							"olm.operatorframework.io/nonStatusCopyHash": "hn-1",
							"olm.operatorframework.io/statusCopyHash":    "hs",
						},
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
					Name:        "name",
					Annotations: map[string]string{},
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
						nonStatusCopyHashAnnotation: "hn-2", // differs => triggers normal Update
						statusCopyHashAnnotation:    "hs",   // same => no status update
					},
				},
			},
			ExpectedActions: []ktesting.Action{
				// Non-status differs => 1 normal Update
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
						// We'll set the new nonStatusCopyHashAnnotation = "hn-1"
						Annotations: map[string]string{
							nonStatusCopyHashAnnotation: "hn-1",
						},
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
					UID:       "uid",
				},
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
					Name:        "name",
					Annotations: map[string]string{},
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
						// non-status matches => no normal update
						nonStatusCopyHashAnnotation: "hn",
						// status differs => subresource + normal update
						statusCopyHashAnnotation: "hs-2",
					},
				},
			},
			ExpectedActions: []ktesting.Action{
				// UpdateStatus (we set the new status, and the in-memory CSV includes the matching nonStatus)
				ktesting.NewUpdateSubresourceAction(gvr, "status", "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
						Annotations: map[string]string{
							nonStatusCopyHashAnnotation: "hn",
						},
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				// Normal Update to set statusCopyHashAnnotation = "hs-1"
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
						Annotations: map[string]string{
							nonStatusCopyHashAnnotation: "hn",
							statusCopyHashAnnotation:    "hs-1",
						},
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
					UID:       "uid",
				},
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
					Name:        "name",
					Annotations: map[string]string{},
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
						// Both nonStatus and status mismatch
						nonStatusCopyHashAnnotation: "hn-2",
						statusCopyHashAnnotation:    "hs-2",
					},
				},
			},
			ExpectedActions: []ktesting.Action{
				// Normal update for the non-status mismatch
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
						Annotations: map[string]string{
							nonStatusCopyHashAnnotation: "hn-1",
						},
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				// UpdateStatus because status hash mismatch
				ktesting.NewUpdateSubresourceAction(gvr, "status", "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
						Annotations: map[string]string{
							nonStatusCopyHashAnnotation: "hn-1",
						},
					},
					Spec: v1alpha1.ClusterServiceVersionSpec{
						Replaces: "replacee",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						Phase: "waxing gibbous",
					},
				}),
				// Normal update for the new statusCopyHashAnnotation
				ktesting.NewUpdateAction(gvr, "to", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:            "name",
						Namespace:       "to",
						UID:             "uid",
						ResourceVersion: "42",
						Annotations: map[string]string{
							nonStatusCopyHashAnnotation: "hn-1",
							statusCopyHashAnnotation:    "hs-1",
						},
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
					UID:       "uid",
				},
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
					Name:        "name",
					Annotations: map[string]string{},
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
			ExpectedActions: nil, // no update calls if neither hash differs
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			// Create a new fake clientset populated with the "existing copy" if any
			client := fake.NewSimpleClientset()
			var lister metadatalister.Lister

			// If we have an existing CSV in that target namespace, add it to the slice
			items := []*metav1.PartialObjectMetadata{}
			if tc.ExistingCopy != nil {
				existingObj := &v1alpha1.ClusterServiceVersion{
					ObjectMeta: tc.ExistingCopy.ObjectMeta,
					// ... if you want to set Spec/Status in the client, you can
				}
				client = fake.NewSimpleClientset(existingObj)
				items = []*metav1.PartialObjectMetadata{tc.ExistingCopy}
			}

			// Create the full Lister
			lister = &fakeCSVLister{
				items: items,
			}

			logger, _ := test.NewNullLogger()
			o := &Operator{
				copiedCSVLister: lister,
				client:          client,
				logger:          logger,
			}

			proto := tc.Prototype.DeepCopy()
			result, err := o.copyToNamespace(proto, tc.FromNamespace, tc.ToNamespace, tc.Hash, tc.StatusHash)

			if tc.ExpectedError == nil {
				require.NoError(t, err)

				// Ensure the in-memory 'proto' has the correct final annotations
				annotations := proto.GetObjectMeta().GetAnnotations()
				require.Equal(t, tc.Hash, annotations[nonStatusCopyHashAnnotation],
					"proto should have the non-status hash annotation set")
				require.Equal(t, tc.StatusHash, annotations[statusCopyHashAnnotation],
					"proto should have the status hash annotation set")
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

func TestOperator_getClusterRoleAggregationRule(t *testing.T) {
	tests := []struct {
		name    string
		apis    cache.APISet
		suffix  string
		want    func(*testing.T, *rbacv1.AggregationRule)
		wantErr require.ErrorAssertionFunc
	}{
		{
			name:   "no aggregation rule when no APIs",
			apis:   cache.APISet{},
			suffix: "admin",
			want: func(t *testing.T, rule *rbacv1.AggregationRule) {
				require.Nil(t, rule)
			},
			wantErr: require.NoError,
		},
		{
			name: "ordered selectors in aggregation rule",
			apis: cache.APISet{
				registry.APIKey{Group: "example.com", Version: "v1alpha1", Kind: "Foo"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha2", Kind: "Foo"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha3", Kind: "Foo"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha4", Kind: "Foo"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha5", Kind: "Foo"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha1", Kind: "Bar"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha2", Kind: "Bar"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha3", Kind: "Bar"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha4", Kind: "Bar"}: {},
				registry.APIKey{Group: "example.com", Version: "v1alpha5", Kind: "Bar"}: {},
			},
			suffix: "admin",
			want: func(t *testing.T, rule *rbacv1.AggregationRule) {
				getOneKey := func(t *testing.T, m map[string]string) string {
					require.Len(t, m, 1)
					for k := range m {
						return k
					}
					t.Fatalf("no keys found in map")
					return ""
				}

				a := getOneKey(t, rule.ClusterRoleSelectors[0].MatchLabels)
				for _, selector := range rule.ClusterRoleSelectors[1:] {
					b := getOneKey(t, selector.MatchLabels)
					require.Lessf(t, a, b, "expected selector match labels keys to be in sorted ascending order")
					a = b
				}
			},
			wantErr: require.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Operator{}
			got, err := a.getClusterRoleAggregationRule(tt.apis, tt.suffix)
			tt.wantErr(t, err)
			tt.want(t, got)
		})
	}
}
