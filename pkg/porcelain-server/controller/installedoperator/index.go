package installedoperator

import (
	"github.com/pkg/errors"

	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

const (
	// CSVSubscriptionIndexFuncKey is the recommended key to use for registering the index func with an indexer.
	CSVSubscriptionIndexFuncKey string = "csvsubscriptionindexfunc"
)

// CSVSubscriptionIndexFunc generates indices for the current and installed CSVs of a given Subscription.
func CSVSubscriptionIndexFunc(obj interface{}) ([]string, error) {
	indices := []string{}
	sub, ok := obj.(*operatorsv1alpha1.Subscription)
	if !ok {
		// Not being a Subscription is an indication of a misconfiguration, return an error (fatal)
		return nil, errors.Errorf("object is not a subscription: %v", obj)
	}

	key := Key{
		Namespace: sub.GetNamespace(),
	}
	if key.Name = sub.Status.CurrentCSV; key.Name != "" {
		indices = append(indices, key.String())
	}
	if key.Name = sub.Status.InstalledCSV; key.Name != "" && key.Name != sub.Status.CurrentCSV {
		indices = append(indices, key.String())
	}

	return indices, nil
}
