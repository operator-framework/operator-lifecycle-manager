package servicebroker

import (
	"errors"
	"fmt"
	"testing"

	opClient "github.com/coreos-inc/tectonic-operators/operator-client/pkg/client"
	gomock "github.com/golang/mock/gomock"
	osb "github.com/pmorie/go-open-service-broker-client/v2"
	"github.com/pmorie/osb-broker-lib/pkg/broker"
	"github.com/stretchr/testify/require"
	"k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
)

// compareResources compares resource equality then prints a diff for easier debugging
func compareResources(t *testing.T, expected, actual interface{}) {
	if eq := equality.Semantic.DeepEqual(expected, actual); !eq {
		require.Failf(t, "Resource does not match expected value: %s",
			diff.ObjectDiff(expected, actual))
	}
}

const (
	testNamespace            = "testns"
	testCatalogConfigMapName = "test-catalog-configmap"
)

const testAppCRD = `- apiVersion: apiextensions.k8s.io/v1beta1
  kind: CustomResourceDefinition
  metadata:
    name: testapps.servicebroker.testing.coreos.com
  spec:
    group: servicebroker.testing.coreos.com
    names:
      kind: TestApp
      listKind: TestAppList
      plural: testapps
      singular: testapp
    scope: Namespaced
    version: v1alpha1
`

const testOtherAppCRD = `- apiVersion: apiextensions.k8s.io/v1beta1
  kind: CustomResourceDefinition
  metadata:
    name: othertestapps.servicebroker.testing.coreos.com
  spec:
    group: servicebroker.testing.coreos.com
    names:
      kind: OtherTestApp
      listKind: OtherTestAppList
      plural: othertestapps
      singular: othertestapp
    scope: Namespaced
    version: v1beta1
    validation:
      openAPIv3:
        type: object
        required:
        - replicas
        properties:
          replicas:
            type: number
            description: The number of copies of the app to run
`

const testServiceCSV = `- apiVersion: app.coreos.com/v1alpha1
  kind: ClusterServiceVersion-v1
  metadata:
    name: test-service.0.1.0
    namespace: placeholder
  spec:
    displayName: Test Service
    version: 0.1.0
    maturity: stable
    provider:
      name: Operator Framework
    keywords: ['test', 'service', 'servicebroker']
    selector:
      matchLabels:
        olm-owner: test-service
    install:
      strategy: deployment
      spec:
        permissions:
        - apiGroups:
          - apps
          resources:
          - deployments
          verbs:
          - "*"
        deployments:
        - name: test-service
          spec:
            replicas: 1
            selector:
              matchLabels:
                app: test-service
            template:
              metadata:
                labels:
                  app: test-service
              spec:
                containers:
                  - name: test-service
                    image: test-service:v1alpha1
                    command:
                    - run-app
                restartPolicy: Always
                terminationGracePeriodSeconds: 5
    customresourcedefinitions:
      owned:
      - description: Represents an instance of a test application
        displayName: Test Application Resource
        kind: TestApp
        name: testapps.servicebroker.testing.coreos.com
        version: v1alpha1
`

const testServiceAlphaCSV = `- apiVersion: app.coreos.com/v1alpha1
  kind: ClusterServiceVersion-v1
  metadata:
    name: test-service.0.1.1
    namespace: placeholder
  spec:
    displayName: Test Service
    version: 0.1.1
    maturity: alpha
    replaces: test-service.0.1.0
    provider:
      name: Operator Framework
      url: https://github.com/operator-framework
    description: "Test Service for unit testing OLM service broker"
    keywords: ['test', 'service', 'servicebroker']
    labels:
      env: testing
      pkg: github.com/operator-framework/operator-lifecycle-manager/pkg/server/servicebroker
    selector:
      matchLabels:
        olm-owner: test-service
    install:
      strategy: deployment
      spec:
        permissions:
        - apiGroups:
          - apps
          resources:
          - deployments
          verbs:
          - "*"
        deployments:
        - name: test-service
          spec:
            replicas: 1
            selector:
              matchLabels:
                app: test-service
            template:
              metadata:
                labels:
                  app: test-service
              spec:
                containers:
                  - name: test-service
                    image: test-service:v1alpha1
                    command:
                    - run-app
                restartPolicy: Always
                terminationGracePeriodSeconds: 5
    customresourcedefinitions:
      owned:
      - description: Represents an instance of a test application
        displayName: Test Application Resource
        kind: TestApp
        name: testapps.servicebroker.testing.coreos.com
        version: v1alpha1
`

const testOtherServiceCSV = `- apiVersion: app.coreos.com/v1alpha1
  kind: ClusterServiceVersion-v1
  metadata:
    name: other-test-service.0.2.0
    namespace: placeholder
  spec:
    displayName: Other Test Service
    version: 0.2.0
    maturity: stable
    provider:
      name: Operator Framework
      url: https://github.com/operator-framework
    description: |
      # Other Test Service

      For unit testing OLM service broker.

    keywords: ['test', 'other', 'service', 'servicebroker']
    maintainers:
    - name: Operator Framework Maintainers
      email: operator-framework@googlegroups.com
    links:
    - name: OLM Source Code
      url: https://github.com/operator-framework/operator-lifecycle-manager
    - name: Introduction Blog Post
      url: https://coreos.com/blog/introducing-operator-framework
    icon:
    - base64data: VGhpcyB3b24ndCByZW5kZXI=
      mediatype: image/png
    labels:
      env: testing
      pkg: github.com/operator-framework/operator-lifecycle-manager/pkg/server/servicebroker
    selector:
      matchLabels:
        olm-owner: other-test-service
    install:
      strategy: deployment
      spec:
        permissions:
        - apiGroups:
          - apps
          resources:
          - deployments
          verbs:
          - "*"
        deployments:
        - name: other-test-service
          spec:
            replicas: 1
            selector:
              matchLabels:
                app: other-test-service
            template:
              metadata:
                labels:
                  app: other-test-service
              spec:
                containers:
                  - name: other-test-service
                    image: other-test-service:v2
                    command:
                    - run-other-app
                restartPolicy: Always
                terminationGracePeriodSeconds: 5
    customresourcedefinitions:
      owned:
      - description: Represents an instance of another test application
        displayName: Other Test Application Resource
        kind: OtherTestApp
        name: othertestapps.servicebroker.testing.coreos.com
        version: v1beta1
      - description: Represents an instance of a test application
        displayName: Test Application Resource
        kind: TestApp
        name: testapps.servicebroker.testing.coreos.com
        version: v1alpha1
`
const testNoDefaultPackageManifest = `- packageName: test-package-no-default
  channels:
  - name: not-default
    currentCSV: test-service.0.1.1
  - name: also-not-default
    currentCSV: other-test-service.0.2.0
`
const testServicePackageManifest = `- packageName: TestService
  channels:
  - name: stable
    currentCSV: test-service.0.1.0
  - name: alpha
    currentCSV: test-service.0.1.1
  defaultChannel: stable
`

const testOtherServicePackageManifest = `- packageName: TestOtherService
  channels:
  - name: beta
    currentCSV: other-test-service.0.2.0
`

var freePlan = true
var notBindable = false

var testServicePlan = osb.Plan{
	ID:          "test-service-0-1-0-testapp",
	Name:        "test-service-0-1-0-testapp",
	Description: "Represents an instance of a test application",
	Free:        &freePlan,
	Bindable:    &notBindable,
	Metadata: map[string]interface{}{
		"displayName": "Test Application Resource",
		"Name":        "testapps.servicebroker.testing.coreos.com",
		"Version":     "v1alpha1",
		"Kind":        "TestApp",
	},
	Schemas: &osb.Schemas{
		ServiceInstance: &osb.ServiceInstanceSchema{},
	},
}
var testServiceAlphaPlan = osb.Plan{
	ID:          "test-service-0-1-1-testapp",
	Name:        "test-service-0-1-1-testapp",
	Description: "Represents an instance of a test application",
	Free:        &freePlan,
	Bindable:    &notBindable,
	Metadata: map[string]interface{}{
		"displayName": "Test Application Resource",
		"Name":        "testapps.servicebroker.testing.coreos.com",
		"Version":     "v1alpha1",
		"Kind":        "TestApp",
	},
	Schemas: &osb.Schemas{
		ServiceInstance: &osb.ServiceInstanceSchema{},
	},
}
var testOtherAppValidation = &v1beta1.JSONSchemaProps{
	Type:     "object",
	Required: []string{"replicas"},
	Properties: map[string]v1beta1.JSONSchemaProps{
		"replicas": v1beta1.JSONSchemaProps{
			Type:        "number",
			Description: " The number of copies of the app to run",
		},
	},
}
var testOtherAppFormDef = openshiftFormDefinition{
	serviceInstance: formResource{
		create: formAction{
			params: []string{"replicas"},
		},
	},
}
var testOtherServiceOtherPlan = osb.Plan{
	ID:          "other-test-service-0-2-0-othertestapp",
	Name:        "other-test-service-0-2-0-othertestapp",
	Description: "Represents an instance of another test application",
	Free:        &freePlan,
	Bindable:    &notBindable,
	Metadata: map[string]interface{}{
		"displayName": "Other Test Application Resource",
		"schemas":     testOtherAppFormDef,
		"Name":        "othertestapps.servicebroker.testing.coreos.com",
		"Version":     "v1beta1",
		"Kind":        "OtherTestApp",
	},
	Schemas: &osb.Schemas{
		ServiceInstance: &osb.ServiceInstanceSchema{
			Create: &osb.InputParametersSchema{
				Parameters: testOtherAppValidation,
			},
		},
	},
}
var testOtherServicePlan = osb.Plan{
	ID:          "other-test-service-0-2-0-testapp",
	Name:        "other-test-service-0-2-0-testapp",
	Description: "Represents an instance of a test application",
	Free:        &freePlan,
	Bindable:    &notBindable,
	Metadata: map[string]interface{}{
		"displayName": "Test Application Resource",
		"Name":        "testapps.servicebroker.testing.coreos.com",
		"Version":     "v1alpha1",
		"Kind":        "TestApp",
	},
	Schemas: &osb.Schemas{
		ServiceInstance: &osb.ServiceInstanceSchema{},
	},
}
var testServiceClass = osb.Service{
	Name:            "test-service-0-1-0",
	ID:              "test-service-0-1-0",
	Description:     "Test Service 0.1.0 (stable) by Operator Framework",
	Tags:            []string{"test", "service", "servicebroker"},
	Requires:        []string{},
	Bindable:        false,
	Plans:           []osb.Plan{testServicePlan},
	DashboardClient: nil,
	Metadata: map[string]interface{}{
		"displayName":         "Test Service 0.1.0",
		"longDescription":     "Cloud Service for test-service.0.1.0",
		"providerDisplayName": "Operator Framework",
		csvNameLabel:          "test-service.0.1.0",
	},
}

var testServiceAlphaClass = osb.Service{
	Name:            "test-service-0-1-1",
	ID:              "test-service-0-1-1",
	Description:     "Test Service 0.1.1 (alpha) by Operator Framework",
	Tags:            []string{"test", "service", "servicebroker"},
	Requires:        []string{},
	Bindable:        false,
	Plans:           []osb.Plan{testServiceAlphaPlan},
	DashboardClient: nil,
	Metadata: map[string]interface{}{
		"displayName":                "Test Service 0.1.1",
		"longDescription":            "Test Service for unit testing OLM service broker",
		"providerDisplayName":        "Operator Framework",
		"clusterserviceversion-name": "test-service.0.1.1",
	},
}
var testOtherServiceClass = osb.Service{
	Name:            "other-test-service-0-2-0",
	ID:              "other-test-service-0-2-0",
	Description:     "Other Test Service 0.2.0 (stable) by Operator Framework",
	Tags:            []string{"test", "other", "service", "servicebroker"},
	Requires:        []string{},
	Bindable:        false,
	Plans:           []osb.Plan{testOtherServiceOtherPlan, testOtherServicePlan},
	DashboardClient: nil,
	Metadata: map[string]interface{}{
		"displayName":                "Other Test Service 0.2.0",
		"longDescription":            "Other Test Service\n\nFor unit testing OLM service broker.\n\n",
		"providerDisplayName":        "Operator Framework",
		"imageUrl":                   "data:image/png;base64,VGhpcyB3b24ndCByZW5kZXI=",
		"supportUrl":                 "https://github.com/operator-framework/operator-lifecycle-manager",
		"clusterserviceversion-name": "other-test-service.0.2.0",
	},
}

type mockCatalogLoader struct {
	configMaps []v1.ConfigMap
	err        error
}

func (m *mockCatalogLoader) Load(namespace string) (registry.Source, error) {
	loader := registry.ConfigMapCatalogResourceLoader{
		Catalog:   registry.NewInMem(),
		Namespace: namespace,
	}
	for _, cm := range m.configMaps {
		if namespace != "" && cm.GetNamespace() != namespace {
			continue
		}
		if err := loader.LoadCatalogResourcesFromConfigMap(&cm); err != nil {
			return nil, err
		}
	}
	return loader.Catalog, m.err
}

func mockALMBroker(ctrl *gomock.Controller, namespace string, configMaps []v1.ConfigMap, err error) *ALMBroker {
	return &ALMBroker{
		opClient:  opClient.NewMockInterface(ctrl),
		client:    fake.NewSimpleClientset(),
		catalog:   &mockCatalogLoader{configMaps, err},
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
		configMaps       []v1.ConfigMap
		catalogLoadError error
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
		// Loads and Converts Packages to Services
		{
			name:        "CatalogLoad",
			description: "returns errors loading catalog",
			initial: state{
				configMaps:       []v1.ConfigMap{},
				catalogLoadError: errors.New("test error"),
			},
			inputs: args{
				namespace: "test-ns",
				ctx:       nil,
			},
			expect: output{
				err:  errors.New("test error"),
				resp: nil,
			},
		},
		{
			name:        "EmptyCatalog",
			description: "empty catalog returns empty list of services",
			initial: state{
				configMaps: []v1.ConfigMap{},
			},
			inputs: args{
				namespace: "test-ns",
				ctx:       nil,
			},
			expect: output{
				err:  nil,
				resp: &broker.CatalogResponse{osb.CatalogResponse{[]osb.Service{}}},
			},
		},
		{
			name:        "SkipPackage",
			description: "without a default channel set",
			initial: state{
				configMaps: []v1.ConfigMap{v1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testCatalogConfigMapName,
						Namespace: testNamespace,
					},
					Data: map[string]string{
						"packages":                  testServicePackageManifest + testNoDefaultPackageManifest,
						"customResourceDefinitions": testAppCRD + testOtherAppCRD,
						"clusterServiceVersions":    testServiceCSV + testServiceAlphaCSV + testOtherServiceCSV,
					},
				}},
			},
			inputs: args{
				namespace: testNamespace,
				ctx:       nil,
			},
			expect: output{
				err: nil,
				resp: &broker.CatalogResponse{osb.CatalogResponse{[]osb.Service{
					testServiceClass,
				}}},
			},
		},
		{
			name:        "SkipPackage",
			description: "with invalid service names",
			initial: state{
				configMaps: []v1.ConfigMap{v1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testCatalogConfigMapName,
						Namespace: testNamespace,
					},
					Data: map[string]string{
						"packages": testServicePackageManifest + `- packageName: test-package-bad-service-name
  channels:
  - name: default
    currentCSV: .._..
`,
						"customResourceDefinitions": testAppCRD + testOtherAppCRD,
						"clusterServiceVersions": testServiceCSV + testServiceAlphaCSV + `- apiVersion: app.coreos.com/v1alpha1
  kind: ClusterServiceVersion-v1
  metadata:
    name: .._..
    namespace: placeholder
  spec:
    displayName: Test Service with Invalid Name
    version: 0.1.0
    install:
      strategy: deployment
      spec:
        deployments:
        - name: test-service
    customresourcedefinitions:
      owned:
`,
					},
				}},
			},
			inputs: args{
				namespace: testNamespace,
				ctx:       nil,
			},
			expect: output{
				err: nil,
				resp: &broker.CatalogResponse{osb.CatalogResponse{[]osb.Service{
					testServiceClass,
				}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s: %s", tt.name, tt.description), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// configure cluster state
			mk := mockALMBroker(ctrl, tt.inputs.namespace, tt.initial.configMaps, tt.initial.catalogLoadError)
			resp, err := mk.GetCatalog(tt.inputs.ctx)
			if tt.expect.err != nil {
				require.EqualError(t, err, tt.expect.err.Error())
			} else {
				require.NoError(t, err)
			}
			compareResources(t, tt.expect.resp, resp)
		})
	}
}

func TODO_TestProvision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, nil).Provision(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TODO_TestDeprovision(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, nil).Deprovision(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TODO_TestLastOperation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, nil).LastOperation(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TestBind(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, nil).Bind(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TestUnbind(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, nil).Unbind(nil, nil)
	require.EqualError(t, err, "not supported")
}

func TestUpdate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	_, err := mockALMBroker(ctrl, testNamespace, []v1.ConfigMap{}, nil).Update(nil, nil)
	require.EqualError(t, err, "not supported")
}
