package main

import (
	"fmt"
	"path/filepath"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operator"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/features"
)

var log = ctrl.Log.WithName("olm-manager")

func Manager() (ctrl.Manager, error) {
	ctrl.SetLogger(zap.Logger(true))
	setupLog := log.WithName("setup")

	// Setup a Manager
	setupLog.Info("configuring manager")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{MetricsBindAddress: "0"}) // TODO: Enable metrics on non-conflicting port (not 8080)
	if err != nil {
		return nil, err
	}

	// Setup a new controller to reconcile Operators
	setupLog.Info("configuring controller")

	if features.Gate.Enabled(features.OperatorLifecycleManagerV2) {
		setupLog.Info(fmt.Sprintf("feature enabled: %v", features.OperatorLifecycleManagerV2))

		_, err := envtest.InstallCRDs(mgr.GetConfig(), envtest.CRDInstallOptions{
			Paths:              []string{filepath.Join("..", "..", "config", "crd", "bases")},
			ErrorIfPathMissing: true,
		})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}

		setupLog.Info("v2alpha1 CRDs installed")

		if err := operator.AddController(mgr, log); err != nil {
			return nil, err
		}
	}

	setupLog.Info("manager configured")

	return mgr, nil
}
