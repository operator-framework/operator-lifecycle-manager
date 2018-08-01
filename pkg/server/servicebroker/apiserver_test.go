package servicebroker

import (
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

const (
	testNamespace = "testns"
)

type mockCatalogLoader struct {
	configMaps []v1.ConfigMap
}

func (m *mockCatalogLoader) Load(namespace string) (registry.Source, error) {
	loader := registry.NewConfigMapCatalogResourceLoader(namespace, nil)
	catalog := registry.NewInMem()
	for _, cm := range m.configMaps {
		if namespace != "" && cm.GetNamespace() != namespace {
			continue
		}
		if err := loader.LoadCatalogResourcesFromConfigMap(catalog, &cm); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func mockALMBroker(ctrl *gomock.Controller, namespace string, configMaps []v1.ConfigMap, objects []runtime.Object) *ALMBroker {
	return &ALMBroker{
		opClient:  operatorclient.NewMockClientInterface(ctrl),
		client:    fake.NewSimpleClientset(objects...),
		catalog:   &mockCatalogLoader{configMaps},
		namespace: namespace,
	}
}

func TestValidateBrokerAPIVersion(t *testing.T) {
	// test bad version
	brokerMock := &ALMBroker{}
	err := brokerMock.ValidateBrokerAPIVersion("oops")
	require.EqualError(t, err, "unknown OpenServiceBroker API Version: oops")

	// supported version
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.11"))
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.12"))
	require.NoError(t, brokerMock.ValidateBrokerAPIVersion("2.13"))
}

func TestGetCatalog(t *testing.T) {
	type state struct {
		catalogSources []v1alpha1.CatalogSource
		configMaps     []v1.ConfigMap
	}
	type args struct {
		namespace string
		ctx       *broker.RequestContext
	}
	type output struct {
		err  error
		resp *broker.CatalogResponse
	}
	tests := []struct {
		name        string
		description string
		initial     state
		inputs      args
		expect      output
	}{
		{
			name:        "GetCatalog",
			description: "empty catalog returns empty list of services",
			initial: state{
				catalogSources: []v1alpha1.CatalogSource{},
				configMaps:     []v1.ConfigMap{},
			},
			inputs: args{
				namespace: testNamespace,
				ctx:       nil,
			},
			expect: output{
				err:  nil,
				resp: &broker.CatalogResponse{osb.CatalogResponse{[]osb.Service{}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s: %s", tt.name, tt.description), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// configure cluster state
			existingObjects := []runtime.Object{
				&v1alpha1.CatalogSourceList{Items: tt.initial.catalogSources},
			}

			for i, _ := range tt.initial.configMaps {
				existingObjects = append(existingObjects, &tt.initial.configMaps[i])
			}
			mk := mockALMBroker(ctrl, tt.inputs.namespace, tt.initial.configMaps, existingObjects)
			resp, err := mk.GetCatalog(tt.inputs.ctx)
			if tt.expect.err != nil {
				require.EqualError(t, err, tt.expect.err.Error())
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expect.resp, resp)
		})
	}
	// Broker loads catalog from CatalogSources/ConfigMaps
	// When a namespace specified in config:
	// - only loads from specified namespace
	// When no namespace speciied in config:
	// - loads from all namespaces

	// Loads and Converts Packages to Services
	// Skips packages:
	// - without a default channel set
	// - where default channel lists CSV not in catalog
	// - with invalid service names
	// - where definition for CSV owned CRD is not in catalog
	// Loads package:
	// - using default channel CSV
	// - converted to serviceClass correctly
	// - with bindable set to false on service and plans
}

func TODO_TestProvision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, []runtime.Object{}).Provision(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TODO_TestDeprovision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, []runtime.Object{}).Deprovision(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TODO_TestLastOperation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, []runtime.Object{}).LastOperation(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TestBind(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, []runtime.Object{}).Bind(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TestUnbind(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, []runtime.Object{}).Unbind(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TestUpdate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, []runtime.Object{}).Update(nil, nil)
	require.EqualError(t, err, "not supported")
}
