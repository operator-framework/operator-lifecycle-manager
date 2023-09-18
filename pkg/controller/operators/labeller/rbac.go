package labeller

import (
	"context"
	"fmt"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/ownerutil"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func hasHashLabel(obj metav1.Object) bool {
	_, ok := obj.GetLabels()[resolver.ContentHashLabelKey]
	return ok
}

func ContentHashLabeler[T metav1.Object, A ApplyConfig[A]](
	ctx context.Context,
	logger *logrus.Logger,
	check func(metav1.Object) bool,
	hasher func(object T) (string, error),
	applyConfigFor func(name, namespace string) A,
	apply func(namespace string, ctx context.Context, cfg A, opts metav1.ApplyOptions) (T, error),
) queueinformer.LegacySyncHandler {
	return func(obj interface{}) error {
		cast, ok := obj.(T)
		if !ok {
			err := fmt.Errorf("wrong type %T, expected %T: %#v", obj, new(T), obj)
			logger.WithError(err).Error("casting failed")
			return fmt.Errorf("casting failed: %w", err)
		}

		if _, _, ok := ownerutil.GetOwnerByKindLabel(cast, v1alpha1.ClusterServiceVersionKind); !ok {
			return nil
		}

		if !check(cast) || hasHashLabel(cast) {
			return nil
		}

		hash, err := hasher(cast)
		if err != nil {
			return fmt.Errorf("failed to calculate hash: %w", err)
		}

		cfg := applyConfigFor(cast.GetName(), cast.GetNamespace())
		cfg.WithLabels(map[string]string{
			resolver.ContentHashLabelKey: hash,
		})
		_, err = apply(cast.GetNamespace(), ctx, cfg, metav1.ApplyOptions{})
		return err
	}
}
