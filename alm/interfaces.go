package alm

import (
	"context"
	"fmt"

	"github.com/coreos/go-semver/semver"

	"github.com/coreos-inc/alm/apis/opver/v1alpha1"
)

type ALM interface {
	RegisterAppType(name string, appinfo *AppType) (*AppTypeResource, error)
	ListAppTypes() (*AppTypeList, error)
	InstallAppOperator(appType AppTypeResource, version semver.Version) (*v1alpha1.OperatorVersion, error)
	ListOperatorVersionsForApp(appType AppType) (*v1alpha1.OperatorVersionSpec, error)
}

type OperatorInstaller interface {
	Install(ctx context.Context, ns string, data interface{}) error
}

type MockALM struct {
	Name    string
	Catalog map[string]*AppTypeResource
}

func NewMock(name string) MockALM {
	return MockALM{Name: name, Catalog: map[string]*AppTypeResource{}}
}
func (m *MockALM) RegisterAppType(name string, app *AppType) (*AppTypeResource, error) {
	if _, ok := m.Catalog[name]; ok {
		return nil, fmt.Errorf("app '%s' already registered", name)
	}
	resource := NewAppTypeResource(app)
	resource.Name = name
	m.Catalog[name] = resource
	return resource, nil
}
