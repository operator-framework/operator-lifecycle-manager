package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/connectivity"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
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
	Outcome         = "outcome"
	Succeeded       = "succeeded"
	Failed          = "failed"
	APPROVAL_LABEL  = "approval"
)

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
	lister v1alpha1.InstallPlanLister
}

func NewMetricsInstallPlan(lister v1alpha1.InstallPlanLister) MetricsProvider {
	return &metricsInstallPlan{lister}
}

func (m *metricsInstallPlan) HandleMetrics() error {
	cList, err := m.lister.InstallPlans(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		return err
	}
	installPlanCount.Set(float64(len(cList)))
	return nil
}

type metricsSubscription struct {
	lister v1alpha1.SubscriptionLister
}

func NewMetricsSubscription(lister v1alpha1.SubscriptionLister) MetricsProvider {
	return &metricsSubscription{lister}
}

func (m *metricsSubscription) HandleMetrics() error {
	cList, err := m.lister.Subscriptions(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		return err
	}
	subscriptionCount.Set(float64(len(cList)))
	return nil
}

type metricsCatalogSource struct {
	lister v1alpha1.CatalogSourceLister
}

func NewMetricsCatalogSource(lister v1alpha1.CatalogSourceLister) MetricsProvider {
	return &metricsCatalogSource{lister}

}

func (m *metricsCatalogSource) HandleMetrics() error {
	cList, err := m.lister.CatalogSources(metav1.NamespaceAll).List(labels.Everything())
	if err != nil {
		return err
	}
	catalogSourceCount.Set(float64(len(cList)))
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

	catalogSourceReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "catalogsource_ready",
			Help: "State of a CatalogSource. 1 indicates that the CatalogSource is in a READY state. 0 indicates CatalogSource is in a Non READY state.",
		},
		[]string{NAMESPACE_LABEL, NAME_LABEL},
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
		[]string{NAME_LABEL, INSTALLED_LABEL, CHANNEL_LABEL, PACKAGE_LABEL, APPROVAL_LABEL},
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

	dependencyResolutionSummary = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "olm_resolution_duration_seconds",
			Help:       "The duration of a dependency resolution attempt",
			Objectives: map[float64]float64{0.95: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{Outcome},
	)

	// subscriptionSyncCounters keeps a record of the promethues counters emitted by
	// Subscription objects. The key of a record is the Subscription name, while the value
	//  is struct containing label values used in the counter
	subscriptionSyncCounters = make(map[string]subscriptionSyncLabelValues)
)

type subscriptionSyncLabelValues struct {
	installedCSV     string
	pkg              string
	channel          string
	approvalStrategy string
}

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
	prometheus.MustRegister(catalogSourceReady)
	prometheus.MustRegister(SubscriptionSyncCount)
	prometheus.MustRegister(dependencyResolutionSummary)
}

func CounterForSubscription(name, installedCSV, channelName, packageName, planApprovalStrategy string) prometheus.Counter {
	return SubscriptionSyncCount.WithLabelValues(name, installedCSV, channelName, packageName, planApprovalStrategy)
}

func RegisterCatalogSourceState(name, namespace string, state connectivity.State) {
	switch state {
	case connectivity.Ready:
		catalogSourceReady.WithLabelValues(namespace, name).Set(1)
	default:
		catalogSourceReady.WithLabelValues(namespace, name).Set(0)
	}
}

func DeleteCatalogSourceStateMetric(name, namespace string) {
	catalogSourceReady.DeleteLabelValues(namespace, name)
}

func DeleteCSVMetric(oldCSV *olmv1alpha1.ClusterServiceVersion) {
	// Delete the old CSV metrics
	csvAbnormal.DeleteLabelValues(oldCSV.Namespace, oldCSV.Name, oldCSV.Spec.Version.String(), string(oldCSV.Status.Phase), string(oldCSV.Status.Reason))
	csvSucceeded.DeleteLabelValues(oldCSV.Namespace, oldCSV.Name, oldCSV.Spec.Version.String())
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

func EmitSubMetric(sub *olmv1alpha1.Subscription) {
	if sub.Spec == nil {
		return
	}
	SubscriptionSyncCount.WithLabelValues(sub.GetName(), sub.Status.InstalledCSV, sub.Spec.Channel, sub.Spec.Package, string(sub.Spec.InstallPlanApproval)).Inc()
	if _, present := subscriptionSyncCounters[sub.GetName()]; !present {
		subscriptionSyncCounters[sub.GetName()] = subscriptionSyncLabelValues{
			installedCSV:     sub.Status.InstalledCSV,
			pkg:              sub.Spec.Package,
			channel:          sub.Spec.Channel,
			approvalStrategy: string(sub.Spec.InstallPlanApproval),
		}
	}
}

func DeleteSubsMetric(sub *olmv1alpha1.Subscription) {
	if sub.Spec == nil {
		return
	}
	SubscriptionSyncCount.DeleteLabelValues(sub.GetName(), sub.Status.InstalledCSV, sub.Spec.Channel, sub.Spec.Package, string(sub.Spec.InstallPlanApproval))
}

func UpdateSubsSyncCounterStorage(sub *olmv1alpha1.Subscription) {
	if sub.Spec == nil {
		return
	}
	counterValues := subscriptionSyncCounters[sub.GetName()]
	approvalStrategy := string(sub.Spec.InstallPlanApproval)

	if sub.Spec.Channel != counterValues.channel ||
		sub.Spec.Package != counterValues.pkg ||
		sub.Status.InstalledCSV != counterValues.installedCSV ||
		approvalStrategy != counterValues.approvalStrategy {

		// Delete metric will label values of old Subscription first
		SubscriptionSyncCount.DeleteLabelValues(sub.GetName(), counterValues.installedCSV, counterValues.channel, counterValues.pkg, counterValues.approvalStrategy)

		counterValues.installedCSV = sub.Status.InstalledCSV
		counterValues.pkg = sub.Spec.Package
		counterValues.channel = sub.Spec.Channel
		counterValues.approvalStrategy = approvalStrategy

		subscriptionSyncCounters[sub.GetName()] = counterValues
	}
}

func RegisterDependencyResolutionSuccess(duration time.Duration) {
	dependencyResolutionSummary.WithLabelValues(Succeeded).Observe(duration.Seconds())
}

func RegisterDependencyResolutionFailure(duration time.Duration) {
	dependencyResolutionSummary.WithLabelValues(Failed).Observe(duration.Seconds())
}
