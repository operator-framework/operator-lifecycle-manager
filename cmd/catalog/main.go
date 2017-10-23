package main

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	source "github.com/coreos-inc/alm/catalog"
	"github.com/coreos-inc/alm/client"
	"github.com/coreos-inc/alm/operators/catalog"
)

const (
	defaultWakeupInterval   = 15 * time.Minute
	defaultCatalogNamespace = "tectonic-system"
	defaultCatalogDirectory = "/var/catalog_resources"
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

	catalogDirectory := flag.String(
		"directory", defaultCatalogDirectory, "path to directory with resources to load into the in-memory catalog")

	flag.Parse()
	log.SetLevel(log.DebugLevel)
	inMemoryCatalog, err := source.NewInMemoryFromDirectory(*catalogDirectory)
	if err != nil {
		log.Fatalf("Error loading in memory catalog from %s: %s", *catalogDirectory, err.Error())
	}

	alphaCatalogClient, err := client.NewAlphaCatalogEntryClient(*kubeConfigPath)
	if err != nil {
		log.Fatalf("Couldn't create alpha catalog entry client: %s", err.Error())
	}
	catalogStore := source.CustomResourceCatalogStore{
		Client:    alphaCatalogClient,
		Namespace: *catalogNamespace,
	}
	entries, err := catalogStore.Sync(inMemoryCatalog)
	if err != nil {
		log.Fatalf("couldn't sync entries from catalog to AlphaCatalogEntries in cluster: %s", err.Error())
	}
	log.Infof("created %d AlphaCatalogEntry resources", len(entries))

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	// Create a new instance of the operator.
	catalogOperator, err := catalog.NewOperator(*kubeConfigPath, *wakeupInterval,
		[]source.Source{inMemoryCatalog}, strings.Split(*watchedNamespaces, ",")...)
	if err != nil {
		log.Panicf("error configuring operator: %s", err.Error())
	}

	// TODO: Handle any signals to shutdown cleanly.
	stop := make(chan struct{})
	catalogOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
