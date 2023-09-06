package labeller

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/internal/alongside"
)

func Filter(gvr schema.GroupVersionResource) func(metav1.Object) bool {
	if f, ok := filters[gvr]; ok {
		return f
	}
	return func(object metav1.Object) bool {
		return false
	}
}

func JobFilter(getConfigMap func(namespace, name string) (metav1.Object, error)) func(object metav1.Object) bool {
	return func(object metav1.Object) bool {
		for _, ownerRef := range object.GetOwnerReferences() {
			if ownerRef.APIVersion == corev1.SchemeGroupVersion.String() && ownerRef.Kind == "ConfigMap" {
				cm, err := getConfigMap(object.GetNamespace(), ownerRef.Name)
				if err != nil {
					return false
				}
				return HasOLMOwnerRef(cm)
			}
		}
		return false
	}
}

var filters = map[schema.GroupVersionResource]func(metav1.Object) bool{
	corev1.SchemeGroupVersion.WithResource("services"): HasOLMOwnerRef,
	corev1.SchemeGroupVersion.WithResource("pods"): func(object metav1.Object) bool {
		_, ok := object.GetLabels()[reconciler.CatalogSourceLabelKey]
		return ok
	},
	corev1.SchemeGroupVersion.WithResource("serviceaccounts"): func(object metav1.Object) bool {
		return HasOLMOwnerRef(object) || HasOLMLabel(object)
	},
	appsv1.SchemeGroupVersion.WithResource("deployments"):         HasOLMOwnerRef,
	rbacv1.SchemeGroupVersion.WithResource("roles"):               HasOLMOwnerRef,
	rbacv1.SchemeGroupVersion.WithResource("rolebindings"):        HasOLMOwnerRef,
	rbacv1.SchemeGroupVersion.WithResource("clusterroles"):        HasOLMOwnerRef,
	rbacv1.SchemeGroupVersion.WithResource("clusterrolebindings"): HasOLMOwnerRef,
	apiextensionsv1.SchemeGroupVersion.WithResource("customresourcedefinitions"): func(object metav1.Object) bool {
		for key := range object.GetAnnotations() {
			if strings.HasPrefix(key, alongside.AnnotationPrefix) {
				return true
			}
		}
		return false
	},
}

func Validate(ctx context.Context, logger *logrus.Logger, metadataClient metadata.Interface) (bool, error) {
	okLock := sync.Mutex{}
	var ok bool
	g, ctx := errgroup.WithContext(ctx)
	allFilters := map[schema.GroupVersionResource]func(metav1.Object) bool{}
	for gvr, filter := range filters {
		allFilters[gvr] = filter
	}
	allFilters[batchv1.SchemeGroupVersion.WithResource("jobs")] = JobFilter(func(namespace, name string) (metav1.Object, error) {
		return metadataClient.Resource(corev1.SchemeGroupVersion.WithResource("configmaps")).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	})
	for gvr, filter := range allFilters {
		gvr, filter := gvr, filter
		g.Go(func() error {
			list, err := metadataClient.Resource(gvr).List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("failed to list %s: %w", gvr.String(), err)
			}
			var count int
			for _, item := range list.Items {
				if filter(&item) && !hasLabel(&item) {
					count++
				}
			}
			if count > 0 {
				logger.WithFields(logrus.Fields{
					"gvr":           gvr.String(),
					"nonconforming": count,
				}).Info("found nonconforming items")
			}
			okLock.Lock()
			ok = ok && count == 0
			okLock.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return false, err
	}
	return ok, nil
}
