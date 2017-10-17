package main

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	source "github.com/coreos-inc/alm/catalog"
	"github.com/coreos-inc/alm/operators/catalog"
)

func main() {
	// Parse the command-line flags.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	kubeConfigPath := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	wakeupInterval := flag.Duration("interval", 15*time.Minute, "wake up interval")
	namespaces := flag.String("namespaces", "", "comma separated list of namespaces")
	catalogDirectory := flag.String("directory", "catalog_resources", "path to directory with resources to load into the in-memory catalog")
	flag.Parse()

	inMemoryCatalog, err := source.NewInMemoryFromDirectory(*catalogDirectory)
	if err != nil {
		panic("Error loading in memory catalog from " + *catalogDirectory)
	}

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	// Create a new instance of the operator.
	catalogOperator, err := catalog.NewOperator(*kubeConfigPath, *wakeupInterval, []source.Source{inMemoryCatalog}, strings.Split(*namespaces, ",")...)
	if err != nil {
		panic("error configuring operator: " + err.Error())
	}

	// TODO: Handle any signals to shutdown cleanly.
	stop := make(chan struct{})
	catalogOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
