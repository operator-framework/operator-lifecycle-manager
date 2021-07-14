package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	configclientset "github.com/openshift/client-go/config/clientset/versioned"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/openshift"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/feature"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorstatus"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/queueinformer"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/server"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
)

const (
	defaultWakeupInterval          = 5 * time.Minute
	defaultOperatorName            = ""
	defaultPackageServerStatusName = ""
)

// config flags defined globally so that they appear on the test binary as well
var (
	wakeupInterval = pflag.Duration(
		"interval", defaultWakeupInterval, "wake up interval")

	watchedNamespaces = pflag.String(
		"watchedNamespaces", "", "comma separated list of namespaces for olm operator to watch. "+
			"If not set, or set to the empty string (e.g. `-watchedNamespaces=\"\"`), "+
			"olm operator will watch all namespaces in the cluster.")

	writeStatusName = pflag.String(
		"writeStatusName", defaultOperatorName, "ClusterOperator name in which to write status, set to \"\" to disable.")

	writePackageServerStatusName = pflag.String(
		"writePackageServerStatusName", defaultPackageServerStatusName, "ClusterOperator name in which to write status for package API server, set to \"\" to disable.")

	debug = pflag.Bool(
		"debug", false, "use debug log level")

	version = pflag.Bool("version", false, "displays olm version")

	tlsKeyPath = pflag.String(
		"tls-key", "", "Path to use for private key (requires tls-cert)")

	tlsCertPath = pflag.String(
		"tls-cert", "", "Path to use for certificate key (requires tls-key)")

	profiling = pflag.Bool("profiling", false, "deprecated")

	clientCAPath = pflag.String("client-ca", "", "path to watch for client ca bundle")

	namespace = pflag.String(
		"namespace", "", "namespace where cleanup runs")
)

func init() {
	metrics.RegisterOLM()

	// Add feature gates before parsing
	feature.AddFlag(pflag.CommandLine)
}

// main function - entrypoint to OLM operator
func main() {
	// Get exit signal context
	ctx, cancel := context.WithCancel(signals.Context())
	defer cancel()

	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	pflag.Parse()

	// Parse the command-line flags.

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

	// Set log level to debug if `debug` flag set
	logger := logrus.New()
	if *debug {
		logger.SetLevel(logrus.DebugLevel)
		klogVerbosity := klogFlags.Lookup("v")
		klogVerbosity.Value.Set("99")
	}
	logger.Infof("log level %s", logger.Level)

	listenAndServe, err := server.GetListenAndServeFunc(logger, tlsCertPath, tlsKeyPath, clientCAPath)
	if err != nil {
		logger.Fatal("Error setting up health/metric/pprof service: %v", err)
	}

	go func() {
		if err := listenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err)
		}
	}()

	mgr, err := Manager(ctx, *debug)
	if err != nil {
		logger.WithError(err).Fatalf("error configuring controller manager")
	}
	config := mgr.GetConfig()

	versionedConfigClient, err := configclientset.NewForConfig(config)
	if err != nil {
		logger.WithError(err).Fatal("error configuring openshift proxy client")
	}
	configClient, err := configv1client.NewForConfig(config)
	if err != nil {
		logger.WithError(err).Fatal("error configuring config client")
	}
	opClient, err := operatorclient.NewClientFromRestConfig(config)
	if err != nil {
		logger.WithError(err).Fatal("error configuring operator client")
	}
	crClient, err := versioned.NewForConfig(config)
	if err != nil {
		logger.WithError(err).Fatal("error configuring custom resource client")
	}

	// Create a new instance of the operator.
	op, err := olm.NewOperator(
		ctx,
		olm.WithLogger(logger),
		olm.WithWatchedNamespaces(namespaces...),
		olm.WithResyncPeriod(queueinformer.ResyncWithJitter(*wakeupInterval, 0.2)),
		olm.WithExternalClient(crClient),
		olm.WithOperatorClient(opClient),
		olm.WithRestConfig(config),
		olm.WithConfigClient(versionedConfigClient),
	)
	if err != nil {
		logger.WithError(err).Fatalf("error configuring operator")
		return
	}

	op.Run(ctx)
	<-op.Ready()

	if *writeStatusName != "" {
		reconciler, err := openshift.NewClusterOperatorReconciler(
			openshift.WithClient(mgr.GetClient()),
			openshift.WithScheme(mgr.GetScheme()),
			openshift.WithLog(ctrl.Log.WithName("controllers").WithName("clusteroperator")),
			openshift.WithName(*writeStatusName),
			openshift.WithNamespace(*namespace),
			openshift.WithSyncChannel(op.AtLevel()),
			openshift.WithOLMOperator(),
		)
		if err != nil {
			logger.WithError(err).Fatalf("error configuring openshift integration")
			return
		}

		if err := reconciler.SetupWithManager(mgr); err != nil {
			logger.WithError(err).Fatalf("error configuring openshift integration")
			return
		}
	}

	if *writePackageServerStatusName != "" {
		logger.Info("Initializing cluster operator monitor for package server")

		names := *writePackageServerStatusName
		discovery := opClient.KubernetesInterface().Discovery()
		monitor, sender := operatorstatus.NewMonitor(logger, discovery, configClient, names)

		handler := operatorstatus.NewCSVWatchNotificationHandler(logger, op.GetCSVSetGenerator(), op.GetReplaceFinder(), sender)
		op.RegisterCSVWatchNotification(handler)

		go monitor.Run(op.Done())
	}

	// Start the controller manager
	if err := mgr.Start(ctx); err != nil {
		logger.WithError(err).Fatal("controller manager stopped")
	}

	<-op.Done()
}
