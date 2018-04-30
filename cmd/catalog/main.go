package main

import (
	"flag"
	"net/http"
	"strings"
	"time"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	log "github.com/sirupsen/logrus"
)

const (
	defaultWakeupInterval   = 15 * time.Minute
	defaultCatalogNamespace = "tectonic-system"
)

// config flags defined globally so that they appear on the test binary as well
var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")

	wakeupInterval = flag.Duration(
		"interval", defaultWakeupInterval, "wakeup interval")

	watchedNamespaces = flag.String(
		"watchedNamespaces", "", "comma separated list of namespaces that catalog watches, leave empty to watch all namespaces")

	catalogNamespace = flag.String(
		"namespace", defaultCatalogNamespace, "namespace where catalog will run and install catalog resources")

	debug = flag.Bool(
		"debug", false, "use debug log level")
)

func main() {
	stopCh := signals.SetupSignalHandler()

	// Parse the command-line flags.
	flag.Parse()

	if *debug {
		log.SetLevel(log.DebugLevel)
	}

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	// Create a new instance of the operator.
	catalogOperator, err := catalog.NewOperator(*kubeConfigPath, *wakeupInterval, *catalogNamespace, strings.Split(*watchedNamespaces, ",")...)
	if err != nil {
		log.Panicf("error configuring operator: %s", err.Error())
	}

	catalogOperator.Run(stopCh)
}
