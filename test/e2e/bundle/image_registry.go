package bundle

import (
	"fmt"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient"
	"strings"
	"time"
)

type RegistryClient struct {
	url        string
	auth       string
	namespace  string
	bundleTool string
	client     operatorclient.ClientInterface
}

const (
	openshiftregistryFQDN = "image-registry.openshift-image-registry.svc:5000/openshift-operators"
	pollInterval          = 1 * time.Second
	pollDuration          = 5 * time.Minute
	defaultIndexName      = "operator-index-registry"
)

func InitializeRegistry(testNamespace string, client operatorclient.ClientInterface) (*RegistryClient, func(), error) {
	if client == nil {
		return nil, nil, fmt.Errorf("uninitialized operator client")
	}

	local, err := Local(client)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot determine if test running locally or on CI: %s", err)
	}

	var registryURL, registryAuth string
	var cleanUpRegistry func()

	if local {
		registryURL, err = CreateDockerRegistry(client, testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("error creating container registry: %s", err)
		}
		cleanUpRegistry = func() {
			DeleteDockerRegistry(client, testNamespace)
		}
		defer func() {
			// delete newly created registry if any setup step fails
			if err != nil {
				cleanUpRegistry()
			}
		}()

		// ensure registry pod is ready before attempting port-forwarding
		_, err = awaitPod(client, testNamespace, RegistryName, podReady)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to start registry pod: %v", err)
		}

		err = RegistryPortForward(testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("port-forwarding local registry: %s", err)
		}

	} else {
		registryURL = openshiftregistryFQDN
		registryAuth, err = OpenshiftRegistryAuth(client, testNamespace)
		if err != nil {
			return nil, nil, fmt.Errorf("error getting openshift registry authentication: %s", err)
		}
		cleanUpRegistry = func() {}
	}

	return &RegistryClient{
		url:       registryURL,
		auth:      registryAuth,
		namespace: testNamespace,
		client:    client,
	}, cleanUpRegistry, nil
}

// Recreates index each time, does not update index. Returns the indexReference to use in CatalogSources
func (r *RegistryClient) CreateBundles(bundles []*Bundle) ([]string, error) {
	bundleRefs := make([]string, 0)

	for _, b := range bundles {
		labels, err := r.GetAnnotations(b)
		if err != nil {
			return nil, fmt.Errorf("failed to get bundle annotations for %s: %v", b.PackageName, err)
		}
		destImageRef := fmt.Sprintf("%s/%s:%s", r.url, b.BundleURLPath, b.Tag)
		err = buildAndUploadBundleImage(destImageRef, r.auth, []string{labels[manifestsLabel], labels[metadataLabel]}, labels)
		if err != nil {
			return nil, fmt.Errorf("build step for local bundle image failed %s: %v", b.PackageName, err)
		}
		bundleRefs = append(bundleRefs, destImageRef)
	}
	return bundleRefs, nil
}

func (r *RegistryClient) CreateIndex(indexName, indexTag string, bundleReferences []string) (string, error) {
	if len(indexName) == 0 {
		indexName = defaultIndexName
	}
	if len(indexTag) == 0 {
		indexTag = "latest"
	}
	indexReference := fmt.Sprintf("%s/%s:%s", r.url, indexName, indexTag)

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
			if err := execLocal(cmd[0], cmd[1:]...); err != nil {
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
