package main

import (
	"context"
	"fmt"

	"github.com/operator-framework/api/crds"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/feature"
)

var log = ctrl.Log.WithName("manager")

func Manager(ctx context.Context) (ctrl.Manager, error) {
	ctrl.SetLogger(zap.Logger(true))
	setupLog := log.WithName("setup").V(4)

	// Setup a Manager
	setupLog.Info("configuring manager")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{MetricsBindAddress: "0"}) // TODO(njhale): Enable metrics on non-conflicting port (not 8080)
	if err != nil {
		return nil, err
	}

	// Setup a new controller to reconcile Operators
	setupLog.Info("configuring controller")
	client, err := apiextensionsv1.NewForConfig(mgr.GetConfig())
	if err != nil {
		return nil, err
	}

	if feature.Gate.Enabled(feature.OperatorLifecycleManagerV2) {
		setupLog.Info(fmt.Sprintf("feature enabled: %v", feature.OperatorLifecycleManagerV2))

		reconciler, err := operators.NewOperatorReconciler(
			mgr.GetClient(),
			ctrl.Log.WithName("controllers").WithName("operator"),
			mgr.GetScheme(),
		)
		if err != nil {
			return nil, err
		}

		crd, err := client.CustomResourceDefinitions().Create(ctx, crds.Operator(), metav1.CreateOptions{})
		if err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return nil, err
			}

			// Already exists, try to update
			if crd, err = client.CustomResourceDefinitions().Get(ctx, crds.Operator().GetName(), metav1.GetOptions{}); err != nil {
				return nil, err
			}

			crd.Spec = crds.Operator().Spec
			if _, err = client.CustomResourceDefinitions().Update(ctx, crd, metav1.UpdateOptions{}); err != nil {
				return nil, err
			}
		}
		setupLog.Info("v2alpha1 CRDs installed")

		if err = reconciler.SetupWithManager(mgr); err != nil {
			return nil, err
		}
	}

	setupLog.Info("manager configured")

	return mgr, nil
}
