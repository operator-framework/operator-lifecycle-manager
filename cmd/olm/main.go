package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorstatus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
)

const (
	defaultWakeupInterval = 5 * time.Minute
	defaultOperatorName   = ""
)

// config flags defined globally so that they appear on the test binary as well
var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")

	wakeupInterval = flag.Duration(
		"interval", defaultWakeupInterval, "wake up interval")

	watchedNamespaces = flag.String(
		"watchedNamespaces", "", "comma separated list of namespaces for olm operator to watch. "+
			"If not set, or set to the empty string (e.g. `-watchedNamespaces=\"\"`), "+
			"olm operator will watch all namespaces in the cluster.")

	writeStatusName = flag.String(
		"writeStatusName", defaultOperatorName, "ClusterOperator name in which to write status, set to \"\" to disable.")

	debug = flag.Bool(
		"debug", false, "use debug log level")

	version = flag.Bool("version", false, "displays olm version")

	tlsKeyPath = flag.String(
		"tls-key", "", "Path to use for private key (requires tls-cert)")

	tlsCertPath = flag.String(
		"tls-cert", "", "Path to use for certificate key (requires tls-key)")
)

func init() {
	metrics.RegisterOLM()
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

		// Exit early
		os.Exit(0)
	}

	// Set log level to debug if `debug` flag set
	logger := log.New()
	if *debug {
		logger.SetLevel(log.DebugLevel)
	}
	logger.Infof("log level %s", logger.Level)

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
	go func() {
		err := http.ListenAndServe(":8080", healthMux)
		if err != nil {
			logger.Errorf("Health serving failed: %v", err)
		}
	}()

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

	// `namespaces` will always contain at least one entry: if `*watchedNamespaces` is
	// the empty string, the resulting array will be `[]string{""}`.
	namespaces := strings.Split(*watchedNamespaces, ",")
	for _, ns := range namespaces {
		if ns == v1.NamespaceAll {
			namespaces = []string{v1.NamespaceAll}
			break
		}
	}

	// Create a new instance of the OLM operator
	builder := olm.NewBuilder()
	builder.WithNamespaces(namespaces...).
		WithKubeconfig(*kubeConfigPath).
		WithLogger(logger).
		WithResyncPeriod(*wakeupInterval)

	op, err := builder.BuildOLMOperator()
	if err != nil {
		logger.Panicf("error configuring operator: %s", err.Error())
	}

	ready, done, sync := op.Run(ctx)
	<-ready

	if *writeStatusName != "" {
		// create a config client for operator status
		config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
		if err != nil {
			log.Fatalf("error configuring client: %s", err.Error())
		}
		configClient, err := configv1client.NewForConfig(config)
		if err != nil {
			log.Fatalf("error configuring client: %s", err.Error())
		}

		operatorstatus.MonitorClusterStatus(*writeStatusName, sync, ctx.Done(), op.OpClient, configClient)
	}

	<-done
}
