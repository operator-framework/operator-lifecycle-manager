package catalog

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	apiregistrationfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/fake"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	olmerrors "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/errors"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

type mockTransitioner struct {
	err error
}

var _ installPlanTransitioner = &mockTransitioner{}

func (m *mockTransitioner) ResolvePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func (m *mockTransitioner) ExecutePlan(plan *v1alpha1.InstallPlan) error {
	return m.err
}

func TestTransitionInstallPlan(t *testing.T) {

	errMsg := "transition test error"
	err := errors.New(errMsg)

	resolved := &v1alpha1.InstallPlanCondition{
		Type:   v1alpha1.InstallPlanResolved,
		Status: corev1.ConditionTrue,
	}
	unresolved := &v1alpha1.InstallPlanCondition{
		Type:    v1alpha1.InstallPlanResolved,
		Status:  corev1.ConditionFalse,
		Reason:  v1alpha1.InstallPlanReasonInstallCheckFailed,
		Message: errMsg,
	}
	installed := &v1alpha1.InstallPlanCondition{
		Type:   v1alpha1.InstallPlanInstalled,
		Status: corev1.ConditionTrue,
	}
	failed := &v1alpha1.InstallPlanCondition{
		Type:    v1alpha1.InstallPlanInstalled,
		Status:  corev1.ConditionFalse,
		Reason:  v1alpha1.InstallPlanReasonComponentFailed,
		Message: errMsg,
	}

	tests := []struct {
		initial    v1alpha1.InstallPlanPhase
		transError error
		approval   v1alpha1.Approval
		approved   bool
		expected   v1alpha1.InstallPlanPhase
		condition  *v1alpha1.InstallPlanCondition
	}{
		{v1alpha1.InstallPlanPhaseNone, nil, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhasePlanning, nil},
		{v1alpha1.InstallPlanPhaseNone, nil, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhasePlanning, nil},
		{v1alpha1.InstallPlanPhaseNone, err, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhasePlanning, nil},
		{v1alpha1.InstallPlanPhaseNone, err, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhasePlanning, nil},

		{v1alpha1.InstallPlanPhasePlanning, nil, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseInstalling, resolved},
		{v1alpha1.InstallPlanPhasePlanning, nil, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseInstalling, resolved},
		{v1alpha1.InstallPlanPhasePlanning, nil, v1alpha1.ApprovalManual, false, v1alpha1.InstallPlanPhaseRequiresApproval, resolved},
		{v1alpha1.InstallPlanPhasePlanning, nil, v1alpha1.ApprovalManual, true, v1alpha1.InstallPlanPhaseInstalling, resolved},
		{v1alpha1.InstallPlanPhasePlanning, err, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseFailed, unresolved},
		{v1alpha1.InstallPlanPhasePlanning, err, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseFailed, unresolved},

		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseComplete, installed},
		{v1alpha1.InstallPlanPhaseInstalling, nil, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseComplete, installed},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, false, v1alpha1.InstallPlanPhaseFailed, failed},
		{v1alpha1.InstallPlanPhaseInstalling, err, v1alpha1.ApprovalAutomatic, true, v1alpha1.InstallPlanPhaseFailed, failed},

		{v1alpha1.InstallPlanPhaseRequiresApproval, nil, v1alpha1.ApprovalManual, false, v1alpha1.InstallPlanPhaseRequiresApproval, nil},
		{v1alpha1.InstallPlanPhaseRequiresApproval, nil, v1alpha1.ApprovalManual, true, v1alpha1.InstallPlanPhaseInstalling, nil},
	}
	for _, tt := range tests {
		// Create a plan in the provided initial phase.
		plan := &v1alpha1.InstallPlan{
			Spec: v1alpha1.InstallPlanSpec{
				Approval: tt.approval,
				Approved: tt.approved,
			},
			Status: v1alpha1.InstallPlanStatus{
				Phase:      tt.initial,
				Conditions: []v1alpha1.InstallPlanCondition{},
			},
		}

		// Create a transitioner that returns the provided error.
		transitioner := &mockTransitioner{tt.transError}

		// Attempt to transition phases.
		out, _ := transitionInstallPlanState(logrus.New(), transitioner, *plan)

		// Assert that the final phase is as expected.
		require.Equal(t, tt.expected, out.Status.Phase)

		// Assert that the condition set is as expected
		if tt.condition == nil {
			require.Equal(t, 0, len(out.Status.Conditions))
		} else {
			require.Equal(t, 1, len(out.Status.Conditions))
			require.Equal(t, tt.condition.Type, out.Status.Conditions[0].Type)
			require.Equal(t, tt.condition.Status, out.Status.Conditions[0].Status)
			require.Equal(t, tt.condition.Reason, out.Status.Conditions[0].Reason)
			require.Equal(t, tt.condition.Message, out.Status.Conditions[0].Message)
		}
	}
}

func TestSyncCatalogSources(t *testing.T) {
	resolver := &resolver.MultiSourceResolver{}

	tests := []struct {
		testName          string
		operatorNamespace string
		catalogSource     *v1alpha1.CatalogSource
		configMap         *corev1.ConfigMap
		expectedStatus    *v1alpha1.CatalogSourceStatus
		expectedError     error
	}{
		{
			testName:          "CatalogSourceWithBackingConfigMap",
			operatorNamespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					ConfigMap: "cool-configmap",
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
				},
				Data: fakeConfigMapData(),
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
				},
			},
			expectedError: nil,
		},
		{
			testName:          "CatalogSourceUpdatedByDifferentCatalogOperator",
			operatorNamespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					ConfigMap: "cool-configmap",
				},
				Status: v1alpha1.CatalogSourceStatus{
					ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
						Name:            "cool-configmap",
						Namespace:       "cool-namespace",
						UID:             types.UID("configmap-uid"),
						ResourceVersion: "resource-version",
					},
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
				},
				Data: fakeConfigMapData(),
			},
			expectedStatus: &v1alpha1.CatalogSourceStatus{
				ConfigMapResource: &v1alpha1.ConfigMapResourceReference{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
				},
			},
			expectedError: nil,
		},
		{
			testName:          "CatalogSourceWithInvalidConfigMap",
			operatorNamespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					ConfigMap: "cool-configmap",
				},
			},
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "cool-configmap",
					Namespace:       "cool-namespace",
					UID:             types.UID("configmap-uid"),
					ResourceVersion: "resource-version",
				},
				Data: map[string]string{},
			},
			expectedStatus: nil,
			expectedError:  errors.New("failed to create catalog source from ConfigMap cool-configmap: error parsing ConfigMap cool-configmap: no valid resources found"),
		},
		{
			testName:          "CatalogSourceWithMissingConfigMap",
			operatorNamespace: "cool-namespace",
			catalogSource: &v1alpha1.CatalogSource{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cool-catalog",
					Namespace: "cool-namespace",
					UID:       types.UID("catalog-uid"),
				},
				Spec: v1alpha1.CatalogSourceSpec{
					ConfigMap: "cool-configmap",
				},
			},
			configMap:      &corev1.ConfigMap{},
			expectedStatus: nil,
			expectedError:  errors.New("failed to get catalog config map cool-configmap when updating status: configmaps \"cool-configmap\" not found"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.testName, func(t *testing.T) {
			// Create existing objects
			clientObjs := []runtime.Object{tt.catalogSource}
			k8sObjs := []runtime.Object{tt.configMap}

			// Create test operator
			stopCh := make(chan struct{})
			defer func() { stopCh <- struct{}{} }()
			op, _, err := NewFakeOperator(clientObjs, k8sObjs, nil, nil, resolver, tt.operatorNamespace, stopCh)
			require.NoError(t, err)

			// Run sync
			err = op.syncCatalogSources(tt.catalogSource)
			if tt.expectedError != nil {
				require.EqualError(t, err, tt.expectedError.Error())
			} else {
				require.NoError(t, err)
			}

			// Get updated catalog and check status
			updated, err := op.client.OperatorsV1alpha1().CatalogSources(tt.catalogSource.GetNamespace()).Get(tt.catalogSource.GetName(), metav1.GetOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, updated)

			if tt.expectedStatus != nil {
				require.NotEmpty(t, updated.Status)
				require.Equal(t, *tt.expectedStatus.ConfigMapResource, *updated.Status.ConfigMapResource)

				// Ensure that the catalog source has been loaded into memory
				_, ok := op.sources[registry.ResourceKey{Name: tt.catalogSource.GetName(), Namespace: tt.catalogSource.GetNamespace()}]
				require.True(t, ok, "Expected catalog was not loaded into memory")
			}
		})
	}
}

func TestCompetingCRDOwnersExist(t *testing.T) {

	testNamespace := "default"
	tests := []struct {
		name              string
		csv               v1alpha1.ClusterServiceVersion
		existingCRDOwners map[string][]string
		expectedErr       error
		expectedResult    bool
	}{
		{
			name:              "NoCompetingOwnersExist",
			csv:               csv("turkey", []string{"feathers"}, nil),
			existingCRDOwners: nil,
			expectedErr:       nil,
			expectedResult:    false,
		},
		{
			name: "OnlyCompetingWithSelf",
			csv:  csv("turkey", []string{"feathers"}, nil),
			existingCRDOwners: map[string][]string{
				"feathers": []string{"turkey"},
			},
			expectedErr:    nil,
			expectedResult: false,
		},
		{
			name: "CompetingOwnersExist",
			csv:  csv("turkey", []string{"feathers"}, nil),
			existingCRDOwners: map[string][]string{
				"feathers": []string{"seagull"},
			},
			expectedErr:    nil,
			expectedResult: true,
		},
		{
			name: "CompetingOwnerExistsOnSecondCRD",
			csv:  csv("turkey", []string{"feathers", "beak"}, nil),
			existingCRDOwners: map[string][]string{
				"milk": []string{"cow"},
				"beak": []string{"squid"},
			},
			expectedErr:    nil,
			expectedResult: true,
		},
		{
			name: "MoreThanOneCompetingOwnerExists",
			csv:  csv("turkey", []string{"feathers"}, nil),
			existingCRDOwners: map[string][]string{
				"feathers": []string{"seagull", "turkey"},
			},
			expectedErr:    olmerrors.NewMultipleExistingCRDOwnersError([]string{"seagull", "turkey"}, "feathers", testNamespace),
			expectedResult: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			competing, err := competingCRDOwnersExist(testNamespace, &tt.csv, tt.existingCRDOwners)

			// Assert the error is as expected
			if tt.expectedErr == nil {
				require.Nil(t, err)
			} else {
				require.Equal(t, tt.expectedErr, err)
			}

			require.Equal(t, competing, tt.expectedResult)
		})
	}
}

func fakeConfigMapData() map[string]string {
	data := make(map[string]string)
	yaml, err := yaml.Marshal([]v1beta1.CustomResourceDefinition{crd("fake-crd")})
	if err != nil {
		return data
	}

	data["customResourceDefinitions"] = string(yaml)
	return data
}

// NewFakeOprator creates a new operator using fake clients
func NewFakeOperator(clientObjs []runtime.Object, k8sObjs []runtime.Object, extObjs []runtime.Object, regObjs []runtime.Object, resolver resolver.DependencyResolver, namespace string, stopCh <-chan struct{}) (*Operator, []cache.InformerSynced, error) {
	// Create client fakes
	clientFake := fake.NewSimpleClientset(clientObjs...)
	opClientFake := operatorclient.NewClient(k8sfake.NewSimpleClientset(k8sObjs...), apiextensionsfake.NewSimpleClientset(extObjs...), apiregistrationfake.NewSimpleClientset(regObjs...))

	// Create test namespace
	_, err := opClientFake.KubernetesInterface().CoreV1().Namespaces().Create(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
	if err != nil {
		return nil, nil, err
	}

	// Create the new operator
	queueOperator, err := queueinformer.NewOperatorFromClient(opClientFake, logrus.New())
	op := &Operator{
		Operator:           queueOperator,
		client:             clientFake,
		lister:             operatorlister.NewLister(),
		namespace:          namespace,
		sources:            make(map[registry.ResourceKey]registry.Source),
		dependencyResolver: resolver,
	}

	// Register informers
	wakeupInterval := 5 * time.Minute
	informerList := []cache.SharedIndexInformer{}
	subscriptionInformer := externalversions.NewSharedInformerFactoryWithOptions(clientFake, wakeupInterval, externalversions.WithNamespace(namespace)).Operators().V1alpha1().Subscriptions()
	op.lister.OperatorsV1alpha1().RegisterSubscriptionLister(namespace, subscriptionInformer.Lister())
	informerList = append(informerList, subscriptionInformer.Informer())

	// Wait for caches to sync
	var hasSyncedCheckFns []cache.InformerSynced
	for _, informer := range informerList {
		op.RegisterInformer(informer)
		hasSyncedCheckFns = append(hasSyncedCheckFns, informer.HasSynced)
		go informer.Run(stopCh)
	}

	if ok := cache.WaitForCacheSync(stopCh, hasSyncedCheckFns...); !ok {
		return nil, nil, fmt.Errorf("failed to wait for caches to sync")
	}

	return op, hasSyncedCheckFns, nil
}

func installPlan(names ...string) v1alpha1.InstallPlan {
	return v1alpha1.InstallPlan{
		Spec: v1alpha1.InstallPlanSpec{
			ClusterServiceVersionNames: names,
		},
		Status: v1alpha1.InstallPlanStatus{
			Plan: []*v1alpha1.Step{},
		},
	}
}

func csv(name string, owned, required []string) v1alpha1.ClusterServiceVersion {
	requiredCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, name := range required {
		requiredCRDDescs = append(requiredCRDDescs, v1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	ownedCRDDescs := make([]v1alpha1.CRDDescription, 0)
	for _, name := range owned {
		ownedCRDDescs = append(ownedCRDDescs, v1alpha1.CRDDescription{Name: name, Version: "v1", Kind: name})
	}

	return v1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1alpha1.ClusterServiceVersionSpec{
			CustomResourceDefinitions: v1alpha1.CustomResourceDefinitions{
				Owned:    ownedCRDDescs,
				Required: requiredCRDDescs,
			},
		},
	}
}

func crd(name string) v1beta1.CustomResourceDefinition {
	return v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   name + "group",
			Version: "v1",
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind: name,
			},
		},
	}
}
