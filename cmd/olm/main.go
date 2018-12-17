package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-version-operator/lib/resourcemerge"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/install"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/operators/olm"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/metrics"
	olmversion "github.com/operator-framework/operator-lifecycle-manager/pkg/version"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultWakeupInterval      = 5 * time.Minute
	defaultOperatorName        = "openshift-operator-lifecycle-manager"
	openshiftConfigServiceName = "v1.config.openshift.io"
)

// config flags defined globally so that they appear on the test binary as well
var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")

	wakeupInterval = flag.Duration(
		"interval", defaultWakeupInterval, "wake up interval")

	watchedNamespaces = flag.String(
		"watchedNamespaces", "", "comma separated list of namespaces for olm operator to watch. "+
			"If not set, or set to the empty string (e.g. `-watchedNamespaces=\"\"`), "+
			"olm operator will watch all namespaces in the cluster.")

	writeStatusName = flag.String(
		"writeStatusName", defaultOperatorName, "ClusterOperator name in which to write status, set to \"\" to disable.")

	debug = flag.Bool(
		"debug", false, "use debug log level")

	version = flag.Bool("version", false, "displays olm version")
)

func init() {
	metrics.Register()
}

// main function - entrypoint to OLM operator
func main() {
	stopCh := signals.SetupSignalHandler()

	// Parse the command-line flags.
	flag.Parse()

	// Check if version flag was set
	if *version {
		fmt.Print(olmversion.String())

		// Exit early
		os.Exit(0)
	}

	// `namespaces` will always contain at least one entry: if `*watchedNamespaces` is
	// the empty string, the resulting array will be `[]string{""}`.
	namespaces := strings.Split(*watchedNamespaces, ",")
	for _, ns := range namespaces {
		if ns == v1.NamespaceAll {
			namespaces = []string{v1.NamespaceAll}
			break
		}
	}

	// Create a client for OLM
	crClient, err := client.NewClient(*kubeConfigPath)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}

	logger := log.New()

	// Set log level to debug if `debug` flag set
	if *debug {
		logger.SetLevel(log.DebugLevel)
	}
	logger.Infof("log level %s", logger.Level)

	opClient := operatorclient.NewClientFromConfig(*kubeConfigPath, logger)

	// create a config client for operator status
	config, err := clientcmd.BuildConfigFromFlags("", *kubeConfigPath)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}
	configClient, err := configv1client.NewForConfig(config)
	if err != nil {
		log.Fatalf("error configuring client: %s", err.Error())
	}

	// Create a new instance of the operator.
	operator, err := olm.NewOperator(logger, crClient, opClient, &install.StrategyResolver{}, *wakeupInterval, namespaces)

	if err != nil {
		log.Fatalf("error configuring operator: %s", err.Error())
	}

	// Serve a health check.
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// TODO: both of the following require vendor updates (add k8s.io/apiserver and update prometheus)
	//healthz.InstallHandler(mux) //(less code)
	//mux.Handle("/metrics", promhttp.Handler()) //other form is deprecated
	http.Handle("/metrics", prometheus.Handler())
	go http.ListenAndServe(":8080", nil)

	ready, done := operator.Run(stopCh)
	<-ready

	if *writeStatusName != "" {
		_, err := opClient.GetAPIService(openshiftConfigServiceName)
		if k8serrors.IsNotFound(err) {
			log.Info("Did not find openshift config API service, skipping status update")
		} else if err != nil {
			log.Fatalf("APIService check error: %v", err)
		} else {
			existing, err := configClient.ClusterOperators().Get(*writeStatusName, metav1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				log.Info("Existing cluster operator not found, creating")
				created, err := configClient.ClusterOperators().Create(&configv1.ClusterOperator{
					ObjectMeta: metav1.ObjectMeta{
						Name: *writeStatusName,
					},
				})
				if err != nil {
					log.Fatalf("ClusterOperator create failed: %v\n", err)
				}

				created.Status = configv1.ClusterOperatorStatus{
					Conditions: []configv1.ClusterOperatorStatusCondition{
						configv1.ClusterOperatorStatusCondition{
							Type:               configv1.OperatorAvailable,
							Status:             configv1.ConditionTrue,
							Message:            fmt.Sprintf("Done deploying %s.", olmversion.OLMVersion),
							LastTransitionTime: metav1.Now(),
						},
					},
					Version: olmversion.Full(),
				}
				_, err = configClient.ClusterOperators().UpdateStatus(created)
				if err != nil {
					log.Fatalf("ClusterOperator update status failed: %v", err)
				}
			} else if err != nil {
				log.Fatalf("ClusterOperators get failed: %v", err)
			} else {
				resourcemerge.SetOperatorStatusCondition(&existing.Status.Conditions, configv1.ClusterOperatorStatusCondition{
					Type:               configv1.OperatorAvailable,
					Status:             configv1.ConditionTrue,
					Message:            fmt.Sprintf("Done deploying %s.", olmversion.OLMVersion),
					LastTransitionTime: metav1.Now(),
				})
				if existing.Status.Version != olmversion.Full() {
					// if a cluster wide upgrade has occurred, hopefully any existing operator statuses have been deleted
					log.Infof("Updating version from %v to %v\n", existing.Status.Version, olmversion.Full())
				}
				existing.Status.Version = olmversion.Full()
				_, err = configClient.ClusterOperators().UpdateStatus(existing)
				if err != nil {
					log.Fatalf("ClusterOperator update status failed: %v", err)
				}
			}
		}
	}

	<-done
}

