package kubestate

import (
	"context"
	"fmt"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type State interface {
	isState()

	Terminal() bool
	// Add() AddedState
	// Update() UpdatedState
	// Delete() DeletedState
}

type ExistsState interface {
	State

	isExistsState()
}

type AddedState interface {
	ExistsState

	isAddedState()
}

type UpdatedState interface {
	ExistsState

	isUpdatedState()
}

type DoesNotExistState interface {
	State

	isDoesNotExistState()
}

type DeletedState interface {
	DoesNotExistState

	isDeletedState()
}

type state struct{}

func (s state) isState() {}

func (s state) Terminal() bool {
	// Not terminal by default
	return false
}

func (s state) Add() AddedState {
	return &addedState{
		ExistsState: &existsState{
			State: s,
		},
	}
}

func (s state) Update() UpdatedState {
	return &updatedState{
		ExistsState: &existsState{
			State: s,
		},
	}
}

func (s state) Delete() DeletedState {
	return &deletedState{
		DoesNotExistState: &doesNotExistState{
			State: s,
		},
	}
}

func NewState() State {
	return &state{}
}

type existsState struct {
	State
}

func (e existsState) isExistsState() {}

type addedState struct {
	ExistsState
}

func (a addedState) isAddedState() {}

type updatedState struct {
	ExistsState
}

func (u updatedState) isUpdatedState() {}

type doesNotExistState struct {
	State
}

func (d doesNotExistState) isDoesNotExistState() {}

type deletedState struct {
	DoesNotExistState
}

func (d deletedState) isDeletedState() {}

type Reconciler interface {
	Reconcile(ctx context.Context, in State) (out State, err error)
}

type ReconcilerFunc func(ctx context.Context, in State) (out State, err error)

func (r ReconcilerFunc) Reconcile(ctx context.Context, in State) (out State, err error) {
	return r(ctx, in)
}

type ReconcilerChain []Reconciler

func (r ReconcilerChain) Reconcile(ctx context.Context, in State) (out State, err error) {
	out = in
	for _, rec := range r {
		if out, err = rec.Reconcile(ctx, out); err != nil || out == nil || out.Terminal() {
			break
		}
	}

	return
}

// ResourceEventType tells an operator what kind of event has occurred on a given resource.
type ResourceEventType string

const (
	// ResourceAdded tells the operator that a given resources has been added.
	ResourceAdded ResourceEventType = "add"
	// ResourceUpdated tells the operator that a given resources has been updated.
	ResourceUpdated ResourceEventType = "update"
	// ResourceDeleted tells the operator that a given resources has been deleted.
	ResourceDeleted ResourceEventType = "delete"
)

type ResourceEvent interface {
	Type() ResourceEventType
	Resource() interface{}
	String() string
}

type resourceEvent struct {
	eventType ResourceEventType
	resource  interface{}
}

func (r resourceEvent) Type() ResourceEventType {
	return r.eventType
}

func (r resourceEvent) Resource() interface{} {
	return r.resource
}

func (r resourceEvent) String() string {
	key, err := cache.MetaNamespaceKeyFunc(r.resource)
	// should not happen as resources must be either cache.ExplicitKey or client.Object
	// and this should be enforced in NewResourceEvent
	if err != nil {
		panic("could not get resource key: " + err.Error())
	}
	return fmt.Sprintf("%s/%s", string(r.eventType), key)
}

func NewUpdateEvent(resource interface{}) ResourceEvent {
	return NewResourceEvent(ResourceUpdated, resource)
}

// NewResourceEvent creates a new resource event. The resource parameter must either be
// a client.Object, a string, or a cache.ExplicitKey. In case it is a string, it will be
// coerced to cache.ExplicitKey. This ensures that whether a reference (string/cache.ExplicitKey)
// or a resource, workqueue will treat the items in the same way and dedup appropriately.
// This behavior is guaranteed by the String() method, which will also ignore the type of event.
// I.e. Add/Update/Delete events for the same resource object or reference will be ded
func NewResourceEvent(eventType ResourceEventType, resource interface{}) ResourceEvent {
	// assert resource type
	// only accept cache.ExplicitKey or client.Objects
	switch r := resource.(type) {
	case string:
		resource = cache.ExplicitKey(r)
	case cache.ExplicitKey:
	case client.Object:
	default:
		panic(fmt.Sprintf("NewResourceEvent called with invalid resource type: %T", resource))
	}

	return resourceEvent{
		eventType: eventType,
		resource:  resource,
	}
}

type Notifier interface {
	Notify(event ResourceEvent)
}

type NotifyFunc func(event ResourceEvent)

// SyncFunc syncs resource events.
type SyncFunc func(ctx context.Context, event ResourceEvent) error

// Sync lets a sync func implement Syncer.
func (s SyncFunc) Sync(ctx context.Context, event ResourceEvent) error {
	return s(ctx, event)
}

// Syncer describes something that syncs resource events.
type Syncer interface {
	Sync(ctx context.Context, event ResourceEvent) error
}
