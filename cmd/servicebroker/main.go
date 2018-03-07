package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"

	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/pmorie/osb-broker-lib/pkg/metrics"
	"github.com/pmorie/osb-broker-lib/pkg/rest"
	"github.com/pmorie/osb-broker-lib/pkg/server"
	prom "github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"

	"github.com/coreos-inc/alm/pkg/server/servicebroker"
)

const (
	defaultWakeupInterval   = 15 * time.Minute
	defaultCatalogNamespace = "tectonic-system"
	defaultPort             = 8005
)

var options struct {
	servicebroker.Options

	Debug      bool
	KubeConfig string
	Port       int
	TLSCert    string
	TLSKey     string
}

func init() {
	// Parse the command-line flags.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	flag.IntVar(&options.Port,
		"port", defaultPort, "specify the port for broker to listen on")

	flag.StringVar(&options.TLSCert,
		"tlsCert", "", "base-64 encoded PEM block to use as the certificate for TLS. If '--tlsCert' is used, then '--tlsKey' must also be used. If '--tlsCert' is not used, then TLS will not be used.")

	flag.StringVar(&options.TLSKey,
		"tlsKey", "", "base-64 encoded PEM block to use as the private key matching the TLS certificate. If '--tlsKey' is used, then '--tlsCert' must also be used")

	flag.StringVar(&options.KubeConfig,
		"kubeconfig", "", "absolute path to the kubeconfig file")

	flag.StringVar(&options.Namespace,
		"namespace", "", "namespace to restrict service scope")

	flag.BoolVar(&options.Debug,
		"debug", false, "use debug log level")

	flag.Parse()
}

func main() {
	if options.Debug {
		log.SetLevel(log.DebugLevel)
	}

	if err := run(); err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		log.Fatal(err)
	}

	log.Info("ALM broker stopped")
}

func run() error {
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	go cancelOnInterrupt(ctx, cancelFunc)

	return runWithContext(ctx)
}

func runWithContext(ctx context.Context) error {
	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go http.ListenAndServe(":8080", nil)

	if flag.Arg(0) == "version" {
		fmt.Printf("%s/%s\n", path.Base(os.Args[0]), "0.1.0")
		return nil
	}
	if (options.TLSCert != "" || options.TLSKey != "") && (options.TLSCert == "" || options.TLSKey == "") {
		fmt.Println("To use TLS, both --tlsCert and --tlsKey must be used")
		return nil
	}
	addr := ":" + strconv.Itoa(options.Port)

	// Prom. metrics
	reg := prom.NewRegistry()
	osbMetrics := metrics.New()
	reg.MustRegister(osbMetrics)

	// Create an instance of an ALMBroker
	almBroker, err := servicebroker.NewALMBroker(options.KubeConfig, options.Options)
	if err != nil {
		return err
	}
	api, err := rest.NewAPISurface(almBroker, osbMetrics)
	if err != nil {
		return err
	}

	s := server.New(api, reg)

	glog.Infof("Starting broker!")

	if options.TLSCert != "" && options.TLSKey != "" {
		return s.RunTLS(ctx, addr, options.TLSCert, options.TLSKey)
	}
	return s.Run(ctx, addr)
}

func cancelOnInterrupt(ctx context.Context, f context.CancelFunc) {
	term := make(chan os.Signal)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-term:
			glog.Infof("Received SIGTERM, exiting gracefully...")
			f()
			os.Exit(0)
		case <-ctx.Done():
			os.Exit(0)
		}
	}
}
