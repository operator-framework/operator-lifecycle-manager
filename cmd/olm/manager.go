package main

import (
	"context"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/feature"
)

var (
	copiedLabelDoesNotExist labels.Selector
)

func init() {
	requirement, err := labels.NewRequirement(operatorsv1alpha1.CopiedLabelKey, selection.DoesNotExist, nil)
	if err != nil {
		panic(err)
	}
	copiedLabelDoesNotExist = labels.NewSelector().Add(*requirement)
}

func Manager(ctx context.Context, debug bool) (ctrl.Manager, error) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(debug)))
	setupLog := ctrl.Log.WithName("setup").V(1)

	scheme := runtime.NewScheme()
	if err := metav1.AddMetaToScheme(scheme); err != nil {
		return nil, err
	}
	if err := operators.AddToScheme(scheme); err != nil {
		// ctrl.NewManager needs the Scheme to be populated
		// up-front so that the NewCache implementation we
		// provide can configure custom cache behavior on
		// non-core types.
		return nil, err
	}

	setupLog.Info("configuring manager")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&appsv1.Deployment{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&corev1.Service{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&apiextensionsv1.CustomResourceDefinition{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&apiregistrationv1.APIService{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&corev1.ConfigMap{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&corev1.ServiceAccount{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&rbacv1.Role{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&rbacv1.RoleBinding{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&rbacv1.ClusterRole{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&rbacv1.ClusterRoleBinding{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&admissionregistrationv1.MutatingWebhookConfiguration{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&admissionregistrationv1.ValidatingWebhookConfiguration{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&operatorsv1alpha1.ClusterServiceVersion{}: {
					Label: copiedLabelDoesNotExist,
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	operatorConditionReconciler, err := operators.NewOperatorConditionReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("operatorcondition"),
		mgr.GetScheme(),
	)
	if err != nil {
		return nil, err
	}

	if err = operatorConditionReconciler.SetupWithManager(mgr); err != nil {
		return nil, err
	}

	operatorConditionGeneratorReconciler, err := operators.NewOperatorConditionGeneratorReconciler(
		mgr.GetClient(),
		ctrl.Log.WithName("controllers").WithName("operatorcondition-generator"),
		mgr.GetScheme(),
	)
	if err != nil {
		return nil, err
	}

	if err = operatorConditionGeneratorReconciler.SetupWithManager(mgr); err != nil {
		return nil, err
	}

	if feature.Gate.Enabled(feature.OperatorLifecycleManagerV1) {
		// Setup a new controller to reconcile Operators
		operatorReconciler, err := operators.NewOperatorReconciler(
			mgr.GetClient(),
			ctrl.Log.WithName("controllers").WithName("operator"),
			mgr.GetScheme(),
		)
		if err != nil {
			return nil, err
		}

		if err = operatorReconciler.SetupWithManager(mgr); err != nil {
			return nil, err
		}

		adoptionReconciler, err := operators.NewAdoptionReconciler(
			mgr.GetClient(),
			ctrl.Log.WithName("controllers").WithName("adoption"),
			mgr.GetScheme(),
		)
		if err != nil {
			return nil, err
		}

		if err = adoptionReconciler.SetupWithManager(mgr); err != nil {
			return nil, err
		}
	}
	setupLog.Info("manager configured")

	return mgr, nil
}
