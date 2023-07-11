package containerdregistry

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/credentials"
	"github.com/docker/docker/registry"
)

func NewResolver(configDir string, skipTlSVerify, plainHTTP bool, roots *x509.CertPool) (remotes.Resolver, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
			RootCAs:            roots,
		},
	}

	if plainHTTP || skipTlSVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
	headers := http.Header{}
	headers.Set("User-Agent", "opm/alpha")

	client := &http.Client{Transport: transport}

	cfg, err := loadConfig(configDir)
	if err != nil {
		return nil, err
	}

	regopts := []docker.RegistryOpt{
		docker.WithAuthorizer(docker.NewDockerAuthorizer(
			docker.WithAuthClient(client),
			docker.WithAuthHeader(headers),
			docker.WithAuthCreds(credential(cfg)),
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

func credential(cfg *configfile.ConfigFile) func(string) (string, string, error) {
	return func(hostname string) (string, string, error) {
		hostname = resolveHostname(hostname)
		auth, err := cfg.GetAuthConfig(hostname)
		if err != nil {
			return "", "", err
		}
		if auth.IdentityToken != "" {
			return "", auth.IdentityToken, nil
		}
		if auth.Username == "" && auth.Password == "" {
			return "", "", nil
		}

		return auth.Username, auth.Password, nil
	}
}

// protects against a data race inside the docker CLI
// TODO: upstream issue for 20.10.x is tracked here https://github.com/docker/cli/pull/3410
// newer versions already contain the fix
var configMutex sync.Mutex

func loadConfig(dir string) (*configfile.ConfigFile, error) {
	configMutex.Lock()
	defer configMutex.Unlock()

	if dir == "" {
		dir = config.Dir()
	}

	dockerConfigJSON := filepath.Join(dir, config.ConfigFileName)
	cfg := configfile.New(dockerConfigJSON)

	switch _, err := os.Stat(dockerConfigJSON); {
	case err == nil:
		cfg, err = config.Load(dir)
		if err != nil {
			return cfg, err
		}
	case os.IsNotExist(err):
		podmanConfig := filepath.Join(xdg.RuntimeDir, "containers/auth.json")
		if file, err := os.Open(podmanConfig); err == nil {
			defer file.Close()
			cfg, err = config.LoadFromReader(file)
			if err != nil {
				return cfg, err
			}
		} else if !os.IsNotExist(err) {
			return cfg, err
		}
	}

	if !cfg.ContainsAuth() {
		cfg.CredentialsStore = credentials.DetectDefaultStore(cfg.CredentialsStore)
	}

	return cfg, nil
}

// resolveHostname resolves Docker specific hostnames
func resolveHostname(hostname string) string {
	switch hostname {
	case registry.IndexHostname, registry.IndexName, registry.DefaultV2Registry.Host:
		return registry.IndexServer
	}
	return hostname
}
