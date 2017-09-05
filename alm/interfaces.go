package alm

import (
	"context"
	"fmt"
)

type ALM interface {
	RegisterAppType(*AppType) (*AppTypeResource, error)
	ListAppTypes() (*AppTypeList, error)
	InstallAppOperator(apptype string, version string) (*OperatorVersionResource, error)
	ListOperatorVersionsForApp(apptype string, version string) (*OperatorVersion, error)
}

type OperatorInstaller interface {
	Install(ctx context.Context, ns string, data interface{}) error
}

type MockALM struct {
	Name string
}

func (m *MockALM) RegisterAppType(app *AppType) (*AppTypeResource, error) {
	resource := AppTypeResource{}
	resource.Kind = AppTypeCRDName
	resource.APIVersion = AppTypeAPIVersion
	resource.Spec = app
	fmt.Printf("[%s] AppType: %+v\n Resource: %+v\n", m.Name, app, resource)
	return &resource, nil
}
