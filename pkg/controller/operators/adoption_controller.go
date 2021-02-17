package operators

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/decorators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
)

// AdoptionReconciler automagically associates Operator components with their respective operator resource.
type AdoptionReconciler struct {
	client.Client

	log     logr.Logger
	mu      sync.RWMutex
	factory decorators.OperatorFactory
}

// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operators/status,verbs=update;patch
// +kubebuilder:rbac:groups=*,resources=*,verbs=get;list;watch

// SetupWithManager adds the operator reconciler to the given controller manager.
func (r *AdoptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Trigger operator events from the events of their compoenents.
	enqueueSub := handler.EnqueueRequestsFromMapFunc(r.mapToSubscriptions)

	// Create multiple controllers for resource types that require automatic adoption
	err := ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.Subscription{}).
		Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueueSub).
		Watches(&source.Kind{Type: &operatorsv1alpha1.InstallPlan{}}, enqueueSub).
		Complete(reconcile.Func(r.ReconcileSubscription))
	if err != nil {
		return err
	}

	var (
		enqueueCSV       = handler.EnqueueRequestsFromMapFunc(r.mapToClusterServiceVersions)
		enqueueProviders = handler.EnqueueRequestsFromMapFunc(r.mapToProviders)
	)
	err = ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.ClusterServiceVersion{}).
		Watches(&source.Kind{Type: &appsv1.Deployment{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.Namespace{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.Service{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.ServiceAccount{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.Secret{}}, enqueueCSV).
		Watches(&source.Kind{Type: &corev1.ConfigMap{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.Role{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.RoleBinding{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.ClusterRole{}}, enqueueCSV).
		Watches(&source.Kind{Type: &rbacv1.ClusterRoleBinding{}}, enqueueCSV).
		Watches(&source.Kind{Type: &apiextensionsv1.CustomResourceDefinition{}}, enqueueProviders).
		Watches(&source.Kind{Type: &apiregistrationv1.APIService{}}, enqueueCSV).
		Watches(&source.Kind{Type: &operatorsv1alpha1.Subscription{}}, enqueueCSV).
		Watches(&source.Kind{Type: &operatorsv1.OperatorCondition{}}, enqueueCSV).
		Complete(reconcile.Func(r.ReconcileClusterServiceVersion))
	if err != nil {
		return err
	}

	return nil
}

// NewAdoptionReconciler constructs and returns an AdoptionReconciler.
// As a side effect, the given scheme has operator discovery types added to it.
func NewAdoptionReconciler(cli client.Client, log logr.Logger, scheme *runtime.Scheme) (*AdoptionReconciler, error) {
	// Add watched types to scheme.
	if err := AddToScheme(scheme); err != nil {
		return nil, err
	}

	factory, err := decorators.NewSchemedOperatorFactory(scheme)
	if err != nil {
		return nil, err
	}

	return &AdoptionReconciler{
		Client: cli,

		log:     log,
		factory: factory,
	}, nil
}

// ReconcileSubscription labels the CSVs installed by a Subscription as components of an operator named after the subscribed package and install namespace.
func (r *AdoptionReconciler) ReconcileSubscription(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling subscription")

	// Fetch the Subscription from the cache
	in := &operatorsv1alpha1.Subscription{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Could not find Subscription")
		} else {
			log.Error(err, "Error finding Subscription")
		}

		return reconcile.Result{}, nil
	}

	// OLM generated Operators are named after their packages and further qualified by the install namespace
	if in.Spec == nil || in.Spec.Package == "" {
		log.Info("subscription spec missing package, ignoring")
		return reconcile.Result{}, nil
	}

	// Wrap with convenience decorator
	operator, err := r.factory.NewPackageOperator(in.Spec.Package, in.GetNamespace())
	if err != nil {
		log.Error(err, "Could not wrap Operator with convenience decorator")
		return reconcile.Result{}, nil
	}

	// Adopt the Subscription
	var errs []error
	if err := r.adopt(ctx, operator, in); err != nil {
		log.Error(err, "Error adopting Subscription")
		errs = append(errs, err)
	}

	// Adopt the Subscription's installed CSV
	if name := in.Status.InstalledCSV; name != "" {
		csv := &operatorsv1alpha1.ClusterServiceVersion{}
		csv.SetNamespace(in.GetNamespace())
		csv.SetName(name)
		if err := r.adopt(ctx, operator, csv); err != nil {
			log.Error(err, "Error adopting installed CSV")
			errs = append(errs, err)
		}
	}

	// Adopt the Subscription's latest InstallPlan and Disown all others in the same namespace
	if ref := in.Status.InstallPlanRef; ref != nil {
		ip := &operatorsv1alpha1.InstallPlan{}
		ip.SetNamespace(ref.Namespace)
		ip.SetName(ref.Name)
		if err := r.adoptInstallPlan(ctx, operator, ip); err != nil {
			errs = append(errs, err)
		}
	}

	return reconcile.Result{}, utilerrors.NewAggregate(errs)
}

// ReconcileClusterServiceVersion projects the component labels of a given CSV onto all resources owned by it.
func (r *AdoptionReconciler) ReconcileClusterServiceVersion(ctx context.Context, req ctrl.Request) (reconcile.Result, error) {
	// Set up a convenient log object so we don't have to type request over and over again
	log := r.log.WithValues("request", req)
	log.V(1).Info("reconciling csv")

	// Fetch the CSV from the cache
	in := &operatorsv1alpha1.ClusterServiceVersion{}
	if err := r.Get(ctx, req.NamespacedName, in); err != nil {
		if apierrors.IsNotFound(err) {
			err = nil
		} else {
			log.Error(err, "Error finding ClusterServiceVersion")
		}

		return reconcile.Result{}, err
	}

	// Adopt all resources owned by the CSV if necessary
	return reconcile.Result{}, r.adoptComponents(ctx, in)
}

func (r *AdoptionReconciler) adoptComponents(ctx context.Context, csv *operatorsv1alpha1.ClusterServiceVersion) error {
	if csv.IsCopied() {
		// For now, skip copied CSVs
		return nil
	}

	var operators []decorators.Operator
	for _, name := range decorators.OperatorNames(csv.GetLabels()) {
		o := &operatorsv1.Operator{}
		o.SetName(name.Name)
		operator, err := r.factory.NewOperator(o)
		if err != nil {
			return err
		}
		operators = append(operators, *operator)
	}

	if len(operators) < 1 {
		// No operators to adopt for
		return nil
	}

	// Label (adopt) prospective components
	var (
		errs []error
		mu   sync.Mutex
		wg   sync.WaitGroup
	)
	for _, operator := range operators {
		components, err := r.adoptees(ctx, operator, csv)
		if err != nil {
			func() {
				mu.Lock()
				defer mu.Unlock()
				errs = append(errs, err)
			}()
		}

		for _, component := range components {
			var (
				// Copy variables into iteration scope
				operator  = operator
				component = component
			)
			wg.Add(1)

			go func() {
				defer wg.Done()
				if err := r.adopt(ctx, &operator, component); err != nil {
					mu.Lock()
					defer mu.Unlock()
					errs = append(errs, err)
				}
			}()
		}
	}
	wg.Wait()

	return utilerrors.NewAggregate(errs)
}

func (r *AdoptionReconciler) adopt(ctx context.Context, operator *decorators.Operator, component runtime.Object) error {
	m, err := meta.Accessor(component)
	if err != nil {
		return nil
	}

	cObj, ok := component.(client.Object)
	if !ok {
		return fmt.Errorf("Unable to typecast runtime.Object to client.Object")
	}

	if err := r.Get(ctx, types.NamespacedName{Namespace: m.GetNamespace(), Name: m.GetName()}, cObj); err != nil {
		if apierrors.IsNotFound(err) {
			r.log.V(1).Info("not found", "component", cObj)
			err = nil
		}

		return err
	}
	candidate := cObj.DeepCopyObject()

	adopted, err := operator.AdoptComponent(candidate)
	if err != nil {
		return err
	}

	if adopted {
		// Only update if freshly adopted
		pCObj, ok := candidate.(client.Object)
		if !ok {
			return fmt.Errorf("Unable to typecast runtime.Object to client.Object")
		}
		return r.Patch(ctx, pCObj, client.MergeFrom(cObj))
	}

	return nil
}

func (r *AdoptionReconciler) disown(ctx context.Context, operator *decorators.Operator, component runtime.Object) error {
	cObj, ok := component.(client.Object)
	if !ok {
		return fmt.Errorf("Unable to typecast runtime.Object to client.Object")
	}
	candidate := component.DeepCopyObject()
	disowned, err := operator.DisownComponent(candidate)
	if err != nil {
		return err
	}

	if !disowned {
		// Wasn't a component
		return nil
	}

	// Only update if freshly disowned
	r.log.V(1).Info("component disowned", "component", candidate)
	uCObj, ok := candidate.(client.Object)
	if !ok {
		return fmt.Errorf("Unable to typecast runtime.Object to client.Object")
	}
	return r.Patch(ctx, uCObj, client.MergeFrom(cObj))
}

func (r *AdoptionReconciler) adoptees(ctx context.Context, operator decorators.Operator, csv *operatorsv1alpha1.ClusterServiceVersion) ([]runtime.Object, error) {
	// Note: We need to figure out how to dynamically add new list types here (or some equivalent) in
	// order to support operators composed of custom resources.
	componentLists := []runtime.Object{
		&appsv1.DeploymentList{},
		&corev1.ServiceList{},
		&corev1.NamespaceList{},
		&corev1.ServiceAccountList{},
		&corev1.SecretList{},
		&corev1.ConfigMapList{},
		&rbacv1.RoleList{},
		&rbacv1.RoleBindingList{},
		&rbacv1.ClusterRoleList{},
		&rbacv1.ClusterRoleBindingList{},
		&apiregistrationv1.APIServiceList{},
		&apiextensionsv1.CustomResourceDefinitionList{},
		&operatorsv1alpha1.SubscriptionList{},
		&operatorsv1alpha1.InstallPlanList{},
		&operatorsv1alpha1.ClusterServiceVersionList{},
		&operatorsv1.OperatorConditionList{},
	}

	// Only resources that aren't already labelled are adoption candidates
	selector, err := operator.NonComponentSelector()
	if err != nil {
		return nil, err
	}
	opt := client.MatchingLabelsSelector{Selector: selector}
	for _, list := range componentLists {
		cList, ok := list.(client.ObjectList)
		if !ok {
			return nil, fmt.Errorf("Unable to typecast runtime.Object to client.ObjectList")
		}
		if err := r.List(ctx, cList, opt); err != nil {
			return nil, err
		}
	}

	var (
		components []runtime.Object
		errs       []error
	)
	for _, candidate := range flatten(componentLists) {
		m, err := meta.Accessor(candidate)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if ownerutil.IsOwnedBy(m, csv) || ownerutil.IsOwnedByLabel(m, csv) {
			components = append(components, candidate)
		}
	}

	// Pick up owned CRDs
	for _, provided := range csv.Spec.CustomResourceDefinitions.Owned {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := r.Get(ctx, types.NamespacedName{Name: provided.Name}, crd); err != nil {
			if !apierrors.IsNotFound(err) {
				// Inform requeue on transient error
				errs = append(errs, err)
			}

			// Skip on transient error or missing CRD
			continue
		}

		if crd == nil || !selector.Matches(labels.Set(crd.GetLabels())) {
			// Skip empty and labelled CRDs
			continue
		}

		components = append(components, crd)
	}

	return components, utilerrors.NewAggregate(errs)
}

func (r *AdoptionReconciler) adoptInstallPlan(ctx context.Context, operator *decorators.Operator, latest *operatorsv1alpha1.InstallPlan) error {
	// Adopt the latest InstallPlan
	if err := r.adopt(ctx, operator, latest); err != nil {
		return err
	}

	// Disown older InstallPlans
	selector, err := operator.ComponentSelector()
	if err != nil {
		return err
	}

	var (
		ips = &operatorsv1alpha1.InstallPlanList{}
		opt = client.MatchingLabelsSelector{Selector: selector}
	)
	if err := r.List(ctx, ips, opt, client.InNamespace(latest.GetNamespace())); err != nil {
		return err
	}

	var errs []error
	for _, ip := range ips.Items {
		if ip.GetName() == latest.GetName() {
			// Don't disown latest
			continue
		}

		if err := r.disown(ctx, operator, &ip); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (r *AdoptionReconciler) mapToSubscriptions(obj client.Object) (requests []reconcile.Request) {
	if obj == nil {
		return
	}

	// Requeue all Subscriptions in the resource namespace
	// The Subscription reconciler will sort out the important changes
	ctx := context.TODO()
	subs := &operatorsv1alpha1.SubscriptionList{}
	if err := r.List(ctx, subs, client.InNamespace(obj.GetNamespace())); err != nil {
		r.log.Error(err, "error listing subscriptions")
	}

	visited := map[types.NamespacedName]struct{}{}
	for _, sub := range subs.Items {
		nsn := types.NamespacedName{Namespace: sub.GetNamespace(), Name: sub.GetName()}

		if _, ok := visited[nsn]; ok {
			// Already requested
			continue
		}

		requests = append(requests, reconcile.Request{NamespacedName: nsn})
		visited[nsn] = struct{}{}
	}

	return
}

func (r *AdoptionReconciler) mapToClusterServiceVersions(obj client.Object) (requests []reconcile.Request) {
	if obj == nil {
		return
	}

	// Get all owner CSV from owner labels if cluster scoped
	namespace := obj.GetNamespace()
	if namespace == metav1.NamespaceAll {
		name, ns, ok := ownerutil.GetOwnerByKindLabel(obj, operatorsv1alpha1.ClusterServiceVersionKind)
		if ok {
			nsn := types.NamespacedName{Namespace: ns, Name: name}
			requests = append(requests, reconcile.Request{NamespacedName: nsn})
		}
		return
	}

	// Get all owner CSVs from OwnerReferences
	owners := ownerutil.GetOwnersByKind(obj, operatorsv1alpha1.ClusterServiceVersionKind)
	for _, owner := range owners {
		nsn := types.NamespacedName{Namespace: namespace, Name: owner.Name}
		requests = append(requests, reconcile.Request{NamespacedName: nsn})
	}

	return
}

func (r *AdoptionReconciler) mapToProviders(obj client.Object) (requests []reconcile.Request) {
	if obj == nil {
		return nil
	}

	var (
		ctx  = context.TODO()
		csvs = &operatorsv1alpha1.ClusterServiceVersionList{}
	)
	if err := r.List(ctx, csvs); err != nil {
		r.log.Error(err, "error listing csvs")
		return
	}

	for _, csv := range csvs.Items {
		request := reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: csv.GetNamespace(), Name: csv.GetName()},
		}
		for _, provided := range csv.Spec.CustomResourceDefinitions.Owned {
			if provided.Name == obj.GetName() {
				requests = append(requests, request)
				break
			}
		}
	}

	return
}

func flatten(objs []runtime.Object) (flattened []runtime.Object) {
	for _, obj := range objs {
		if nested, err := meta.ExtractList(obj); err == nil {
			flattened = append(flattened, flatten(nested)...)
			continue
		}

		flattened = append(flattened, obj)
	}

	return
}
