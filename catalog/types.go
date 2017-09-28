package catalog

import (
	appcache "github.com/coreos-inc/alm/appcache"
	"github.com/coreos-inc/operator-client/pkg/client"
)

// InstallPlanController watches InstallPlanResolver resources and resolves it into a set of
// a set of installable resources
type InstallPlanController struct {
	client   client.Interface
	AppCache appcache.AppCache
}
