package planner

import (
	catalog "github.com/coreos-inc/alm/catalog"
	"github.com/coreos-inc/operator-client/pkg/client"
)

// InstallPlanController watches InstallPlanResolver resources and resolves it into a set of
// a set of installable resources
type InstallPlanController struct {
	client  client.Interface
	catalog catalog.Source
}
