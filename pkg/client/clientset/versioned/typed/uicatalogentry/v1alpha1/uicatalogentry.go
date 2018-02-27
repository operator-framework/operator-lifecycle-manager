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
package v1alpha1

import (
	v1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
	scheme "github.com/coreos-inc/alm/pkg/client/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// UICatalogEntriesGetter has a method to return a UICatalogEntryInterface.
// A group's client should implement this interface.
type UICatalogEntriesGetter interface {
	UICatalogEntries(namespace string) UICatalogEntryInterface
}

// UICatalogEntryInterface has methods to work with UICatalogEntry resources.
type UICatalogEntryInterface interface {
	Create(*v1alpha1.UICatalogEntry) (*v1alpha1.UICatalogEntry, error)
	Update(*v1alpha1.UICatalogEntry) (*v1alpha1.UICatalogEntry, error)
	UpdateStatus(*v1alpha1.UICatalogEntry) (*v1alpha1.UICatalogEntry, error)
	Delete(name string, options *v1.DeleteOptions) error
	DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error
	Get(name string, options v1.GetOptions) (*v1alpha1.UICatalogEntry, error)
	List(opts v1.ListOptions) (*v1alpha1.UICatalogEntryList, error)
	Watch(opts v1.ListOptions) (watch.Interface, error)
	Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.UICatalogEntry, err error)
	UICatalogEntryExpansion
}

// uICatalogEntries implements UICatalogEntryInterface
type uICatalogEntries struct {
	client rest.Interface
	ns     string
}

// newUICatalogEntries returns a UICatalogEntries
func newUICatalogEntries(c *UicatalogentryV1alpha1Client, namespace string) *uICatalogEntries {
	return &uICatalogEntries{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the uICatalogEntry, and returns the corresponding uICatalogEntry object, and an error if there is any.
func (c *uICatalogEntries) Get(name string, options v1.GetOptions) (result *v1alpha1.UICatalogEntry, err error) {
	result = &v1alpha1.UICatalogEntry{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of UICatalogEntries that match those selectors.
func (c *uICatalogEntries) List(opts v1.ListOptions) (result *v1alpha1.UICatalogEntryList, err error) {
	result = &v1alpha1.UICatalogEntryList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested uICatalogEntries.
func (c *uICatalogEntries) Watch(opts v1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch()
}

// Create takes the representation of a uICatalogEntry and creates it.  Returns the server's representation of the uICatalogEntry, and an error, if there is any.
func (c *uICatalogEntries) Create(uICatalogEntry *v1alpha1.UICatalogEntry) (result *v1alpha1.UICatalogEntry, err error) {
	result = &v1alpha1.UICatalogEntry{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		Body(uICatalogEntry).
		Do().
		Into(result)
	return
}

// Update takes the representation of a uICatalogEntry and updates it. Returns the server's representation of the uICatalogEntry, and an error, if there is any.
func (c *uICatalogEntries) Update(uICatalogEntry *v1alpha1.UICatalogEntry) (result *v1alpha1.UICatalogEntry, err error) {
	result = &v1alpha1.UICatalogEntry{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		Name(uICatalogEntry.Name).
		Body(uICatalogEntry).
		Do().
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().

func (c *uICatalogEntries) UpdateStatus(uICatalogEntry *v1alpha1.UICatalogEntry) (result *v1alpha1.UICatalogEntry, err error) {
	result = &v1alpha1.UICatalogEntry{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		Name(uICatalogEntry.Name).
		SubResource("status").
		Body(uICatalogEntry).
		Do().
		Into(result)
	return
}

// Delete takes name of the uICatalogEntry and deletes it. Returns an error if one occurs.
func (c *uICatalogEntries) Delete(name string, options *v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		Name(name).
		Body(options).
		Do().
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *uICatalogEntries) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		VersionedParams(&listOptions, scheme.ParameterCodec).
		Body(options).
		Do().
		Error()
}

// Patch applies the patch and returns the patched uICatalogEntry.
func (c *uICatalogEntries) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.UICatalogEntry, err error) {
	result = &v1alpha1.UICatalogEntry{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("uicatalogentry-v1s").
		SubResource(subresources...).
		Name(name).
		Body(data).
		Do().
		Into(result)
	return
}
