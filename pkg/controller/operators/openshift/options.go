package openshift

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
)

type NowFunc func() metav1.Time

type ReconcilerConfig struct {
	Client       client.Client
	Scheme       *runtime.Scheme
	Log          logr.Logger
	RequeueDelay time.Duration
	TweakBuilder func(*builder.Builder) *builder.Builder
	Now          NowFunc

	Name           string
	Namespace      string
	SyncCh         <-chan error
	TargetVersions []configv1.OperandVersion

	Mutator Mutator
}

type ReconcilerOption func(*ReconcilerConfig)

func (c *ReconcilerConfig) apply(opts []ReconcilerOption) {
	for _, opt := range opts {
		opt(c)
	}
}

func (c *ReconcilerConfig) complete() error {
	if c.Client == nil {
		return fmt.Errorf("No client specified")
	}
	if c.Name == "" {
		return fmt.Errorf("No ClusterOperator name specified")
	}
	if c.Log == nil {
		c.Log = ctrl.Log.WithName(c.Name)
	}
	if c.Now == nil {
		c.Now = NowFunc(metav1.Now)
	}
	if c.Scheme == nil {
		c.Scheme = runtime.NewScheme()
	}
	if c.RequeueDelay == 0 {
		c.RequeueDelay = time.Second * 5
	}
	if err := AddToScheme(c.Scheme); err != nil {
		return err
	}

	if len(c.TargetVersions) < 1 {
		c.TargetVersions = []configv1.OperandVersion{
			{
				Name:    "operator",
				Version: os.Getenv("RELEASE_VERSION"),
			},
			{
				Name:    c.Name,
				Version: olmversion.OLMVersion,
			},
		}
	}

	return nil
}

func (c *ReconcilerConfig) mapClusterOperator(_ client.Object) []reconcile.Request {
	// Enqueue the cluster operator
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: c.Name}},
	}
}

func WithClient(cli client.Client) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Client = cli
	}
}

func WithScheme(scheme *runtime.Scheme) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Scheme = scheme
	}
}

func WithName(name string) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Name = name
	}
}

func WithNamespace(namespace string) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Namespace = namespace
	}
}

func WithSyncChannel(syncCh <-chan error) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.SyncCh = syncCh
	}
}

func WithNow(now NowFunc) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Now = now
	}
}

func WithLog(log logr.Logger) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.Log = log
	}
}

func WithTargetVersions(targetVersions ...configv1.OperandVersion) ReconcilerOption {
	return func(config *ReconcilerConfig) {
		config.TargetVersions = targetVersions
	}
}

func WithOLMOperator() ReconcilerOption {
	return func(config *ReconcilerConfig) {
		var mutations SerialMutations
		if config.Mutator != nil {
			mutations = append(mutations, config.Mutator)
		}

		mutations = append(mutations, MutateFunc(func(ctx context.Context, co *ClusterOperator) error {
			refs, err := olmOperatorRelatedObjects(ctx, config.Client, config.Namespace)
			if len(refs) > 0 {
				// Set any refs we found, regardless of any errors encountered (best effort)
				co.Status.RelatedObjects = refs
			}

			return err
		}))
		config.Mutator = mutations

		enqueue := handler.EnqueueRequestsFromMapFunc(config.mapClusterOperator)

		name := "version"
		originalCSV := predicate.NewPredicateFuncs(func(obj client.Object) bool {
			csv, ok := obj.(*operatorsv1alpha1.ClusterServiceVersion)
			if !ok {
				// Not a CSV, throw out
				return false
			}

			return !csv.IsCopied() // Keep original CSVs only
		})
		config.TweakBuilder = func(bldr *builder.Builder) *builder.Builder {
			return bldr.Watches(&source.Kind{Type: &operatorsv1alpha1.ClusterServiceVersion{}}, enqueue, builder.WithPredicates(originalCSV)).
				Watches(&source.Kind{Type: &configv1.ClusterVersion{}}, enqueue, builder.WithPredicates(watchName(&name)))
		}
	}
}
