//go:build experimental_metrics
// +build experimental_metrics

package metrics

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	// Register experimental metrics
	reconcileMetrics = reconcileCounters(operatorController, adoptionCSVController, adoptionSubscriptionController, operatorConditionController, operatorConditionGeneratorController)
	registerReconcileMetrics()
}

func reconcileCounters(reconcilerNames ...string) map[string]*prometheus.CounterVec {
	result := map[string]*prometheus.CounterVec{}
	for _, s := range reconcilerNames {
		result[s] = createReconcileCounterVec(s)
	}
	return result
}

func createReconcileCounterVec(name string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "controller_reconcile_" + name,
			Help: fmt.Sprintf("Count of reconcile events by the %s controller", strings.Replace(name, "_", " ", -1)),
		},
		[]string{NamespaceLabel, NameLabel},
	)
}

func registerReconcileMetrics() {
	for _, v := range reconcileMetrics {
		prometheus.MustRegister(v)
	}
}
