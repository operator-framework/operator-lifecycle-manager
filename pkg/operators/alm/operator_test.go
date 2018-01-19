package alm

import (
	"fmt"
	"strings"
	"testing"

	"github.com/coreos-inc/alm/pkg/annotator"
	"github.com/coreos-inc/alm/pkg/apis"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/install"
	"github.com/coreos-inc/alm/pkg/queueinformer"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	conversion "k8s.io/apimachinery/pkg/conversion/unstructured"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type MockALMOperator struct {
	ALMOperator
	MockQueueOperator    *queueinformer.MockOperator
	MockCSVClient        *MockClusterServiceVersionInterface
	MockOpClient         *opClient.MockInterface
	TestQueueInformer    queueinformer.TestQueueInformer
	MockStrategyResolver *MockStrategyResolverInterface
}

// Helpers

func mockCRDExistence(mockClient opClient.MockInterface, crdDescriptions []v1alpha1.CRDDescription) {
	for _, crd := range crdDescriptions {
		if strings.HasPrefix(crd.Name, "nonExistent") {
			mockClient.EXPECT().
				GetCustomResourceDefinition(crd.Name).
				Return(nil, fmt.Errorf("Requirement not found"))
		}
		if strings.HasPrefix(crd.Name, "found") {
			mockClient.EXPECT().
				GetCustomResourceDefinition(crd.Name).
				Return(&v1beta1.CustomResourceDefinition{}, nil)
		}
	}
}

func mockIntermediates(t *testing.T, mockOpClient *opClient.MockInterface, mockResolver *MockStrategyResolverInterface, current *v1alpha1.ClusterServiceVersion, intermediates []*v1alpha1.ClusterServiceVersion) {
	mockCSVsInNamespace(t, mockOpClient, current.GetNamespace(), intermediates, nil)
	prevCSV := current
	for _, csv := range intermediates {
		mockIsReplacing(t, mockOpClient, prevCSV, csv, nil)
		testInstallStrategy := TestStrategy{}
		mockResolver.EXPECT().UnmarshalStrategy(csv.Spec.InstallStrategy).Return(&testInstallStrategy, nil)
		mockResolver.EXPECT().UnmarshalStrategy(prevCSV.Spec.InstallStrategy).Return(&testInstallStrategy, nil)
		mockResolver.EXPECT().InstallerForStrategy(testInstallStrategy.GetStrategyName(), mockOpClient, csv.ObjectMeta, &testInstallStrategy).Return(NewTestInstaller(nil, nil))
		prevCSV = csv
	}
}

func mockIsReplacing(t *testing.T, mockOpClient *opClient.MockInterface, prevCSV *v1alpha1.ClusterServiceVersion, currentCSV *v1alpha1.ClusterServiceVersion, csvQueryErr error) {
	unstructuredConverter := conversion.NewConverter(true)
	var unstructuredOldCSV *unstructured.Unstructured = nil
	if prevCSV != nil {
		unst, err := unstructuredConverter.ToUnstructured(prevCSV)
		require.NoError(t, err)
		unstructuredOldCSV = &unstructured.Unstructured{Object: unst}
	} else {
		unstructuredOldCSV = nil
	}

	if currentCSV.Spec.Replaces != "" {
		mockOpClient.EXPECT().GetCustomResource(apis.GroupName, v1alpha1.GroupVersion, currentCSV.GetNamespace(), v1alpha1.ClusterServiceVersionKind, currentCSV.Spec.Replaces).Return(unstructuredOldCSV, csvQueryErr)
	}
}

func mockCSVsInNamespace(t *testing.T, mockOpClient *opClient.MockInterface, namespace string, csvsInNamespace []*v1alpha1.ClusterServiceVersion, csvQueryErr error) {
	unstructuredConverter := conversion.NewConverter(true)
	unstructuredCSVs := []*unstructured.Unstructured{}
	for _, csv := range csvsInNamespace {
		unst, err := unstructuredConverter.ToUnstructured(csv)
		require.NoError(t, err)
		unstructuredCSVs = append(unstructuredCSVs, &unstructured.Unstructured{Object: unst})
	}
	csvList := &opClient.CustomResourceList{Items: unstructuredCSVs}

	mockOpClient.EXPECT().ListCustomResource(apis.GroupName, v1alpha1.GroupVersion, namespace, v1alpha1.ClusterServiceVersionKind).Return(csvList, csvQueryErr)
}

func mockInstallStrategy(t *testing.T, mockResolver *MockStrategyResolverInterface, strategy *v1alpha1.NamedInstallStrategy, installErr error, checkInstallErr error, prevStrategy *v1alpha1.NamedInstallStrategy, prevCSVQueryErr error) {
	testInstallStrategy := TestStrategy{}
	matchPrev := gomock.Nil()
	if prevStrategy != nil {
		matchPrev = gomock.Any()
	}
	stratErr := fmt.Errorf("couldn't unmarshal install strategy")
	if strategy.StrategyName == "teststrategy" {
		stratErr = nil
	}
	mockResolver.EXPECT().UnmarshalStrategy(*strategy).Return(&testInstallStrategy, stratErr)
	if stratErr == nil {
		mockResolver.EXPECT().
			InstallerForStrategy((&testInstallStrategy).GetStrategyName(), gomock.Any(), gomock.Any(), matchPrev).
			Return(NewTestInstaller(installErr, checkInstallErr))
	}
	if prevStrategy != nil {
		mockResolver.EXPECT().UnmarshalStrategy(*prevStrategy).Return(&testInstallStrategy, prevCSVQueryErr)
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
	mockCSVClient := NewMockClusterServiceVersionInterface(gomockCtrl)
	mockInstallResolver := NewMockStrategyResolverInterface(gomockCtrl)

	almOperator := ALMOperator{
		csvClient: mockCSVClient,
		resolver:  mockInstallResolver,
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
		ALMOperator:          almOperator,
		MockCSVClient:        mockCSVClient,
		MockQueueOperator:    qOp,
		MockOpClient:         qOp.MockClient,
		TestQueueInformer:    *csvQueueInformer,
		MockStrategyResolver: mockInstallResolver,
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

		mockOp.MockOpClient.EXPECT().ListCustomResource(apis.GroupName, v1alpha1.GroupVersion, tt.in.GetNamespace(), v1alpha1.ClusterServiceVersionKind).Return(&opClient.CustomResourceList{}, nil)

		// Test the transition
		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
		})
		ctrl.Finish()
	}
}

func TestCSVStateTransitionsFromPending(t *testing.T) {
	type clusterState struct {
		crdDescriptons []*v1alpha1.CRDDescription
	}
	tests := []struct {
		in          *v1alpha1.ClusterServiceVersion
		out         *v1alpha1.ClusterServiceVersion
		state       clusterState
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
			description: "RequirementsMet/RequiredFound",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		mockCRDExistence(*mockOp.MockQueueOperator.MockClient, tt.in.Spec.CustomResourceDefinitions.Owned)
		mockCRDExistence(*mockOp.MockQueueOperator.MockClient, tt.in.Spec.CustomResourceDefinitions.Required)
		mockOp.MockOpClient.EXPECT().ListCustomResource(apis.GroupName, v1alpha1.GroupVersion, tt.in.GetNamespace(), v1alpha1.ClusterServiceVersionKind).Return(&opClient.CustomResourceList{}, nil)

		// Test the transition
		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
		})
		ctrl.Finish()
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
		mockInstallStrategy(t, mockOp.MockStrategyResolver, &tt.in.Spec.InstallStrategy, tt.state.installErr, tt.state.checkInstallErr, prevStrategy, tt.state.prevCSVQueryErr)
		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
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
		mockInstallStrategy(t, mockOp.MockStrategyResolver, &tt.in.Spec.InstallStrategy, tt.state.installErr, tt.state.checkInstallErr, prevStrategy, tt.state.prevCSVQueryErr)
		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
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
					Phase:  v1alpha1.CSVPhaseSucceeded,
					Reason: v1alpha1.CSVReasonInstallSuccessful,
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
		mockInstallStrategy(t, mockOp.MockStrategyResolver, &tt.in.Spec.InstallStrategy, tt.state.installErr, tt.state.checkInstallErr, prevStrategy, tt.state.prevCSVQueryErr)
		mockIsReplacing(t, mockOp.MockOpClient, tt.state.prevCSV, tt.in, tt.state.prevCSVQueryErr)

		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
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
			mockIntermediates(t, mockOp.MockOpClient, mockOp.MockStrategyResolver, tt.in, tt.state.csvsInNamespace)
		}

		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
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
			DeleteCustomResource(apis.GroupName, v1alpha1.GroupVersion, tt.in.GetNamespace(), v1alpha1.ClusterServiceVersionKind, tt.in.GetName()).
			Return(tt.state.deleteErr)

		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			require.EqualValues(t, tt.err, err)
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Message, tt.in.Status.Message)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)
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
