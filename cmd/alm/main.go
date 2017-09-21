package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/coreos-inc/alm/alm"
)

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hi there, I love %s!", r.URL.Path[1:])
}

func main() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	kubeConfigPath := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	flag.Parse()

	http.HandleFunc("/healthz", handler)
	go http.ListenAndServe(":8080", nil)

	almOperator, err := alm.New(*kubeConfigPath)
	if err != nil {
		panic(fmt.Errorf("error configuring operator: %s", err))
	}
	stop := make(chan struct{})
	almOperator.Run(stop)
	close(stop)

	panic("unreachable")
}
