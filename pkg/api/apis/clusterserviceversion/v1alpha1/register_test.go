package v1alpha1

import (
	"testing"

	"github.com/coreos-inc/alm/pkg/api/apis"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func TestScheme(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, addKnownTypes(scheme))
	require.NoError(t, AddToScheme(scheme))
	require.NotNil(t, serializer.NewCodecFactory(scheme))

}

func TestResource(t *testing.T) {
	name := "test"
	require.Equal(t, Resource(name), schema.GroupResource{Group: apis.GroupName, Resource: name})
}
