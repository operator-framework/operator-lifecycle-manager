package olm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/annotator"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/fakes"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	opFake "github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"k8s.io/api/apps/v1beta2"
	"k8s.io/api/core/v1"
)

type MockALMOperator struct {
	Operator
	MockQueueOperator    *queueinformer.MockOperator
	ClientFake           *fake.Clientset
	MockOpClient         *operatorclient.MockClientInterface
	TestQueueInformer    queueinformer.TestQueueInformer
	StrategyResolverFake *fakes.FakeStrategyResolverInterface
}

type Expect func()

// Helpers

func mockCRDExistence(mockClient operatorclient.MockClientInterface, crdDescriptions []v1alpha1.CRDDescription) {
	for _, crd := range crdDescriptions {
		if strings.HasPrefix(crd.Name, "nonExistent") {
			mockClient.EXPECT().ApiextensionsV1beta1Interface().Return(apiextensionsfake.NewSimpleClientset())
		}
		if strings.HasPrefix(crd.Name, "found") {
			crd := v1beta1.CustomResourceDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name: crd.Name,
				},
			}
			var objects []runtime.Object
			objects = append(objects, &crd)
			mockClient.EXPECT().ApiextensionsV1beta1Interface().Return(apiextensionsfake.NewSimpleClientset(objects...))
		}
	}
}

func mockIntermediates(t *testing.T, mockOpClient *operatorclient.MockClientInterface, resolverFake *fakes.FakeStrategyResolverInterface, current *v1alpha1.ClusterServiceVersion, intermediates []*v1alpha1.ClusterServiceVersion) Expect {
	mockCSVsInNamespace(t, mockOpClient, current.GetNamespace(), intermediates, nil)
	prevCSV := current

	expectFns := []func(){}
	call := -2
	for i, csv := range intermediates {
		call += 2
		mockIsReplacing(t, mockOpClient, prevCSV, csv, nil)
		testInstallStrategy := TestStrategy{}
		resolverFake.UnmarshalStrategyReturns(&testInstallStrategy, nil)
		resolverFake.UnmarshalStrategyReturns(&testInstallStrategy, nil)
		resolverFake.InstallerForStrategyReturns(NewTestInstaller(nil, nil))
		expectFns = append(expectFns, func() {
			require.Equal(t, csv.Spec.InstallStrategy, resolverFake.UnmarshalStrategyArgsForCall(call))
			require.Equal(t, prevCSV.Spec.InstallStrategy, resolverFake.UnmarshalStrategyArgsForCall(call+1))
			name, opClient, _, strategy := resolverFake.InstallerForStrategyArgsForCall(i)
			require.Equal(t, testInstallStrategy.GetStrategyName(), name)
			require.Equal(t, mockOpClient, opClient)
			require.Equal(t, &testInstallStrategy, strategy)
		})
		prevCSV = csv
	}
	// Return a set of expectations that can be deferred until the test fn has finished
	return func() {
		for _, fn := range expectFns {
			fn()
		}
	}
}

func mockIsReplacing(t *testing.T, mockOpClient *operatorclient.MockClientInterface, prevCSV *v1alpha1.ClusterServiceVersion, currentCSV *v1alpha1.ClusterServiceVersion, csvQueryErr error) {
	var unstructuredOldCSV *unstructured.Unstructured = nil
	if prevCSV != nil {
		unst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(prevCSV)
		require.NoError(t, err)
		unstructuredOldCSV = &unstructured.Unstructured{Object: unst}
	} else {
		unstructuredOldCSV = nil
	}

	if currentCSV.Spec.Replaces != "" {
		mockOpClient.EXPECT().GetCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, currentCSV.GetNamespace(), v1alpha1.ClusterServiceVersionKind, currentCSV.Spec.Replaces).Return(unstructuredOldCSV, csvQueryErr)
	}
}

func mockCSVsInNamespace(t *testing.T, mockOpClient *operatorclient.MockClientInterface, namespace string, csvsInNamespace []*v1alpha1.ClusterServiceVersion, csvQueryErr error) {
	unstructuredCSVs := []*unstructured.Unstructured{}
	for _, csv := range csvsInNamespace {
		unst, err := runtime.DefaultUnstructuredConverter.ToUnstructured(csv)
		require.NoError(t, err)
		unstructuredCSVs = append(unstructuredCSVs, &unstructured.Unstructured{Object: unst})
	}
	csvList := &operatorclient.CustomResourceList{Items: unstructuredCSVs}

	mockOpClient.EXPECT().ListCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, namespace, v1alpha1.ClusterServiceVersionKind).Return(csvList, csvQueryErr)
}

func mockInstallStrategy(t *testing.T, resolverFake *fakes.FakeStrategyResolverInterface, strategy *v1alpha1.NamedInstallStrategy, installErr error, checkInstallErr error, prevStrategy *v1alpha1.NamedInstallStrategy, prevCSVQueryErr error) Expect {
	testInstallStrategy := TestStrategy{}
	expectFns := []func(){}
	stratErr := fmt.Errorf("couldn't unmarshal install strategy")
	if strategy.StrategyName == "teststrategy" {
		stratErr = nil
	}
	resolverFake.UnmarshalStrategyReturns(&testInstallStrategy, stratErr)
	expectFns = append(expectFns, func() {
		strat := resolverFake.UnmarshalStrategyArgsForCall(0)
		require.Equal(t, strategy, strat)
	})
	if stratErr == nil {
		resolverFake.InstallerForStrategyReturns(NewTestInstaller(installErr, checkInstallErr))
		expectFns = append(expectFns, func() {
			strategyName, _, _, prev := resolverFake.InstallerForStrategyArgsForCall(0)
			require.Equal(t, (&testInstallStrategy).GetStrategyName(), strategyName)
			if prevStrategy != nil {
				require.NotNil(t, prev)
			}
		})
	}
	if prevStrategy != nil {
		resolverFake.UnmarshalStrategyReturns(&testInstallStrategy, prevCSVQueryErr)
		expectFns = append(expectFns, func() {
			strat := resolverFake.UnmarshalStrategyArgsForCall(1)
			require.Equal(t, prevStrategy, strat)
		})
	}
	// Return a set of expectations that can be deferred until the test fn has finished
	return func() {
		for _, fn := range expectFns {
			fn()
		}
	}
}

// Fakes

type TestStrategy struct{}

func (t *TestStrategy) GetStrategyName() string {
	return "teststrategy"
}

type TestInstaller struct {
	installErr      error
	checkInstallErr error
}

func NewTestInstaller(installErr error, checkInstallErr error) install.StrategyInstaller {
	return &TestInstaller{
		installErr:      installErr,
		checkInstallErr: checkInstallErr,
	}
}

func (i *TestInstaller) Install(s install.Strategy) error {
	return i.installErr
}

func (i *TestInstaller) CheckInstalled(s install.Strategy) (bool, error) {
	if i.checkInstallErr != nil {
		return false, i.checkInstallErr
	}
	return true, nil
}

func testCSV(name string) *v1alpha1.ClusterServiceVersion {
	if name == "" {
		name = "test-csv"
	}
	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.ClusterServiceVersionAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:     name,
			SelfLink: "/link/" + name,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			DisplayName: name,
		},
	}
}

func makeCRDDescriptions(names ...string) []v1alpha1.CRDDescription {
	crds := []v1alpha1.CRDDescription{}
	for _, name := range names {
		crds = append(crds, v1alpha1.CRDDescription{
			Name: name,
		})
	}
	return crds
}

func withStatus(csv *v1alpha1.ClusterServiceVersion, status *v1alpha1.ClusterServiceVersionStatus) *v1alpha1.ClusterServiceVersion {
	status.DeepCopyInto(&csv.Status)
	return csv
}

func withSpec(csv *v1alpha1.ClusterServiceVersion, spec *v1alpha1.ClusterServiceVersionSpec) *v1alpha1.ClusterServiceVersion {
	spec.DeepCopyInto(&csv.Spec)
	return csv
}

func withReplaces(csv *v1alpha1.ClusterServiceVersion, replaces string) *v1alpha1.ClusterServiceVersion {
	csv.Spec.Replaces = replaces
	return csv
}

func NewMockALMOperator(gomockCtrl *gomock.Controller) *MockALMOperator {
	clientFake := fake.NewSimpleClientset()
	resolverFake := new(fakes.FakeStrategyResolverInterface)

	almOperator := Operator{
		client:   clientFake,
		resolver: resolverFake,
	}
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "test-clusterserviceversions")
	csvQueueInformer := queueinformer.NewTestQueueInformer(
		queue,
		cache.NewSharedIndexInformer(&queueinformer.MockListWatcher{}, &v1alpha1.ClusterServiceVersion{}, 0, nil),
		almOperator.syncClusterServiceVersion,
		nil,
	)

	qOp := queueinformer.NewMockOperator(gomockCtrl, csvQueueInformer)
	almOperator.Operator = &qOp.Operator
	almOperator.annotator = annotator.NewAnnotator(qOp.OpClient, map[string]string{})
	almOperator.csvQueue = queue
	return &MockALMOperator{
		Operator:             almOperator,
		ClientFake:           clientFake,
		MockQueueOperator:    qOp,
		MockOpClient:         qOp.MockClient,
		TestQueueInformer:    *csvQueueInformer,
		StrategyResolverFake: resolverFake,
	}
}

// Tests

func TestCSVStateTransitionsFromNone(t *testing.T) {
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: testCSV(""),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhasePending,
				Message: "requirements not yet checked",
				Reason:  v1alpha1.CSVReasonRequirementsUnknown,
			}),
			description: "ToRequirementsUnknown",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		mockOp.MockOpClient.EXPECT().ListCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, tt.in.GetNamespace(), v1alpha1.ClusterServiceVersionKind).Return(&operatorclient.CustomResourceList{}, nil)

		// Test the transition
		t.Run(tt.description, func(t *testing.T) {
			out, err := mockOp.transitionCSVState(*tt.in)
			t.Log(out)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestCSVStateTransitionsFromPending(t *testing.T) {
	type clusterState struct {
		csvs []*v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		out         *v1alpha1.ClusterServiceVersion
		state       *clusterState
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned: makeCRDDescriptions("nonExistent"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhasePending,
				Message: "one or more requirements couldn't be found",
				Reason:  v1alpha1.CSVReasonRequirementsNotMet,
			}),
			description: "RequirementsNotMet/OwnedMissing",
			err:         ErrRequirementsNotMet,
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Required: makeCRDDescriptions("nonExistent"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhasePending,
				Message: "one or more requirements couldn't be found",
				Reason:  v1alpha1.CSVReasonRequirementsNotMet,
			}),
			description: "RequirementsNotMet/RequiredMissing",
			err:         ErrRequirementsNotMet,
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("nonExistent1", "found1"),
						Required: makeCRDDescriptions("nonExistent2", "found2"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhasePending,
				Message: "one or more requirements couldn't be found",
				Reason:  v1alpha1.CSVReasonRequirementsNotMet,
			}),
			description: "RequirementsNotMet/OwnedAndRequiredMissingWithFound",
			err:         ErrRequirementsNotMet,
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found"),
						Required: makeCRDDescriptions("nonExistent"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhasePending,
				Message: "one or more requirements couldn't be found",
				Reason:  v1alpha1.CSVReasonRequirementsNotMet,
			}),
			description: "RequirementsNotMet/OwnedFoundRequiredMissing",
			err:         ErrRequirementsNotMet,
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("nonExistent"),
						Required: makeCRDDescriptions("found"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhasePending,
				Message: "one or more requirements couldn't be found",
				Reason:  v1alpha1.CSVReasonRequirementsNotMet,
			}),
			description: "RequirementsNotMet/OwnedMissingRequiredFound",
			err:         ErrRequirementsNotMet,
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found1", "found2"),
						Required: makeCRDDescriptions("found3", "found4"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstallReady,
				Message: "all requirements found, attempting install",
				Reason:  v1alpha1.CSVReasonRequirementsMet,
			}),
			state: &clusterState{
				csvs: []*v1alpha1.ClusterServiceVersion{},
			},
			description: "RequirementsMet/OwnedAndRequiredFound",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned: makeCRDDescriptions("found"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstallReady,
				Message: "all requirements found, attempting install",
				Reason:  v1alpha1.CSVReasonRequirementsMet,
			}),
			state: &clusterState{
				csvs: []*v1alpha1.ClusterServiceVersion{},
			},
			description: "RequirementsMet/OwnedFound",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Required: makeCRDDescriptions("found"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstallReady,
				Message: "all requirements found, attempting install",
				Reason:  v1alpha1.CSVReasonRequirementsMet,
			}),
			state: &clusterState{
				csvs: []*v1alpha1.ClusterServiceVersion{},
			},
			description: "RequirementsMet/RequiredFound",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found1", "found2"),
						Required: makeCRDDescriptions("found3", "found4"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseFailed,
				Message: "owner conflict: test-csv and existing-owner both own found1, but there is no replacement chain linking them",
				Reason:  v1alpha1.CSVReasonOwnerConflict,
			}),
			state: &clusterState{
				csvs: []*v1alpha1.ClusterServiceVersion{withSpec(testCSV("existing-owner"),
					&v1alpha1.ClusterServiceVersionSpec{
						CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
							Owned: makeCRDDescriptions("found1"),
						},
					})},
			},
			description: "RequirementsMet/OwnedAndRequiredFound/CRDAlreadyOwnedNoReplacementChain",
			err:         fmt.Errorf("test-csv and existing-owner both own found1, but there is no replacement chain linking them"),
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found1", "found2"),
						Required: makeCRDDescriptions("found3", "found4"),
					},
					Replaces: "existing-owner-2",
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstallReady,
				Message: "all requirements found, attempting install",
				Reason:  v1alpha1.CSVReasonRequirementsMet,
			}),
			state: &clusterState{
				csvs: []*v1alpha1.ClusterServiceVersion{
					withSpec(testCSV("existing-owner-1"),
						&v1alpha1.ClusterServiceVersionSpec{
							CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
								Owned: makeCRDDescriptions("found1"),
							},
						}),
					withSpec(testCSV("existing-owner-2"),
						&v1alpha1.ClusterServiceVersionSpec{
							CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
								Owned: makeCRDDescriptions("found1", "found2"),
							},
							Replaces: "existing-owner-1",
						}),
				},
			},
			description: "RequirementsMet/OwnedAndRequiredFound/CRDOwnedInReplacementChain",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found1", "found2"),
						Required: makeCRDDescriptions("found3", "found4"),
					},
					Replaces: "existing-owner-2",
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstallReady,
				Message: "all requirements found, attempting install",
				Reason:  v1alpha1.CSVReasonRequirementsMet,
			}),
			state: &clusterState{
				csvs: []*v1alpha1.ClusterServiceVersion{
					withSpec(testCSV("existing-owner-1"),
						&v1alpha1.ClusterServiceVersionSpec{
							CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
								Owned: makeCRDDescriptions("found1"),
							},
							Replaces: "existing-owner-3",
						}),
					withSpec(testCSV("existing-owner-2"),
						&v1alpha1.ClusterServiceVersionSpec{
							CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
								Owned: makeCRDDescriptions("found1", "found2"),
							},
							Replaces: "existing-owner-1",
						}),
					withSpec(testCSV("existing-owner-3"),
						&v1alpha1.ClusterServiceVersionSpec{
							CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
								Owned: makeCRDDescriptions("found1", "found2"),
							},
							Replaces: "existing-owner-2",
						}),
				},
			},
			description: "RequirementsMet/OwnedAndRequiredFound/CRDOwnedInReplacementChainLoop",
		},
	}

	for _, tt := range tests {
		// Test the transition
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockOp := NewMockALMOperator(ctrl)

			mockCRDExistence(*mockOp.MockQueueOperator.MockClient, tt.in.Spec.CustomResourceDefinitions.Owned)
			mockCRDExistence(*mockOp.MockQueueOperator.MockClient, tt.in.Spec.CustomResourceDefinitions.Required)

			// mock for call to short-circuit when replacing
			mockCSVsInNamespace(t, mockOp.MockQueueOperator.MockClient, tt.in.Namespace, nil, nil)

			// mock for pending, check that no other CSV owns the CRDs (unless being replaced)
			if tt.state != nil {
				mockCSVsInNamespace(t, mockOp.MockQueueOperator.MockClient, tt.in.Namespace, tt.state.csvs, nil)
			}

			out, err := mockOp.transitionCSVState(*tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
			ctrl.Finish()
		})
	}
}

func TestCSVStateTransitionsFromInstallReady(t *testing.T) {
	type clusterState struct {
		csvsInNamespace []*v1alpha1.ClusterServiceVersion
		csvQueryErr     error
		prevCSV         *v1alpha1.ClusterServiceVersion
		prevCSVQueryErr error
		installErr      error
		checkInstallErr error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "bad",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstallReady,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "bad",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseFailed,
					Message: "install strategy invalid: couldn't unmarshal install strategy",
					Reason:  v1alpha1.CSVReasonInvalidStrategy,
				}),
			description: "InvalidInstallStrategy",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstallReady,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseInstalling,
					Message: "waiting for install components to report healthy",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			description: "InstallStrategy/NotReplacing/Installing",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstallReady,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseFailed,
				Message: "install strategy failed: error installing component",
				Reason:  v1alpha1.CSVReasonComponentFailed,
			}),
			state: clusterState{
				installErr: fmt.Errorf("error installing component"),
			},
			err:         fmt.Errorf("error installing component"),
			description: "InstallStrategy/NotReplacing/ComponentFailed",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					Replaces: "prev",
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstallReady,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseInstalling,
					Message: "waiting for install components to report healthy",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			state: clusterState{
				prevCSV: withStatus(withSpec(testCSV("prev"),
					&v1alpha1.ClusterServiceVersionSpec{
						InstallStrategy: v1alpha1.NamedInstallStrategy{
							StrategyName:    "teststrategy",
							StrategySpecRaw: []byte(`{"test":"spec"}`),
						},
					}),
					&v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					}),
			},
			description: "InstallStrategy/Replacing/Installing",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					Replaces: "prev",
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstallReady,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseInstalling,
					Message: "waiting for install components to report healthy",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			state: clusterState{
				prevCSVQueryErr: fmt.Errorf("error getting prev csv"),
			},
			description: "InstallStrategy/Replacing/PrevCSVErr/Installing",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		var prevStrategy *v1alpha1.NamedInstallStrategy
		if tt.state.prevCSV != nil {
			prevStrategy = &tt.state.prevCSV.Spec.InstallStrategy
		}

		mockCSVsInNamespace(t, mockOp.MockOpClient, tt.in.GetNamespace(), tt.state.csvsInNamespace, tt.state.csvQueryErr)
		mockInstallStrategy(t, mockOp.StrategyResolverFake, &tt.in.Spec.InstallStrategy, tt.state.installErr, tt.state.checkInstallErr, prevStrategy, tt.state.prevCSVQueryErr)
		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			out, err := mockOp.transitionCSVState(*tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestCSVStateTransitionsFromInstalling(t *testing.T) {
	type clusterState struct {
		csvsInNamespace []*v1alpha1.ClusterServiceVersion
		csvQueryErr     error
		prevCSV         *v1alpha1.ClusterServiceVersion
		prevCSVQueryErr error
		installErr      error
		checkInstallErr error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "bad",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "bad",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseFailed,
					Message: "install strategy invalid: couldn't unmarshal install strategy",
					Reason:  v1alpha1.CSVReasonInvalidStrategy,
				}),
			description: "InvalidInstallStrategy",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseSucceeded,
					Message: "install strategy completed with no errors",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			description: "InstallStrategy/NotReplacing/Installing",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstalling,
				Message: "installing: error installing component",
				Reason:  v1alpha1.CSVReasonWaiting,
			}),
			state: clusterState{
				checkInstallErr: fmt.Errorf("error installing component"),
			},
			description: "InstallStrategy/NotReplacing/WaitingForInstall",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseFailed,
				Message: "install failed: Timeout: timeout error",
				Reason:  v1alpha1.CSVReasonInstallCheckFailed,
			}),
			state: clusterState{
				checkInstallErr: &install.StrategyError{Reason: install.StrategyErrReasonTimeout, Message: "timeout error"},
			},
			description: "InstallStrategy/NotReplacing/UnrecoverableError",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					Replaces: "prev",
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseSucceeded,
					Message: "install strategy completed with no errors",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			state: clusterState{
				prevCSV: withStatus(withSpec(testCSV("prev"),
					&v1alpha1.ClusterServiceVersionSpec{
						InstallStrategy: v1alpha1.NamedInstallStrategy{
							StrategyName:    "teststrategy",
							StrategySpecRaw: []byte(`{"test":"spec"}`),
						},
					}),
					&v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					}),
			},
			description: "InstallStrategy/Replacing/Installing",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					Replaces: "prev",
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseInstalling,
					Message: "installing: error installing component",
					Reason:  v1alpha1.CSVReasonWaiting,
				}),
			state: clusterState{
				prevCSV: withStatus(withSpec(testCSV("prev"),
					&v1alpha1.ClusterServiceVersionSpec{
						InstallStrategy: v1alpha1.NamedInstallStrategy{
							StrategyName:    "teststrategy",
							StrategySpecRaw: []byte(`{"test":"spec"}`),
						},
					}),
					&v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					}),
				checkInstallErr: fmt.Errorf("error installing component"),
			},
			description: "InstallStrategy/Replacing/WaitingForInstall",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					Replaces: "prev",
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseFailed,
					Message: "install failed: Timeout: timeout error",
					Reason:  v1alpha1.CSVReasonInstallCheckFailed,
				}),
			state: clusterState{
				prevCSV: withStatus(withSpec(testCSV("prev"),
					&v1alpha1.ClusterServiceVersionSpec{
						InstallStrategy: v1alpha1.NamedInstallStrategy{
							StrategyName:    "teststrategy",
							StrategySpecRaw: []byte(`{"test":"spec"}`),
						},
					}),
					&v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					}),
				checkInstallErr: &install.StrategyError{Reason: install.StrategyErrReasonTimeout, Message: "timeout error"},
			},
			description: "InstallStrategy/Replacing/UnrecoverableError",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		var prevStrategy *v1alpha1.NamedInstallStrategy
		if tt.state.prevCSV != nil {
			prevStrategy = &tt.state.prevCSV.Spec.InstallStrategy
		}

		mockCSVsInNamespace(t, mockOp.MockOpClient, tt.in.GetNamespace(), tt.state.csvsInNamespace, tt.state.csvQueryErr)
		mockInstallStrategy(t, mockOp.StrategyResolverFake, &tt.in.Spec.InstallStrategy, tt.state.installErr, tt.state.checkInstallErr, prevStrategy, tt.state.prevCSVQueryErr)
		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			out, err := mockOp.transitionCSVState(*tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestCSVStateTransitionsFromSucceeded(t *testing.T) {
	type clusterState struct {
		csvsInNamespace []*v1alpha1.ClusterServiceVersion
		csvQueryErr     error
		prevCSV         *v1alpha1.ClusterServiceVersion
		prevCSVQueryErr error
		installErr      error
		checkInstallErr error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "bad",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseSucceeded,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "bad",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseFailed,
					Message: "install strategy invalid: couldn't unmarshal install strategy",
					Reason:  v1alpha1.CSVReasonInvalidStrategy,
				}),
			description: "InvalidInstallStrategy",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseSucceeded,
					Message: "install strategy completed with no errors",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseSucceeded,
					Message: "install strategy completed with no errors",
					Reason:  v1alpha1.CSVReasonInstallSuccessful,
				}),
			description: "InstallStrategy/LookingGood",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseSucceeded,
					Reason: v1alpha1.CSVReasonInstallSuccessful,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseInstalling,
				Message: "installing: error installing component",
				Reason:  v1alpha1.CSVReasonComponentUnhealthy,
			}),
			state: clusterState{
				checkInstallErr: fmt.Errorf("error installing component"),
			},
			description: "InstallStrategy/ComponentWentUnhealthy",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseSucceeded,
					Reason: v1alpha1.CSVReasonInstallSuccessful,
				}),
			out: withStatus(testCSV(""), &v1alpha1.ClusterServiceVersionStatus{
				Phase:   v1alpha1.CSVPhaseFailed,
				Message: "install failed: Timeout: timeout error",
				Reason:  v1alpha1.CSVReasonInstallCheckFailed,
			}),
			state: clusterState{
				checkInstallErr: &install.StrategyError{Reason: install.StrategyErrReasonTimeout, Message: "timeout error"},
			},
			description: "InstallStrategy/ComponentWentUnrecoverable",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		var prevStrategy *v1alpha1.NamedInstallStrategy
		if tt.state.prevCSV != nil {
			prevStrategy = &tt.state.prevCSV.Spec.InstallStrategy
		}

		mockCSVsInNamespace(t, mockOp.MockOpClient, tt.in.GetNamespace(), tt.state.csvsInNamespace, tt.state.csvQueryErr)
		mockInstallStrategy(t, mockOp.StrategyResolverFake, &tt.in.Spec.InstallStrategy, tt.state.installErr, tt.state.checkInstallErr, prevStrategy, tt.state.prevCSVQueryErr)
		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			out, err := mockOp.transitionCSVState(*tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestCSVStateTransitionsFromReplacing(t *testing.T) {
	type clusterState struct {
		csvsInNamespace []*v1alpha1.ClusterServiceVersion
		csvQueryErr     error
		prevCSV         *v1alpha1.ClusterServiceVersion
		prevCSVQueryErr error
		installErr      error
		checkInstallErr error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					Replaces: "prev",
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			state: clusterState{
				prevCSV: withStatus(withSpec(testCSV("prev"),
					&v1alpha1.ClusterServiceVersionSpec{
						InstallStrategy: v1alpha1.NamedInstallStrategy{
							StrategyName:    "teststrategy",
							StrategySpecRaw: []byte(`{"test":"spec"}`),
						},
					}),
					&v1alpha1.ClusterServiceVersionStatus{
						Phase: v1alpha1.CSVPhaseSucceeded,
					}),
			},
			description: "NotALeaf",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			description: "Leaf/NoNewCSV",
		},
		{
			in: withStatus(withSpec(testCSV("current"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			out: withStatus(withSpec(testCSV("current"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseDeleting,
					Message: "has been replaced by a newer ClusterServiceVersion that has successfully installed.",
					Reason:  v1alpha1.CSVReasonReplaced,
				}),
			state: clusterState{
				csvsInNamespace: []*v1alpha1.ClusterServiceVersion{
					withStatus(withSpec(testCSV("next"),
						&v1alpha1.ClusterServiceVersionSpec{
							Replaces: "current",
							InstallStrategy: v1alpha1.NamedInstallStrategy{
								StrategyName:    "teststrategy",
								StrategySpecRaw: []byte(`{"test":"spec"}`),
							},
						}),
						&v1alpha1.ClusterServiceVersionStatus{
							Phase:  v1alpha1.CSVPhaseSucceeded,
							Reason: v1alpha1.CSVReasonInstallSuccessful,
						}),
				},
			},
			description: "Leaf/NewCSVRunning/GCSelf",
		},
		{
			in: withStatus(withSpec(testCSV("current"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			out: withStatus(withSpec(testCSV("current"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			state: clusterState{
				csvsInNamespace: []*v1alpha1.ClusterServiceVersion{
					withStatus(withSpec(testCSV("next"),
						&v1alpha1.ClusterServiceVersionSpec{
							Replaces: "current",
							InstallStrategy: v1alpha1.NamedInstallStrategy{
								StrategyName:    "teststrategy",
								StrategySpecRaw: []byte(`{"test":"spec"}`),
							},
						}),
						&v1alpha1.ClusterServiceVersionStatus{
							Phase:  v1alpha1.CSVPhaseReplacing,
							Reason: v1alpha1.CSVReasonBeingReplaced,
							Conditions: []v1alpha1.ClusterServiceVersionCondition{
								{
									Phase:  v1alpha1.CSVPhaseReplacing,
									Reason: v1alpha1.CSVReasonBeingReplaced,
								},
							},
						}),
				},
			},
			description: "Leaf/NewCSV/NotRunning",
		},
		{
			in: withStatus(withSpec(testCSV("1"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseReplacing,
					Reason: v1alpha1.CSVReasonBeingReplaced,
				}),
			out: withStatus(withSpec(testCSV("current"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "1",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:   v1alpha1.CSVPhaseDeleting,
					Message: "has been replaced by a newer ClusterServiceVersion that has successfully installed.",
					Reason:  v1alpha1.CSVReasonReplaced,
				}),
			state: clusterState{
				csvsInNamespace: []*v1alpha1.ClusterServiceVersion{
					withStatus(withSpec(testCSV("3"),
						&v1alpha1.ClusterServiceVersionSpec{
							Replaces: "2",
							InstallStrategy: v1alpha1.NamedInstallStrategy{
								StrategyName:    "teststrategy",
								StrategySpecRaw: []byte(`{"test":"spec"}`),
							},
						}),
						&v1alpha1.ClusterServiceVersionStatus{
							Phase:  v1alpha1.CSVPhaseSucceeded,
							Reason: v1alpha1.CSVReasonInstallSuccessful,
						}),
					withStatus(withSpec(testCSV("2"),
						&v1alpha1.ClusterServiceVersionSpec{
							Replaces: "1",
							InstallStrategy: v1alpha1.NamedInstallStrategy{
								StrategyName:    "teststrategy",
								StrategySpecRaw: []byte(`{"test":"spec"}`),
							},
						}),
						&v1alpha1.ClusterServiceVersionStatus{
							Phase:  v1alpha1.CSVPhaseReplacing,
							Reason: v1alpha1.CSVReasonBeingReplaced,
							Conditions: []v1alpha1.ClusterServiceVersionCondition{
								{
									Phase:  v1alpha1.CSVPhaseReplacing,
									Reason: v1alpha1.CSVReasonBeingReplaced,
								},
							},
						}),
				},
			},
			description: "Leaf/ManyNewCSVRunning/GCSet",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		// transition short circuits if there's a prevCSV, so we only mock the rest if there isn't
		if tt.state.prevCSV == nil {
			intermediateExpect := mockIntermediates(t, mockOp.MockOpClient, mockOp.StrategyResolverFake, tt.in, tt.state.csvsInNamespace)
			defer intermediateExpect()
		}

		t.Run(tt.description, func(t *testing.T) {
			out, err := mockOp.transitionCSVState(*tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestCSVStateTransitionsFromDeleting(t *testing.T) {
	type clusterState struct {
		deleteErr error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseDeleting,
					Reason: v1alpha1.CSVReasonReplaced,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseDeleting,
					Reason: v1alpha1.CSVReasonReplaced,
				}),
			description: "DeleteSuccessful",
		},
		{
			in: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseDeleting,
					Reason: v1alpha1.CSVReasonReplaced,
				}),
			out: withStatus(withSpec(testCSV(""),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseDeleting,
					Reason: v1alpha1.CSVReasonReplaced,
				}),
			state: clusterState{
				deleteErr: fmt.Errorf("couldn't delete"),
			},
			description: "DeleteUnsuccessful",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		mockOp.MockOpClient.EXPECT().
			DeleteCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, tt.in.GetNamespace(), v1alpha1.ClusterServiceVersionKind, tt.in.GetName()).
			Return(tt.state.deleteErr)

		t.Run(tt.description, func(t *testing.T) {
			out, err := mockOp.transitionCSVState(*tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, out.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, out.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, out.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestReplacingCSV(t *testing.T) {
	type clusterState struct {
		newerCSV    *v1alpha1.ClusterServiceVersion
		csvQueryErr error
	}

	newCSV := withStatus(withSpec(testCSV("new"),
		&v1alpha1.ClusterServiceVersionSpec{
			Replaces: "old",
			InstallStrategy: v1alpha1.NamedInstallStrategy{
				StrategyName:    "teststrategy",
				StrategySpecRaw: []byte(`{"test":"spec"}`),
			},
		}),
		&v1alpha1.ClusterServiceVersionStatus{
			Phase: v1alpha1.CSVPhaseSucceeded,
		})

	beingReplacedStatus := &v1alpha1.ClusterServiceVersionStatus{
		Phase:   v1alpha1.CSVPhaseReplacing,
		Message: "being replaced by csv: /link/new",
		Reason:  v1alpha1.CSVReasonBeingReplaced,
	}

	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		err         error
		description string
	}{
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseSucceeded,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromSucceeded",
		},
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromInstalling",
		},
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromPending",
		},
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseFailed,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromFailed",
		},
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstallReady,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromInstallReady",
		},
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseNone,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromNone",
		},
		{
			in: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseUnknown,
				}),
			out: withStatus(withSpec(testCSV("old"),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "teststrategy",
						StrategySpecRaw: []byte(`{"test":"spec"}`),
					},
				}), beingReplacedStatus),
			state: clusterState{
				newerCSV: newCSV,
			},
			err:         fmt.Errorf("replacing"),
			description: "FromUnknown",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		csvsInNamespace := []*v1alpha1.ClusterServiceVersion{tt.state.newerCSV}
		mockCSVsInNamespace(t, mockOp.MockOpClient, tt.in.GetNamespace(), csvsInNamespace, tt.state.csvQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.checkReplacementsAndUpdateStatus(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestIsBeingReplaced(t *testing.T) {
	type clusterState struct {
		csvsInNamespace []*v1alpha1.ClusterServiceVersion
		csvQueryErr     error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		description string
	}{
		{
			in: testCSV(""),
			state: clusterState{
				csvsInNamespace: nil,
				csvQueryErr:     fmt.Errorf("couldn't query"),
			},
			out:         nil,
			description: "QueryErr",
		},
		{
			in: testCSV(""),
			state: clusterState{
				csvsInNamespace: nil,
				csvQueryErr:     nil,
			},
			out:         nil,
			description: "NoOtherCSVs",
		},
		{
			in: testCSV(""),
			state: clusterState{
				csvsInNamespace: []*v1alpha1.ClusterServiceVersion{testCSV("test2")},
				csvQueryErr:     nil,
			},
			out:         nil,
			description: "CSVInCluster/NotReplacing",
		},
		{
			in: testCSV("test"),
			state: clusterState{
				csvsInNamespace: []*v1alpha1.ClusterServiceVersion{withReplaces(testCSV("test2"), "test")},
				csvQueryErr:     nil,
			},
			out:         withReplaces(testCSV("test2"), "test"),
			description: "CSVInCluster/Replacing",
		},
	}
	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		mockCSVsInNamespace(t, mockOp.MockOpClient, tt.in.GetNamespace(), tt.state.csvsInNamespace, tt.state.csvQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			csvsInNamespace := mockOp.csvsInNamespace(tt.in.GetNamespace())
			out := mockOp.isBeingReplaced(tt.in, csvsInNamespace)
			require.EqualValues(t, out, tt.out)
		})
		ctrl.Finish()
	}
}

func TestIsReplacing(t *testing.T) {
	type clusterState struct {
		oldCSV      *v1alpha1.ClusterServiceVersion
		csvQueryErr error
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		state       clusterState
		out         *v1alpha1.ClusterServiceVersion
		description string
	}{
		{
			in: testCSV(""),
			state: clusterState{
				oldCSV:      nil,
				csvQueryErr: fmt.Errorf("couldn't query"),
			},
			out:         nil,
			description: "QueryErr",
		},
		{
			in: testCSV(""),
			state: clusterState{
				oldCSV:      testCSV("test2"),
				csvQueryErr: nil,
			},
			out:         nil,
			description: "CSVInCluster/NotReplacing",
		},
		{
			in: withReplaces(testCSV("test2"), "test"),
			state: clusterState{
				oldCSV:      testCSV("test"),
				csvQueryErr: nil,
			},
			out:         testCSV("test"),
			description: "CSVInCluster/Replacing",
		},
		{
			in: withReplaces(testCSV("test2"), "test"),
			state: clusterState{
				oldCSV:      nil,
				csvQueryErr: fmt.Errorf("not found"),
			},
			out:         nil,
			description: "CSVInCluster/ReplacingNotFound",
		},
	}
	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		mockIsReplacing(t, mockOp.MockOpClient, tt.state.oldCSV, tt.in, tt.state.csvQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			out := mockOp.isReplacing(tt.in)
			require.EqualValues(t, out, tt.out)
		})
		ctrl.Finish()
	}
}

func deployment(deploymentName, namespace string) *v1beta2.Deployment {
	var singleInstance = int32(1)
	return &v1beta2.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: v1beta2.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": deploymentName,
				},
			},
			Replicas: &singleInstance,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": deploymentName,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  deploymentName + "-c1",
							Image: "nginx:1.7.9",
							Ports: []v1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
						},
					},
				},
			},
		},
		Status: v1beta2.DeploymentStatus{
			Replicas:          singleInstance,
			ReadyReplicas:     singleInstance,
			AvailableReplicas: singleInstance,
			UpdatedReplicas:   singleInstance,
		},
	}
}

func installStrategy(deploymentName string) v1alpha1.NamedInstallStrategy {
	var singleInstance = int32(1)
	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: deploymentName,
				Spec: v1beta2.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": deploymentName,
						},
					},
					Replicas: &singleInstance,
					Template: v1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": deploymentName,
							},
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  deploymentName + "-c1",
									Image: "nginx:1.7.9",
									Ports: []v1.ContainerPort{
										{
											ContainerPort: 80,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	strategyRaw, err := json.Marshal(strategy)
	if err != nil {
		panic(err)
	}

	return v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}
}

func csv(name, namespace, replaces string, installStrategy v1alpha1.NamedInstallStrategy, owned, required []*v1beta1.CustomResourceDefinition, phase v1alpha1.ClusterServiceVersionPhase) *v1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, crd := range required {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: crd.GetName(), Version: crd.Spec.Versions[0].Name, Kind: crd.GetName()})
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, crd := range owned {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: crd.GetName(), Version: crd.Spec.Versions[0].Name, Kind: crd.GetName()})
	}

	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        replaces,
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{
			Phase: phase,
		},
	}
}

func crd(name string, version string) *v1beta1.CustomResourceDefinition {
	return &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: name + "group",
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name:    version,
					Storage: true,
					Served:  true,
				},
			},
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

func TestCSVUpgrades(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	namespace := "ns"

	type csvState struct {
		exists bool
		phase  v1alpha1.ClusterServiceVersionPhase
	}
	type initial struct {
		csvs []runtime.Object
		crds []runtime.Object
		objs []runtime.Object
	}
	type expected struct {
		csvStates map[string]csvState
	}
	tests := []struct {
		name     string
		initial  initial
		expected expected
	}{
		{
			name: "SingleCSVNoneToPending",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "SingleCSVPendingToInstallReady",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToInstalling",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstalling},
				},
			},
		},
		{
			name: "SingleCSVInstallingToSucceeded",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVSucceededToReplacing",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
					"csv2": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "CSVReplacingToDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
					deployment("csv2-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleReplacingToDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1"),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
					deployment("csv2-dep1", namespace),
					deployment("csv3-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			clientFake := fake.NewSimpleClientset(tt.initial.csvs...)

			opClientFake := opFake.NewClient(
				k8sfake.NewSimpleClientset(tt.initial.objs...),
				apiextensionsfake.NewSimpleClientset(tt.initial.crds...))

			op := &Operator{
				Operator: &queueinformer.Operator{
					OpClient: opClientFake,
				},
				client:   clientFake,
				resolver: &install.StrategyResolver{},
			}

			// run csv sync for each CSV
			for _, csv := range tt.initial.csvs {
				err := op.syncClusterServiceVersion(csv)
				require.NoError(t, err)
			}

			// get csvs in the cluster
			outCSVMap := map[string]*v1alpha1.ClusterServiceVersion{}
			outCSVs, err := clientFake.OperatorsV1alpha1().ClusterServiceVersions("ns").List(metav1.ListOptions{})
			require.NoError(t, err)
			for _, csv := range outCSVs.Items {
				outCSVMap[csv.GetName()] = csv.DeepCopy()
			}

			// verify expectations of csvs in cluster
			for csvName, csvState := range tt.expected.csvStates {
				csv, ok := outCSVMap[csvName]
				require.Equal(t, ok, csvState.exists)
				if csvState.exists {
					assert.Equal(t, csvState.phase, csv.Status.Phase, "%s had incorrect phase", csvName)
				}
			}
		})
	}
}
