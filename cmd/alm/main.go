//+build !test

package main

import (
	"flag"
	"net/http"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"fmt"

	"github.com/coreos-inc/alm/pkg/annotater"
	"github.com/coreos-inc/alm/pkg/operators/alm"
)

const (
	EnvOperatorName         = "OPERATOR_NAME"
	EnvOperatorNamespace    = "OPERATOR_NAMESPACE"
	ALMManagedAnnotationKey = "alm-manager"
)

func main() {
	// Parse the command-line flags.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	kubeConfigPath := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	wakeupInterval := flag.Duration("interval", 5*time.Minute, "wake up interval")
	watchedNamespaces := flag.String("watchedNamespaces", "", "comma separated list of namespaces that alm operator will watch")
	flag.Parse()

	namespace := os.Getenv(EnvOperatorNamespace)
	if len(namespace) == 0 {
		log.Fatalf("must set env %s", EnvOperatorNamespace)
	}
	name := os.Getenv(EnvOperatorName)
	if len(name) == 0 {
		log.Fatalf("must set env %s", EnvOperatorName)
	}

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	// Create a new instance of the operator.
	namespaces := strings.Split(*watchedNamespaces, ",")
	almOperator, err := alm.NewALMOperator(*kubeConfigPath, *wakeupInterval, namespace, name, namespaces...)
	if err != nil {
		log.Fatalf("error configuring operator: %s", err.Error())
	}

	namespaceAnnotater := annotater.NewAnnotator(almOperator.OpClient)
	annotations := map[string]string{
		ALMManagedAnnotationKey: fmt.Sprintf("%s.%s", namespace, name),
	}
	if err := namespaceAnnotater.AnnotateNamespaces(namespaces, annotations); err != nil {
		log.Fatalf("error annotating namespaces: %s", err.Error())
	}

	// TODO: Handle any signals to shutdown cleanly.
	stop := make(chan struct{})
	almOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
