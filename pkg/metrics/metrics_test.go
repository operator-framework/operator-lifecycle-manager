package metrics_test

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

func TestUpdateSubsSyncCounterStorageThreadSafety(t *testing.T) {
	for i := 0; i < 1000; i++ {
		go func(ii int) {
			sub := &operatorsv1alpha1.Subscription{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "foo",
				},
				Spec: &operatorsv1alpha1.SubscriptionSpec{
					Channel:             "foo",
					Package:             "foo",
					InstallPlanApproval: "automatic",
				},
				Status: operatorsv1alpha1.SubscriptionStatus{
					InstalledCSV: "foo",
				},
			}
			sub.Spec.Channel = fmt.Sprintf("bar-%v", ii)
			metrics.UpdateSubsSyncCounterStorage(sub)
		}(i)
	}
}
