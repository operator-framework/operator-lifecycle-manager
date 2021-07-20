/*
Copyright 2021 The Kubernetes Authors.

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

package internal

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

func metadataSharedIndexInformerPreserveGVK(gvk schema.GroupVersionKind, si cache.SharedIndexInformer) cache.SharedIndexInformer {
	return &sharedInformerWrapper{
		gvk:                 gvk,
		SharedIndexInformer: si,
	}
}

type sharedInformerWrapper struct {
	gvk schema.GroupVersionKind
	cache.SharedIndexInformer
}

func (s *sharedInformerWrapper) AddEventHandler(handler cache.ResourceEventHandler) {
	s.SharedIndexInformer.AddEventHandler(&handlerPreserveGVK{s.gvk, handler})
}

func (s *sharedInformerWrapper) AddEventHandlerWithResyncPeriod(handler cache.ResourceEventHandler, resyncPeriod time.Duration) {
	s.SharedIndexInformer.AddEventHandlerWithResyncPeriod(&handlerPreserveGVK{s.gvk, handler}, resyncPeriod)
}

type handlerPreserveGVK struct {
	gvk schema.GroupVersionKind
	cache.ResourceEventHandler
}

func (h *handlerPreserveGVK) copyWithGVK(obj interface{}) interface{} {
	switch t := obj.(type) {
	case *metav1.PartialObjectMetadata:
		return &metav1.PartialObjectMetadata{
			TypeMeta: metav1.TypeMeta{
				APIVersion: h.gvk.GroupVersion().String(),
				Kind:       h.gvk.Kind,
			},
			ObjectMeta: t.ObjectMeta,
		}
	case *metav1.PartialObjectMetadataList:
		return &metav1.PartialObjectMetadataList{
			TypeMeta: metav1.TypeMeta{
				APIVersion: h.gvk.GroupVersion().String(),
				Kind:       h.gvk.Kind,
			},
			ListMeta: t.ListMeta,
			Items:    t.Items,
		}
	default:
		return obj
	}

}

func (h *handlerPreserveGVK) OnAdd(obj interface{}) {
	h.ResourceEventHandler.OnAdd(h.copyWithGVK(obj))
}

func (h *handlerPreserveGVK) OnUpdate(oldObj, newObj interface{}) {
	h.ResourceEventHandler.OnUpdate(h.copyWithGVK(oldObj), h.copyWithGVK(newObj))
}

func (h *handlerPreserveGVK) OnDelete(obj interface{}) {
	h.ResourceEventHandler.OnDelete(h.copyWithGVK(obj))
}
