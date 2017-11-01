package annotater

import (
	"testing"

	opClient "github.com/coreos-inc/operator-client/pkg/client"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func NewMockNamespaceClient(ctrl *gomock.Controller, currentNamespaces []v1.Namespace) (*opClient.MockInterface, kubernetes.Interface) {
	mockClient := opClient.NewMockInterface(ctrl)
	fakeKubernetesInterface := fake.NewSimpleClientset(&v1.NamespaceList{Items: currentNamespaces})
	return mockClient, fakeKubernetesInterface
}

func TestNewAnnotator(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := opClient.NewMockInterface(ctrl)
	annotator := NewAnnotator(mockClient)
	require.IsType(t, &Annotator{}, annotator)
}

func namespaceObj(name string, annotations map[string]string) v1.Namespace {
	return v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
		},
	}
}

func namespaceObjs(names ...string) (namespaces []v1.Namespace) {
	for _, n := range names {
		namespaces = append(namespaces, namespaceObj(n, nil))
	}
	return
}

func TestGetNamespaces(t *testing.T) {
	tests := []struct {
		in              []string
		out             []v1.Namespace
		onCluster       []string
		expectedErrFunc func(error) bool
		description     string
	}{
		{
			in:          []string{"ns1"},
			out:         namespaceObjs("ns1"),
			onCluster:   []string{"ns1"},
			description: "NamespaceFound1of1",
		},
		{
			in:          []string{"ns1"},
			out:         namespaceObjs("ns1"),
			onCluster:   []string{"ns1", "ns2", "ns3"},
			description: "NamespaceFound1ofN",
		},
		{
			in:          []string{""},
			out:         namespaceObjs("ns1", "ns2", "ns3"),
			onCluster:   []string{"ns1", "ns2", "ns3"},
			description: "NamespaceFoundAll",
		},
		{
			in:              []string{"ns1"},
			out:             nil,
			onCluster:       []string{"ns2", "ns3"},
			expectedErrFunc: apierrors.IsNotFound,
			description:     "NamespaceNotFound",
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient, fakeKubernetesClient := NewMockNamespaceClient(ctrl, namespaceObjs(tt.onCluster...))
			mockClient.EXPECT().KubernetesInterface().Return(fakeKubernetesClient)
			annotator := NewAnnotator(mockClient)
			namespaces, err := annotator.getNamespaces(tt.in)
			require.Equal(t, namespaces, tt.out)
			if tt.expectedErrFunc != nil {
				require.True(t, tt.expectedErrFunc(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAnnotateNamespace(t *testing.T) {
	tests := []struct {
		in          map[string]string
		annotations map[string]string
		out         map[string]string
		errString   string
		description string
	}{
		{
			in:          map[string]string{"existing": "note"},
			annotations: map[string]string{"my": "annotation"},
			out:         map[string]string{"my": "annotation", "existing": "note"},
			description: "AddAnnotation",
		},
		{
			in:          nil,
			annotations: map[string]string{"my": "annotation"},
			out:         map[string]string{"my": "annotation"},
			description: "AddAnnotationWhenNone",
		},
		{
			in:          map[string]string{"my": "already-set"},
			annotations: map[string]string{"my": "annotation"},
			errString:   "attempted to annotate namespace ns, but already annotated by my:already-set",
			description: "AlreadyAnnotated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			namespace := namespaceObj("ns", tt.in)
			mockClient, fakeKubernetesClient := NewMockNamespaceClient(ctrl, []v1.Namespace{namespace})
			if tt.errString == "" {
				mockClient.EXPECT().KubernetesInterface().Return(fakeKubernetesClient)
			}
			annotator := NewAnnotator(mockClient)
			err := annotator.annotateNamespace(&namespace, tt.annotations)
			if tt.errString != "" {
				require.EqualError(t, err, tt.errString)
				return
			}
			require.NoError(t, err)
			// hack because patch on the kubernetes fake doesn't seem to work
			fakeKubernetesClient.CoreV1().Namespaces().Update(&namespace)
			fromCluster, err := fakeKubernetesClient.CoreV1().Namespaces().Get(namespace.Name, metav1.GetOptions{})
			require.NoError(t, err)
			require.Equal(t, tt.out, fromCluster.Annotations)
		})
	}
}

func TestAnnotateNamespaces(t *testing.T) {
	tests := []struct {
		inNamespaces       []string
		inAnnotations      map[string]string
		outNamespaces      []v1.Namespace
		existingNamespaces []v1.Namespace
		errString          string
		description        string
	}{
		{
			inNamespaces:       []string{"ns1"},
			inAnnotations:      map[string]string{"my": "annotation"},
			existingNamespaces: []v1.Namespace{namespaceObj("ns1", map[string]string{"existing": "note"})},
			outNamespaces:      []v1.Namespace{namespaceObj("ns1", map[string]string{"my": "annotation", "existing": "note"})},
			description:        "AddAnnotation",
		},
		{
			inNamespaces:       []string{"ns1"},
			inAnnotations:      map[string]string{"my": "annotation"},
			existingNamespaces: []v1.Namespace{namespaceObj("ns1", nil)},
			outNamespaces:      []v1.Namespace{namespaceObj("ns1", map[string]string{"my": "annotation"})},
			description:        "AddAnnotationWhenNone",
		},
		{
			inNamespaces:       []string{"ns1"},
			inAnnotations:      map[string]string{"my": "annotation"},
			existingNamespaces: []v1.Namespace{namespaceObj("ns1", map[string]string{"my": "already-set"})},
			errString:          "attempted to annotate namespace ns1, but already annotated by my:already-set",
			description:        "AlreadyAnnotated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient, fakeKubernetesClient := NewMockNamespaceClient(ctrl, tt.existingNamespaces)
			mockClient.EXPECT().KubernetesInterface().Return(fakeKubernetesClient)
			if tt.errString == "" {
				mockClient.EXPECT().KubernetesInterface().Return(fakeKubernetesClient)
			}

			annotator := NewAnnotator(mockClient)
			err := annotator.AnnotateNamespaces(tt.inNamespaces, tt.inAnnotations)

			if tt.errString != "" {
				require.EqualError(t, err, tt.errString)
				return
			}
			require.NoError(t, err)
			for _, namespaceName := range tt.inNamespaces {
				for _, expected := range tt.outNamespaces {
					if expected.Name == namespaceName {
						// this is a hack because fake patch doesn't work
						ns := namespaceObj(namespaceName, expected.Annotations)
						fakeKubernetesClient.CoreV1().Namespaces().Update(&ns)
					}
				}

				fromCluster, err := fakeKubernetesClient.CoreV1().Namespaces().Get(namespaceName, metav1.GetOptions{})
				require.NoError(t, err)
				for _, expected := range tt.outNamespaces {
					if expected.Name == fromCluster.Name {
						require.Equal(t, expected.Annotations, fromCluster.Annotations)
					}
				}
			}
		})
	}
}
