package admission

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/permissions"
)

const (
	MaxRequeues = 10
	NumWorkers  = 1
)

type CSVAdmissionRequest struct {
	Name      string
	Namespace string
	User      string
}

type RBACGenerationController struct {
	sync.Once
	queue                   workqueue.RateLimitingInterface
	lister                  operatorlister.OperatorLister
	logger                  *logrus.Logger
	userPermissionValidator permissions.Validator
	permissionCreator       permissions.Creator
}

// RBACGenerationController returns a new RBACGenerationController
func NewRBACGenerationController(lister operatorlister.OperatorLister, client kubernetes.Interface, options ...RbacGenerationOption) (*RBACGenerationController, error) {
	config := defaultRbacGenerationConfig(lister, client)
	config.apply(options)
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &RBACGenerationController{
		queue:                   config.queue,
		lister:                  config.lister,
		logger:                  config.logger,
		userPermissionValidator: config.userPermissionValidator,
		permissionCreator:       config.permissionCreator,
	}, nil
}

func (g *RBACGenerationController) Start(ctx context.Context) {
	g.logger = g.logger.WithField("stage", "rbacgen").Logger
	for w := 0; w < NumWorkers; w++ {
		go g.worker(ctx)
	}
}

func (g *RBACGenerationController) worker(ctx context.Context) {
	for g.processNextWorkItem(ctx) {
	}
}

func (g *RBACGenerationController) processNextWorkItem(ctx context.Context) bool {
	// TODO: context done?
	item, quit := g.queue.Get()

	if quit {
		return false
	}
	defer g.queue.Done(item)

	logger := g.logger.WithField("item", item)
	logger.WithField("queue-length", g.queue.Len()).Trace("popped queue")

	csvReq, ok := item.(CSVAdmissionRequest)
	if !ok {
		g.logger.Debugf("wrong type: %#v", item)
		return true
	}

	logger = logger.WithFields(logrus.Fields{
		"name":      csvReq.Name,
		"namespace": csvReq.Namespace,
		"user":      csvReq.User,
	})

	logger.Info("generating rbac for csv")

	csv, err := g.lister.OperatorsV1alpha1().ClusterServiceVersionLister().ClusterServiceVersions(csvReq.Namespace).Get(csvReq.Name)
	if err != nil {
		logger.Info("csv not found for rbac generation, requeue")
		g.requeue(csvReq)
		return true
	}

	if err := g.userPermissionValidator.UserCanCreateV1Alpha1CSV(csvReq.User, csv); err != nil {
		logger.Infof("user lacks permission to create CSV rbac permissions automatically: %s", err.Error())
		return true
	}

	operatorPermissions, err := resolver.RBACForClusterServiceVersion(csv)
	if err != nil {
		logger.Info("failed to get rbac from csv for generation")
		return true
	}

	if err := g.permissionCreator.FromOperatorPermissions(csv.GetNamespace(), operatorPermissions); err != nil {
		g.requeue(csvReq)
		return true
	}
	g.queue.Forget(item)

	return true
}

func (g *RBACGenerationController) requeue(item CSVAdmissionRequest) {
	if requeues := g.queue.NumRequeues(item); requeues < MaxRequeues {
		g.logger.WithField("requeues", requeues).Trace("requeuing with rate limiting")
		g.queue.AddRateLimited(item)
	}
}
