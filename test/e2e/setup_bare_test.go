// +build bare

package e2e

import (
	"context"
	"flag"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilclock "k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/tools/clientcmd"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
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

	communityOperators = flag.String(
		"communityOperators",
		"quay.io/operator-framework/upstream-community-operators@sha256:098457dc5e0b6ca9599bd0e7a67809f8eca397907ca4d93597380511db478fec",
		"reference to upstream-community-operators image")

	dummyImage = flag.String(
		"dummyImage",
		"redis",
		"dummy image to treat as an operator in tests")

	testNamespace           = ""
	operatorNamespace       = ""
	communityOperatorsImage = ""
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
	communityOperatorsImage = *communityOperators

	cleaner = newNamespaceCleaner(testNamespace)
	namespaces := strings.Split(*watchedNamespaces, ",")

	// Get exit signal context
	ctx, cancel := context.WithCancel(signals.Context())
	defer cancel()

	config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}

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
	olmlogger.SetLevel(logrus.DebugLevel)
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
	catlogger.SetLevel(logrus.DebugLevel)
	cmw := io.MultiWriter(os.Stderr, catLog)
	catlogger.SetOutput(cmw)
	catlogger.SetFormatter(&logrus.TextFormatter{
		ForceColors:      true,
		DisableTimestamp: true,
	})

	// start operators
	olmOperator, err := olm.NewOperator(
		ctx,
		olm.WithLogger(olmlogger),
		olm.WithWatchedNamespaces(namespaces...),
		olm.WithResyncPeriod(time.Minute),
		olm.WithExternalClient(crClient),
		olm.WithOperatorClient(olmOpClient),
		olm.WithRestConfig(config),
	)
	if err != nil {
		logrus.WithError(err).Fatalf("error configuring olm")
	}
	olmOperator.Run(ctx)
	catalogOperator, err := catalog.NewOperator(ctx, *kubeConfigPath, utilclock.RealClock{}, catlogger, time.Minute, "quay.io/operatorframework/configmap-operator-registry:latest", *namespace, namespaces...)
	if err != nil {
		logrus.WithError(err).Fatalf("error configuring catalog")
	}
	catalogOperator.Run(ctx)
	<-olmOperator.Ready()
	<-catalogOperator.Ready()

	c, err := client.NewClient(*kubeConfigPath)
	if err != nil {
		panic(err)
	}

	_, err = c.OperatorsV1().OperatorGroups(testNamespace).Create(&v1.OperatorGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "opgroup",
			Namespace: testNamespace,
		},
	})
	if err != nil {
		panic(err)
	}

	// run tests
	os.Exit(m.Run())
}
