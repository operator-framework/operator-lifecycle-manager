package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	v1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/available-csvs/apis/available/v1alpha1"
)

const (
	targetNamespacesField = ".metadata.annotations.olm.targetNamespaces"
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

type AvailabilityCache struct {
	cache cache.Cache
}

// AvailableCSVProvider returns which CSVs are available based on namespace and operatorgroup scope.
type AvailableCSVProvider struct {
	log logr.Logger

	cache cache.Cache
	start manager.RunnableFunc
}

// Implement Provider interface so that the AvailableCSVProvider can be used as apiserver storage
var _ Interface = &AvailableCSVProvider{}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &AvailableCSVProvider{}

// NewAvailableCSVProvider
func NewAvailableCSVProvider(log logr.Logger) (*AvailableCSVProvider, error) {
	p := &AvailableCSVProvider{
		log: log,
	}
	scheme := runtime.NewScheme()
	if err := available.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := operatorsv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		NewCache: cache.BuilderWithOptions(cache.Options{
			SelectorsByObject: cache.SelectorsByObject{
				&operatorsv1alpha1.ClusterServiceVersion{}: {
					Label: copiedLabelDoesNotExist,
				},
			},
		}),
	})

	if err != nil {
		return nil, err
	}
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&operatorsv1alpha1.ClusterServiceVersion{}).
		Complete(p); err != nil {
		return nil, err
	}
	if err := mgr.GetCache().IndexField(context.TODO(), &operatorsv1alpha1.ClusterServiceVersion{}, targetNamespacesField, func(o client.Object) []string {
		l := log.WithValues("object", o)
		annotations := o.GetAnnotations()
		if annotations == nil {
			return nil
		}

		targetNamespaces, ok := annotations[v1.OperatorGroupTargetsAnnotationKey]
		if !ok {
			l.V(1).V(3).Info("missing target namespaces annotation on csv")
			return nil
		}
		return strings.Split(targetNamespaces, ",")
	}); err != nil {
		return nil, err
	}
	if err := mgr.GetCache().IndexField(context.TODO(), &operatorsv1alpha1.ClusterServiceVersion{}, ".metadata.name", func(o client.Object) []string {
		return []string{o.GetName()}
	}); err != nil {
		return nil, err
	}

	p.cache = mgr.GetCache()
	p.start = mgr.Start
	return p, nil
}

// TODO: should we even bother?
func (r *AvailableCSVProvider) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log.WithValues("request", req)
	log.V(1).Info("updating availability cache")
	return reconcile.Result{}, nil
}

// Run starts the provider's informers and reconcilers
func (p *AvailableCSVProvider) Run(ctx context.Context) error {
	return p.start(ctx)
}

func (p *AvailableCSVProvider) Get(namespace, name string) (*available.AvailableClusterServiceVersion, error) {
	var virtualCSVsInNamespace operatorsv1alpha1.ClusterServiceVersionList
	if err := p.cache.List(context.Background(), &virtualCSVsInNamespace, client.MatchingFields{targetNamespacesField: namespace}, client.MatchingFields{".metadata.name": name}); err != nil {
		p.log.V(1).Info(err.Error())
		return nil, err
	}
	var allNamespaceCSVs operatorsv1alpha1.ClusterServiceVersionList
	if err := p.cache.List(context.Background(), &allNamespaceCSVs, client.MatchingFields{targetNamespacesField: ""}, client.MatchingFields{".metadata.name": name}); err != nil {
		p.log.V(1).Info(err.Error())
		return nil, err
	}
	virtualCSVsInNamespace.Items = append(virtualCSVsInNamespace.Items, allNamespaceCSVs.Items...)
	if len(virtualCSVsInNamespace.Items) != 1 {
		p.log.V(1).Info("expected 1 matching csv, found %d: %v", len(virtualCSVsInNamespace.Items), virtualCSVsInNamespace)
		return nil, fmt.Errorf("not found")
	}
	csv := virtualCSVsInNamespace.Items[0]
	return &available.AvailableClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.AvailableClusterServiceVersionKind,
			APIVersion: v1alpha1.Group,
		},
		ObjectMeta: csv.ObjectMeta,
		Spec:       available.AvailableClusterServiceVersionSpec{ClusterServiceVersionSpec: csv.Spec},
		Status:     available.AvailableClusterServiceVersionStatus{ClusterServiceVersionStatus: csv.Status},
	}, nil

}

// TODO: selector support
func (p *AvailableCSVProvider) List(namespace string, selector labels.Selector) (*available.AvailableClusterServiceVersionList, error) {
	var virtualCSVsInNamespace operatorsv1alpha1.ClusterServiceVersionList
	if err := p.cache.List(context.Background(), &virtualCSVsInNamespace, client.MatchingFields{targetNamespacesField: namespace}); err != nil {
		return nil, err
	}
	var allNamespaceCSVs operatorsv1alpha1.ClusterServiceVersionList
	if err := p.cache.List(context.Background(), &allNamespaceCSVs, client.MatchingFields{targetNamespacesField: ""}); err != nil {
		p.log.V(1).Info(err.Error())
		return nil, err
	}
	virtualCSVsInNamespace.Items = append(virtualCSVsInNamespace.Items, allNamespaceCSVs.Items...)

	var out []available.AvailableClusterServiceVersion

	for _, c := range virtualCSVsInNamespace.Items {
		out = append(out, available.AvailableClusterServiceVersion{
			TypeMeta: metav1.TypeMeta{
				Kind:       v1alpha1.AvailableClusterServiceVersionKind,
				APIVersion: v1alpha1.Group,
			},
			ObjectMeta: c.ObjectMeta,
			Spec:       available.AvailableClusterServiceVersionSpec{ClusterServiceVersionSpec: c.Spec},
			Status:     available.AvailableClusterServiceVersionStatus{ClusterServiceVersionStatus:c.Status},
		})
	}

	return &available.AvailableClusterServiceVersionList{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.AvailableClusterServiceVersionListKind,
			APIVersion: v1alpha1.Group,
		},
		Items: out,
	}, nil
}
