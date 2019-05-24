package operators

import (
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

type Builder interface {
	WithKubeconfig(kubeconfig string) Builder
	WithClock(clk clock.Clock) Builder
	WithNamespaces(namespaces ...string) Builder
	WithResyncPeriod(resyncPeriod time.Duration) Builder
	WithLogger(logger *logrus.Logger) Builder
	WithClient(cli versioned.Interface) Builder
	WithOperatorClient(opClient operatorclient.ClientInterface) Builder
	WithLister(lister operatorlister.OperatorLister) Builder
	Build() (*Operator, error)
}

type operatorBuilder struct {
	kubeconfig   string
	clock        clock.Clock
	namespaces   []string
	resyncPeriod time.Duration
	logger       *logrus.Logger
	client       versioned.Interface
	opClient     operatorclient.ClientInterface
	lister       operatorlister.OperatorLister
}

func NewBuilder() Builder {
	return &operatorBuilder{
		clock:        clock.RealClock{},
		namespaces:   []string{metav1.NamespaceAll},
		resyncPeriod: 2 * time.Hour,
		logger:       logrus.New(),
		lister:       operatorlister.NewLister(),
	}
}

func (o *operatorBuilder) Build() (*Operator, error) {
	// Set additional defaults if not set by options
	if o.client == nil {
		cli, err := client.NewClient(o.kubeconfig)
		if err != nil {
			return nil, err
		}

		o.client = cli
	}

	if o.opClient == nil {
		o.opClient = operatorclient.NewClientFromConfig(o.kubeconfig, o.logger)
	}

	queueOperator, err := queueinformer.NewOperatorFromClient(o.opClient, o.logger)
	if err != nil {
		return nil, err
	}

	op := &Operator{
		Operator:     queueOperator,
		clock:        o.clock,
		namespaces:   o.namespaces,
		resyncPeriod: o.resyncPeriod,
		Client:       o.client,
		Lister:       o.lister,
	}

	return op, nil
}

func (o *operatorBuilder) WithKubeconfig(kubeconfig string) Builder {
	o.kubeconfig = kubeconfig
	return o
}

func (o *operatorBuilder) WithClock(clk clock.Clock) Builder {
	o.clock = clk
	return o
}

func (o *operatorBuilder) WithNamespaces(namespaces ...string) Builder {
	if len(namespaces) > 0 {
		o.namespaces = namespaces
		return o
	}

	o.namespaces = []string{metav1.NamespaceAll}
	return o
}

func (o *operatorBuilder) WithResyncPeriod(resyncPeriod time.Duration) Builder {
	o.resyncPeriod = resyncPeriod
	return o
}

func (o *operatorBuilder) WithLogger(logger *logrus.Logger) Builder {
	o.logger = logger
	return o
}

func (o *operatorBuilder) WithClient(cli versioned.Interface) Builder {
	o.client = cli
	return o
}

func (o *operatorBuilder) WithOperatorClient(opClient operatorclient.ClientInterface) Builder {
	o.opClient = opClient
	return o
}

func (o *operatorBuilder) WithLister(lister operatorlister.OperatorLister) Builder {
	o.lister = lister
	return o
}
