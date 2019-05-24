package operators

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

// Operator contains shared operator tooling.
type Operator struct {
	*queueinformer.Operator

	Client       versioned.Interface
	Lister       operatorlister.OperatorLister
	clock        clock.Clock
	namespaces   []string
	resyncPeriod time.Duration
}

// Now returns the operator's view of the current time.
func (o *Operator) Now() metav1.Time {
	return metav1.NewTime(o.clock.Now())
}

// Namespaces returns the list namespaces the operator is configured to watch.
func (o *Operator) Namespaces() []string {
	return o.namespaces
}

// ResyncPeriod returns the period that the operator is configured to resync with.
func (o *Operator) ResyncPeriod() time.Duration {
	return o.resyncPeriod
}
