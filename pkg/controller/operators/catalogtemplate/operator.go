package catalogtemplate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
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
	logger                        *logrus.Logger                  // common logger
	namespace                     string                          // operator namespace
	client                        versioned.Interface             // client used for OLM CRs
	dynamicClient                 dynamic.Interface               // client used to dynamically discover resources
	discoveryClient               *discovery.DiscoveryClient      // queries the API server to discover resources
	lister                        operatorlister.OperatorLister   // union of versioned informer listers
	catalogSourceTemplateQueueSet *queueinformer.ResourceQueueSet // work queues for a catalog source update
	resyncPeriod                  func() time.Duration            // period of time between resync
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

	// Create an OperatorLister
	lister := operatorlister.NewLister()

	op := &Operator{
		Operator:                      queueOperator,
		logger:                        logger,
		namespace:                     operatorNamespace,
		client:                        crClient,
		dynamicClient:                 dynamicClient,
		discoveryClient:               discoveryClient,
		lister:                        lister,
		catalogSourceTemplateQueueSet: queueinformer.NewEmptyResourceQueueSet(),
		resyncPeriod:                  resyncPeriod,
	}

	// Wire OLM CR sharedIndexInformers
	crInformerFactory := externalversions.NewSharedInformerFactoryWithOptions(op.client, op.resyncPeriod())

	// Wire CatalogSources
	catsrcInformer := crInformerFactory.Operators().V1alpha1().CatalogSources()
	op.lister.OperatorsV1alpha1().RegisterCatalogSourceLister(metav1.NamespaceAll, catsrcInformer.Lister())
	catalogTemplateSrcQueue := workqueue.NewTypedRateLimitingQueueWithConfig[types.NamespacedName](workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
		workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{
			Name: "catalogSourceTemplate",
		})
	op.catalogSourceTemplateQueueSet.Set(metav1.NamespaceAll, catalogTemplateSrcQueue)
	catsrcQueueInformer, err := queueinformer.NewQueueInformer(
		ctx,
		queueinformer.WithLogger(op.logger),
		queueinformer.WithQueue(catalogTemplateSrcQueue),
		queueinformer.WithInformer(catsrcInformer.Informer()),
		queueinformer.WithSyncer(queueinformer.LegacySyncHandler(op.syncCatalogSources).ToSyncer()),
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
		return nil
	}

	outputCatalogSource := inputCatalogSource.DeepCopy()

	logger := o.logger.WithFields(logrus.Fields{
		"catSrcNamespace": outputCatalogSource.GetNamespace(),
		"catSrcName":      outputCatalogSource.GetName(),
		"id":              queueinformer.NewLoopID(),
	})
	logger.Debug("syncing catalog source for annotation templates")

	catalogImageTemplate := catalogsource.GetCatalogTemplateAnnotation(outputCatalogSource)
	if catalogImageTemplate == "" {
		logger.Debug("this catalog source is not participating in template replacement")
		// make sure the conditions are removed
		return catalogsource.RemoveStatusConditions(logger, o.client, outputCatalogSource, StatusTypeTemplatesHaveResolved, StatusTypeResolvedImage)
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

			if err := catalogsource.UpdateSpecAndStatusConditions(logger, o.client, outputCatalogSource, conditions...); err != nil {
				return err
			}
			logger.Infof("The catalog image has been updated to %q", processedCatalogImageTemplate)
		} else {
			if err := catalogsource.UpdateStatusWithConditions(logger, o.client, outputCatalogSource, conditions...); err != nil {
				return err
			}
			logger.Infof("The catalog image %q does not require an update because the image has not changed", processedCatalogImageTemplate)
		}
		return nil
	}

	// if we get here, at least one template was unresolved because:
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
	err = catalogsource.UpdateStatusWithConditions(logger, o.client, outputCatalogSource,
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
	if err != nil {
		return err
	}

	logger.Info(message)

	return nil
}

func (o *Operator) updateServerVersion() {
	if serverVersion, err := o.discoveryClient.ServerVersion(); err != nil {
		o.logger.WithError(err).Warn("unable to obtain server version from discovery client")
	} else {
		catalogsource.UpdateKubeVersion(serverVersion, o.logger)
	}
}
