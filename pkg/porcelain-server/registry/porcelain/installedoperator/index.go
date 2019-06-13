package installedoperator

import (
	"strings"

	"github.com/pkg/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	operatorsv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1"
	operatorsv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

const (
	// TargetNamespaceIndexFuncKey is the recommended key to use for registering the index func with an indexer.
	TargetNamespaceIndexFuncKey string = "targetnamespaceindexfunc"
)

// TargetNamespaceIndexFunc generates namespace indices for the target namespaces listed in CSV's annotations.
func TargetNamespaceIndexFunc(obj interface{}) ([]string, error) {
	indices := []string{}
	csv, ok := obj.(*operatorsv1alpha1.ClusterServiceVersion)
	if !ok {
		// Not being a CSV is an indication of a misconfiguration, return an error (fatal)
		return nil, errors.Errorf("object is not a csv: %v", obj)
	}

	if _, ok := csv.GetAnnotations()[operatorsv1.OperatorGroupAnnotationKey]; !ok {
		// Not being in an OperatorGroup is transient, eat the error here
		utilruntime.HandleError(errors.Errorf("csv not a member of an operatorgroup: %s", csv.GetSelfLink()))
		return indices, nil
	}

	targets, ok := csv.GetAnnotations()[operatorsv1.OperatorGroupTargetsAnnotationKey]
	if !ok {
		// Having no targets is transient, eat the error here
		utilruntime.HandleError(errors.Errorf("csv has no target namespaces: %s", csv.GetSelfLink()))
		return indices, nil
	}

	// Targets annotation is a comma-delimited set of namespaces
	indices = strings.Split(targets, ",")

	return indices, nil
}
