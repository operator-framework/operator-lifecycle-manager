package main

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/kubestate"
	"k8s.io/client-go/tools/cache"
)

func main() {
	k, ok := cache.MetaNamespaceKeyFunc(kubestate.NewUpdateEvent("bob"))
	fmt.Printf("key: %s (%t)\n", k, ok)
}
