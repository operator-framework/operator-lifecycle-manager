package main

import (
	"flag"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/coreos/alm/pkg/api/apis/catalogsource/v1alpha1"
	"github.com/coreos/alm/pkg/api/client"
	"github.com/coreos/alm/pkg/controller/operators/catalog"
	"github.com/coreos/alm/pkg/lib/signals"
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

	// Create an instance of a client for accessing ALM types
	crClient, err := client.NewClient(*kubeConfigPath)
	if err != nil {
		log.Fatalf("failed to bootstrap initial catalogs: %s", err)
	}

	// TODO: catalog sources must be hardcoded because x-operator does not support CR creation. fix.
	_, err = crClient.CatalogsourceV1alpha1().CatalogSources(*catalogNamespace).Create(&v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tectonic-ocs",
			Namespace: *catalogNamespace,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Name:        "tectonic-ocs",
			DisplayName: "Tectonic Open Cloud Services",
			Publisher:   "CoreOS, Inc.",
			SourceType:  "internal",
			ConfigMap:   "tectonic-ocs",
			Secrets: []string{
				"coreos-pull-secret",
			},
		},
	})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		log.Fatalf("failed to bootstrap initial catalogs: %s", err)
	}
	_, err = crClient.CatalogsourceV1alpha1().CatalogSources(*catalogNamespace).Create(&v1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tectonic-components",
			Namespace: *catalogNamespace,
		},
		Spec: v1alpha1.CatalogSourceSpec{
			Name:        "tectonic-components",
			DisplayName: "Tectonic Components",
			Publisher:   "CoreOS, Inc.",
			SourceType:  "internal",
			ConfigMap:   "tectonic-components",
			Secrets: []string{
				"coreos-pull-secret",
			},
		},
	})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		log.Fatalf("failed to bootstrap initial catalogs: %s", err)
	}

	// Create a new instance of the operator.
	catalogOperator, err := catalog.NewOperator(*kubeConfigPath, *wakeupInterval, *catalogNamespace, strings.Split(*watchedNamespaces, ",")...)
	if err != nil {
		log.Panicf("error configuring operator: %s", err.Error())
	}

	catalogOperator.Run(stopCh)
}
