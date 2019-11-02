package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	v1 "k8s.io/api/core/v1"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorstatus"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/profile"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
)

const (
	catalogNamespaceEnvVarName  = "GLOBAL_CATALOG_NAMESPACE"
	defaultWakeupInterval       = 15 * time.Minute
	defaultCatalogNamespace     = "openshift-operator-lifecycle-manager"
	defaultConfigMapServerImage = "quay.io/operatorframework/configmap-operator-registry:latest"
	defaultOperatorName         = ""
)

// config flags defined globally so that they appear on the test binary as well
var (
	kubeConfigPath = pflag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")

	wakeupInterval = pflag.Duration(
		"interval", defaultWakeupInterval, "wakeup interval")

	watchedNamespaces = pflag.String(
		"watchedNamespaces", "", "comma separated list of namespaces that catalog watches, leave empty to watch all namespaces")

	catalogNamespace = pflag.String(
		"namespace", defaultCatalogNamespace, "namespace where catalog will run and install catalog resources")

	configmapServerImage = pflag.String(
		"configmapServerImage", defaultConfigMapServerImage, "the image to use for serving the operator registry api for a configmap")

	writeStatusName = pflag.String(
		"writeStatusName", defaultOperatorName, "ClusterOperator name in which to write status, set to \"\" to disable.")

	debug = pflag.Bool(
		"debug", false, "use debug log level")

	version = pflag.Bool("version", false, "displays olm version")

	tlsKeyPath = pflag.String(
		"tls-key", "", "Path to use for private key (requires tls-cert)")

	tlsCertPath = pflag.String(
		"tls-cert", "", "Path to use for certificate key (requires tls-key)")

	profiling = pflag.Bool(
		"profiling", false, "serve profiling data (on port 8080)")
)

func init() {
	metrics.RegisterCatalog()
}

func main() {
	// Get exit signal context
	ctx, cancel := context.WithCancel(signals.Context())
	defer cancel()

	// Parse the command-line flags.
	pflag.Parse()

	// Check if version flag was set
	if *version {
		fmt.Print(olmversion.String())

		// Exit early
		os.Exit(0)
	}

	// `namespaces` will always contain at least one entry: if `*watchedNamespaces` is
	// the empty string, the resulting array will be `[]string{""}`.
	namespaces := strings.Split(*watchedNamespaces, ",")
	for _, ns := range namespaces {
		if ns == v1.NamespaceAll {
			namespaces = []string{v1.NamespaceAll}
			break
		}
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

	var useTLS bool
	if *tlsCertPath != "" && *tlsKeyPath == "" || *tlsCertPath == "" && *tlsKeyPath != "" {
		logger.Warn("both --tls-key and --tls-crt must be provided for TLS to be enabled, falling back to non-https")
	} else if *tlsCertPath == "" && *tlsKeyPath == "" {
		logger.Info("TLS keys not set, using non-https for metrics")
	} else {
		logger.Info("TLS keys set, using https for metrics")
		useTLS = true
	}

	// Serve a health check.
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Serve profiling if enabled
	if *profiling {
		logger.Infof("profiling enabled")
		profile.RegisterHandlers(healthMux)
	}

	go http.ListenAndServe(":8080", healthMux)

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	if useTLS {
		go func() {
			err := http.ListenAndServeTLS(":8081", *tlsCertPath, *tlsKeyPath, metricsMux)
			if err != nil {
				logger.Errorf("Metrics (https) serving failed: %v", err)
			}
		}()
	} else {
		go func() {
			err := http.ListenAndServe(":8081", metricsMux)
			if err != nil {
				logger.Errorf("Metrics (http) serving failed: %v", err)
			}
		}()
	}

	// create a config client for operator status
	config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}
	configClient, err := configv1client.NewForConfig(config)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}
	opClient := operatorclient.NewClientFromConfig(*kubeConfigPath, logger)
	crClient, err := client.NewClient(*kubeConfigPath)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}

	// Create a new instance of the operator.
	op, err := catalog.NewOperator(ctx, *kubeConfigPath, utilclock.RealClock{}, logger, *wakeupInterval, *configmapServerImage, *catalogNamespace, namespaces...)
	if err != nil {
		log.Panicf("error configuring operator: %s", err.Error())
	}

	op.Run(ctx)
	<-op.Ready()

	if *writeStatusName != "" {
		operatorstatus.MonitorClusterStatus(*writeStatusName, op.AtLevel(), op.Done(), opClient, configClient, crClient)
	}

	<-op.Done()
}
