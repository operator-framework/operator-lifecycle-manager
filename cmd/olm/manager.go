package main

import (
	"context"
	"io/ioutil"
	"os"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	helmclient "github.com/operator-framework/helm-operator-plugins/pkg/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators"
	kuberpakv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/api/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/controllers"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/pkg/storage"
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
	if err := kuberpakv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	setupLog.Info("configuring manager")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0", // TODO(njhale): Enable metrics on non-conflicting port (not 8080)
		NewCache: cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&corev1.Secret{}: {
					Label: labels.SelectorFromValidatedSet(map[string]string{install.OLMManagedLabelKey: install.OLMManagedLabelValue}),
				},
				&operatorsv1alpha1.ClusterServiceVersion{}: {
					Label: copiedLabelDoesNotExist,
				},
			},
		}),
	})
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes client")
		os.Exit(1)
	}

	ns := "kuberpak-system"
	namespace, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		setupLog.Error(err, "failed to determine whether manager is running inside of a pod")
	} else {
		ns = string(namespace)
	}

	bundleStorage := &storage.ConfigMaps{
		Client:     mgr.GetClient(),
		Namespace:  ns,
		NamePrefix: "bundle-",
	}

	if err = (&controllers.BundleReconciler{
		Client:       mgr.GetClient(),
		KubeClient:   kubeClient,
		Scheme:       mgr.GetScheme(),
		PodNamespace: ns,
		Storage:      bundleStorage,
		// TODO: CLI flag
		UnpackImage: "quay.io/joelanford/kuberpak-unpack:v0.1.0",
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Bundle")
		os.Exit(1)
	}

	cfgGetter := helmclient.NewActionConfigGetter(mgr.GetConfig(), mgr.GetRESTMapper(), mgr.GetLogger())
	if err = (&controllers.BundleInstanceReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		BundleStorage:      bundleStorage,
		ReleaseNamespace:   ns,
		ActionClientGetter: helmclient.NewActionClientGetter(cfgGetter),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BundleInstance")
		os.Exit(1)
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
