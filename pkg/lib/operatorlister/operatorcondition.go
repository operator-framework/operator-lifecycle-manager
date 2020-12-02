package operatorlister

import (
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	listers "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1"
)

type UnionOperatorConditionLister struct {
	opConditionListers map[string]listers.OperatorConditionLister
	opConditionLock    sync.RWMutex
}

// List lists all OperatorConditions in the indexer.
func (uol *UnionOperatorConditionLister) List(selector labels.Selector) (ret []*v1.OperatorConditionOperatorCondition, err error) {
	uol.csvLock.RLock()
	defer uol.csvLock.RUnlock()

	set := make(map[types.UID]*v1.OperatorCondition)
	for _, cl := range uol.csvListers {
		csvs, err := cl.List(selector)
		if err != nil {
			return nil, err
		}

		for _, csv := range csvs {
			set[csv.GetUID()] = csv
		}
	}

	for _, csv := range set {
		ret = append(ret, csv)
	}

	return
}

// OperatorConditions returns an object that can list and get OperatorConditions.
func (uol *UnionOperatorConditionLister) OperatorConditions(namespace string) listers.OperatorConditionNamespaceLister {
	uol.csvLock.RLock()
	defer uol.csvLock.RUnlock()

	// Check for specific namespace listers
	if cl, ok := uol.csvListers[namespace]; ok {
		return cl.OperatorConditions(namespace)
	}

	// Check for any namespace-all listers
	if cl, ok := uol.csvListers[metav1.NamespaceAll]; ok {
		return cl.OperatorConditions(namespace)
	}

	return &NullOperatorConditionNamespaceLister{}
}

func (uol *UnionOperatorConditionLister) RegisterOperatorConditionLister(namespace string, lister listers.OperatorConditionLister) {
	uol.csvLock.Lock()
	defer uol.csvLock.Unlock()

	if uol.csvListers == nil {
		uol.csvListers = make(map[string]listers.OperatorConditionLister)
	}

	uol.csvListers[namespace] = lister
}

func (l *operatorsV1Lister) RegisterOperatorConditionLister(namespace string, lister listers.OperatorConditionLister) {
	l.OperatorConditionLister.RegisterOperatorConditionLister(namespace, lister)
}

func (l *operatorsV1Lister) OperatorConditionLister() listers.OperatorConditionLister {
	return l.OperatorConditionLister
}

// NullOperatorConditionNamespaceLister is an implementation of a null OperatorConditionNamespaceLister. It is
// used to prevent nil pointers when no OperatorConditionNamespaceLister has been registered for a given
// namespace.
type NullOperatorConditionNamespaceLister struct {
	listers.OperatorConditionNamespaceLister
}

// List returns nil and an error explaining that this is a NullOperatorConditionNamespaceLister.
func (n *NullOperatorConditionNamespaceLister) List(selector labels.Selector) (ret []*v1.OperatorCondition, err error) {
	return nil, fmt.Errorf("cannot list OperatorConditions with a NullOperatorConditionNamespaceLister")
}

// Get returns nil and an error explaining that this is a NullOperatorConditionNamespaceLister.
func (n *NullOperatorConditionNamespaceLister) Get(name string) (*v1.OperatorCondition, error) {
	return nil, fmt.Errorf("cannot get OperatorCondition with a NullOperatorConditionNamespaceLister")
}
