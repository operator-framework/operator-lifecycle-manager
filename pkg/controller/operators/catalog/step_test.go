package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	olmfake "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	v1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks"
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

// TestNewBundleSecretStep is a regression test for OCPBUGS-35210.
//
// SA-token Secrets appear earlier in the InstallPlan step list than their
// owning ServiceAccount. Without the fix, OLM creates the Secret before the
// SA exists; KCM's TokensController immediately deletes orphaned token
// secrets, and OLM marks the step Created permanently with no retry path.
//
// The fix: NewBundleSecretStep returns WaitingForAPI when the SA is absent
// (so NeedsRequeue keeps phase=Installing) and creates the Secret once the
// SA exists on the next reconcile.
func TestNewBundleSecretStep(t *testing.T) {
	const (
		namespace = "test-ns"
		saName    = "test-operator-sa"
		secName   = "test-operator-metrics-token"
		csvName   = "test-operator.v1.0.0"
	)

	// Build the SA-token Secret manifest that would appear in the bundle.
	tokenSecret := corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secName,
			Namespace: namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: saName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	manifest, err := json.Marshal(&tokenSecret)
	require.NoError(t, err)

	step := &v1alpha1.Step{
		Resolving: csvName,
		Status:    v1alpha1.StepStatusUnknown,
		Resource: v1alpha1.StepResource{
			Name:     secName,
			Kind:     "BundleSecret",
			Manifest: string(manifest),
		},
	}

	csv := v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      csvName,
			Namespace: namespace,
			UID:       "test-csv-uid",
		},
	}
	plan := &v1alpha1.InstallPlan{
		ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: namespace},
	}

	t.Run("SA absent: returns WaitingForAPI without creating Secret", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// No SA in the fake client — SA is absent.
		fakeK8s := k8sfake.NewSimpleClientset()
		mockClient := operatorclientmocks.NewMockClientInterface(ctrl)
		mockClient.EXPECT().KubernetesInterface().Return(fakeK8s).AnyTimes()

		csvIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
		_ = csvIndexer.Add(&csv)
		csvLister := v1alpha1listers.NewClusterServiceVersionLister(csvIndexer)

		b := &builder{
			plan:             plan,
			attenuatedClient: mockClient,
			csvLister:        csvLister,
			olmClient:        olmfake.NewSimpleClientset(&csv),
		}

		stepCopy := *step
		stepCopy.Status = v1alpha1.StepStatusUnknown
		status, err := b.NewBundleSecretStep(&stepCopy, string(manifest))()

		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.StepStatusWaitingForAPI, status,
			"should return WaitingForAPI when SA is absent so NeedsRequeue keeps phase=Installing")

		// Secret must NOT have been created.
		_, getErr := fakeK8s.CoreV1().Secrets(namespace).Get(context.TODO(), secName, metav1.GetOptions{})
		assert.True(t, errors.IsNotFound(getErr),
			"Secret must not be created before its ServiceAccount exists (OCPBUGS-35210)")
	})

	t.Run("SA present: creates Secret and returns Created", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: namespace},
		}
		fakeK8s := k8sfake.NewSimpleClientset(sa)
		mockClient := operatorclientmocks.NewMockClientInterface(ctrl)
		mockClient.EXPECT().KubernetesInterface().Return(fakeK8s).AnyTimes()

		csvIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
		_ = csvIndexer.Add(&csv)
		csvLister := v1alpha1listers.NewClusterServiceVersionLister(csvIndexer)

		b := &builder{
			plan:             plan,
			attenuatedClient: mockClient,
			csvLister:        csvLister,
			olmClient:        olmfake.NewSimpleClientset(&csv),
		}

		stepCopy := *step
		stepCopy.Status = v1alpha1.StepStatusWaitingForAPI // simulates the retry reconcile
		status, err := b.NewBundleSecretStep(&stepCopy, string(manifest))()

		assert.NoError(t, err)
		assert.Equal(t, v1alpha1.StepStatusCreated, status,
			"should create the Secret and return Created once the SA exists")

		// Secret must exist.
		secret, getErr := fakeK8s.CoreV1().Secrets(namespace).Get(context.TODO(), secName, metav1.GetOptions{})
		require.NoError(t, getErr, "Secret should have been created (OCPBUGS-35210)")
		assert.Equal(t, corev1.SecretTypeServiceAccountToken, secret.Type)
		assert.Equal(t, saName, secret.Annotations[corev1.ServiceAccountNameKey])
	})
}
