package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/component-base/logs"
	"k8s.io/klog"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/porcelain-server/cmd/server"
)

func main() {
	// Get exit signal context
	ctx, cancel := context.WithCancel(signals.Context())
	defer cancel()

	logs.InitLogs()
	defer logs.FlushLogs()

	options := server.NewPorcelainServerOptions(os.Stdout, os.Stderr)
	cmd := server.NewCommandStartPorcelainServer(ctx, options)
	cmd.Flags().AddGoFlagSet(flag.CommandLine)
	if err := cmd.Execute(); err != nil {
		klog.Fatal(err)
	}
}
