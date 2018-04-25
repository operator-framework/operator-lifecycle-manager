/*
Copyright 2018 CoreOS, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package versioned

import (
	catalogsourcev1alpha1 "github.com/coreos/alm/pkg/api/client/clientset/versioned/typed/catalogsource/v1alpha1"
	clusterserviceversionv1alpha1 "github.com/coreos/alm/pkg/api/client/clientset/versioned/typed/clusterserviceversion/v1alpha1"
	installplanv1alpha1 "github.com/coreos/alm/pkg/api/client/clientset/versioned/typed/installplan/v1alpha1"
	subscriptionv1alpha1 "github.com/coreos/alm/pkg/api/client/clientset/versioned/typed/subscription/v1alpha1"
	glog "github.com/golang/glog"
	discovery "k8s.io/client-go/discovery"
	rest "k8s.io/client-go/rest"
	flowcontrol "k8s.io/client-go/util/flowcontrol"
)

type Interface interface {
	Discovery() discovery.DiscoveryInterface
	CatalogsourceV1alpha1() catalogsourcev1alpha1.CatalogsourceV1alpha1Interface
	// Deprecated: please explicitly pick a version if possible.
	Catalogsource() catalogsourcev1alpha1.CatalogsourceV1alpha1Interface
	ClusterserviceversionV1alpha1() clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Interface
	// Deprecated: please explicitly pick a version if possible.
	Clusterserviceversion() clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Interface
	InstallplanV1alpha1() installplanv1alpha1.InstallplanV1alpha1Interface
	// Deprecated: please explicitly pick a version if possible.
	Installplan() installplanv1alpha1.InstallplanV1alpha1Interface
	SubscriptionV1alpha1() subscriptionv1alpha1.SubscriptionV1alpha1Interface
	// Deprecated: please explicitly pick a version if possible.
	Subscription() subscriptionv1alpha1.SubscriptionV1alpha1Interface
}

// Clientset contains the clients for groups. Each group has exactly one
// version included in a Clientset.
type Clientset struct {
	*discovery.DiscoveryClient
	catalogsourceV1alpha1         *catalogsourcev1alpha1.CatalogsourceV1alpha1Client
	clusterserviceversionV1alpha1 *clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Client
	installplanV1alpha1           *installplanv1alpha1.InstallplanV1alpha1Client
	subscriptionV1alpha1          *subscriptionv1alpha1.SubscriptionV1alpha1Client
}

// CatalogsourceV1alpha1 retrieves the CatalogsourceV1alpha1Client
func (c *Clientset) CatalogsourceV1alpha1() catalogsourcev1alpha1.CatalogsourceV1alpha1Interface {
	return c.catalogsourceV1alpha1
}

// Deprecated: Catalogsource retrieves the default version of CatalogsourceClient.
// Please explicitly pick a version.
func (c *Clientset) Catalogsource() catalogsourcev1alpha1.CatalogsourceV1alpha1Interface {
	return c.catalogsourceV1alpha1
}

// ClusterserviceversionV1alpha1 retrieves the ClusterserviceversionV1alpha1Client
func (c *Clientset) ClusterserviceversionV1alpha1() clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Interface {
	return c.clusterserviceversionV1alpha1
}

// Deprecated: Clusterserviceversion retrieves the default version of ClusterserviceversionClient.
// Please explicitly pick a version.
func (c *Clientset) Clusterserviceversion() clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Interface {
	return c.clusterserviceversionV1alpha1
}

// InstallplanV1alpha1 retrieves the InstallplanV1alpha1Client
func (c *Clientset) InstallplanV1alpha1() installplanv1alpha1.InstallplanV1alpha1Interface {
	return c.installplanV1alpha1
}

// Deprecated: Installplan retrieves the default version of InstallplanClient.
// Please explicitly pick a version.
func (c *Clientset) Installplan() installplanv1alpha1.InstallplanV1alpha1Interface {
	return c.installplanV1alpha1
}

// SubscriptionV1alpha1 retrieves the SubscriptionV1alpha1Client
func (c *Clientset) SubscriptionV1alpha1() subscriptionv1alpha1.SubscriptionV1alpha1Interface {
	return c.subscriptionV1alpha1
}

// Deprecated: Subscription retrieves the default version of SubscriptionClient.
// Please explicitly pick a version.
func (c *Clientset) Subscription() subscriptionv1alpha1.SubscriptionV1alpha1Interface {
	return c.subscriptionV1alpha1
}

// Discovery retrieves the DiscoveryClient
func (c *Clientset) Discovery() discovery.DiscoveryInterface {
	if c == nil {
		return nil
	}
	return c.DiscoveryClient
}

// NewForConfig creates a new Clientset for the given config.
func NewForConfig(c *rest.Config) (*Clientset, error) {
	configShallowCopy := *c
	if configShallowCopy.RateLimiter == nil && configShallowCopy.QPS > 0 {
		configShallowCopy.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(configShallowCopy.QPS, configShallowCopy.Burst)
	}
	var cs Clientset
	var err error
	cs.catalogsourceV1alpha1, err = catalogsourcev1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.clusterserviceversionV1alpha1, err = clusterserviceversionv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.installplanV1alpha1, err = installplanv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}
	cs.subscriptionV1alpha1, err = subscriptionv1alpha1.NewForConfig(&configShallowCopy)
	if err != nil {
		return nil, err
	}

	cs.DiscoveryClient, err = discovery.NewDiscoveryClientForConfig(&configShallowCopy)
	if err != nil {
		glog.Errorf("failed to create the DiscoveryClient: %v", err)
		return nil, err
	}
	return &cs, nil
}

// NewForConfigOrDie creates a new Clientset for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *Clientset {
	var cs Clientset
	cs.catalogsourceV1alpha1 = catalogsourcev1alpha1.NewForConfigOrDie(c)
	cs.clusterserviceversionV1alpha1 = clusterserviceversionv1alpha1.NewForConfigOrDie(c)
	cs.installplanV1alpha1 = installplanv1alpha1.NewForConfigOrDie(c)
	cs.subscriptionV1alpha1 = subscriptionv1alpha1.NewForConfigOrDie(c)

	cs.DiscoveryClient = discovery.NewDiscoveryClientForConfigOrDie(c)
	return &cs
}

// New creates a new Clientset for the given RESTClient.
func New(c rest.Interface) *Clientset {
	var cs Clientset
	cs.catalogsourceV1alpha1 = catalogsourcev1alpha1.New(c)
	cs.clusterserviceversionV1alpha1 = clusterserviceversionv1alpha1.New(c)
	cs.installplanV1alpha1 = installplanv1alpha1.New(c)
	cs.subscriptionV1alpha1 = subscriptionv1alpha1.New(c)

	cs.DiscoveryClient = discovery.NewDiscoveryClient(c)
	return &cs
}
