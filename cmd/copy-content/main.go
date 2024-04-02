package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/otiai10/copy"
)

func main() {
	catalogSource := flag.String("catalog.from", "", "Path to catalog contents to copy.")
	catalogDestination := flag.String("catalog.to", "", "Path to where catalog contents should be copied.")
	cacheSource := flag.String("cache.from", "", "Path to cache contents to copy.")
	cacheDestination := flag.String("cache.to", "", "Path to where cache contents should be copied.")
	flag.Parse()

	for flagName, value := range map[string]*string{
		"catalog.from": catalogSource,
		"catalog.to":   catalogDestination,
		"cache.from":   cacheSource,
		"cache.to":     cacheDestination,
	} {
		if value == nil || *value == "" {
			fmt.Printf("--%s is required", flagName)
			os.Exit(1)
		}
	}

	for from, to := range map[string]string{
		*catalogSource: *catalogDestination,
		*cacheSource:   *cacheDestination,
	} {
		if err := os.RemoveAll(to); err != nil {
			fmt.Printf("failed to remove %s: %s", to, err)
			os.Exit(1)
		}
		if err := copy.Copy(from, to); err != nil {
			fmt.Printf("failed to copy %s to %s: %s\n", from, to, err)
			os.Exit(1)
		}
	}
}
