package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/sirupsen/logrus"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	utilclock "k8s.io/utils/clock"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalog"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/catalogtemplate"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorstatus"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/server"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
)

const (
	catalogNamespaceEnvVarName  = "GLOBAL_CATALOG_NAMESPACE"
	defaultWakeupInterval       = 6 * time.Hour
	defaultCatalogNamespace     = "olm"
	defaultConfigMapServerImage = "quay.io/operator-framework/configmap-operator-registry:latest"
	defaultOPMImage             = "quay.io/operator-framework/opm:latest"
	defaultUtilImage            = "quay.io/operator-framework/olm:latest"
	defaultOperatorName         = ""
	defaultWorkLoadUserID       = int64(1001)
)

// config flags defined globally so that they appear on the test binary as well

func init() {
	metrics.RegisterCatalog()
}

func main() {
	cmd := newRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func (o *options) run(ctx context.Context, logger *logrus.Logger) error {
	// If the catalogNamespaceEnvVarName environment variable is set, then  update the value of catalogNamespace.
	if catalogNamespaceEnvVarValue := os.Getenv(catalogNamespaceEnvVarName); catalogNamespaceEnvVarValue != "" {
		logger.Infof("%s environment variable is set. Updating Global Catalog Namespace to %s", catalogNamespaceEnvVarName, catalogNamespaceEnvVarValue)
		o.catalogNamespace = catalogNamespaceEnvVarValue
	}

	listenAndServe, err := server.GetListenAndServeFunc(
		server.WithLogger(logger),
		server.WithTLS(&o.tlsCertPath, &o.tlsKeyPath, &o.clientCAPath),
		server.WithDebug(o.debug),
	)
	if err != nil {
		return fmt.Errorf("error setting up health/metric/pprof service: %v", err)
	}

	go func() {
		if err := listenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error(err)
		}
	}()

	// create a config client for operator status
	config, err := clientcmd.BuildConfigFromFlags("", o.kubeconfig)
	if err != nil {
		return fmt.Errorf("error configuring client: %s", err.Error())
	}
	configClient, err := configv1client.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("error configuring client: %s", err.Error())
	}
	opClient := operatorclient.NewClientFromConfig(o.kubeconfig, logger)
	crClient, err := client.NewClient(o.kubeconfig)
	if err != nil {
		return fmt.Errorf("error configuring client: %s", err.Error())
	}

	workloadUserID := int64(-1)
	if o.setWorkloadUserID {
		workloadUserID = defaultWorkLoadUserID
	}

	// the scheme is used by the catalog operator to create
	// a validatingroundtripper that ensures that all created
	// resources are appropriately labeled
	scheme := k8sscheme.Scheme
	_ = apiextensionsv1.AddToScheme(scheme)   // required by opClient
	_ = apiregistrationv1.AddToScheme(scheme) // required by opClient

	// TODO(tflannag): Use options pattern for catalog operator
	// Create a new instance of the operator.
	op, err := catalog.NewOperator(
		ctx,
		o.kubeconfig,
		utilclock.RealClock{},
		logger,
		o.wakeupInterval,
		o.configMapServerImage,
		o.opmImage,
		o.utilImage,
		o.catalogNamespace,
		scheme,
		o.installPlanTimeout,
		o.bundleUnpackTimeout,
		workloadUserID,
	)
	if err != nil {
		return fmt.Errorf("error configuring catalog operator: %s", err.Error())
	}

	opCatalogTemplate, err := catalogtemplate.NewOperator(
		ctx,
		o.kubeconfig,
		logger,
		o.wakeupInterval,
		o.catalogNamespace,
	)
	if err != nil {
		return fmt.Errorf("error configuring catalog template operator: %s", err.Error())
	}

	op.Run(ctx)
	<-op.Ready()

	opCatalogTemplate.Run(ctx)
	<-opCatalogTemplate.Ready()

	if o.writeStatusName != "" {
		operatorstatus.MonitorClusterStatus(o.writeStatusName, op.AtLevel(), op.Done(), opClient, configClient, crClient, logger)
	}

	<-op.Done()

	return nil
}
