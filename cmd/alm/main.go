//+build !test

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/coreos-inc/alm/pkg/operators/alm"
)

const (
	envOperatorName         = "OPERATOR_NAME"
	envOperatorNamespace    = "OPERATOR_NAMESPACE"
	ALMManagedAnnotationKey = "alm-manager"

	defaultWakeupInterval = 5 * time.Minute
)

// helper function for required env vars
func envOrDie(varname, description string) string {
	val := os.Getenv(varname)
	if len(val) == 0 {
		log.Fatalf("must set env %s - %s", varname, description)
	}
	return val
}

// main function - entrypoint to ALM operator
func main() {

	// Parse the command-line flags.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	kubeConfigPath := flag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")

	wakeupInterval := flag.Duration(
		"interval", defaultWakeupInterval, "wake up interval")

	watchedNamespaces := flag.String(
		"watchedNamespaces", "", "comma separated list of namespaces that alm operator will watch"+
			"\n***If not set, or value is the empty string (e.g. `-watchedNamespaces=\"\"`), "+
			"alm operator will watch all namespaces in the cluster***")

	debug := flag.Bool(
		"debug", false, "use debug log level")

	flag.Parse()

	// Env Vars
	operatorNamespace := envOrDie(
		envOperatorName, "used to set annotation indicating which ALM operator manages a namespace")

	operatorName := envOrDie(
		envOperatorNamespace, "used to distinguish ALM operators of the same name")

	annotation := map[string]string{
		ALMManagedAnnotationKey: fmt.Sprintf("%s.%s", operatorNamespace, operatorName),
	}

	// Set log level to debug if `debug` flag set
	if *debug {
		log.SetLevel(log.DebugLevel)
	}

	// `namespaces` will always contain at least one entry: if `*watchedNamespaces` is
	// the empty string, the resulting array will be `[]string{""}`.
	namespaces := strings.Split(*watchedNamespaces, ",")

	// Create a new instance of the operator.
	almOperator, err := alm.NewALMOperator(*kubeConfigPath, *wakeupInterval, annotation, namespaces)

	if err != nil {
		log.Fatalf("error configuring operator: %s", err.Error())
	}

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	// TODO: Handle any signals to shutdown cleanly.
	stop := make(chan struct{})
	almOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
