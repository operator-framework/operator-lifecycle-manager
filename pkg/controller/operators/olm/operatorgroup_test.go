package olm

import (
	"io/ioutil"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ktesting "k8s.io/client-go/testing"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	listersv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes"
)

func TestCopyToNamespace(t *testing.T) {
	gvr := v1alpha1.SchemeGroupVersion.WithResource("clusterserviceversions")

	for _, tc := range []struct {
		Name            string
		Namespace       string
		Original        *v1alpha1.ClusterServiceVersion
		ExistingCopy    *v1alpha1.ClusterServiceVersion
		ExpectedResult  *v1alpha1.ClusterServiceVersion
		ExpectedError   string
		ExpectedActions []ktesting.Action
	}{
		{
			Name:      "copy to original namespace returns error",
			Namespace: "foo",
			Original: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
				},
			},
			ExpectedError: "bug: can not copy to active namespace foo",
		},
		{
			Name:      "status updated if meaningfully changed",
			Namespace: "bar",
			Original: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "foo",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					LastUpdateTime: &metav1.Time{Time: time.Unix(2, 0)},
				},
			},
			ExistingCopy: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					LastUpdateTime: &metav1.Time{Time: time.Unix(1, 0)},
				},
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					LastUpdateTime: &metav1.Time{Time: time.Unix(2, 0)},
					Message:        "The operator is running in foo but is managing this namespace",
					Reason:         v1alpha1.CSVReasonCopied,
				},
			},
			ExpectedActions: []ktesting.Action{
				ktesting.NewUpdateSubresourceAction(gvr, "status", "bar", &v1alpha1.ClusterServiceVersion{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "name",
						Namespace: "bar",
					},
					Status: v1alpha1.ClusterServiceVersionStatus{
						LastUpdateTime: &metav1.Time{Time: time.Unix(2, 0)},
						Message:        "The operator is running in foo but is managing this namespace",
						Reason:         v1alpha1.CSVReasonCopied,
					},
				}),
			},
		},
		{
			Name:      "status not updated if not meaningfully changed",
			Namespace: "bar",
			Original: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "foo",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					LastUpdateTime: &metav1.Time{Time: time.Unix(2, 0)},
				},
			},
			ExistingCopy: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					LastUpdateTime: &metav1.Time{Time: time.Unix(2, 0)},
					Message:        "The operator is running in foo but is managing this namespace",
					Reason:         v1alpha1.CSVReasonCopied},
			},
			ExpectedResult: &v1alpha1.ClusterServiceVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "name",
					Namespace: "bar",
				},
				Status: v1alpha1.ClusterServiceVersionStatus{
					LastUpdateTime: &metav1.Time{Time: time.Unix(2, 0)},
					Message:        "The operator is running in foo but is managing this namespace",
					Reason:         v1alpha1.CSVReasonCopied,
				},
			},
		},
	} {
		t.Run(tc.Name, func(t *testing.T) {
			lister := &operatorlisterfakes.FakeOperatorLister{}
			v1alpha1lister := &operatorlisterfakes.FakeOperatorsV1alpha1Lister{}
			lister.OperatorsV1alpha1Returns(v1alpha1lister)

			client := fake.NewSimpleClientset()
			if tc.ExistingCopy != nil {
				client = fake.NewSimpleClientset(tc.ExistingCopy)
				v1alpha1lister.ClusterServiceVersionListerReturns(FakeClusterServiceVersionLister{tc.ExistingCopy})
			}

			logger := logrus.New()
			logger.SetOutput(ioutil.Discard)

			o := &Operator{
				lister: lister,
				client: client,
				logger: logger,
			}
			result, err := o.copyToNamespace(tc.Original, tc.Namespace)

			require := require.New(t)
			if tc.ExpectedError == "" {
				require.NoError(err)
			} else {
				require.EqualError(err, tc.ExpectedError)
			}
			require.Equal(tc.ExpectedResult, result)

			actions := client.Actions()
			if len(actions) == 0 {
				actions = nil
			}
			require.Equal(tc.ExpectedActions, actions)
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
