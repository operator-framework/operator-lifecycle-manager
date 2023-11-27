package containerdregistry

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containers/common/pkg/auth"
	"github.com/containers/image/v5/pkg/docker/config"
	"github.com/containers/image/v5/types"
	dockerconfig "github.com/docker/cli/cli/config"
)

func NewResolver(client *http.Client, configDir string, plainHTTP bool, repo string) (remotes.Resolver, error) {
	headers := http.Header{}
	headers.Set("User-Agent", "opm/alpha")

	regopts := []docker.RegistryOpt{
		docker.WithAuthorizer(docker.NewDockerAuthorizer(
			docker.WithAuthClient(client),
			docker.WithAuthHeader(headers),
			docker.WithAuthCreds(credentialFunc(configDir, repo)),
		)),
		docker.WithClient(client),
	}
	if plainHTTP {
		regopts = append(regopts, docker.WithPlainHTTP(docker.MatchAllHosts))
	}

	opts := docker.ResolverOptions{
		Hosts:   docker.ConfigureDefaultRegistries(regopts...),
		Headers: headers,
	}

	return docker.NewResolver(opts), nil
}

func credentialFunc(configDir, repo string) func(string) (string, string, error) {
	if configDir == "" {
		configDir = dockerconfig.Dir()
	}

	// By default, we will use the docker config file in the standard docker config directory.
	// However, if REGISTRY_AUTH_FILE or DOCKER_CONFIG environment variables are set, we will
	// use those (in that order) instead to derive the auth config file.
	authFile := filepath.Join(configDir, dockerconfig.ConfigFileName)
	if defaultAuthFile := auth.GetDefaultAuthFile(); defaultAuthFile != "" {
		authFile = defaultAuthFile
	}

	// We don't use the function parameter in the credential function we return because containerd
	// only passes in the hostname. Instead, we will use our repo parameter to get the credentials
	// using the repo-aware GetCredentials function.
	return func(_ string) (string, string, error) {
		var (
			cred types.DockerAuthConfig
			err  error
		)

		// In order to maintain backward-compatibility with the original credential getter,
		// we will first try to get the credentials from the auth config file we derived above,
		// if it exists.
		if stat, statErr := os.Stat(authFile); statErr == nil && stat.Mode().IsRegular() {
			cred, err = config.GetCredentials(&types.SystemContext{AuthFilePath: authFile}, repo)
		}

		// If the auth file doesn't exist or if we couldn't find credentials in it, we'll use
		// system defaults from containers/image (podman/skopeo) to lookup the credentials.
		if cred == (types.DockerAuthConfig{}) || err != nil {
			cred, err = config.GetCredentials(nil, repo)
		}

		if err != nil {
			return "", "", err
		}
		if cred.IdentityToken != "" {
			return "", cred.IdentityToken, nil
		}
		return cred.Username, cred.Password, nil
	}
}
