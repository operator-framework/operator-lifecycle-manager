package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"
	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/alphacatalogentry/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

func TestUpdateEntry(t *testing.T) {

	testCSVName := "MockServiceName-v1"
	testCSVVersion := "0.2.4+alpha"

	testEntry := v1alpha1.AlphaCatalogEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       v1alpha1.AlphaCatalogEntryKind,
			APIVersion: v1alpha1.AlphaCatalogEntryCRDAPIVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testCSVName,
			Namespace: "alm-coreos-tests",
		},
		Spec: &v1alpha1.AlphaCatalogEntrySpec{
			ClusterServiceVersionSpec: csvv1alpha1.ClusterServiceVersionSpec{
				Version: *semver.New(testCSVVersion),
				CustomResourceDefinitions: csvv1alpha1.CustomResourceDefinitions{
					Owned:    []csvv1alpha1.CRDDescription{},
					Required: []csvv1alpha1.CRDDescription{},
				},
			},
		},
	}
	rawResp, err := json.Marshal(testEntry)
	assert.NoError(t, err)
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(rawResp)
	})

	ts := httptest.NewServer(testHandler)
	defer ts.Close()
	config := rest.Config{
		Host: ts.URL,
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, v1alpha1.AddToScheme(scheme))

	config.GroupVersion = &v1alpha1.SchemeGroupVersion
	config.APIPath = "/apis"
	config.ContentType = runtime.ContentTypeJSON
	config.NegotiatedSerializer = serializer.DirectCodecFactory{
		CodecFactory: serializer.NewCodecFactory(scheme),
	}

	restClient, err := rest.RESTClientFor(&config)
	assert.NoError(t, err)
	assert.NotNil(t, restClient)

	aceClient := &AlphaCatalogEntryClient{
		RESTClient: restClient,
	}
	entry, err := aceClient.UpdateEntry(&testEntry)
	assert.NoError(t, err)
	assert.NotNil(t, entry)
}
