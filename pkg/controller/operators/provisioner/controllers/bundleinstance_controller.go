/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	helmclient "github.com/operator-framework/helm-operator-plugins/pkg/client"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sigs.k8s.io/yaml"

	olmv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/api/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/pkg/convert"
	helmpredicate "github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/pkg/helm-operator-plugins/predicate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/provisioner/pkg/storage"
)

// BundleInstanceReconciler reconciles a BundleInstance object
type BundleInstanceReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Controller controller.Controller

	ActionClientGetter helmclient.ActionClientGetter
	BundleStorage      storage.Storage
	ReleaseNamespace   string

	dynamicWatchMutex sync.RWMutex
	dynamicWatchGVKs  map[schema.GroupVersionKind]struct{}
}

//+kubebuilder:rbac:groups=olm.operatorframework.io,resources=bundleinstances,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=olm.operatorframework.io,resources=bundleinstances/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=olm.operatorframework.io,resources=bundleinstances/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=operators.coreos.com,resources=operatorgroups,verbs=get;list;watch
//+kubebuilder:rbac:groups=*,resources=*,verbs=*

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the BundleInstance object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.9.2/pkg/reconcile
func (r *BundleInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)
	l.V(1).Info("starting reconciliation")
	defer l.V(1).Info("ending reconciliation")

	bi := &olmv1alpha1.BundleInstance{}
	if err := r.Get(ctx, req.NamespacedName, bi); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	defer func() {
		bi := bi.DeepCopy()
		bi.ObjectMeta.ManagedFields = nil
		if err := r.Status().Patch(ctx, bi, client.Apply, client.FieldOwner("kuberpak.io/registry+v1")); err != nil {
			l.Error(err, "failed to patch status")
		}
	}()

	b := &olmv1alpha1.Bundle{}
	if err := r.Get(ctx, types.NamespacedName{Name: bi.Spec.BundleName}, b); err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    "Installed",
			Status:  metav1.ConditionFalse,
			Reason:  "BundleLookupFailed",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	reg, err := r.loadBundle(ctx, bi)
	if err != nil {
		var bnuErr *errBundleNotUnpacked
		if errors.As(err, &bnuErr) {
			reason := fmt.Sprintf("BundleUnpack%s", b.Status.Phase)
			if b.Status.Phase == olmv1alpha1.PhaseUnpacking {
				reason = "BundleUnpackRunning"
			}
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:   "Installed",
				Status: metav1.ConditionFalse,
				Reason: reason,
			})
			return ctrl.Result{}, nil
		} else {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "BundleLookupFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	}

	installNamespace := fmt.Sprintf("%s-system", b.Status.Info.Package)
	if ns, ok := bi.Annotations["kuberpak.io/install-namespace"]; ok && ns != "" {
		installNamespace = ns
	} else if ns, ok := reg.CSV.Annotations["operatorframework.io/suggested-namespace"]; ok && ns != "" {
		installNamespace = ns
	}

	og, err := r.getOperatorGroup(ctx, installNamespace)
	if err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    "Installed",
			Status:  metav1.ConditionFalse,
			Reason:  "OperatorGroupLookupFailed",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}
	if og.Status.LastUpdated == nil {
		err := errors.New("target naemspaces unknown")
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    "Installed",
			Status:  metav1.ConditionFalse,
			Reason:  "OperatorGroupNotReady",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}
	desiredObjects, err := r.getDesiredObjects(*reg, installNamespace, og.Status.Namespaces)
	if err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    "Installed",
			Status:  metav1.ConditionFalse,
			Reason:  "BundleLookupFailed",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	chrt := &chart.Chart{
		Metadata: &chart.Metadata{},
	}
	for _, obj := range desiredObjects {
		jsonData, err := yaml.Marshal(obj)
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "BundleLookupFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
		hash := sha256.Sum256(jsonData)
		chrt.Templates = append(chrt.Templates, &chart.File{
			Name: fmt.Sprintf("object-%x.yaml", hash[0:8]),
			Data: jsonData,
		})
	}

	bi.SetNamespace(r.ReleaseNamespace)
	cl, err := r.ActionClientGetter.ActionClientFor(bi)
	bi.SetNamespace("")
	if err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    "Installed",
			Status:  metav1.ConditionFalse,
			Reason:  "ErrorGettingClient",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	rel, state, err := r.getReleaseState(cl, bi, chrt)
	if err != nil {
		meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
			Type:    "Installed",
			Status:  metav1.ConditionFalse,
			Reason:  "ErrorGettingReleaseState",
			Message: err.Error(),
		})
		return ctrl.Result{}, err
	}

	switch state {
	case stateNeedsInstall:
		rel, err = cl.Install(bi.Name, r.ReleaseNamespace, chrt, nil, func(install *action.Install) error {
			install.CreateNamespace = false
			return nil
		})
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "InstallFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	case stateNeedsUpgrade:
		rel, err = cl.Upgrade(bi.Name, r.ReleaseNamespace, chrt, nil)
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "UpgradeFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	case stateUnchanged:
		if err := cl.Reconcile(rel); err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "ReconcileFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	default:
		return ctrl.Result{}, fmt.Errorf("unexpected release state %q", state)
	}

	for _, obj := range desiredObjects {
		uMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "CreateDynamicWatchFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}

		u := &unstructured.Unstructured{Object: uMap}
		if err := func() error {
			r.dynamicWatchMutex.Lock()
			defer r.dynamicWatchMutex.Unlock()

			_, isWatched := r.dynamicWatchGVKs[u.GroupVersionKind()]
			if !isWatched {
				if err := r.Controller.Watch(
					&source.Kind{Type: u},
					&handler.EnqueueRequestForOwner{OwnerType: bi, IsController: true},
					helmpredicate.DependentPredicateFuncs()); err != nil {
					return err
				}
				r.dynamicWatchGVKs[u.GroupVersionKind()] = struct{}{}
			}
			return nil
		}(); err != nil {
			meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
				Type:    "Installed",
				Status:  metav1.ConditionFalse,
				Reason:  "CreateDynamicWatchFailed",
				Message: err.Error(),
			})
			return ctrl.Result{}, err
		}
	}
	meta.SetStatusCondition(&bi.Status.Conditions, metav1.Condition{
		Type:   "Installed",
		Status: metav1.ConditionTrue,
		Reason: "InstallationSucceeded",
	})
	bi.Status.InstalledBundleName = bi.Spec.BundleName
	return ctrl.Result{}, nil
}

type releaseState string

const (
	stateNeedsInstall releaseState = "NeedsInstall"
	stateNeedsUpgrade releaseState = "NeedsUpgrade"
	stateUnchanged    releaseState = "Unchanged"
	stateError        releaseState = "Error"
)

func (r *BundleInstanceReconciler) getOperatorGroup(ctx context.Context, installNamespace string) (*v1.OperatorGroup, error) {
	ogs := v1.OperatorGroupList{}
	if err := r.List(ctx, &ogs, client.InNamespace(installNamespace)); err != nil {
		return nil, err
	}
	switch len(ogs.Items) {
	case 0:
		return nil, fmt.Errorf("no operator group found in install namespace %q", installNamespace)
	case 1:
		return &ogs.Items[0], nil
	default:
		return nil, fmt.Errorf("multiple operator groups found in install namespace")
	}
}

func (r *BundleInstanceReconciler) getReleaseState(cl helmclient.ActionInterface, obj metav1.Object, chrt *chart.Chart) (*release.Release, releaseState, error) {
	currentRelease, err := cl.Get(obj.GetName())
	if err != nil && !errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, stateError, err
	}
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, stateNeedsInstall, nil
	}
	desiredRelease, err := cl.Upgrade(obj.GetName(), r.ReleaseNamespace, chrt, nil, func(upgrade *action.Upgrade) error {
		upgrade.DryRun = true
		return nil
	})
	if err != nil {
		return currentRelease, stateError, err
	}
	if desiredRelease.Manifest != currentRelease.Manifest ||
		currentRelease.Info.Status == release.StatusFailed ||
		currentRelease.Info.Status == release.StatusSuperseded {
		return currentRelease, stateNeedsUpgrade, nil
	}
	return currentRelease, stateUnchanged, nil
}

type errBundleNotUnpacked struct {
	currentPhase string
}

func (err errBundleNotUnpacked) Error() string {
	const baseError = "bundle is not yet unpacked"
	if err.currentPhase == "" {
		return baseError
	}
	return fmt.Sprintf("%s, current phase=%s", baseError, err.currentPhase)
}

func (r *BundleInstanceReconciler) loadBundle(ctx context.Context, bi *olmv1alpha1.BundleInstance) (*convert.RegistryV1, error) {
	b := &olmv1alpha1.Bundle{}
	if err := r.Get(ctx, types.NamespacedName{Name: bi.Spec.BundleName}, b); err != nil {
		return nil, fmt.Errorf("get bundle %q: %v", bi.Spec.BundleName, err)
	}
	if b.Status.Phase != olmv1alpha1.PhaseUnpacked {
		return nil, &errBundleNotUnpacked{currentPhase: b.Status.Phase}
	}

	objects, err := r.BundleStorage.Load(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("load bundle objects: %v", err)
	}

	reg := convert.RegistryV1{}
	for _, obj := range objects {
		obj := obj
		obj.SetLabels(map[string]string{
			"kuberpak.io/owner-name": bi.Name,
		})
		switch obj.GetObjectKind().GroupVersionKind().Kind {
		case "ClusterServiceVersion":
			csv := v1alpha1.ClusterServiceVersion{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &csv); err != nil {
				return nil, err
			}
			reg.CSV = csv
		case "CustomResourceDefinition":
			crd := apiextensionsv1.CustomResourceDefinition{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &crd); err != nil {
				return nil, err
			}
			reg.CRDs = append(reg.CRDs, crd)
		default:
			reg.Others = append(reg.Others, obj)
		}
	}
	return &reg, nil
}

func (r *BundleInstanceReconciler) getDesiredObjects(reg convert.RegistryV1, installNamespace string, watchNamespaces []string) ([]client.Object, error) {
	reg.CSV.Namespace = installNamespace
	plain, err := convert.Convert(reg, installNamespace, watchNamespaces)
	if err != nil {
		return nil, err
	}
	return append(plain.Objects, &reg.CSV), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	//r.ActionConfigGetter = helmclient.NewActionConfigGetter(mgr.GetConfig(), mgr.GetRESTMapper(), mgr.GetLogger())
	//r.ActionClientGetter = helmclient.NewActionClientGetter(r.ActionConfigGetter)
	controller, err := ctrl.NewControllerManagedBy(mgr).
		For(&olmv1alpha1.BundleInstance{}, builder.WithPredicates(bundleInstanceProvisionerFilter("kuberpak.io/registry+v1"))).
		Watches(&source.Kind{Type: &olmv1alpha1.Bundle{}}, handler.EnqueueRequestsFromMapFunc(mapBundleToBundleInstanceHandler(mgr.GetClient(), mgr.GetLogger()))).
		Build(r)
	if err != nil {
		return err
	}
	r.Controller = controller
	r.dynamicWatchGVKs = map[schema.GroupVersionKind]struct{}{}
	return nil
}

func bundleInstanceProvisionerFilter(provisionerClassName string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		b := obj.(*olmv1alpha1.BundleInstance)
		return b.Spec.ProvisionerClassName == provisionerClassName
	})
}

func mapBundleToBundleInstanceHandler(cl client.Client, log logr.Logger) handler.MapFunc {
	return func(object client.Object) []reconcile.Request {
		b := object.(*olmv1alpha1.Bundle)
		bundleInstances := &olmv1alpha1.BundleInstanceList{}
		var requests []reconcile.Request
		if err := cl.List(context.Background(), bundleInstances); err != nil {
			log.WithName("mapBundleToBundleInstanceHandler").Error(err, "list bundles")
			return requests
		}
		for _, bi := range bundleInstances.Items {
			bi := bi
			if bi.Spec.BundleName == b.Name {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&bi)})
			}
		}
		return requests
	}
}
