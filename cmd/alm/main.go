package main

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/coreos-inc/alm/operators/alm"
)

func main() {
	// Parse the command-line flags.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	kubeConfigPath := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	wakeupInterval := flag.Duration("interval", 5*time.Minute, "wake up interval")
	watchedNamespaces := flag.String("watchedNamespaces", "", "comma separated list of namespaces that alm operator will watch")
	flag.Parse()

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	// Create a new instance of the operator.
	almOperator, err := alm.NewALMOperator(*kubeConfigPath, *wakeupInterval, strings.Split(*watchedNamespaces, ",")...)
	if err != nil {
		log.Fatalf("error configuring operator: %s", err.Error())
	}

	// TODO: Handle any signals to shutdown cleanly.
	stop := make(chan struct{})
	almOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
