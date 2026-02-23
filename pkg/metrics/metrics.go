package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/connectivity"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	v1alpha1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/listers/operators/v1alpha1"
)

const (
	NameLabel      = "name"
	InstalledLabel = "installed"
	NamespaceLabel = "namespace"
	ChannelLabel   = "channel"
	VersionLabel   = "version"
	PhaseLabel     = "phase"
	ReasonLabel    = "reason"
	PackageLabel   = "package"
	Outcome        = "outcome"
	Succeeded      = "succeeded"
	Failed         = "failed"
	ApprovalLabel  = "approval"
	WarningLabel   = "warning"
	GVKLabel       = "gvk"
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
		[]string{NamespaceLabel, NameLabel},
	)

	catalogSourceSnapshotsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "catalog_source_snapshots_total",
			Help: "The number of times the catalog operator has requested a snapshot of data from a catalog source",
		},
		[]string{NamespaceLabel, NameLabel},
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
		[]string{NameLabel, InstalledLabel, ChannelLabel, PackageLabel, ApprovalLabel},
	)

	csvSucceeded = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "csv_succeeded",
			Help: "Successful CSV install",
		},
		[]string{NamespaceLabel, NameLabel, VersionLabel},
	)

	csvAbnormal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "csv_abnormal",
			Help: "CSV is not installed",
		},
		[]string{NamespaceLabel, NameLabel, VersionLabel, PhaseLabel, ReasonLabel},
	)

	dependencyResolutionSummary = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "olm_resolution_duration_seconds",
			Help:       "The duration of a dependency resolution attempt",
			Objectives: map[float64]float64{0.95: 0.05, 0.9: 0.01, 0.99: 0.001},
		},
		[]string{Outcome},
	)

	installPlanWarningCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "installplan_warnings_total",
			Help: "monotonic count of resources that generated warnings when applied as part of an InstallPlan (for example, due to deprecation)",
		},
	)

	subscriptionSyncCounters = newSubscriptionSyncCounter()
)

// subscriptionSyncCounter keeps a record of the Prometheus counters emitted by
// Subscription objects. The key of a record is the Subscription name, while the value
// is struct containing label values used in the counter. Read and Write access are
// protected by mutex.
type subscriptionSyncCounter struct {
	counters     map[string]subscriptionSyncLabelValues
	countersLock sync.RWMutex
}

func newSubscriptionSyncCounter() subscriptionSyncCounter {
	return subscriptionSyncCounter{
		counters: make(map[string]subscriptionSyncLabelValues),
	}
}

func (s *subscriptionSyncCounter) setValues(key string, val subscriptionSyncLabelValues) {
	s.countersLock.Lock()
	defer s.countersLock.Unlock()
	s.counters[key] = val
}

func (s *subscriptionSyncCounter) readValues(key string) (subscriptionSyncLabelValues, bool) {
	s.countersLock.RLock()
	defer s.countersLock.RUnlock()
	val, ok := s.counters[key]
	return val, ok
}

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
	prometheus.MustRegister(catalogSourceSnapshotsTotal)
	prometheus.MustRegister(SubscriptionSyncCount)
	prometheus.MustRegister(dependencyResolutionSummary)
	prometheus.MustRegister(installPlanWarningCount)
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

func RegisterCatalogSourceSnapshotsTotal(name, namespace string) {
	catalogSourceSnapshotsTotal.WithLabelValues(namespace, name).Add(0)
}

func IncrementCatalogSourceSnapshotsTotal(name, namespace string) {
	catalogSourceSnapshotsTotal.WithLabelValues(namespace, name).Inc()
}

func DeleteCatalogSourceSnapshotsTotal(name, namespace string) {
	catalogSourceSnapshotsTotal.DeleteLabelValues(namespace, name)
}

func DeleteCSVMetric(oldCSV *operatorsv1alpha1.ClusterServiceVersion) {
	// Delete the old CSV metrics
	csvAbnormal.DeleteLabelValues(oldCSV.Namespace, oldCSV.Name, oldCSV.Spec.Version.String(), string(oldCSV.Status.Phase), string(oldCSV.Status.Reason))
	csvSucceeded.DeleteLabelValues(oldCSV.Namespace, oldCSV.Name, oldCSV.Spec.Version.String())
}

func EmitCSVMetric(oldCSV *operatorsv1alpha1.ClusterServiceVersion, newCSV *operatorsv1alpha1.ClusterServiceVersion) {
	if oldCSV == nil || newCSV == nil {
		return
	}

	// Don't update the metric for copies
	if newCSV.Status.Reason == operatorsv1alpha1.CSVReasonCopied {
		return
	}

	// Delete the old CSV metrics
	csvAbnormal.DeleteLabelValues(oldCSV.Namespace, oldCSV.Name, oldCSV.Spec.Version.String(), string(oldCSV.Status.Phase), string(oldCSV.Status.Reason))

	// Get the phase of the new CSV
	newCSVPhase := string(newCSV.Status.Phase)
	csvSucceededGauge := csvSucceeded.WithLabelValues(newCSV.Namespace, newCSV.Name, newCSV.Spec.Version.String())
	if newCSVPhase == string(operatorsv1alpha1.CSVPhaseSucceeded) {
		csvSucceededGauge.Set(1)
	} else {
		csvSucceededGauge.Set(0)
		csvAbnormal.WithLabelValues(newCSV.Namespace, newCSV.Name, newCSV.Spec.Version.String(), string(newCSV.Status.Phase), string(newCSV.Status.Reason)).Set(1)
	}
}

func EmitSubMetric(sub *operatorsv1alpha1.Subscription) {
	if sub.Spec == nil {
		return
	}

	SubscriptionSyncCount.WithLabelValues(sub.GetName(), sub.Status.InstalledCSV, sub.Spec.Channel, sub.Spec.Package, string(sub.Spec.InstallPlanApproval)).Inc()
	if _, present := subscriptionSyncCounters.readValues(sub.GetName()); !present {
		subscriptionSyncCounters.setValues(sub.GetName(), subscriptionSyncLabelValues{
			installedCSV:     sub.Status.InstalledCSV,
			pkg:              sub.Spec.Package,
			channel:          sub.Spec.Channel,
			approvalStrategy: string(sub.Spec.InstallPlanApproval),
		})
	}
}

func DeleteSubsMetric(sub *operatorsv1alpha1.Subscription) {
	if sub.Spec == nil {
		return
	}
	SubscriptionSyncCount.DeleteLabelValues(sub.GetName(), sub.Status.InstalledCSV, sub.Spec.Channel, sub.Spec.Package, string(sub.Spec.InstallPlanApproval))
}

func UpdateSubsSyncCounterStorage(sub *operatorsv1alpha1.Subscription) {
	if sub.Spec == nil {
		return
	}
	counterValues, _ := subscriptionSyncCounters.readValues(sub.GetName())
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

		subscriptionSyncCounters.setValues(sub.GetName(), counterValues)
	}
}

func RegisterDependencyResolutionSuccess(duration time.Duration) {
	dependencyResolutionSummary.WithLabelValues(Succeeded).Observe(duration.Seconds())
}

func RegisterDependencyResolutionFailure(duration time.Duration) {
	dependencyResolutionSummary.WithLabelValues(Failed).Observe(duration.Seconds())
}

func EmitInstallPlanWarning() {
	installPlanWarningCount.Inc()
}
