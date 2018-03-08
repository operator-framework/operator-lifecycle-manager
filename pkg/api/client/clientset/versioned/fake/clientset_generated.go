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
package fake

import (
	clientset "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned"
	catalogsourcev1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/catalogsource/v1alpha1"
	fakecatalogsourcev1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/catalogsource/v1alpha1/fake"
	clusterserviceversionv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/clusterserviceversion/v1alpha1"
	fakeclusterserviceversionv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/clusterserviceversion/v1alpha1/fake"
	installplanv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/installplan/v1alpha1"
	fakeinstallplanv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/installplan/v1alpha1/fake"
	subscriptionv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/subscription/v1alpha1"
	fakesubscriptionv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/subscription/v1alpha1/fake"
	uicatalogentryv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/uicatalogentry/v1alpha1"
	fakeuicatalogentryv1alpha1 "github.com/coreos-inc/alm/pkg/api/client/clientset/versioned/typed/uicatalogentry/v1alpha1/fake"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/testing"
)

// NewSimpleClientset returns a clientset that will respond with the provided objects.
// It's backed by a very simple object tracker that processes creates, updates and deletions as-is,
// without applying any validations and/or defaults. It shouldn't be considered a replacement
// for a real clientset and is mostly useful in simple unit tests.
func NewSimpleClientset(objects ...runtime.Object) *Clientset {
	o := testing.NewObjectTracker(scheme, codecs.UniversalDecoder())
	for _, obj := range objects {
		if err := o.Add(obj); err != nil {
			panic(err)
		}
	}

	fakePtr := testing.Fake{}
	fakePtr.AddReactor("*", "*", testing.ObjectReaction(o))
	fakePtr.AddWatchReactor("*", testing.DefaultWatchReactor(watch.NewFake(), nil))

	return &Clientset{fakePtr, &fakediscovery.FakeDiscovery{Fake: &fakePtr}}
}

// Clientset implements clientset.Interface. Meant to be embedded into a
// struct to get a default implementation. This makes faking out just the method
// you want to test easier.
type Clientset struct {
	testing.Fake
	discovery *fakediscovery.FakeDiscovery
}

func (c *Clientset) Discovery() discovery.DiscoveryInterface {
	return c.discovery
}

var _ clientset.Interface = &Clientset{}

// CatalogsourceV1alpha1 retrieves the CatalogsourceV1alpha1Client
func (c *Clientset) CatalogsourceV1alpha1() catalogsourcev1alpha1.CatalogsourceV1alpha1Interface {
	return &fakecatalogsourcev1alpha1.FakeCatalogsourceV1alpha1{Fake: &c.Fake}
}

// Catalogsource retrieves the CatalogsourceV1alpha1Client
func (c *Clientset) Catalogsource() catalogsourcev1alpha1.CatalogsourceV1alpha1Interface {
	return &fakecatalogsourcev1alpha1.FakeCatalogsourceV1alpha1{Fake: &c.Fake}
}

// ClusterserviceversionV1alpha1 retrieves the ClusterserviceversionV1alpha1Client
func (c *Clientset) ClusterserviceversionV1alpha1() clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Interface {
	return &fakeclusterserviceversionv1alpha1.FakeClusterserviceversionV1alpha1{Fake: &c.Fake}
}

// Clusterserviceversion retrieves the ClusterserviceversionV1alpha1Client
func (c *Clientset) Clusterserviceversion() clusterserviceversionv1alpha1.ClusterserviceversionV1alpha1Interface {
	return &fakeclusterserviceversionv1alpha1.FakeClusterserviceversionV1alpha1{Fake: &c.Fake}
}

// InstallplanV1alpha1 retrieves the InstallplanV1alpha1Client
func (c *Clientset) InstallplanV1alpha1() installplanv1alpha1.InstallplanV1alpha1Interface {
	return &fakeinstallplanv1alpha1.FakeInstallplanV1alpha1{Fake: &c.Fake}
}

// Installplan retrieves the InstallplanV1alpha1Client
func (c *Clientset) Installplan() installplanv1alpha1.InstallplanV1alpha1Interface {
	return &fakeinstallplanv1alpha1.FakeInstallplanV1alpha1{Fake: &c.Fake}
}

// SubscriptionV1alpha1 retrieves the SubscriptionV1alpha1Client
func (c *Clientset) SubscriptionV1alpha1() subscriptionv1alpha1.SubscriptionV1alpha1Interface {
	return &fakesubscriptionv1alpha1.FakeSubscriptionV1alpha1{Fake: &c.Fake}
}

// Subscription retrieves the SubscriptionV1alpha1Client
func (c *Clientset) Subscription() subscriptionv1alpha1.SubscriptionV1alpha1Interface {
	return &fakesubscriptionv1alpha1.FakeSubscriptionV1alpha1{Fake: &c.Fake}
}

// UicatalogentryV1alpha1 retrieves the UicatalogentryV1alpha1Client
func (c *Clientset) UicatalogentryV1alpha1() uicatalogentryv1alpha1.UicatalogentryV1alpha1Interface {
	return &fakeuicatalogentryv1alpha1.FakeUicatalogentryV1alpha1{Fake: &c.Fake}
}

// Uicatalogentry retrieves the UicatalogentryV1alpha1Client
func (c *Clientset) Uicatalogentry() uicatalogentryv1alpha1.UicatalogentryV1alpha1Interface {
	return &fakeuicatalogentryv1alpha1.FakeUicatalogentryV1alpha1{Fake: &c.Fake}
}
