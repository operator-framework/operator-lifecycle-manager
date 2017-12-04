//+build !test

package main

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/coreos-inc/alm/pkg/operators/catalog"
)

const (
	defaultWakeupInterval   = 15 * time.Minute
	defaultCatalogNamespace = "tectonic-system"
)

func main() {
	// Parse the command-line flags.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	kubeConfigPath := flag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")

	wakeupInterval := flag.Duration(
		"interval", defaultWakeupInterval, "wakeup interval")

	watchedNamespaces := flag.String(
		"watchedNamespaces", "", "comma separated list of namespaces that catalog watches, leave empty to watch all namespaces")

	catalogNamespace := flag.String(
		"namespace", defaultCatalogNamespace, "namespace where catalog will run and install catalog resources")

	debug := flag.Bool(
		"debug", false, "use debug log level")
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

	// TODO: Handle any signals to shutdown cleanly.
	stop := make(chan struct{})
	catalogOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
