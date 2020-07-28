package bundle

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/sirupsen/logrus"
	"strings"
	"time"
)

type Registry struct {
	url        string
	auth       string
	namespace  string
	bundleTool string
	client     operatorclient.ClientInterface
	logger     *logrus.Logger
}

const (
	openshiftregistryFQDN = "image-registry.openshift-image-registry.svc:5000/openshift-operators"
	pollInterval          = 1 * time.Second
	pollDuration          = 5 * time.Minute
    defaultIndexName = "operator-index-registry"
)

func initializeRegistry(testNamespace string, client operatorclient.ClientInterface, logger *logrus.Logger) (*Registry, func(), error) {
	if client == nil {
		return nil, nil, fmt.Errorf("uninitialized operator client")
	}

	if logger == nil {
		logger = logrus.StandardLogger()
	}

	local, err := Local(client)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine if test running locally or on CI: %s", err)
	}

	var registryURL, registryAuth string
	var cleanUpRegistry func()

	if local {
		logger.Debugf("Detected local cluster")
		registryURL, err = CreateDockerRegistry(client, testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating container registry: %s", err)
		}
		logger.Debugf("Created new docker registry pod 'registry'")
		cleanUpRegistry = func() {
			logger.Debugf("Cleaning up docker registry pod")
			DeleteDockerRegistry(client, testNamespace)
		}
		defer func () {
			// delete newly created registry if any setup step fails
			if err != nil {
				logger.Errorf("Unable to create docker registry: %v", err)
				cleanUpRegistry()
			}
		} ()

		logger.Debugf("Waiting for docker registry pod to become heathy")
		// ensure registry pod is ready before attempting port-forwarding
		_, err = awaitPod(client, testNamespace, RegistryName, podReady)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to start registry pod: %v", err)
		}

		logger.Debugf("Forwarding port from pod 5000 to localhost:5000")
		err = RegistryPortForward(testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("port-forwarding local registry: %s", err)
		}

	} else {
		logger.Debugf("Detected remote cluster")
		registryURL = openshiftregistryFQDN
		registryAuth, err = OpenshiftRegistryAuth(client, testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting openshift registry authentication: %s", err)
		}
		cleanUpRegistry = func(){}
	}

	return &Registry{
		url:        registryURL,
		auth:       registryAuth,
		namespace:  testNamespace,
		client:     client,
		logger:     logger,
	}, cleanUpRegistry, nil
}

// Recreates index each time, does not update index. Returns the indexReference to use in CatalogSources
func (r *Registry) CreateBundles(bundles []*Bundle) ([]string, error) {
	bundleRefs := make([]string, 0)

	for _, b := range bundles {
		labels, err := r.GetAnnotations(b)
		if err != nil {
			return nil, fmt.Errorf("failed to get bundle annotations for %s: %v", b.PackageName, err)
		}
		destImageRef := fmt.Sprintf("%s/%s:%s", r.url, b.BundleURLPath, b.Version)
		err = buildAndUploadBundleImage(destImageRef, r.auth, []string{labels[manifestsLabel], labels[metadataLabel]}, labels, &r.logger.Out)
		if err != nil {
			return nil, fmt.Errorf("build step for local bundle image failed %s: %v", b.PackageName, err)
		}
		bundleRefs = append(bundleRefs, destImageRef)
	}
	return bundleRefs, nil
}

func (r *Registry) CreateIndex(indexName string, bundleReferences []string) (string, error) {
	if len(indexName) == 0 {
		indexName = defaultIndexName
	}
	indexReference := fmt.Sprintf("%s/%s:latest", r.url, indexName)

	local, err := Local(r.client)
	if err != nil {
		return "", fmt.Errorf("failed to detect kubeconfig type: %v", err)
	}
	bundleString := strings.Join(bundleReferences, ",")
	if len(bundleString) == 0 {
		bundleString = "\"\""
	}
	if local {
		opmCmd := []string{"opm", "index", "add", "--tag", indexReference, "--pull-tool", "docker", "--build-tool", "docker", "--skip-tls", "--bundles", bundleString}
		pushCmd := []string{"docker", "push", indexReference}
		for _, cmd := range [][]string{opmCmd, pushCmd} {
			if err := execLocal(r.logger.Out, cmd[0], cmd[1:]...); err != nil {
				return "", err
			}
		}
	} else {
		opmCmd := []string{"opm", "index", "add", "--tag", indexReference, "--bundles", bundleString}
		pushCmd := []string{"podman", "push", indexReference}
		argString := fmt.Sprintf("%s && %s", strings.Join(opmCmd, " "), strings.Join(pushCmd, " "))
		if err := r.runIndexBuilderPod(argString); err != nil {
			return "", err
		}
	}
	return indexReference, nil
}
