package admission

import (
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/workqueue"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/permissions"
)

type rbacGenerationConfig struct {
	queue                   workqueue.RateLimitingInterface
	lister                  operatorlister.OperatorLister
	logger                  *logrus.Logger
	userPermissionValidator permissions.Validator
	permissionCreator       permissions.Creator
}

type RbacGenerationOption func(*rbacGenerationConfig)

// apply sequentially applies the given options to the config.
func (c *rbacGenerationConfig) apply(options []RbacGenerationOption) {
	for _, option := range options {
		option(c)
	}
}

func newInvalidRbacGenerationConfigError(msg string) error {
	return errors.Errorf("invalid rbac generator config: %s", msg)
}

// WithLogger sets the logger used by the RBAC generator.
func WithLogger(logger *logrus.Logger) RbacGenerationOption {
	return func(config *rbacGenerationConfig) {
		config.logger = logger
	}
}

// WithLister sets the lister used by the RBAC generator.
func WithLister(lister operatorlister.OperatorLister) RbacGenerationOption {
	return func(config *rbacGenerationConfig) {
		config.lister = lister
	}
}

// WithQueue sets the queue used by the RBAC generator.
func WithQueue(queue workqueue.RateLimitingInterface) RbacGenerationOption {
	return func(config *rbacGenerationConfig) {
		config.queue = queue
	}
}

// WithPermissionValidator sets the permission validator used by the RBAC generator.
func WithPermissionValidator(validator permissions.Validator) RbacGenerationOption {
	return func(config *rbacGenerationConfig) {
		config.userPermissionValidator = validator
	}
}

// WithPermissionCreator sets the permission validator used by the RBAC generator.
func WithPermissionCreator(creator permissions.Creator) RbacGenerationOption {
	return func(config *rbacGenerationConfig) {
		config.permissionCreator = creator
	}
}

// validate returns an error if the config isn't valid.
func (c *rbacGenerationConfig) validate() (err error) {
	switch config := c; {
	case config.lister == nil:
		err = newInvalidRbacGenerationConfigError("lister nil")
	case config.userPermissionValidator == nil:
		err = newInvalidRbacGenerationConfigError("validator nil")
	case config.permissionCreator == nil:
		err = newInvalidRbacGenerationConfigError("creator nil")
	}

	return
}

func defaultRbacGenerationConfig(lister operatorlister.OperatorLister, client kubernetes.Interface) *rbacGenerationConfig {
	return &rbacGenerationConfig{
		logger:                  logrus.New(),
		queue:                   workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "rbacgen"),
		lister:                  lister,
		userPermissionValidator: permissions.NewPermissionValidator(lister),
		permissionCreator:       permissions.NewPermissionCreator(client),
	}
}
