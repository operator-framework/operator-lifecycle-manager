package metrics

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MetricsProvider interface {
	HandleMetrics() error
}

type metricsCSV struct {
	opClient operatorclient.ClientInterface
}

func NewMetricsCSV(opClient operatorclient.ClientInterface) MetricsProvider {
	return &metricsCSV{opClient}
}

func (m *metricsCSV) HandleMetrics() error {
	cList, err := m.opClient.ListCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, metav1.NamespaceAll, v1alpha1.ClusterServiceVersionKind)
	if err != nil {
		return err
	}
	csvCount.Set(float64(len(cList.Items)))
	return nil
}

type metricsInstallPlan struct {
	opClient operatorclient.ClientInterface
}

func NewMetricsInstallPlan(opClient operatorclient.ClientInterface) MetricsProvider {
	return &metricsInstallPlan{opClient}
}

func (m *metricsInstallPlan) HandleMetrics() error {
	cList, err := m.opClient.ListCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, metav1.NamespaceAll, v1alpha1.InstallPlanKind)
	if err != nil {
		return err
	}
	installPlanCount.Set(float64(len(cList.Items)))
	return nil
}

type metricsSubscription struct {
	opClient operatorclient.ClientInterface
}

func NewMetricsSubscription(opClient operatorclient.ClientInterface) MetricsProvider {
	return &metricsSubscription{opClient}
}

func (m *metricsSubscription) HandleMetrics() error {
	cList, err := m.opClient.ListCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, metav1.NamespaceAll, v1alpha1.SubscriptionKind)
	if err != nil {
		return err
	}
	subscriptionCount.Set(float64(len(cList.Items)))
	return nil
}

type metricsCatalogSource struct {
	opClient operatorclient.ClientInterface
}

func NewMetricsCatalogSource(opClient operatorclient.ClientInterface) MetricsProvider {
	return &metricsCatalogSource{opClient}

}

func (m *metricsCatalogSource) HandleMetrics() error {
	cList, err := m.opClient.ListCustomResource(v1alpha1.GroupName, v1alpha1.GroupVersion, metav1.NamespaceAll, v1alpha1.CatalogSourceKind)
	if err != nil {
		return err
	}
	catalogSourceCount.Set(float64(len(cList.Items)))
	return nil
}

type MetricsNil struct{}

func NewMetricsNil() MetricsProvider {
	return &MetricsNil{}
}

func (*MetricsNil) HandleMetrics() error {
	return nil
}

// To add new metrics:
// 1. Register new metrics in Register() below.
// 2. Add appropriate metric updates in HandleMetrics (or elsewhere instead).
var (
	csvCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "csv_count",
			Help: "Number of CSVs successfully registered",
		},
	)

	installPlanCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "install_plan_count",
			Help: "Number of install plans",
		},
	)

	subscriptionCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "subscription_count",
			Help: "Number of subscriptions",
		},
	)

	catalogSourceCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "catalog_source_count",
			Help: "Number of catalog sources",
		},
	)

	// exported since it's not handled by HandleMetrics
	CSVUpgradeCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "csv_upgrade_count",
			Help: "Monotonic count of catalog sources",
		},
	)
)

func Register() {
	prometheus.MustRegister(csvCount)
	prometheus.MustRegister(installPlanCount)
	prometheus.MustRegister(subscriptionCount)
	prometheus.MustRegister(catalogSourceCount)
	prometheus.MustRegister(CSVUpgradeCount)
}
