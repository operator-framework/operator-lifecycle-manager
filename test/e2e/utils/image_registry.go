package main

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"github.com/sirupsen/logrus"
	"path"
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
	BuildahTool           = "buildah"
	DockerTool            = "docker"
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
		bundleTool: BuildahTool,
		client:     client,
		logger:     logger,
	}, cleanUpRegistry, nil
}

// Recreates index each time, does not update index. Returns the indexReference to use in CatalogSources
func (r *Registry) CreateBundlesAndIndex(indexName string, bundles []*Bundle) (string, error) {
	bundleRefs := make([]string, 0)

	switch r.bundleTool {
	case DockerTool:
		r.logger.Debugf("Using docker as bundle image build tool")
		for _, b := range bundles {
			ref, err := r.buildBundleImage(b)
			if err != nil {
				return "", err
			}
			bundleRefs = append(bundleRefs, ref)
		}

		if err := r.uploadBundleReferences(bundleRefs); err != nil {
			return "", err
		}
	case BuildahTool:
		r.logger.Debugf("Using buildah as bundle image build tool")
		for _, b := range bundles {
			labels := generateBundleLabels(b.PackageName, b.DefaultChannel, b.Channels)
			imageRef, err := createLocalBundleImage(path.Dir(b.BundleManifestDirectory), b.BundlePath, []string{"manifests", "metadata"}, labels, &r.logger.Out)
			if err != nil {
				return "", err
			}
			destImageRef := fmt.Sprintf("%s/%s", r.url, b.BundlePath)
			if err := r.skopeoCopy(r.client, destImageRef, b.Version, imageRef, ""); err != nil {
				return "", err
			}
			bundleRefs = append(bundleRefs, fmt.Sprintf("%s:%s", destImageRef, b.Version))
		}
	}
	r.logger.Debugf("Creating new index for upload: %s, bundles: %v", indexName, bundleRefs)
	indexReference, err := r.CreateAndUploadIndex(indexName, bundleRefs)
	if err != nil {
		return "", err
	}
	return indexReference, nil
}