package main

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operator"
)

var log = ctrl.Log.WithName("olm-manager")

func Manager() (ctrl.Manager, error) {
	ctrl.SetLogger(zap.Logger(true))
	setupLog := log.WithName("setup")

	// Setup a Manager
	setupLog.Info("configuring manager")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{Scheme: runtime.NewScheme(), MetricsBindAddress: "0"}) // TODO: Enable metrics on non-conflicting port (not 8080)
	if err != nil {
		return nil, err
	}

	// Setup a new controller to reconcile Operators
	setupLog.Info("configuring controller")
	if err := operator.AddController(mgr, log); err != nil {
		return nil, err
	}

	setupLog.Info("manager configured")

	return mgr, nil
}
