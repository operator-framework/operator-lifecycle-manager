package main

import (
	"os"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/operator-framework/operator-registry/cmd/opm/root"
	registrylib "github.com/operator-framework/operator-registry/pkg/registry"
)

func main() {
	showAlphaHelp := os.Getenv("HELP_ALPHA") == "true"
	cmd := root.NewCmd(showAlphaHelp)
	if err := cmd.Execute(); err != nil {
		agg, ok := err.(utilerrors.Aggregate)
		if !ok {
			os.Exit(1)
		}
		for _, e := range agg.Errors() {
			if _, ok := e.(registrylib.BundleImageAlreadyAddedErr); ok {
				os.Exit(2)
			}
			if _, ok := e.(registrylib.PackageVersionAlreadyAddedErr); ok {
				os.Exit(3)
			}
		}
		os.Exit(1)
	}
}
