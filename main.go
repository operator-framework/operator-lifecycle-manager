package main

import (
	"context"
	"fmt"
	"github.com/coreos-inc/alm/alm"
	"net/http"
	"os"
)

func doesShitWork() {
	fmt.Println("testing...")
	installer := alm.MockInstall{"does this shit even work?"}
	err := installer.Install(context.TODO(), "alm-system", []byte("wat"))
	fmt.Printf("Done. err=%s\n", err)
	os.Exit(0)
}

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hi there, I love %s!", r.URL.Path[1:])
	doesShitWork()
}

func main() {
	http.HandleFunc("/healthz", handler)
	http.ListenAndServe(":8080", nil)
}
