package olm

import (
	"encoding/json"
	"fmt"
	"strings"

	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha2"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

// Fakes

type TestStrategy struct{}

func (t *TestStrategy) GetStrategyName() string {
	return "teststrategy"
}

type TestInstaller struct {
	installErr      error
	checkInstallErr error
}

func NewTestInstaller(installErr error, checkInstallErr error) install.StrategyInstaller {
	return &TestInstaller{
		installErr:      installErr,
		checkInstallErr: checkInstallErr,
	}
}

func (i *TestInstaller) Install(s install.Strategy) error {
	return i.installErr
}

func (i *TestInstaller) CheckInstalled(s install.Strategy) (bool, error) {
	if i.checkInstallErr != nil {
		return false, i.checkInstallErr
	}
	return true, nil
}

func apiResourcesForObjects(objs []runtime.Object) []*metav1.APIResourceList {
	apis := []*metav1.APIResourceList{}
	for _, o := range objs {
		switch o.(type) {
		case *v1beta1.CustomResourceDefinition:
			crd := o.(*v1beta1.CustomResourceDefinition)
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: crd.Spec.Group, Version: crd.Spec.Versions[0].Name}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:         crd.GetName(),
						SingularName: crd.Spec.Names.Singular,
						Namespaced:   crd.Spec.Scope == v1beta1.NamespaceScoped,
						Group:        crd.Spec.Group,
						Version:      crd.Spec.Versions[0].Name,
						Kind:         crd.Spec.Names.Kind,
					},
				},
			})
		case *apiregistrationv1.APIService:
			a := o.(*apiregistrationv1.APIService)
			names := strings.Split(a.Name, ".")
			apis = append(apis, &metav1.APIResourceList{
				GroupVersion: metav1.GroupVersion{Group: names[1], Version: a.Spec.Version}.String(),
				APIResources: []metav1.APIResource{
					{
						Name:    names[1],
						Group:   names[1],
						Version: a.Spec.Version,
						Kind:    names[1] + "Kind",
					},
				},
			})
		}
	}
	log.Info(apis)
	return apis
}

func NewFakeOperator(clientObjs []runtime.Object, k8sObjs []runtime.Object, extObjs []runtime.Object, regObjs []runtime.Object, resolver install.StrategyResolverInterface, namespaces []v1.Namespace) (*Operator, error) {
	k8sClientFake := k8sfake.NewSimpleClientset(k8sObjs...)
	k8sClientFake.Resources = apiResourcesForObjects(append(extObjs, regObjs...))
	opClientFake := operatorclient.NewClient(k8sClientFake, apiextensionsfake.NewSimpleClientset(extObjs...), apiregistrationfake.NewSimpleClientset(regObjs...))
	annotations := map[string]string{"test": "annotation"}
	clientFake := fake.NewSimpleClientset(clientObjs...)

	var nsList []string
	for ix := range namespaces {
		nsList = append(nsList, namespaces[ix].Name)
	}
	return NewOperator(clientFake, opClientFake, resolver, 5*time.Second, annotations, nsList)
}

func (o *Operator) GetClient() versioned.Interface {
	return o.client
}

// Tests

func deployment(deploymentName, namespace string) *appsv1.Deployment {
	var singleInstance = int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": deploymentName,
				},
			},
			Replicas: &singleInstance,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": deploymentName,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  deploymentName + "-c1",
							Image: "nginx:1.7.9",
							Ports: []v1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
						},
					},
				},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:          singleInstance,
			ReadyReplicas:     singleInstance,
			AvailableReplicas: singleInstance,
			UpdatedReplicas:   singleInstance,
		},
	}
}

func installStrategy(deploymentName string, permissions []install.StrategyDeploymentPermissions, clusterPermissions []install.StrategyDeploymentPermissions) v1alpha1.NamedInstallStrategy {
	var singleInstance = int32(1)
	strategy := install.StrategyDetailsDeployment{
		DeploymentSpecs: []install.StrategyDeploymentSpec{
			{
				Name: deploymentName,
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": deploymentName,
						},
					},
					Replicas: &singleInstance,
					Template: v1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": deploymentName,
							},
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  deploymentName + "-c1",
									Image: "nginx:1.7.9",
									Ports: []v1.ContainerPort{
										{
											ContainerPort: 80,
										},
									},
								},
							},
						},
					},
				},
			},
		},
		Permissions:        permissions,
		ClusterPermissions: clusterPermissions,
	}
	strategyRaw, err := json.Marshal(strategy)
	if err != nil {
		panic(err)
	}

	return v1alpha1.NamedInstallStrategy{
		StrategyName:    install.InstallStrategyNameDeployment,
		StrategySpecRaw: strategyRaw,
	}
}

func csv(
	name, namespace, replaces string,
	installStrategy v1alpha1.NamedInstallStrategy,
	owned, required []*v1beta1.CustomResourceDefinition,
	phase v1alpha1.ClusterServiceVersionPhase,
) *v1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, crd := range required {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: crd.GetName(), Version: crd.Spec.Versions[0].Name, Kind: crd.Spec.Names.Kind})
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, crd := range owned {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: crd.GetName(), Version: crd.Spec.Versions[0].Name, Kind: crd.Spec.Names.Kind})
	}

	return &v1alpha1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.ClusterServiceVersionKind,
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			Replaces:        replaces,
			InstallStrategy: installStrategy,
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
		Status: v1alpha1.ClusterServiceVersionStatus{
			Phase: phase,
		},
	}
}

func withCertInfo(csv *v1alpha1.ClusterServiceVersion, rotateAt metav1.Time, lastUpdated metav1.Time) *v1alpha1.ClusterServiceVersion {
	csv.Status.CertsRotateAt = rotateAt
	csv.Status.CertsLastUpdated = lastUpdated
	return csv
}

func withAPIServices(csv *v1alpha1.ClusterServiceVersion, owned, required []v1alpha1.APIServiceDescription) *v1alpha1.ClusterServiceVersion {
	csv.Spec.APIServiceDefinitions = v1alpha1.APIServiceDefinitions{
		Owned:    owned,
		Required: required,
	}
	return csv
}

func apis(apis ...string) []v1alpha1.APIServiceDescription {
	descs := []v1alpha1.APIServiceDescription{}
	for _, av := range apis {
		split := strings.Split(av, ".")
		descs = append(descs, v1alpha1.APIServiceDescription{
			Group:          split[0],
			Version:        split[1],
			Kind:           split[2],
			DeploymentName: split[0],
		})
	}
	return descs
}

func apiService(name, version string, availableStatus apiregistrationv1.ConditionStatus) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s.%s", version, name),
		},
		Spec: apiregistrationv1.APIServiceSpec{
			Version: version,
		},
		Status: apiregistrationv1.APIServiceStatus{
			Conditions: []apiregistrationv1.APIServiceCondition{
				{
					Type:   apiregistrationv1.Available,
					Status: availableStatus,
				},
			},
		},
	}
}

func crd(name string, version string) *v1beta1.CustomResourceDefinition {
	return &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name + "group",
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group: name + "group",
			Versions: []v1beta1.CustomResourceDefinitionVersion{
				{
					Name:    version,
					Storage: true,
					Served:  true,
				},
			},
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}

func TestTransitionCSV(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	namespace := "ns"

	type csvState struct {
		exists bool
		phase  v1alpha1.ClusterServiceVersionPhase
	}
	type initial struct {
		csvs []runtime.Object
		crds []runtime.Object
		objs []runtime.Object
		apis []runtime.Object
	}
	type expected struct {
		csvStates map[string]csvState
		err       map[string]error
	}
	tests := []struct {
		name     string
		initial  initial
		expected expected
	}{
		{
			name: "SingleCSVNoneToPending/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "SingleCSVNoneToPending/APIService/Required",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					), nil, apis("a1.v1.a1Kind")),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "SingleCSVPendingToFailed/BadStrategy",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "SingleCSVPendingToFailed/BadStrategyPermissions",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1",
							nil,
							[]install.StrategyDeploymentPermissions{
								{
									ServiceAccountName: "sa",
									Rules: []rbacv1.PolicyRule{
										{
											Verbs:           []string{"*"},
											Resources:       []string{"*"},
											NonResourceURLs: []string{"/osb"},
										},
									},
								},
							}),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					&v1.ServiceAccount{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "sa",
							Namespace: namespace,
						},
					},
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Required/Missing",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Required/Unavailable",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
				apis: []runtime.Object{apiService("a1", "v1", apiregistrationv1.ConditionFalse)},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToPending/APIService/Required/Unknown",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
				apis: []runtime.Object{apiService("a1", "v1", apiregistrationv1.ConditionUnknown)},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "SingleCSVPendingToFailed/APIService/Owned/DeploymentNotFound",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("b1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), apis("a1.v1.a1Kind"), nil),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
				err: map[string]error{
					"csv1": ErrRequirementsNotMet,
				},
			},
		},
		{
			name: "CSVPendingToFailed/OwnerConflict",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv2",
						namespace,
						"",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
				err: map[string]error{
					"csv2": ErrCRDOwnerConflict,
				},
			},
		},
		{
			name: "SingleCSVPendingToInstallReady/CRD",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady},
				},
			},
		},
		{
			name: "SingleCSVPendingToInstallReady/APIService/Required",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhasePending,
					), nil, apis("a1.v1.a1Kind")),
				},
				apis: []runtime.Object{apiService("a1", "v1", apiregistrationv1.ConditionTrue)},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstallReady},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToInstalling",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstalling},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToInstalling/APIService/Owned",
			initial: initial{
				csvs: []runtime.Object{
					withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					), apis("a1.v1.a1Kind"), nil),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseInstalling},
				},
			},
		},
		{
			name: "SingleCSVSucceededToPending/APIService/Owned/CertRotation",
			initial: initial{
				csvs: []runtime.Object{
					withCertInfo(withAPIServices(csv("csv1",
						namespace,
						"",
						installStrategy("a1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					), apis("a1.v1.a1Kind"), nil), metav1.Now(), metav1.Now()),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "SingleCSVInstallReadyToFailed/BadStrategy",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						v1alpha1.NamedInstallStrategy{"deployment", json.RawMessage{}},
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstallReady,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseFailed},
				},
			},
		},

		{
			name: "SingleCSVInstallingToSucceeded",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseInstalling,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVSucceededToReplacing",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseNone,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
					"csv2": {exists: true, phase: v1alpha1.CSVPhasePending},
				},
			},
		},
		{
			name: "CSVReplacingToDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
					deployment("csv2-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVDeletedToGone",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
					deployment("csv2-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleReplacingToDeleted",
			initial: initial{
				// order matters in this test case - we want to apply the latest CSV first to test the GC marking
				csvs: []runtime.Object{
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
					deployment("csv2-dep1", namespace),
					deployment("csv3-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseReplacing},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
					csv("csv1",
						namespace,
						"",
						installStrategy("csv1-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					),
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv1-dep1", namespace),
					deployment("csv2-dep1", namespace),
					deployment("csv3-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone/AfterOneDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseReplacing,
					),
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv2-dep1", namespace),
					deployment("csv3-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv1": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv2": {exists: true, phase: v1alpha1.CSVPhaseDeleting},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
		{
			name: "CSVMultipleDeletedToGone/AfterTwoDeleted",
			initial: initial{
				csvs: []runtime.Object{
					csv("csv2",
						namespace,
						"csv1",
						installStrategy("csv2-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseDeleting,
					),
					csv("csv3",
						namespace,
						"csv2",
						installStrategy("csv3-dep1", nil, nil),
						[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
						[]*v1beta1.CustomResourceDefinition{},
						v1alpha1.CSVPhaseSucceeded,
					),
				},
				crds: []runtime.Object{
					crd("c1", "v1"),
				},
				objs: []runtime.Object{
					deployment("csv2-dep1", namespace),
					deployment("csv3-dep1", namespace),
				},
			},
			expected: expected{
				csvStates: map[string]csvState{
					"csv2": {exists: false, phase: v1alpha1.CSVPhaseNone},
					"csv3": {exists: true, phase: v1alpha1.CSVPhaseSucceeded},
				},
			},
		},
	}
	for _, tt := range tests {
		namespaceObj := v1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			op, err := NewFakeOperator(tt.initial.csvs, tt.initial.objs, tt.initial.crds, tt.initial.apis, &install.StrategyResolver{}, []v1.Namespace{namespaceObj})
			require.NoError(t, err)

			// run csv sync for each CSV
			for _, csv := range tt.initial.csvs {
				err := op.syncClusterServiceVersion(csv)
				expectedErr := tt.expected.err[csv.(*v1alpha1.ClusterServiceVersion).Name]
				require.Equal(t, expectedErr, err)
			}

			// get csvs in the cluster
			outCSVMap := map[string]*v1alpha1.ClusterServiceVersion{}
			outCSVs, err := op.GetClient().OperatorsV1alpha1().ClusterServiceVersions("ns").List(metav1.ListOptions{})
			require.NoError(t, err)
			for _, csv := range outCSVs.Items {
				outCSVMap[csv.GetName()] = csv.DeepCopy()
			}

			// verify expectations of csvs in cluster
			for csvName, csvState := range tt.expected.csvStates {
				csv, ok := outCSVMap[csvName]
				assert.Equal(t, ok, csvState.exists, "%s existence should be %t", csvName, csvState.exists)
				if csvState.exists {
					assert.Equal(t, csvState.phase, csv.Status.Phase, "%s had incorrect phase", csvName)
				}
			}
		})
	}
}

func TestSyncOperatorGroups(t *testing.T) {
	//log.SetLevel(log.DebugLevel)

	nowTime := metav1.Date(2006, time.January, 2, 15, 4, 5, 0, time.FixedZone("MST", -7*3600))
	timeNow = func() metav1.Time { return nowTime }

	testNS := "test-ns"
	aLabel := map[string]string{"app": "matchLabel"}

	ownedDeployment := deployment("csv1-dep1", testNS)
	ownedDeployment.SetOwnerReferences([]metav1.OwnerReference{
		{
			Kind: "ClusterServiceVersion",
			Name: "csv1-dep1",
		},
	})

	tests := []struct {
		name          string
		expectedEqual bool
		// first item in initialObjs must be an OperatorGroup
		initialObjs []runtime.Object
		initialCrds []runtime.Object
		// first item in initialK8sObjs must be a namespace
		initialK8sObjs     []runtime.Object
		initialApis        []runtime.Object
		expectedStatus     v1alpha2.OperatorGroupStatus
		expectedAnnotation map[string]string
	}{
		{
			name:          "operator group with no matching namespace, no CSVs",
			expectedEqual: true,
			initialObjs: []runtime.Object{
				&v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: testNS,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: aLabel,
						},
					},
				},
			},
			initialK8sObjs: []runtime.Object{
				&v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: testNS,
					},
				},
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{},
		},
		{
			name:          "operator group with matching namespace, no CSVs",
			expectedEqual: true,
			initialObjs: []runtime.Object{
				&v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: testNS,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "matchLabel",
							},
						},
					},
				},
			},
			initialK8sObjs: []runtime.Object{
				&v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:        testNS,
						Labels:      aLabel,
						Annotations: map[string]string{"test": "annotation"},
					},
				},
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces: []*v1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:        testNS,
							Labels:      aLabel,
							Annotations: map[string]string{"test": "annotation"},
						},
					},
				},
				LastUpdated: timeNow(),
			},
		},
		{
			name:          "operator group with matching namespace, CSV present",
			expectedEqual: false,
			initialObjs: []runtime.Object{
				&v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: testNS,
						Labels:    aLabel,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: aLabel,
						},
					},
				},
				csv("csv1",
					testNS,
					"",
					installStrategy("csv1-dep1", nil, nil),
					[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
					[]*v1beta1.CustomResourceDefinition{},
					v1alpha1.CSVPhaseSucceeded,
				),
			},
			initialK8sObjs: []runtime.Object{
				&v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:        testNS,
						Labels:      aLabel,
						Annotations: map[string]string{"test": "annotation"},
					},
				},
				deployment("csv1-dep1", testNS),
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces: []*v1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:        testNS,
							Labels:      aLabel,
							Annotations: map[string]string{"test": "annotation"},
						},
					},
				},
				LastUpdated: timeNow(),
			},
			expectedAnnotation: map[string]string{"NOTFOUND": testNS},
		},
		{
			name:          "operator group with matching namespace, CSV present",
			expectedEqual: true,
			initialObjs: []runtime.Object{
				&v1alpha2.OperatorGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "operator-group-1",
						Namespace: testNS,
						Labels:    aLabel,
					},
					Spec: v1alpha2.OperatorGroupSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: aLabel,
						},
					},
				},
				csv("csv1",
					testNS,
					"",
					installStrategy("csv1-dep1", nil, nil),
					[]*v1beta1.CustomResourceDefinition{crd("c1", "v1")},
					[]*v1beta1.CustomResourceDefinition{},
					v1alpha1.CSVPhaseSucceeded,
				),
			},
			initialK8sObjs: []runtime.Object{
				&v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name:        testNS,
						Labels:      aLabel,
						Annotations: map[string]string{"test": "annotation"},
					},
				},
				ownedDeployment,
			},
			expectedStatus: v1alpha2.OperatorGroupStatus{
				Namespaces: []*v1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:        testNS,
							Labels:      aLabel,
							Annotations: map[string]string{"test": "annotation"},
						},
					},
				},
				LastUpdated: timeNow(),
			},
			expectedAnnotation: map[string]string{"olm.targetNamespaces": testNS, "olm.operatorGroup": "operator-group-1", "olm.operatorNamespace": testNS},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			namespaceList := []v1.Namespace{*tc.initialK8sObjs[0].(*v1.Namespace)}
			op, err := NewFakeOperator(tc.initialObjs, tc.initialK8sObjs, tc.initialCrds, tc.initialApis, &install.StrategyResolver{}, namespaceList)
			require.NoError(t, err)

			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			ready, _ := op.Run(stopCh)
			<-ready

			operatorGroup, ok := tc.initialObjs[0].(*v1alpha2.OperatorGroup)
			require.True(t, ok)

			err = op.syncOperatorGroups(operatorGroup)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedStatus, operatorGroup.Status)

			if tc.expectedAnnotation != nil {
				// assuming CSVs are in correct namespace
				for _, ns := range namespaceList {
					deployments, err := op.lister.AppsV1().DeploymentLister().Deployments(ns.GetName()).List(labels.Everything())
					if err != nil {
						t.Fatal(err)
					}
					for _, deploy := range deployments {
						if tc.expectedEqual {
							assert.Equal(t, tc.expectedAnnotation, deploy.Spec.Template.Annotations)
						} else {
							assert.NotEqual(t, tc.expectedAnnotation, deploy.Spec.Template.Annotations)
						}
					}
				}
			}
		})
	}
}

func TestIsReplacing(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	namespace := "ns"

	type initial struct {
		csvs []runtime.Object
	}
	tests := []struct {
		name     string
		initial  initial
		in       *v1alpha1.ClusterServiceVersion
		expected *v1alpha1.ClusterServiceVersion
	}{
		{
			name: "QueryErr",
			in:   csv("name", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
		{
			name: "CSVInCluster/ReplacingNotFound",
			in:   csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: []runtime.Object{
					csv("csv3", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			clientFake := fake.NewSimpleClientset(tt.initial.csvs...)

			op := &Operator{
				client: clientFake,
			}

			require.Equal(t, tt.expected, op.isReplacing(tt.in))
		})
	}
}

func TestIsBeingReplaced(t *testing.T) {
	namespace := "ns"

	type initial struct {
		csvs map[string]*v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name     string
		initial  initial
		in       *v1alpha1.ClusterServiceVersion
		expected *v1alpha1.ClusterServiceVersion
	}{
		{
			name:     "QueryErr",
			in:       csv("name", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			op := &Operator{}

			require.Equal(t, tt.expected, op.isBeingReplaced(tt.in, tt.initial.csvs))
		})
	}
}

func TestCheckReplacement(t *testing.T) {
	namespace := "ns"

	type initial struct {
		csvs map[string]*v1alpha1.ClusterServiceVersion
	}
	tests := []struct {
		name     string
		initial  initial
		in       *v1alpha1.ClusterServiceVersion
		expected *v1alpha1.ClusterServiceVersion
	}{
		{
			name:     "QueryErr",
			in:       csv("name", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			expected: nil,
		},
		{
			name: "CSVInCluster/NotReplacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: nil,
		},
		{
			name: "CSVInCluster/Replacing",
			in:   csv("csv1", namespace, "", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
			initial: initial{
				csvs: map[string]*v1alpha1.ClusterServiceVersion{
					"csv2": csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
				},
			},
			expected: csv("csv2", namespace, "csv1", installStrategy("dep", nil, nil), nil, nil, v1alpha1.CSVPhaseSucceeded),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// configure cluster state
			op := &Operator{}

			require.Equal(t, tt.expected, op.isBeingReplaced(tt.in, tt.initial.csvs))
		})
	}
}
