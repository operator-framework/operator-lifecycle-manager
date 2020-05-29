package scoped

import (
	"fmt"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
)

func TestUserDefinedServiceAccountQuerier(t *testing.T) {
	tests := []struct {
		name          string
		crclient      versioned.Interface
		namespace     string
		wantReference *corev1.ObjectReference
		wantErr       bool
		err           error
	}{
		{
			name:      "NoOperatorGroup",
			crclient:  fake.NewSimpleClientset(),
			namespace: "ns",
			wantErr:   true,
			err:       fmt.Errorf("no operator group found that is managing this namespace"),
		},
		{
			name: "OperatorGroup/NamespaceNotInSpec",
			crclient: fake.NewSimpleClientset(&v1.OperatorGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "og",
					Namespace: "ns",
				},
				Spec: v1.OperatorGroupSpec{
					TargetNamespaces: []string{"other"},
				},
			}),
			namespace: "ns",
			wantErr:   true,
			err:       fmt.Errorf("no operator group found that is managing this namespace"),
		},
		{
			name: "OperatorGroup/NamespaceNotInStatus",
			crclient: fake.NewSimpleClientset(&v1.OperatorGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "og",
					Namespace: "ns",
				},
				Spec: v1.OperatorGroupSpec{
					TargetNamespaces: []string{"ns"},
				},
			}),
			namespace: "ns",
			wantErr:   true,
			err:       fmt.Errorf("no operator group found that is managing this namespace"),
		},
		{
			name: "OperatorGroup/Multiple",
			crclient: fake.NewSimpleClientset(
				&v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og",
						Namespace: "ns",
					},
					Spec: v1.OperatorGroupSpec{
						TargetNamespaces: []string{"ns"},
					},
					Status: v1.OperatorGroupStatus{
						Namespaces: []string{"ns"},
					},
				},
				&v1.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "og2",
						Namespace: "ns",
					},
					Spec: v1.OperatorGroupSpec{
						TargetNamespaces: []string{"ns"},
					},
					Status: v1.OperatorGroupStatus{
						Namespaces: []string{"ns"},
					},
				},
			),
			namespace: "ns",
			wantErr:   true,
			err:       fmt.Errorf("more than one operator group(s) are managing this namespace count=2"),
		},
		{
			name: "OperatorGroup/NamespaceInStatus/NoSA",
			crclient: fake.NewSimpleClientset(&v1.OperatorGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "og",
					Namespace: "ns",
				},
				Spec: v1.OperatorGroupSpec{
					TargetNamespaces: []string{"ns"},
				},
				Status: v1.OperatorGroupStatus{
					Namespaces: []string{"ns"},
				},
			}),
			namespace: "ns",
			wantErr:   false,
			err:       nil,
		},
		{
			name: "OperatorGroup/NamespaceInStatus/ServiceAccountRef",
			crclient: fake.NewSimpleClientset(&v1.OperatorGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "og",
					Namespace: "ns",
				},
				Spec: v1.OperatorGroupSpec{
					TargetNamespaces:   []string{"ns"},
					ServiceAccountName: "sa",
				},
				Status: v1.OperatorGroupStatus{
					Namespaces: []string{"ns"},
					ServiceAccountRef: &corev1.ObjectReference{
						Kind:      "ServiceAccount",
						Namespace: "ns",
						Name:      "sa",
					},
				},
			}),
			namespace: "ns",
			wantErr:   false,
			err:       nil,
			wantReference: &corev1.ObjectReference{
				Kind:      "ServiceAccount",
				Namespace: "ns",
				Name:      "sa",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := test.NewNullLogger()
			f := &UserDefinedServiceAccountQuerier{
				crclient: tt.crclient,
				logger:   logger,
			}
			gotReference, err := f.NamespaceQuerier(tt.namespace)()
			if tt.wantErr {
				require.Equal(t, tt.err, err)
			} else {
				require.Nil(t, tt.err)
			}
			require.Equal(t, tt.wantReference, gotReference)
		})
	}
}
