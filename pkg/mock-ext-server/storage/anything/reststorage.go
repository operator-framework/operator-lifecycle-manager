package packagemanifest

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/mock-ext-server/apis/anything/v1alpha1"
)

type AnythingStorage struct {
	kind string
}

var _ rest.KindProvider = &AnythingStorage{}
var _ rest.Storage = &AnythingStorage{}
var _ rest.Scoper = &AnythingStorage{}

// NewStorage returns an in-memory implementation of storage.Interface.
func NewStorage(kind string) *AnythingStorage {
	return &AnythingStorage{
		kind: kind,
	}
}

// Storage interface
func (m *AnythingStorage) New() runtime.Object {
	return &v1alpha1.Anything{}
}

// KindProvider interface
func (m *AnythingStorage) Kind() string {
	return m.kind
}

// Scoper interface
func (m *AnythingStorage) NamespaceScoped() bool {
	return true
}
