package olm

import (
	"fmt"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ktesting "k8s.io/client-go/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
	"github.com/operator-framework/operator-registry/pkg/registry"
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
		ExistingCopy    *v1alpha1.ClusterServiceVersion
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
			ExistingCopy: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "to",
					UID:             "uid",
					ResourceVersion: "42",
					Annotations: map[string]string{
						"$copyhash-nonstatus": "hn-2",
						"$copyhash-status":    "hs",
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
			ExistingCopy: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "to",
					UID:             "uid",
					ResourceVersion: "42",
					Annotations: map[string]string{
						"$copyhash-spec":   "hn",
						"$copyhash-status": "hs-2",
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
			ExistingCopy: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "name",
					Namespace:       "to",
					UID:             "uid",
					ResourceVersion: "42",
					Annotations: map[string]string{
						"$copyhash-spec":   "hn-2",
						"$copyhash-status": "hs-2",
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
			ExistingCopy: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "to",
					UID:       "uid",
					Annotations: map[string]string{
						"$copyhash-spec":   "hn",
						"$copyhash-status": "hs",
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
			lister := &operatorlisterfakes.FakeOperatorLister{}
			v1alpha1lister := &operatorlisterfakes.FakeOperatorsV1alpha1Lister{}
			lister.OperatorsV1alpha1Returns(v1alpha1lister)
			//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
			client := fake.NewSimpleClientset()

			if tc.ExistingCopy != nil {
				//nolint:staticcheck // SA1019: NewClientset not available until apply configurations are generated
				client = fake.NewSimpleClientset(tc.ExistingCopy)
				v1alpha1lister.ClusterServiceVersionListerReturns(FakeClusterServiceVersionLister{tc.ExistingCopy})
			} else {
				v1alpha1lister.ClusterServiceVersionListerReturns(FakeClusterServiceVersionLister(nil))
			}

			logger, _ := test.NewNullLogger()
			o := &Operator{
				copiedCSVLister: v1alpha1lister.ClusterServiceVersionLister(),
				client:          client,
				logger:          logger,
			}

			result, err := o.copyToNamespace(tc.Prototype.DeepCopy(), tc.FromNamespace, tc.ToNamespace, tc.Hash, tc.StatusHash)

			if tc.ExpectedError == nil {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.ExpectedError.Error())
			}
			assert.Equal(t, tc.ExpectedResult, result)

			actions := client.Actions()
			if len(actions) == 0 {
				actions = nil
			}
			assert.Equal(t, tc.ExpectedActions, actions)
		})
	}
}

type FakeClusterServiceVersionLister []*v1alpha1.ClusterServiceVersion

func (l FakeClusterServiceVersionLister) List(selector labels.Selector) ([]*v1alpha1.ClusterServiceVersion, error) {
	var result []*v1alpha1.ClusterServiceVersion
	for _, csv := range l {
		if !selector.Matches(labels.Set(csv.GetLabels())) {
			continue
		}
		result = append(result, csv)
	}
	return result, nil
}

func (l FakeClusterServiceVersionLister) ClusterServiceVersions(namespace string) listersv1alpha1.ClusterServiceVersionNamespaceLister {
	var filtered []*v1alpha1.ClusterServiceVersion
	for _, csv := range l {
		if csv.GetNamespace() != namespace {
			continue
		}
		filtered = append(filtered, csv)
	}
	return FakeClusterServiceVersionLister(filtered)
}

func (l FakeClusterServiceVersionLister) Get(name string) (*v1alpha1.ClusterServiceVersion, error) {
	for _, csv := range l {
		if csv.GetName() == name {
			return csv, nil
		}
	}
	return nil, errors.NewNotFound(v1alpha1.Resource("clusterserviceversion"), name)
}

var (
	_ listersv1alpha1.ClusterServiceVersionLister          = FakeClusterServiceVersionLister{}
	_ listersv1alpha1.ClusterServiceVersionNamespaceLister = FakeClusterServiceVersionLister{}
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
