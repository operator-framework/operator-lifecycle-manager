package alm

import (
	"fmt"
	"strings"
	"testing"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/coreos-inc/alm/pkg/annotator"
	"github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/install"
	"github.com/coreos-inc/alm/pkg/queueinformer"
)

type MockALMOperator struct {
	ALMOperator
	MockQueueOperator    *queueinformer.MockOperator
	MockCSVClient        *MockClusterServiceVersionInterface
	TestQueueInformer    queueinformer.TestQueueInformer
	MockStrategyResolver *MockStrategyResolverInterface
}

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

type TestStrategy struct{}

func (t *TestStrategy) GetStrategyName() string {
	return "teststrategy"
}

type TestInstaller struct {
	state StateTransitionTestState
}

func NewTestInstaller(state StateTransitionTestState) install.StrategyInstaller {
	return &TestInstaller{
		state: state,
	}
}

func (i *TestInstaller) Install(s install.Strategy) error {
	if i.state.installApplySuccess {
		return nil
	}
	return fmt.Errorf(i.state.errString)
}

func (i *TestInstaller) CheckInstalled(s install.Strategy) (bool, error) {
	return i.state.checkInstall, i.state.checkInstallErr
}

func testCSV() *v1alpha1.ClusterServiceVersion {
	return &v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:     "test-csv",
			SelfLink: "/link/test-csv",
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			DisplayName: "Test",
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

func NewMockALMOperator(gomockCtrl *gomock.Controller) *MockALMOperator {
	mockCSVClient := NewMockClusterServiceVersionInterface(gomockCtrl)
	mockInstallResolver := NewMockStrategyResolverInterface(gomockCtrl)

	almOperator := ALMOperator{
		csvClient: mockCSVClient,
		resolver:  mockInstallResolver,
	}

	csvQueueInformer := queueinformer.NewTestQueueInformer(
		"test-clusterserviceversions",
		cache.NewSharedIndexInformer(&queueinformer.MockListWatcher{}, &v1alpha1.ClusterServiceVersion{}, 0, nil),
		almOperator.syncClusterServiceVersion,
		nil,
	)

	qOp := queueinformer.NewMockOperator(gomockCtrl, csvQueueInformer)
	almOperator.Operator = &qOp.Operator
	almOperator.annotator = annotator.NewAnnotator(qOp.OpClient, map[string]string{})
	return &MockALMOperator{
		ALMOperator:          almOperator,
		MockCSVClient:        mockCSVClient,
		MockQueueOperator:    qOp,
		TestQueueInformer:    *csvQueueInformer,
		MockStrategyResolver: mockInstallResolver,
	}
}

type StateTransitionTestState struct {
	in                  *v1alpha1.ClusterServiceVersion
	out                 *v1alpha1.ClusterServiceVersion
	mockCRDs            bool
	mockCheckInstall    bool
	checkInstall        bool
	checkInstallErr     error
	mockApplyStrategy   bool
	installApplySuccess bool
	installErrString    string
	description         string
	errString           string
}

func TestCSVStateTransitions(t *testing.T) {
	testInstallStrategy := TestStrategy{}
	tests := []StateTransitionTestState{
		{
			in: testCSV(),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonRequirementsUnknown,
			}),
			mockCRDs:    false,
			description: "TransitionNoneToPending/RequirementsUnknown",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned: makeCRDDescriptions("nonExistent"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonRequirementsNotMet,
			}),
			mockCRDs:    true,
			description: "TransitionNoneToPending/RequirementsNotMet/OwnedMissing",
			errString:   ErrRequirementsNotMet.Error(),
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Required: makeCRDDescriptions("nonExistent"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonRequirementsNotMet,
			}),
			mockCRDs:    true,
			description: "TransitionNoneToPending/RequirementsNotMet/RequiredMissing",
			errString:   ErrRequirementsNotMet.Error(),
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("nonExistent1", "found1"),
						Required: makeCRDDescriptions("nonExistent2", "found2"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonRequirementsNotMet,
			}),
			mockCRDs:    true,
			description: "TransitionNoneToPending/RequirementsNotMet/OwnedAndRequiredMissingWithFound",
			errString:   ErrRequirementsNotMet.Error(),
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found"),
						Required: makeCRDDescriptions("nonExistent"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonRequirementsNotMet,
			}),
			mockCRDs:    true,
			description: "TransitionNoneToPending/RequirementsNotMet/OwnedFoundRequiredMissing",
			errString:   ErrRequirementsNotMet.Error(),
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("nonExistent"),
						Required: makeCRDDescriptions("found"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonRequirementsNotMet,
			}),
			mockCRDs:    true,
			description: "TransitionNoneToPending/RequirementsNotMet/OwnedMissingRequiredFound",
			errString:   ErrRequirementsNotMet.Error(),
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned:    makeCRDDescriptions("found1", "found2"),
						Required: makeCRDDescriptions("found3", "found4"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhaseInstalling,
				Reason: v1alpha1.CSVReasonRequirementsMet,
			}),
			mockCRDs:    true,
			description: "TransitionPendingToInstalling/RequirementsMet/OwnedAndRequiredFound",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Owned: makeCRDDescriptions("found"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhaseInstalling,
				Reason: v1alpha1.CSVReasonRequirementsMet,
			}),
			mockCRDs:    true,
			description: "TransitionPendingToInstalling/RequirementsMet/OwnedFound",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
						Required: makeCRDDescriptions("found"),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhasePending,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhaseInstalling,
				Reason: v1alpha1.CSVReasonRequirementsMet,
			}),
			mockCRDs:    true,
			description: "TransitionPendingToInstalling/RequirementsMet/RequiredFound",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "test",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhaseUnknown,
				Reason: v1alpha1.CSVReasonInstallCheckFailed,
			}),
			mockCheckInstall: true,
			checkInstall:     false,
			checkInstallErr:  fmt.Errorf("check failed"),
			description:      "TransitionInstallingToUnknown/InstallCheckFailed",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "test",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhaseFailed,
				Reason: v1alpha1.CSVReasonComponentFailed,
			}),
			mockCheckInstall:  true,
			checkInstall:      false,
			mockApplyStrategy: true,
			errString:         "install failed",
			description:       "TransitionInstallingToFailed/InstallComponentFailed",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "test",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase: v1alpha1.CSVPhaseInstalling,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhaseSucceeded,
				Reason: v1alpha1.CSVReasonInstallSuccessful,
			}),
			mockCheckInstall: true,
			checkInstall:     true,
			description:      "TransitionInstallingToSucceeded/InstallSucceeded",
		},
		{
			in: withStatus(withSpec(testCSV(),
				&v1alpha1.ClusterServiceVersionSpec{
					InstallStrategy: v1alpha1.NamedInstallStrategy{
						StrategyName:    "test",
						StrategySpecRaw: []byte(`"test":"spec"`),
					},
				}),
				&v1alpha1.ClusterServiceVersionStatus{
					Phase:  v1alpha1.CSVPhaseSucceeded,
					Reason: v1alpha1.CSVReasonInstallSuccessful,
				}),
			out: withStatus(testCSV(), &v1alpha1.ClusterServiceVersionStatus{
				Phase:  v1alpha1.CSVPhasePending,
				Reason: v1alpha1.CSVReasonComponentUnhealthy,
			}),
			mockCheckInstall: true,
			checkInstall:     false,
			description:      "TransitionSucceededToPending/ComponentUnhealthy",
		},
	}

	for _, tt := range tests {
		ctrl := gomock.NewController(t)
		mockOp := NewMockALMOperator(ctrl)

		// Mock CRD calls if needed
		if tt.mockCRDs {
			mockCRDExistence(*mockOp.MockQueueOperator.MockClient, tt.in.Spec.CustomResourceDefinitions.Owned)
			mockCRDExistence(*mockOp.MockQueueOperator.MockClient, tt.in.Spec.CustomResourceDefinitions.Required)
		}

		// Mock install check and install strategy if needed
		if tt.in.Spec.InstallStrategy.StrategyName != "" {
			mockOp.MockStrategyResolver.EXPECT().UnmarshalStrategy(tt.in.Spec.InstallStrategy).Return(&testInstallStrategy, nil)
			mockOp.MockStrategyResolver.EXPECT().
				InstallerForStrategy((&testInstallStrategy).GetStrategyName(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(NewTestInstaller(tt))
		}

		// Test the transition
		t.Run(tt.description, func(t *testing.T) {
			err := mockOp.transitionCSVState(tt.in)
			if tt.errString != "" {
				require.EqualError(t, err, tt.errString)
			} else {
				require.NoError(t, err)
			}
			require.EqualValues(t, tt.out.Status.Phase, tt.in.Status.Phase)
			require.EqualValues(t, tt.out.Status.Reason, tt.in.Status.Reason)

		})
		ctrl.Finish()
	}
}
