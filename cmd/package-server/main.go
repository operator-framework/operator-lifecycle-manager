package main

import (
	"flag"
	"os"

	log "github.com/sirupsen/logrus"
	"k8s.io/component-base/logs"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/server"
)

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	ctx := signals.Context()
	options := server.NewPackageServerOptions(os.Stdout, os.Stderr)
	cmd := server.NewCommandStartPackageServer(ctx, options)
	cmd.Flags().AddGoFlagSet(flag.CommandLine)
	if err := cmd.Flags().Parse(flag.Args()); err != nil {
		log.Fatal(err)
	}
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
