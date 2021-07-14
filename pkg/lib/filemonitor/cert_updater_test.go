package filemonitor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"html"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOLMGetCertRotationFn(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		TimestampFormat: time.RFC3339Nano,
	})

	testData := "testdata"
	monitorDir := "monitor"
	caCrt := filepath.Join(testData, "ca.crt")
	oldCrt := filepath.Join(testData, "server-old.crt")
	oldKey := filepath.Join(testData, "server-old.key")
	newCrt := filepath.Join(testData, "server-new.crt")
	newKey := filepath.Join(testData, "server-new.key")
	loadCrt := filepath.Join(monitorDir, "loaded.crt")
	loadKey := filepath.Join(monitorDir, "loaded.key")

	// these values must match values specified in the testdata generation script
	expectedOldCN := "CN=127.0.0.1,OU=OpenShift,O=Red Hat,L=Columbia,ST=SC,C=US"
	expectedNewCN := "CN=127.0.0.1,OU=OpenShift,O=Red Hat,L=New York City,ST=NY,C=US"

	// the directory is expected to contain exactly one keypair, so create an empty directory to swap the keys in
	err := os.RemoveAll(monitorDir) // this is for test development, shouldn't ever exist beforehand otherwise
	require.NoError(t, err)
	err = os.Mkdir(monitorDir, 0777)
	require.NoError(t, err)

	// symlink old files to loading files
	err = os.Symlink(filepath.Join("..", oldCrt), loadCrt)
	require.NoError(t, err)
	err = os.Symlink(filepath.Join("..", oldKey), loadKey)
	require.NoError(t, err)

	certStore, err := NewCertStore(loadCrt, loadKey)
	if err != nil {
		require.NoError(t, err)
	}

	tlsGetCertFn := func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return certStore.GetCertificate(), nil
	}

	csw, err := NewWatch(logger, []string{filepath.Dir(loadCrt), filepath.Dir(loadKey)}, certStore.HandleFilesystemUpdate)
	require.NoError(t, err)
	csw.Run(context.Background())

	// find a free port to listen on and start server
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	freePort := listener.Addr().(*net.TCPAddr).Port
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Path: %q", html.EscapeString(r.URL.Path))
	})
	httpsServer := &http.Server{
		Addr: ":" + strconv.Itoa(freePort),
		TLSConfig: &tls.Config{
			GetCertificate: tlsGetCertFn,
		},
	}
	go func() {
		if err := httpsServer.ServeTLS(listener, "", ""); err != nil {
			panic(err)
		}
	}()

	caCert, err := ioutil.ReadFile(caCrt)
	require.NoError(t, err)
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
	}

	resp, err := client.Get(fmt.Sprintf("https://localhost:%v", freePort))
	require.NoError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, expectedOldCN, resp.TLS.PeerCertificates[0].Subject.String())
	resp.Body.Close()
	client.CloseIdleConnections()

	// atomically switch out the symlink so the file contents are always seen in a consistent state
	// (the same idea is used in the atomic writer in kubernetes)
	atomicCrt := loadCrt + ".atomic-op"
	atomicKey := loadKey + ".atomic-op"
	err = os.Symlink(filepath.Join("..", newCrt), atomicCrt)
	require.NoError(t, err)
	err = os.Symlink(filepath.Join("..", newKey), atomicKey)
	require.NoError(t, err)

	err = os.Rename(atomicCrt, loadCrt)
	require.NoError(t, err)
	err = os.Rename(atomicKey, loadKey)
	require.NoError(t, err)

	// sometimes the the filesystem operations need time to catch up so the server cert is updated
	err = wait.PollImmediate(500*time.Millisecond, 10*time.Second, func() (bool, error) {
		currentCert, err := tlsGetCertFn(nil)
		require.NoError(t, err)
		info, err := x509.ParseCertificate(currentCert.Certificate[0])
		if err != nil {
			return false, err
		}
		if info.Subject.String() == expectedNewCN {
			return true, nil
		}

		return false, nil
	})
	require.NoError(t, err)

	resp, err = client.Get(fmt.Sprintf("https://localhost:%v", freePort))
	require.NoError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, expectedNewCN, resp.TLS.PeerCertificates[0].Subject.String())

	os.RemoveAll(monitorDir)
}
