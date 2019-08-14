package storage

import (
	"context"
	"encoding/base64"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericreq "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/provider"
)

// LogoStorage implements Kubernetes methods needed to provide the `packagemanifests/icon` subresource
type LogoStorage struct {
	groupResource schema.GroupResource
	prov          provider.PackageManifestProvider
}

var _ rest.Connecter = &LogoStorage{}
var _ rest.StorageMetadata = &LogoStorage{}

// NewLogoStorage returns struct which implements Kubernetes methods needed to provide the `packagemanifests/icon` subresource
func NewLogoStorage(groupResource schema.GroupResource, prov provider.PackageManifestProvider) *LogoStorage {
	return &LogoStorage{groupResource, prov}
}

// New satisfies the Storage interface
func (s *LogoStorage) New() runtime.Object {
	return &operators.PackageManifest{}
}

// Connect satisfies the Connector interface and returns the image icon file for a given `PackageManifest`
func (s *LogoStorage) Connect(ctx context.Context, name string, options runtime.Object, responder rest.Responder) (http.Handler, error) {
	var handler http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
		if match := r.Header.Get("If-None-Match"); match != "" && r.URL.Query().Get("resourceVersion") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		namespace := genericreq.NamespaceValue(ctx)
		pkg, err := s.prov.Get(namespace, name)
		if err != nil || pkg == nil || len(pkg.Status.Channels) == 0 || len(pkg.Status.Channels[0].CurrentCSVDesc.Icon) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		data := pkg.Status.Channels[0].CurrentCSVDesc.Icon[0].Base64Data
		mimeType := pkg.Status.Channels[0].CurrentCSVDesc.Icon[0].Mediatype
		etag := `"` + strings.Join([]string{name, pkg.Status.Channels[0].Name, pkg.Status.Channels[0].CurrentCSV}, ".") + `"`

		reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))
		imgBytes, err := ioutil.ReadAll(reader)

		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Content-Length", strconv.Itoa(len(imgBytes)))
		w.Header().Set("Etag", etag)
		_, err = w.Write(imgBytes)
	}

	return handler, nil
}

// NewConnectOptions satisfies the Connector interface
func (s *LogoStorage) NewConnectOptions() (runtime.Object, bool, string) {
	return nil, false, ""
}

// ConnectMethods satisfies the Connector interface
func (s *LogoStorage) ConnectMethods() []string {
	return []string{"GET"}
}

// ProducesMIMETypes satisfies the StorageMetadata interface and returns the supported icon image file types
func (s *LogoStorage) ProducesMIMETypes(verb string) []string {
	return []string{
		"image/png",
		"image/jpeg",
		"image/svg+xml",
	}
}

// ProducesObject satisfies the StorageMetadata interface
func (s *LogoStorage) ProducesObject(verb string) interface{} {
	return ""
}
