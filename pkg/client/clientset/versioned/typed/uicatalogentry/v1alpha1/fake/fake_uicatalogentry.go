/*
Copyright 2018 The Kubernetes Authors.

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
	v1alpha1 "github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeUICatalogEntries implements UICatalogEntryInterface
type FakeUICatalogEntries struct {
	Fake *FakeUicatalogentryV1alpha1
	ns   string
}

var uicatalogentriesResource = schema.GroupVersionResource{Group: "uicatalogentry", Version: "v1alpha1", Resource: "uicatalogentries"}

var uicatalogentriesKind = schema.GroupVersionKind{Group: "uicatalogentry", Version: "v1alpha1", Kind: "UICatalogEntry"}

// Get takes name of the uICatalogEntry, and returns the corresponding uICatalogEntry object, and an error if there is any.
func (c *FakeUICatalogEntries) Get(name string, options v1.GetOptions) (result *v1alpha1.UICatalogEntry, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(uicatalogentriesResource, c.ns, name), &v1alpha1.UICatalogEntry{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.UICatalogEntry), err
}

// List takes label and field selectors, and returns the list of UICatalogEntries that match those selectors.
func (c *FakeUICatalogEntries) List(opts v1.ListOptions) (result *v1alpha1.UICatalogEntryList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(uicatalogentriesResource, uicatalogentriesKind, c.ns, opts), &v1alpha1.UICatalogEntryList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.UICatalogEntryList{}
	for _, item := range obj.(*v1alpha1.UICatalogEntryList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested uICatalogEntries.
func (c *FakeUICatalogEntries) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(uicatalogentriesResource, c.ns, opts))

}

// Create takes the representation of a uICatalogEntry and creates it.  Returns the server's representation of the uICatalogEntry, and an error, if there is any.
func (c *FakeUICatalogEntries) Create(uICatalogEntry *v1alpha1.UICatalogEntry) (result *v1alpha1.UICatalogEntry, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(uicatalogentriesResource, c.ns, uICatalogEntry), &v1alpha1.UICatalogEntry{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.UICatalogEntry), err
}

// Update takes the representation of a uICatalogEntry and updates it. Returns the server's representation of the uICatalogEntry, and an error, if there is any.
func (c *FakeUICatalogEntries) Update(uICatalogEntry *v1alpha1.UICatalogEntry) (result *v1alpha1.UICatalogEntry, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(uicatalogentriesResource, c.ns, uICatalogEntry), &v1alpha1.UICatalogEntry{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.UICatalogEntry), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeUICatalogEntries) UpdateStatus(uICatalogEntry *v1alpha1.UICatalogEntry) (*v1alpha1.UICatalogEntry, error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceAction(uicatalogentriesResource, "status", c.ns, uICatalogEntry), &v1alpha1.UICatalogEntry{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.UICatalogEntry), err
}

// Delete takes name of the uICatalogEntry and deletes it. Returns an error if one occurs.
func (c *FakeUICatalogEntries) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteAction(uicatalogentriesResource, c.ns, name), &v1alpha1.UICatalogEntry{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeUICatalogEntries) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(uicatalogentriesResource, c.ns, listOptions)

	_, err := c.Fake.Invokes(action, &v1alpha1.UICatalogEntryList{})
	return err
}

// Patch applies the patch and returns the patched uICatalogEntry.
func (c *FakeUICatalogEntries) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.UICatalogEntry, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(uicatalogentriesResource, c.ns, name, data, subresources...), &v1alpha1.UICatalogEntry{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.UICatalogEntry), err
}
