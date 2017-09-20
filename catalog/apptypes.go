package alm

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/coreos-inc/operator-client/pkg/client"
	"github.com/coreos/go-semver/semver"
)

// 1. AppType Loop
//
//    a. Watches for new AppType defintions in a namespace
//       i. Finds the latestOperatorVersion for the AppType in the catalog.
//      ii. Creates the OperatorVersion in the namespace.

// 2. OperatorVersion Loop
//
//    a. Watches for pending OperatorVersion
//       i. If it has a requirement on a CRD that doesn't exist, looks it up and creates it in the
//          namespace

// 3. CRD loop
//
//    a. Watches CRDs for definitions that have ownerReference set to <ALM managed resource>
//       i. Queries catalog by (group, kind, apiVersion) for the AppType that lists an
//          OperatorVersion that has the CRD as a requirement.
//      ii. If the AppType does not exist in the cluster, it is created.

// 4. Catalog loop
//
//    a. Finds all OperatorVersions with a higher version and a replaces field that includes an
//       existing OperatorVersion's version.
//       i. If found, creates the new OperatorVersion in the namespace.

type Catalog interface {
	LatestOperatorVersionForApp(appname string) (*alm.OperatorVersion, error)
	FindOperatorVersionForApp(appname string, version semver.Version) (*alm.OperatorVersion, error)
	ListOperatorVersionsForApp(appType AppType) ([]alm.OperatorVersion, error)

	ListOperatorVersionsForCRD(crdname string) ([]alm.OperatorVersion, error)

	ListAppTypes(group, kind, apiVersion string) ([]alm.AppType, error)

	// TEMP
	ListRequiredCRDs(opver *alm.OperatorVersion) ([]string, error)
}

type AppController struct {
	catalog Catalog
	client  client.Interface
}

func (ctl *AppController) HandleNewAppType(apptype *alm.AppTypeResource) error {
	opver, err := ctl.catalog.LatestOperatorVersionForApp(apptype.ObjectMeta.Name)
	if err != nil {
		return err
	}
	err = ctl.client.CreateOrUpdateCustomResourceDefinition(opver) // TODO, obviously
	return err
}

func (ctl *AppController) HandlePendingOperatorVersion(oppver *alm.OperatorVersion) error {
	reqs, err := ctl.catalog.ListRequiredCRDs(oppver)
	if err != nil {
		return err
	}
	for _, crd := range reqs {
		// TODO get actual apiGroup/version/namespace from OperatorVersion
		crdlist, err := ct.client.ListCustomResourceDefinition("apiGroup", "version", "namespace", crd)
		if err != nil {
			return err // TODO less fragile error handling?
		}
		if len(crdlist.Items) < 1 {
			ctl.client.CreateCustomResourceDefinition(crd) // TODO broken, need to create unstructured obj
		}
	}
	return nil
}

func (ctl *AppController) HandleALMManagedCRD(crd *unstructured.Unstructured) error {
	opver, err := ctl.catalog.LatestOperatorVersionForApp(crd.ObjectMeta.Name)
	if err != nil {
		return err
	}
	err = ctl.client.CreateOrUpdateCustomeResourceDefinition(opver) // TODO, obviously
	return err
}
