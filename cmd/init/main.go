package main

import (
	"context"
	"flag"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/signals"
)

var (
	kubeConfigPath = flag.String(
		"kubeconfig", "", "absolute path to the kubeconfig file")
	namespace = flag.String(
		"namespace", "", "namespace where init runs")
	crc versioned.Interface
	c   operatorclient.ClientInterface
)

const (
	pollInterval = 1 * time.Second
	pollDuration = 5 * time.Minute
)

type checkResourceFunc func() error
type deleteResourceFunc func() error

func main() {
	// Get exit signal context
	ctx, cancel := context.WithCancel(signals.Context())
	defer cancel()

	logger := log.New()

	// Parse the command-line flags.
	flag.Parse()

	c = operatorclient.NewClientFromConfig(*kubeConfigPath, logger)

	if client, err := client.NewClient(*kubeConfigPath); err != nil {
		logger.WithError(err).Fatalf("error configuring client")
	} else {
		crc = client
	}

	if err := waitForDelete(checkCatalogSource("olm-operators"), deleteCatalogSource("olm-operators")); err != nil {
		log.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkConfigMap("olm-operators"), deleteConfigMap("olm-operators")); err != nil {
		log.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkSubscription("packageserver"), deleteSubscription("packageserver")); err != nil {
		log.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkClusterServiceVersion("packageserver.v0.10.0"), deleteClusterServiceVersion("packageserver.v0.10.0")); err != nil {
		log.WithError(err).Fatal("couldn't clean previous release")
	}

	if err := waitForDelete(checkClusterServiceVersion("packageserver.v0.9.0"), deleteClusterServiceVersion("packageserver.v0.9.0")); err != nil {
		log.WithError(err).Fatal("couldn't clean previous release")
	}

	ctx.Done()
}

func checkClusterServiceVersion(name string) checkResourceFunc {
	return func() error {
		_, err := crc.OperatorsV1alpha1().ClusterServiceVersions(*namespace).Get(name, metav1.GetOptions{})
		return err
	}
}

func deleteClusterServiceVersion(name string) deleteResourceFunc {
	return func() error {
		return crc.OperatorsV1alpha1().ClusterServiceVersions(*namespace).Delete(name, metav1.NewDeleteOptions(0))
	}
}

func checkSubscription(name string) checkResourceFunc {
	return func() error {
		_, err := crc.OperatorsV1alpha1().Subscriptions(*namespace).Get(name, metav1.GetOptions{})
		return err
	}
}

func deleteSubscription(name string) deleteResourceFunc {
	return func() error {
		return crc.OperatorsV1alpha1().Subscriptions(*namespace).Delete(name, metav1.NewDeleteOptions(0))
	}
}

func checkConfigMap(name string) checkResourceFunc {
	return func() error {
		_, err := c.KubernetesInterface().CoreV1().ConfigMaps(*namespace).Get(name, metav1.GetOptions{})
		return err
	}
}

func deleteConfigMap(name string) deleteResourceFunc {
	return func() error {
		return c.KubernetesInterface().CoreV1().ConfigMaps(*namespace).Delete(name, metav1.NewDeleteOptions(0))
	}
}

func checkCatalogSource(name string) checkResourceFunc {
	return func() error {
		_, err := crc.OperatorsV1alpha1().CatalogSources(*namespace).Get(name, metav1.GetOptions{})
		return err
	}
}

func deleteCatalogSource(name string) deleteResourceFunc {
	return func() error {
		return crc.OperatorsV1alpha1().CatalogSources(*namespace).Delete(name, metav1.NewDeleteOptions(0))
	}
}

func waitForDelete(checkResource checkResourceFunc, deleteResource deleteResourceFunc) error {
	if err := checkResource(); err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err := deleteResource(); err != nil {
		return err
	}
	var err error
	err = wait.Poll(pollInterval, pollDuration, func() (bool, error) {
		err := checkResource()
		if errors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})

	return err
}
