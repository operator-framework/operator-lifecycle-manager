package queueinformer

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
)

func TestDefaultKeyFunc(t *testing.T) {
	tests := []struct {
		description     string
		obj             interface{}
		expectedKey     string
		expectedCreated bool
	}{
		{
			description:     "String/Created",
			obj:             "a-string-key",
			expectedKey:     "a-string-key",
			expectedCreated: true,
		},
		{
			description:     "ExplicitKey/Created",
			obj:             cache.ExplicitKey("an-explicit-key"),
			expectedKey:     "an-explicit-key",
			expectedCreated: true,
		},
		{
			description:     "Meta/Created",
			obj:             &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "a-pod"}},
			expectedKey:     "default/a-pod",
			expectedCreated: true,
		},
		{
			description:     "Meta/NonNamespaced/Created",
			obj:             &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "a-namespace"}},
			expectedKey:     "a-namespace",
			expectedCreated: true,
		},
		{
			description:     "ResourceEvent/String/Created",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, "a-string-key"),
			expectedKey:     "a-string-key",
			expectedCreated: true,
		},
		{
			description:     "ResourceEvent/ExplicitKey/Created",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, cache.ExplicitKey("an-explicit-key")),
			expectedKey:     "an-explicit-key",
			expectedCreated: true,
		},
		{
			description:     "ResourceEvent/Meta/Created",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "a-pod"}}),
			expectedKey:     "default/a-pod",
			expectedCreated: true,
		},
		{
			description:     "ResourceEvent/Meta/NonNamespaced/Created",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "a-namespace"}}),
			expectedKey:     "a-namespace",
			expectedCreated: true,
		},
		{
			description:     "ResourceEvent/ResourceEvent/ExplicitKey/Created",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, kubestate.NewResourceEvent(kubestate.ResourceAdded, cache.ExplicitKey("an-explicit-key"))),
			expectedKey:     "an-explicit-key",
			expectedCreated: true,
		},
		{
			description:     "ResourceEvent/ResourceEvent/Meta/Created",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, kubestate.NewResourceEvent(kubestate.ResourceAdded, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "a-pod"}})),
			expectedKey:     "default/a-pod",
			expectedCreated: true,
		},
		{
			description:     "Arbitrary/NotCreated",
			obj:             struct{}{},
			expectedKey:     "",
			expectedCreated: false,
		},
		{
			description:     "ResourceEvent/Arbitrary/NotCreated",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, struct{}{}),
			expectedKey:     "",
			expectedCreated: false,
		},
		{
			description:     "ResourceEvent/ResourceEvent/Arbitrary/NotCreated",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, kubestate.NewResourceEvent(kubestate.ResourceAdded, struct{}{})),
			expectedKey:     "",
			expectedCreated: false,
		},
		{
			description:     "ResourceEvent/ResourceEvent/ResourceEvent/String/NotCreated",
			obj:             kubestate.NewResourceEvent(kubestate.ResourceAdded, kubestate.NewResourceEvent(kubestate.ResourceAdded, kubestate.NewResourceEvent(kubestate.ResourceAdded, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "a-pod"}}))),
			expectedKey:     "",
			expectedCreated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			key, created := defaultKeyFunc(tt.obj)
			require.Equal(t, tt.expectedKey, key)
			require.Equal(t, tt.expectedCreated, created)
		})
	}
}
