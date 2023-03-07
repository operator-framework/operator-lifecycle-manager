package util

import (
	"fmt"
	k8scontrollerclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sync"
)

type ResourceQueue struct {
	queue       []k8scontrollerclient.Object
	lookupTable map[k8scontrollerclient.Object]bool
	lock        *sync.Mutex
}

func NewResourceQueue() *ResourceQueue {
	return &ResourceQueue{
		queue:       []k8scontrollerclient.Object{},
		lookupTable: map[k8scontrollerclient.Object]bool{},
		lock:        &sync.Mutex{},
	}
}

func (q *ResourceQueue) Enqueue(obj k8scontrollerclient.Object) error {
	q.lock.Lock()
	defer q.lock.Unlock()

	if _, ok := q.lookupTable[obj]; ok {
		return fmt.Errorf("error inserting duplicate object: %s", obj)
	}
	q.queue = append(q.queue, obj)
	q.lookupTable[obj] = true
	return nil
}

func (q *ResourceQueue) EnqueueIgnoreExisting(obj k8scontrollerclient.Object) {
	q.lock.Lock()
	defer q.lock.Unlock()

	if _, ok := q.lookupTable[obj]; ok {
		return
	}
	q.queue = append(q.queue, obj)
	q.lookupTable[obj] = true
}

func (q *ResourceQueue) Length() int {
	return len(q.queue)
}

func (q *ResourceQueue) RemoveIgnoreNotFound(obj k8scontrollerclient.Object) {
	q.lock.Lock()
	defer q.lock.Unlock()

	if _, ok := q.lookupTable[obj]; ok {
		for index, existingObj := range q.queue {
			if q.equals(existingObj, obj) {
				_ = q.removeItem(index)
				delete(q.lookupTable, obj)
				return
			}
		}
	}
}

func (q *ResourceQueue) equals(objOne k8scontrollerclient.Object, objTwo k8scontrollerclient.Object) bool {
	return objOne.GetName() == objTwo.GetName() && objOne.GetNamespace() == objTwo.GetNamespace()
}

func (q *ResourceQueue) DequeueHead() (k8scontrollerclient.Object, bool) {
	q.lock.Lock()
	defer q.lock.Unlock()

	if q.Length() == 0 {
		return nil, false
	}
	return q.removeItem(0), true
}

func (q *ResourceQueue) DequeueTail() (k8scontrollerclient.Object, bool) {
	q.lock.Lock()
	defer q.lock.Unlock()

	if len(q.queue) == 0 {
		return nil, false
	}
	return q.removeItem(q.Length() - 1), true
}

func (q *ResourceQueue) removeItem(index int) k8scontrollerclient.Object {
	if index < 0 || index >= q.Length() {
		panic("index out of bounds")
	}
	item := q.queue[index]
	copy(q.queue[index:], q.queue[index+1:])
	q.queue[len(q.queue)-1] = nil
	q.queue = q.queue[:len(q.queue)-1]
	delete(q.lookupTable, item)

	return item
}
