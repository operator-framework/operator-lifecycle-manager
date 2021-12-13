package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/config"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalogtemplate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorstatus"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/server"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
	log "github.com/sirupsen/logrus"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
)

const (
	catalogNamespaceEnvVarName  = "GLOBAL_CATALOG_NAMESPACE"
	defaultWakeupInterval       = 15 * time.Minute
	defaultCatalogNamespace     = "olm"
	defaultConfigMapServerImage = "quay.io/operator-framework/configmap-operator-registry:latest"
	defaultOPMImage             = "quay.io/operator-framework/upstream-opm-builder:latest"
	defaultUtilImage            = "quay.io/operator-framework/olm:latest"
	defaultOperatorName         = ""
)

// config flags defined globally so that they appear on the test binary as well
var (
	wakeupInterval = flag.Duration(
		"interval", defaultWakeupInterval, "wakeup interval")

	catalogNamespace = flag.String(
		"namespace", defaultCatalogNamespace, "namespace where catalog will run and install catalog resources")

	configmapServerImage = flag.String(
		"configmapServerImage", defaultConfigMapServerImage, "the image to use for serving the operator registry api for a configmap")

	opmImage = flag.String(
		"opmImage", defaultOPMImage, "the image to use for unpacking bundle content with opm")

	utilImage = flag.String(
		"util-image", defaultUtilImage, "an image containing custom olm utilities")

	writeStatusName = flag.String(
		"writeStatusName", defaultOperatorName, "ClusterOperator name in which to write status, set to \"\" to disable.")

	debug = flag.Bool(
		"debug", false, "use debug log level")

	version = flag.Bool("version", false, "displays olm version")

	tlsKeyPath = flag.String(
		"tls-key", "", "Path to use for private key (requires tls-cert)")

	tlsCertPath = flag.String(
		"tls-cert", "", "Path to use for certificate key (requires tls-key)")

	_ = flag.Bool("profiling", false, "deprecated")

	clientCAPath = flag.String("client-ca", "", "path to watch for client ca bundle")

	installPlanTimeout  = flag.Duration("install-plan-retry-timeout", 1*time.Minute, "time since first attempt at which plan execution errors are considered fatal")
	bundleUnpackTimeout = flag.Duration("bundle-unpack-timeout", 10*time.Minute, "The time limit for bundle unpacking, after which InstallPlan execution is considered to have failed. 0 is considered as having no timeout.")
)

func init() {
	metrics.RegisterCatalog()
}

func main() {
	// Get exit signal context
	ctx, cancel := context.WithCancel(signals.Context())
	defer cancel()

	// Parse the command-line flags.
	flag.Parse()

	// Check if version flag was set
	if *version {
		fmt.Print(olmversion.String())
		os.Exit(0)
	}

	logger := log.New()
	if *debug {
		logger.SetLevel(log.DebugLevel)
	}
	logger.Infof("log level %s", logger.Level)

	// If the catalogNamespaceEnvVarName environment variable is set, then  update the value of catalogNamespace.
	if catalogNamespaceEnvVarValue := os.Getenv(catalogNamespaceEnvVarName); catalogNamespaceEnvVarValue != "" {
		logger.Infof("%s environment variable is set. Updating Global Catalog Namespace to %s", catalogNamespaceEnvVarName, catalogNamespaceEnvVarValue)
		*catalogNamespace = catalogNamespaceEnvVarValue
	}

	listenAndServe, err := server.GetListenAndServeFunc(server.WithLogger(logger), server.WithTLS(tlsCertPath, tlsKeyPath, clientCAPath), server.WithDebug(*debug))
	if err != nil {
		logger.Fatalf("Error setting up health/metric/pprof service: %v", err)
	}

	go func() {
		if err := listenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err)
		}
	}()

	// create a config client for operator status
	kubeconfig, err := config.GetConfig()
	if err != nil {
		log.Fatalf("error getting kubeconfig: %s", err.Error())
	}
	configClient, err := configv1client.NewForConfig(kubeconfig)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}
	opClient, err := operatorclient.NewClientFromRestConfig(kubeconfig)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}

	crClient, err := client.NewClient(kubeconfig)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}

	// Create a new instance of the operator.
	op, err := catalog.NewOperator(ctx, kubeconfig, utilclock.RealClock{}, logger, *wakeupInterval, *configmapServerImage, *opmImage, *utilImage, *catalogNamespace, k8sscheme.Scheme, *installPlanTimeout, *bundleUnpackTimeout)
	if err != nil {
		log.Fatalf("error configuring catalog operator: %s", err.Error())
	}

	opCatalogTemplate, err := catalogtemplate.NewOperator(ctx, kubeconfig, logger, *wakeupInterval, *catalogNamespace)
	if err != nil {
		log.Fatalf("error configuring catalog template operator: %s", err.Error())
	}

	op.Run(ctx)
	<-op.Ready()

	opCatalogTemplate.Run(ctx)
	<-opCatalogTemplate.Ready()

	if *writeStatusName != "" {
		operatorstatus.MonitorClusterStatus(*writeStatusName, op.AtLevel(), op.Done(), opClient, configClient, crClient)
	}

	<-op.Done()
}
