package plugins

import (
	"context"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	operatorsv1informers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions/operators/v1"
	operatorsv1alpha1informers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions/operators/v1alpha1"
	operatorsv2informers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions/operators/v2"
	operatorsv1alpha1listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/sirupsen/logrus"
	appsv1informers "k8s.io/client-go/informers/apps/v1"
	corev1informers "k8s.io/client-go/informers/core/v1"
	rbacv1informers "k8s.io/client-go/informers/rbac/v1"
	"k8s.io/client-go/metadata/metadatalister"
	"k8s.io/client-go/tools/cache"
	apiregistrationv1informers "k8s.io/kube-aggregator/pkg/client/informers/externalversions/apiregistration/v1"
)

// HostOperator is an extensible and observable operator that hosts the plug-in, i.e. which the plug-in is extending
type HostOperator interface {
	queueinformer.ObservableOperator
	queueinformer.ExtensibleOperator
	Informers() map[string]*Informers
}

// Informers exposes informer caches that the host operator has already started, for re-use by plugins.
type Informers struct {
	CSVInformer                operatorsv1alpha1informers.ClusterServiceVersionInformer
	CopiedCSVInformer          cache.SharedIndexInformer
	CopiedCSVLister            operatorsv1alpha1listers.ClusterServiceVersionLister
	OperatorGroupInformer      operatorsv1informers.OperatorGroupInformer
	OperatorConditionInformer  operatorsv2informers.OperatorConditionInformer
	SubscriptionInformer       operatorsv1alpha1informers.SubscriptionInformer
	DeploymentInformer         appsv1informers.DeploymentInformer
	RoleInformer               rbacv1informers.RoleInformer
	RoleBindingInformer        rbacv1informers.RoleBindingInformer
	SecretInformer             corev1informers.SecretInformer
	ServiceInformer            corev1informers.ServiceInformer
	ServiceAccountInformer     corev1informers.ServiceAccountInformer
	OLMConfigInformer          operatorsv1informers.OLMConfigInformer
	ClusterRoleInformer        rbacv1informers.ClusterRoleInformer
	ClusterRoleBindingInformer rbacv1informers.ClusterRoleBindingInformer
	NamespaceInformer          corev1informers.NamespaceInformer
	APIServiceInformer         apiregistrationv1informers.APIServiceInformer
	CRDInformer                cache.SharedIndexInformer
	CRDLister                  metadatalister.Lister
}

// OperatorConfig gives access to required configuration from the host operator
type OperatorConfig interface {
	OperatorClient() operatorclient.ClientInterface
	ExternalClient() versioned.Interface
	ResyncPeriod() func() time.Duration
	WatchedNamespaces() []string
	Logger() *logrus.Logger
}

// OperatorPlugin provides a simple interface
// that can be used to extend the olm operator's functionality
type OperatorPlugin interface {
	// Shutdown is called once the host operator is done
	// to give the plug-in a change to clean up resources if necessary
	Shutdown() error
}

// OperatorPlugInFactoryFunc factory function that returns a new instance of a plug-in
type OperatorPlugInFactoryFunc func(ctx context.Context, config OperatorConfig, hostOperator HostOperator) (OperatorPlugin, error)
