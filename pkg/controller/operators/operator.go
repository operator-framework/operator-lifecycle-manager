package operators

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
)

// TODO: Add comments

type Operator struct {
	*queueinformer.Operator

	Client       versioned.Interface
	Lister       operatorlister.OperatorLister
	clock        clock.Clock
	namespaces   []string
	resyncPeriod time.Duration
}

func (o *Operator) Now() metav1.Time {
	return metav1.NewTime(o.clock.Now())
}

func (o *Operator) Namespaces() []string {
	return o.namespaces
}

func (o *Operator) ResyncPeriod() time.Duration {
	return o.resyncPeriod
}
