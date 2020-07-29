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
		if err != nil || pkg == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		imgBytes, mimeType, etag := func() ([]byte, string, string) {
			for _, pkgChannel := range pkg.Status.Channels {
				if pkgChannel.Name != pkg.Status.DefaultChannel {
					continue
				}

				desc := pkgChannel.CurrentCSVDesc
				if len(desc.Icon) == 0 {
					break
				}

				// The first icon is call we care about
				icon := desc.Icon[0]
				data := icon.Base64Data
				mimeType := icon.Mediatype
				etag := `"` + strings.Join([]string{name, pkgChannel.Name, pkgChannel.CurrentCSV}, ".") + `"`

				reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(data))
				imgBytes, _ := ioutil.ReadAll(reader)

				return imgBytes, mimeType, etag
			}

			return []byte(defaultIcon), "image/svg+xml", ""
		}()

		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Content-Length", strconv.Itoa(len(imgBytes)))
		w.Header().Set("Etag", etag)
		w.Write(imgBytes)
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

const defaultIcon string = `
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 258.51 258.51"><defs><style>.cls-1{fill:#d1d1d1;}.cls-2{fill:#8d8d8f;}</style></defs><title>Asset 4</title><g id="Layer_2" data-name="Layer 2"><g id="Layer_1-2" data-name="Layer 1"><path class="cls-1" d="M129.25,20A109.1,109.1,0,0,1,206.4,206.4,109.1,109.1,0,1,1,52.11,52.11,108.45,108.45,0,0,1,129.25,20m0-20h0C58.16,0,0,58.16,0,129.25H0c0,71.09,58.16,129.26,129.25,129.26h0c71.09,0,129.26-58.17,129.26-129.26h0C258.51,58.16,200.34,0,129.25,0Z"/><path class="cls-2" d="M177.54,103.41H141.66L154.9,65.76c1.25-4.4-2.33-8.76-7.21-8.76H102.93a7.32,7.32,0,0,0-7.4,6l-10,69.61c-.59,4.17,2.89,7.89,7.4,7.89h36.9L115.55,197c-1.12,4.41,2.48,8.55,7.24,8.55a7.58,7.58,0,0,0,6.47-3.48L184,113.85C186.86,109.24,183.29,103.41,177.54,103.41Z"/></g></g></svg>
`
