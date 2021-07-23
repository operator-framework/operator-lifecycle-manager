package catalogtempate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/distribution/distribution/reference"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/informers/externalversions"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/catalogsource"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

const (
	StatusTypeTemplatesHaveResolved = "TemplatesHaveResolved"
	StatusTypeResolvedImage         = "ResolvedImage"

	ReasonUnableToResolve      = "UnableToResolve"
	ReasonAllTemplatesResolved = "AllTemplatesResolved"
)

type Operator struct {
	queueinformer.Operator
	logger                        *logrus.Logger                               // common logger
	namespace                     string                                       // operator namespace
	client                        versioned.Interface                          // client used for OLM CRs
	dynamicClient                 dynamic.Interface                            // client used to dynamically discover resources
	dynamicInformerFactory        dynamicinformer.DynamicSharedInformerFactory // factory to create shared informers for dynamic resources
	discoveryClient               *discovery.DiscoveryClient                   // queries the API server to discover resources
	mapper                        *restmapper.DeferredDiscoveryRESTMapper      // maps between GVK and GVR
	lister                        operatorlister.OperatorLister                // union of versioned informer listers
	catalogSourceTemplateQueueSet *queueinformer.ResourceQueueSet              // work queues for a catalog source update
	resyncPeriod                  func() time.Duration                         // period of time between resync
	dynamicResourceWatchesMap     sync.Map                                     // map to keep track of what GVR we've already opened watches for
	ctx                           context.Context                              // context used for shutting down
}

func NewOperator(ctx context.Context, kubeconfigPath string, logger *logrus.Logger, resync time.Duration, operatorNamespace string) (*Operator, error) {
	resyncPeriod := queueinformer.ResyncWithJitter(resync, 0.2)

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	// Create a new client for OLM types (CRs)
	crClient, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Create a new client for dynamic types
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	// Create a new queueinformer-based operator.
	opClient, err := operatorclient.NewClientFromRestConfig(config)
	if err != nil {
		return nil, err
	}

	queueOperator, err := queueinformer.NewOperator(opClient.KubernetesInterface().Discovery(), queueinformer.WithOperatorLogger(logger))
	if err != nil {
		return nil, err
	}

	// DiscoveryClient queries the API server to discover resources
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))

	// Create an OperatorLister
	lister := operatorlister.NewLister()

	op := &Operator{
		Operator:                      queueOperator,
		logger:                        logger,
		namespace:                     operatorNamespace,
		client:                        crClient,
		dynamicClient:                 dynamicClient,
		dynamicInformerFactory:        dynamicinformer.NewDynamicSharedInformerFactory(dynamicClient, resyncPeriod()),
		discoveryClient:               discoveryClient,
		mapper:                        mapper,
		lister:                        lister,
		catalogSourceTemplateQueueSet: queueinformer.NewEmptyResourceQueueSet(),
		resyncPeriod:                  resyncPeriod,
		// dynamicResourceWatchesMap:     map[string]struct{}{},
		ctx: ctx,
	}

	// Wire OLM CR sharedIndexInformers
	crInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(op.client, op.resyncPeriod())

	// Wire CatalogSources
	catsrcInformer := crInformerFactory.Operators().V1alpha1().CatalogSources()
	op.lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catsrcInformer.Lister())
	catalogTemplateSrcQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "catalogSourceTemplate")
	op.catalogSourceTemplateQueueSet.Set(metav1.NamespaceAll, catalogTemplateSrcQueue)
	catsrcQueueInformer, err := queueinformer.NewQueueInformer(
		op.ctx,
		// TODO: commented out sections I don't think are necessary
		// queueinformer.WithMetricsProvider(metrics.NewMetricsCatalogSource(op.lister.OperatorsV1alpha1().CatalogSourceLister())),
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(catalogTemplateSrcQueue),
		queueinformer.WithInformer(catsrcInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncCatalogSources).ToSyncer()), // ToSyncerWithDelete(op.handleCatSrcDeletion)), TODO do we need to handle deletion specially?
	)
	if err != nil {
		return nil, err
	}
	if err := op.RegisterQueueInformer(catsrcQueueInformer); err != nil {
		return nil, err
	}

	return op, nil
}

func (o *Operator) syncCatalogSources(obj interface{}) error {
	// this is an opportunity to update the server version (regardless of any other actions for processing a catalog source)
	o.updateServerVersion()

	inputCatalogSource, ok := obj.(*v1alpha1.CatalogSource)
	if !ok {
		o.logger.Debugf("wrong type: %#v", obj)
		return fmt.Errorf("casting CatalogSource failed")
	}

	outputCatalogSource := inputCatalogSource.DeepCopy()

	logger := o.logger.WithFields(logrus.Fields{
		"catSrcName": outputCatalogSource.GetName(),
		"id":         queueinformer.NewLoopID(),
	})
	logger.Info("syncing catalog source for annotation templates")

	// this is our opportunity to discover GVK templates and setup watchers (if possible)
	foundGVKs := catalogsource.InitializeCatalogSourceTemplates(outputCatalogSource)

	///////
	// FIXME: There have been concerns about security of allowing selection of arbitrary
	// GVK during code reviews. For now we're disabling setting up dynamic watchers. This
	// code needs to be re-enabled once we've come up with a valid approach
	_ = foundGVKs
	// for _, gvk := range foundGVKs {
	// 	o.processGVK(o.ctx, logger, gvk.GroupVersionKind)
	// }
	///////

	catalogImageTemplate := catalogsource.GetCatalogTemplateAnnotation(outputCatalogSource)
	if catalogImageTemplate == "" {
		logger.Debug("this catalog source is not participating in template replacement")
		// make sure the conditions are removed
		catalogsource.RemoveStatusConditions(logger, o.client, outputCatalogSource, StatusTypeTemplatesHaveResolved, StatusTypeResolvedImage)
		// no further action is needed
		return nil
	}

	processedCatalogImageTemplate, unresolvedTemplates := catalogsource.ReplaceTemplates(catalogImageTemplate)

	templatesAreResolved := len(unresolvedTemplates) == 0
	// curly braces are not allowed in a real image reference (see https://github.com/distribution/distribution/blob/2461543d988979529609e8cb6fca9ca190dc48da/reference/reference.go#L4-L24)
	// so we need to check for this... bottom line, we want to ensure that the result is valid
	_, err := reference.Parse(processedCatalogImageTemplate)
	invalidSyntax := err != nil

	// make sure everything was resolved and valid
	if templatesAreResolved && !invalidSyntax {
		// all templates have been resolved

		namespace := outputCatalogSource.GetNamespace()

		conditions := []metav1.Condition{
			{
				Type:    StatusTypeTemplatesHaveResolved,
				Status:  metav1.ConditionTrue,
				Reason:  ReasonAllTemplatesResolved,
				Message: "catalog image reference was successfully resolved",
			},
			{
				Type:    StatusTypeResolvedImage,
				Status:  metav1.ConditionTrue,
				Reason:  ReasonAllTemplatesResolved,
				Message: processedCatalogImageTemplate,
			},
		}

		// make sure that the processed image reference is actually different
		if outputCatalogSource.Spec.Image != processedCatalogImageTemplate {

			outputCatalogSource.Spec.Image = processedCatalogImageTemplate

			catalogsource.UpdateImageReferenceAndStatusCondition(logger, o.client, outputCatalogSource, conditions...)
			logger.Infof("The catalog image for catalog source %q within namespace %q image has been updated to %q", outputCatalogSource.GetName(), namespace, processedCatalogImageTemplate)
		} else {
			catalogsource.UpdateStatusCondition(logger, o.client, outputCatalogSource, conditions...)
			logger.Infof("The catalog image for catalog source %q within namespace %q image does not require an update because the image has not changed", outputCatalogSource.GetName(), namespace)
		}
	} else {
		// at least one template was unresolved because:
		// - either we explicitly discovered a template without a valid value (as listed in unresolvedTemplates)
		// - or because a template curly brace was found (which means the user screwed up the syntax)
		// so update status accordingly

		// quote the values and use comma separator
		quotedTemplates := fmt.Sprintf(`"%s"`, strings.Join(unresolvedTemplates, `", "`))

		// init to sensible generic message
		message := "cannot construct catalog image reference"
		if invalidSyntax && !templatesAreResolved {
			message = fmt.Sprintf("cannot construct catalog image reference, because variable(s) %s could not be resolved and one or more template(s) has improper syntax", quotedTemplates)
		} else if invalidSyntax {
			message = "cannot construct catalog image reference, because one or more template(s) has improper syntax"
		} else if !templatesAreResolved {
			message = fmt.Sprintf("cannot construct catalog image reference, because variable(s) %s could not be resolved", quotedTemplates)
		}
		catalogsource.UpdateStatusCondition(logger, o.client, outputCatalogSource,
			metav1.Condition{
				Type:    StatusTypeTemplatesHaveResolved,
				Status:  metav1.ConditionFalse,
				Reason:  ReasonUnableToResolve,
				Message: message,
			},
			metav1.Condition{
				Type:    StatusTypeResolvedImage,
				Status:  metav1.ConditionFalse,
				Reason:  ReasonUnableToResolve,
				Message: processedCatalogImageTemplate,
			},
		)
		logger.Infof(message)
	}

	return nil
}

// processGVK sets up a watcher for the GVK provided (if possible). Errors are logged but not returned
func (o *Operator) processGVK(ctx context.Context, logger *logrus.Entry, gvk schema.GroupVersionKind) {
	if gvk.Empty() {
		logger.Warn("provided GVK is empty, unable to add watch")
		return
	}
	// setup a watcher for the GVK

	mapping, err := o.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		logger.WithError(err).Warnf("unable to obtain preferred rest mapping for GVK %s", gvk.String())
		return
	}

	// see if we already setup a watcher for this resource
	if _, ok := o.dynamicResourceWatchesMap.Load(mapping.Resource); !ok {
		// we've not come across this resource before so setup a watcher
		informer := o.dynamicInformerFactory.ForResource(mapping.Resource)
		informer.Informer().AddEventHandlerWithResyncPeriod(o.eventHandlers(ctx, o.processDynamicWatches), o.resyncPeriod())
		go informer.Informer().Run(ctx.Done())
		o.dynamicResourceWatchesMap.Store(mapping.Resource, struct{}{})
	}
}

// eventHandlers is a generic handler that forwards all calls to provided notify function
func (o *Operator) eventHandlers(ctx context.Context, notify func(ctx context.Context, obj interface{})) cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			notify(ctx, obj)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			notify(ctx, newObj)
		},
		DeleteFunc: func(obj interface{}) {
			notify(ctx, obj)
		},
	}
}

func (o *Operator) processDynamicWatches(ctx context.Context, obj interface{}) {
	// this is an opportunity to update the server version (regardless of any other actions for processing a dynamic watch)
	o.updateServerVersion()

	if u, ok := obj.(*unstructured.Unstructured); ok {
		catalogsource.UpdateGVKValue(u, o.logger)
	} else {
		o.logger.Warn("object provided to processDynamicWatches was not unstructured.Unstructured type")
	}

}

func (o *Operator) updateServerVersion() {
	if serverVersion, err := o.discoveryClient.ServerVersion(); err != nil {
		o.logger.WithError(err).Warn("unable to obtain server version from discovery client")
	} else {
		catalogsource.UpdateKubeVersion(serverVersion, o.logger)
	}
}
