package containerdregistry

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/credentials"
	"github.com/docker/docker/registry"
)

func NewResolver(configDir string, insecure bool, roots *x509.CertPool) (remotes.Resolver, error) {
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
			RootCAs: roots,
		},
	}

	if insecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: insecure,
		}
	}
	headers := http.Header{}
	headers.Set("User-Agent", "opm/alpha")

	client := http.DefaultClient
	client.Transport = transport

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
	if insecure {
		regopts = append(regopts, docker.WithPlainHTTP(docker.MatchAllHosts))
	}

	opts := docker.ResolverOptions{
		Hosts: docker.ConfigureDefaultRegistries(regopts...),
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

func loadConfig(dir string) (*configfile.ConfigFile, error) {
	if dir == "" {
		dir = config.Dir()
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
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
