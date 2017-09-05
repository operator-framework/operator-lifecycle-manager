package alm

import (
	"context"
	"fmt"
)

type ALM interface {
	RegisterAppType(name string, appinfo *AppType) (*AppTypeResource, error)
	ListAppTypes() (*AppTypeList, error)
	InstallAppOperator(apptype string, version string) (*OperatorVersionResource, error)
	ListOperatorVersionsForApp(apptype string, version string) (*OperatorVersion, error)
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
		return nil, fmt.Errorf("App '%s' already registered", name)
	}
	resource := CreateAppTypeResource(app)
	resource.Name = name
	m.Catalog[name] = resource
	return resource, nil
}
