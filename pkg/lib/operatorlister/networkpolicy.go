package operatorlister

import (
	"fmt"
	"sync"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	networkingv1listers "k8s.io/client-go/listers/networking/v1"
)

type UnionNetworkPolicyLister struct {
	networkPolicyListers map[string]networkingv1listers.NetworkPolicyLister
	networkPolicyLock    sync.RWMutex
}

// List lists all NetworkPolicies in the indexer.
func (unpl *UnionNetworkPolicyLister) List(selector labels.Selector) (ret []*networkingv1.NetworkPolicy, err error) {
	unpl.networkPolicyLock.RLock()
	defer unpl.networkPolicyLock.RUnlock()

	set := make(map[types.UID]*networkingv1.NetworkPolicy)
	for _, npl := range unpl.networkPolicyListers {
		networkPolicies, err := npl.List(selector)
		if err != nil {
			return nil, err
		}

		for _, networkPolicy := range networkPolicies {
			set[networkPolicy.GetUID()] = networkPolicy
		}
	}

	for _, networkPolicy := range set {
		ret = append(ret, networkPolicy)
	}

	return
}

// NetworkPolicies returns an object that can list and get NetworkPolicies.
func (unpl *UnionNetworkPolicyLister) NetworkPolicies(namespace string) networkingv1listers.NetworkPolicyNamespaceLister {
	unpl.networkPolicyLock.RLock()
	defer unpl.networkPolicyLock.RUnlock()

	// Check for specific namespace listers
	if npl, ok := unpl.networkPolicyListers[namespace]; ok {
		return npl.NetworkPolicies(namespace)
	}

	// Check for any namespace-all listers
	if npl, ok := unpl.networkPolicyListers[metav1.NamespaceAll]; ok {
		return npl.NetworkPolicies(namespace)
	}

	return &NullNetworkPolicyNamespaceLister{}
}

func (unpl *UnionNetworkPolicyLister) RegisterNetworkPolicyLister(namespace string, lister networkingv1listers.NetworkPolicyLister) {
	unpl.networkPolicyLock.Lock()
	defer unpl.networkPolicyLock.Unlock()

	if unpl.networkPolicyListers == nil {
		unpl.networkPolicyListers = make(map[string]networkingv1listers.NetworkPolicyLister)
	}
	unpl.networkPolicyListers[namespace] = lister
}

func (l *networkingV1Lister) RegisterNetworkPolicyLister(namespace string, lister networkingv1listers.NetworkPolicyLister) {
	l.networkPolicyLister.RegisterNetworkPolicyLister(namespace, lister)
}

func (l *networkingV1Lister) NetworkPolicyLister() networkingv1listers.NetworkPolicyLister {
	return l.networkPolicyLister
}

// NullNetworkPolicyNamespaceLister is an implementation of a null NetworkPolicyNamespaceLister. It is
// used to prevent nil pointers when no NetworkPolicyNamespaceLister has been registered for a given
// namespace.
type NullNetworkPolicyNamespaceLister struct {
	networkingv1listers.NetworkPolicyNamespaceLister
}

// List returns nil and an error explaining that this is a NullNetworkPolicyNamespaceLister.
func (n *NullNetworkPolicyNamespaceLister) List(selector labels.Selector) (ret []*networkingv1.NetworkPolicy, err error) {
	return nil, fmt.Errorf("cannot list NetworkPolicies with a NullNetworkPolicyNamespaceLister")
}

// Get returns nil and an error explaining that this is a NullNetworkPolicyNamespaceLister.
func (n *NullNetworkPolicyNamespaceLister) Get(name string) (*networkingv1.NetworkPolicy, error) {
	return nil, fmt.Errorf("cannot get NetworkPolicy with a NullNetworkPolicyNamespaceLister")
}
