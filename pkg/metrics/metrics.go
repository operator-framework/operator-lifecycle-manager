package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	olmv1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	v1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
)

const (
	NAME_LABEL      = "name"
	INSTALLED_LABEL = "installed"
	NAMESPACE_LABEL = "namespace"
	CHANNEL_LABEL   = "channel"
	VERSION_LABEL   = "version"
	PHASE_LABEL     = "phase"
	REASON_LABEL    = "reason"
	PACKAGE_LABEL   = "package"
)

// TODO(alecmerdler): Can we use this to emit Kubernetes events?
type MetricsProvider interface {
	HandleMetrics() error
}

type metricsCSV struct {
	lister v1alpha1.ClusterServiceVersionLister
}

func NewMetricsCSV(lister v1alpha1.ClusterServiceVersionLister) MetricsProvider {
	return &metricsCSV{lister}
}

func (m *metricsCSV) HandleMetrics() error {
	cList, err := m.lister.List(labels.Everything())
	if err != nil {
		return err
	}
	csvCount.Set(float64(len(cList)))
	return nil
}

type metricsInstallPlan struct {
	client versioned.Interface
}

func NewMetricsInstallPlan(client versioned.Interface) MetricsProvider {
	return &metricsInstallPlan{client}
}

func (m *metricsInstallPlan) HandleMetrics() error {
	cList, err := m.client.OperatorsV1alpha1().InstallPlans(metav1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	installPlanCount.Set(float64(len(cList.Items)))
	return nil
}

type metricsSubscription struct {
	client versioned.Interface
}

func NewMetricsSubscription(client versioned.Interface) MetricsProvider {
	return &metricsSubscription{client}
}

func (m *metricsSubscription) HandleMetrics() error {
	cList, err := m.client.OperatorsV1alpha1().Subscriptions(metav1.NamespaceAll).List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	subscriptionCount.Set(float64(len(cList.Items)))
	return nil
}

type metricsCatalogSource struct {
	client versioned.Interface
}

func NewMetricsCatalogSource(client versioned.Interface) MetricsProvider {
	return &metricsCatalogSource{client}

}

func (m *metricsCatalogSource) HandleMetrics() error {
	cList, err := m.client.OperatorsV1alpha1().CatalogSources(metav1.NamespaceAll).List(metav1.ListOptions{})
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
			Help: "Monotonic count of CSV upgrades",
		},
	)

	SubscriptionSyncCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "subscription_sync_total",
			Help: "Monotonic count of subscription syncs",
		},
		[]string{NAME_LABEL, INSTALLED_LABEL, CHANNEL_LABEL, PACKAGE_LABEL},
	)

	csvSucceeded = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "csv_succeeded",
			Help: "Successful CSV install",
		},
		[]string{NAMESPACE_LABEL, NAME_LABEL, VERSION_LABEL},
	)

	csvAbnormal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "csv_abnormal",
			Help: "CSV is not installed",
		},
		[]string{NAMESPACE_LABEL, NAME_LABEL, VERSION_LABEL, PHASE_LABEL, REASON_LABEL},
	)
)

func RegisterOLM() {
	prometheus.MustRegister(csvCount)
	prometheus.MustRegister(csvSucceeded)
	prometheus.MustRegister(csvAbnormal)
	prometheus.MustRegister(CSVUpgradeCount)
}

func RegisterCatalog() {
	prometheus.MustRegister(installPlanCount)
	prometheus.MustRegister(subscriptionCount)
	prometheus.MustRegister(catalogSourceCount)
	prometheus.MustRegister(SubscriptionSyncCount)
}

func CounterForSubscription(name, installedCSV, channelName, packageName string) prometheus.Counter {
	return SubscriptionSyncCount.WithLabelValues(name, installedCSV, channelName, packageName)
}

func EmitCSVMetric(oldCSV *olmv1alpha1.ClusterServiceVersion, newCSV *olmv1alpha1.ClusterServiceVersion) {
	if oldCSV == nil || newCSV == nil {
		return
	}

	// Don't update the metric for copies
	if newCSV.Status.Reason == olmv1alpha1.CSVReasonCopied {
		return
	}

	// Delete the old CSV metrics
	csvAbnormal.DeleteLabelValues(oldCSV.Namespace, oldCSV.Name, oldCSV.Spec.Version.String(), string(oldCSV.Status.Phase), string(oldCSV.Status.Reason))

	// Get the phase of the new CSV
	newCSVPhase := string(newCSV.Status.Phase)
	csvSucceededGauge := csvSucceeded.WithLabelValues(newCSV.Namespace, newCSV.Name, newCSV.Spec.Version.String())
	if newCSVPhase == string(olmv1alpha1.CSVPhaseSucceeded) {
		csvSucceededGauge.Set(1)
	} else {
		csvSucceededGauge.Set(0)
		csvAbnormal.WithLabelValues(newCSV.Namespace, newCSV.Name, newCSV.Spec.Version.String(), string(newCSV.Status.Phase), string(newCSV.Status.Reason)).Set(1)
	}
}
