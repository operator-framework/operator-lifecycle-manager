package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"encoding/json"
	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/apis/alphacatalogentry/v1alpha1"
	csvv1alpha1 "github.com/coreos-inc/alm/apis/clusterserviceversion/v1alpha1"
)

// func mockClient(t *testing.T, ts *httptest.Server) *AlphaCatalogEntryClient {
// }
func testUpdateEntry(t *testing.T, testHandler http.Handler, testEntry *v1alpha1.AlphaCatalogEntry,
	expectedEntry *v1alpha1.AlphaCatalogEntry, expectedError error) {
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

	mockClient := &AlphaCatalogEntryClient{
		RESTClient: restClient,
	}

	_, err = mockClient.UpdateEntry(testEntry)
	actualEntry, err := mockClient.UpdateEntry(testEntry)
	assert.Equal(t, expectedError, err)

	assert.True(t,
		equality.Semantic.DeepEqual(expectedEntry, actualEntry),
		"AlphaCatalogEntry does not match expected value: %s",
		diff.ObjectDiff(expectedEntry, actualEntry),
	)
}

func TestUpdateEntry(t *testing.T) {

	testCSVName := "MockServiceName-v1"
	testCSVVersion := "0.2.4+alpha"

	testEntry := &v1alpha1.AlphaCatalogEntry{
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
	expectedEntry := &v1alpha1.AlphaCatalogEntry{Spec: testEntry.Spec}
	expectedEntry.SetNamespace("alm-coreos-tests-other")
	rawResp, err := json.Marshal(expectedEntry)

	assert.NoError(t, err)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(rawResp)
	})
	testUpdateEntry(t, testHandler, testEntry, expectedEntry, nil)

	testHandler2 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.Write(rawResp)
		case http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			w.Write(rawResp)
		}
	})
	testUpdateEntry(t, testHandler2, testEntry, expectedEntry, nil)

	testHandler3 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
		case http.MethodGet:
			w.WriteHeader(http.StatusInternalServerError)
		case http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			w.Write(rawResp)
		}
	})
	testUpdateEntry(t, testHandler3, testEntry, nil,
		fmt.Errorf("failed to find then update AlphaCatalogEntry: failed to update CR status: "+
			"an error on the server (\"\") has prevented the request from succeeding (get %s.%s %s)",
			v1alpha1.AlphaCatalogEntryCRDName, "app.coreos.com", testCSVName))

	testHandler4 := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.Write(rawResp)
		case http.MethodPut:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	testUpdateEntry(t, testHandler4, testEntry, nil,
		fmt.Errorf("failed to update AlphaCatalogEntry: "+
			"an error on the server (\"\") has prevented the request from succeeding (put %s.%s %s)",
			v1alpha1.AlphaCatalogEntryCRDName, "app.coreos.com", testCSVName))

}
