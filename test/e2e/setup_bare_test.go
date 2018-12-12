// +build bare

package e2e

import (
	"flag"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
)

var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "path to the kubeconfig file")

	watchedNamespaces = flag.String(
		"watchedNamespaces", "", "comma separated list of namespaces for alm operator to watch. "+
			"If not set, or set to the empty string (e.g. `-watchedNamespaces=\"\"`), "+
			"olm operator will watch all namespaces in the cluster.")

	namespace = flag.String(
		"namespace", "", "namespace where tests will run")

	olmNamespace = flag.String(
		"olmNamespace", "", "namespace where olm is running")

	testNamespace     = ""
	operatorNamespace = ""
)

func TestMain(m *testing.M) {
	if err := flag.Set("logtostderr", "true"); err != nil {
		panic(err)
	}
	flag.Parse()

	testNamespace = *namespace
	if testNamespace == "" {
		testNamespaceBytes, err := ioutil.ReadFile("e2e.namespace")
		if err != nil || testNamespaceBytes == nil {
			panic("no namespace set")
		}
		testNamespace = string(testNamespaceBytes)
	}
	operatorNamespace = *olmNamespace
	cleaner = newNamespaceCleaner(testNamespace)
	namespaces := strings.Split(*watchedNamespaces, ",")

	olmStopCh := make(chan struct{}, 1)
	catalogStopCh := make(chan struct{}, 1)

	// operator dependencies
	crClient, err := client.NewClient(*kubeConfigPath)
	if err != nil {
		logrus.WithError(err).Fatalf("error configuring client")
	}

	olmLog, err := os.Create("test/log/e2e-olm.log")
	if err != nil {
		panic(err)
	}
	defer olmLog.Close()
	olmlogger := logrus.New()
	mw := io.MultiWriter(os.Stderr, olmLog)
	olmlogger.SetOutput(mw)
	olmlogger.SetFormatter(&logrus.TextFormatter{
		ForceColors:      true,
		DisableTimestamp: true,
	})
	olmOpClient := operatorclient.NewClientFromConfig(*kubeConfigPath, olmlogger)

	catLog, err := os.Create("test/log/e2e-catalog.log")
	if err != nil {
		panic(err)
	}
	defer catLog.Close()
	catlogger := logrus.New()
	cmw := io.MultiWriter(os.Stderr, catLog)
	catlogger.SetOutput(cmw)
	catlogger.SetFormatter(&logrus.TextFormatter{
		ForceColors:      true,
		DisableTimestamp: true,
	})

	// start operators
	olmOperator, err := olm.NewOperator(olmlogger, crClient, olmOpClient, &install.StrategyResolver{}, time.Minute, namespaces)
	if err != nil {
		logrus.WithError(err).Fatalf("error configuring olm")
	}
	olmready, _ := olmOperator.Run(olmStopCh)
	catalogOperator, err := catalog.NewOperator(*kubeConfigPath, catlogger, time.Minute, "quay.io/operatorframework/configmap-operator-registry:latest", *namespace, namespaces...)
	if err != nil {
		logrus.WithError(err).Fatalf("error configuring catalog")
	}
	catready, _ := catalogOperator.Run(catalogStopCh)
	<-olmready
	<-catready

	// run tests
	os.Exit(m.Run())
}
