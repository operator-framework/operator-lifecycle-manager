package main

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/feature"
)

var log = ctrl.Log.WithName("manager")

func Manager(ctx context.Context, debug bool) (ctrl.Manager, error) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(debug)))
	setupLog := log.WithName("setup").V(4)

	// Setup a Manager
	setupLog.Info("configuring manager")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{MetricsBindAddress: "0"}) // TODO(njhale): Enable metrics on non-conflicting port (not 8080)
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

	// Setup a new controller to reconcile Operators
	setupLog.Info("configuring controller")
	if feature.Gate.Enabled(feature.OperatorLifecycleManagerV1) {
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
