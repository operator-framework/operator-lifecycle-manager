package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/coreos-inc/alm/config"
	"github.com/coreos-inc/alm/operators/alm"
	log "github.com/sirupsen/logrus"
)

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hi there, I love %s!", r.URL.Path[1:])
}

func main() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	kubeConfigPath := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	almConfigPath := flag.String("almConfig", "", "absolute path to the almConfig file")
	flag.Parse()
	cfg, err := config.LoadConfig(*almConfigPath)
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/healthz", handler)
	go http.ListenAndServe(":8080", nil)
	almOperator, err := alm.NewALMOperator(*kubeConfigPath, cfg)
	if err != nil {
		panic(fmt.Errorf("error configuring operator: %s", err))
	}

	stop := make(chan struct{})
	almOperator.Run(stop)
	close(stop)
	panic("unreachable")
}
