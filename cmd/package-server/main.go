package main

import (
	"flag"
	"os"

	log "github.com/sirupsen/logrus"
	"k8s.io/component-base/logs"
	"k8s.io/klog/v2"

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
	// klog flags used by dependencies need to be explicitly initialized now
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	// Opt into the new klog behavior where -stderrthreshold is honored even
	// when -logtostderr=true (see kubernetes/klog#212, kubernetes/klog#432).
	klogFlags.Set("legacy_stderr_threshold_behavior", "false") //nolint:errcheck
	klogFlags.Set("stderrthreshold", "INFO")                   //nolint:errcheck

	cmd.Flags().AddGoFlagSet(klogFlags)

	if err := cmd.Flags().Parse(flag.Args()); err != nil {
		log.Fatal(err)
	}
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
