package main

import (
	"flag"
	"os"

	log "github.com/sirupsen/logrus"
	k8sserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/util/logs"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/server"
)

func main() {
	logs.InitLogs()
	defer logs.FlushLogs()

	stopCh := k8sserver.SetupSignalHandler()
	options := server.NewPackageServerOptions(os.Stdout, os.Stderr)
	cmd := server.NewCommandStartPackageServer(options, stopCh)
	cmd.Flags().AddGoFlagSet(flag.CommandLine)
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
