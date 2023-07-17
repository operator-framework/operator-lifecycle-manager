package plugins

import (
	"context"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/sirupsen/logrus"
)

// HostOperator is an extensible and observable operator that hosts the plug-in, i.e. which the plug-in is extending
type HostOperator interface {
	queueinformer.ObservableOperator
	queueinformer.ExtensibleOperator
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
