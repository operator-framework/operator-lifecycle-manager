package metrics

import "github.com/prometheus/client_golang/prometheus"

const (
	// Controller names
	operatorController                   = "operator"
	adoptionCSVController                = "adoption_csv"
	adoptionSubscriptionController       = "adoption_subscription"
	operatorConditionController          = "operator_condition"
	operatorConditionGeneratorController = "operator_condition_generator"
)

var (
	reconcileMetrics = map[string]*prometheus.CounterVec{}
)

func EmitOperatorReconcile(namespace, name string) {
	emitReconcile(operatorController, namespace, name)
}

func EmitAdoptionCSVReconcile(namespace, name string) {
	emitReconcile(adoptionCSVController, namespace, name)
}

func EmitAdoptionSubscriptionReconcile(namespace, name string) {
	emitReconcile(adoptionSubscriptionController, namespace, name)
}

func EmitOperatorConditionReconcile(namespace, name string) {
	emitReconcile(operatorConditionController, namespace, name)
}

func EmitOperatorConditionGeneratorReconcile(namespace, name string) {
	emitReconcile(operatorConditionGeneratorController, namespace, name)
}

func emitReconcile(controllerName, namespace, name string) {
	if counter, ok := reconcileMetrics[controllerName]; ok {
		counter.WithLabelValues(namespace, name).Inc()
	}
}
