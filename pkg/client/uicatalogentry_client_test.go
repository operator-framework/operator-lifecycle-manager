package client

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coreos/go-semver/semver"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/rest"

	"github.com/coreos-inc/alm/pkg/apis"
	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
	"github.com/coreos-inc/alm/pkg/apis/uicatalogentry/v1alpha1"
)

func mockClient(t *testing.T, ts *httptest.Server) *UICatalogEntryClient {
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
	return &UICatalogEntryClient{
		RESTClient: restClient,
	}
}

func createTestEntry(name, version, label string) *v1alpha1.UICatalogEntry {
	return &v1alpha1.UICatalogEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "alm-coreos-tests",
			Labels:    map[string]string{"test": label},
		},
		Spec: &v1alpha1.UICatalogEntrySpec{
			Manifest: v1alpha1.PackageManifest{
				PackageName: "test",
				Channels: []v1alpha1.PackageChannel{
					{
						Name:           "alpha",
						CurrentCSVName: "testversion",
					},
				},
			},
			CSVSpec: csvv1alpha1.ClusterServiceVersionSpec{
				Version: *semver.New(version),
			},
		},
	}
}
func TestUpdateEntry(t *testing.T) {

	testCSVName := "MockServiceName-v1"
	testCSVOldVersion := "0.0.2"
	testCSVNewVersion := "1.0.1+alpha"

	tests := []struct {
		Description string
		InputEntry  *v1alpha1.UICatalogEntry

		PostStatusCode int
		PostBody       *v1alpha1.UICatalogEntry

		GetStatusCode int
		GetBody       *v1alpha1.UICatalogEntry

		PutStatusCode int
		PutBody       *v1alpha1.UICatalogEntry

		ExpectedError error
		ExpectedEntry *v1alpha1.UICatalogEntry
	}{
		{
			Description: "successfully creates UICatalogEntry via POST when doesn't already exist",
			InputEntry:  createTestEntry(testCSVName, testCSVNewVersion, "create-new-entry-input"),

			PostStatusCode: http.StatusOK,
			PostBody:       createTestEntry(testCSVName, testCSVNewVersion, "create-new-entry"),

			GetStatusCode: http.StatusInternalServerError,
			GetBody:       nil,

			PutStatusCode: http.StatusInternalServerError,
			PutBody:       nil,

			ExpectedEntry: createTestEntry(testCSVName, testCSVNewVersion, "create-new-entry"),
			ExpectedError: nil,
		},
		{
			Description: "handles error when POSTing UICatalogEntry returns unknown error",
			InputEntry:  createTestEntry(testCSVName, testCSVNewVersion, "create-entry-error-input"),

			PostStatusCode: http.StatusForbidden,
			PostBody:       nil,

			GetStatusCode: http.StatusInternalServerError,
			GetBody:       nil,

			PutStatusCode: http.StatusInternalServerError,
			PutBody:       nil,

			ExpectedEntry: nil,
			ExpectedError: fmt.Errorf("failed to create or update UICatalogEntry: "+
				" (post %s.%s %s)",
				v1alpha1.UICatalogEntryCRDName, apis.GroupName, testCSVName),
		},
		{
			Description: "successfully updates UICatalogEntry via PUT when one already exists",
			InputEntry:  createTestEntry(testCSVName, testCSVNewVersion, "patch-entry-input"),

			PostStatusCode: http.StatusConflict,
			PostBody:       nil,

			GetStatusCode: http.StatusOK,
			GetBody:       createTestEntry(testCSVName, testCSVOldVersion, "patch-existing-entry"),

			PutStatusCode: http.StatusOK,
			PutBody:       createTestEntry(testCSVName, testCSVNewVersion, "patch-existing-output"),

			ExpectedEntry: createTestEntry(testCSVName, testCSVNewVersion, "patch-existing-output"),
			ExpectedError: nil,
		},
		{
			Description: "handles error when fetching existing UICatalogEntry fails",
			InputEntry:  createTestEntry(testCSVName, testCSVNewVersion, "patch-entry-error-input"),

			PostStatusCode: http.StatusConflict,
			PostBody:       nil,

			GetStatusCode: http.StatusNotFound,
			GetBody:       nil,

			PutStatusCode: http.StatusInternalServerError,
			PutBody:       nil,

			ExpectedEntry: nil,
			ExpectedError: fmt.Errorf("failed to find then update UICatalogEntry: "+
				"failed to update UICatalogEntry status: the server could not find the requested resource "+
				"(get %s.%s %s)", v1alpha1.UICatalogEntryCRDName, apis.GroupName, testCSVName),
		},
		{
			Description: "handles error when patching UICatalogEntry via PUT fails",
			InputEntry:  createTestEntry(testCSVName, testCSVNewVersion, "patch-entry-error-input"),

			PostStatusCode: http.StatusConflict,
			PostBody:       nil,

			GetStatusCode: http.StatusOK,
			GetBody:       createTestEntry(testCSVName, testCSVOldVersion, "patch-existing-entry"),

			PutStatusCode: http.StatusServiceUnavailable,
			PutBody:       nil,

			ExpectedEntry: nil,
			ExpectedError: fmt.Errorf("failed to update UICatalogEntry: "+
				"an error on the server (\"\") has prevented the request from succeeding "+
				"(put %s.%s %s)", v1alpha1.UICatalogEntryCRDName, apis.GroupName, testCSVName),
		},
	}

	for _, test := range tests {
		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			statusCode := http.StatusMethodNotAllowed
			var body *v1alpha1.UICatalogEntry
			switch r.Method {
			case http.MethodPost:
				statusCode = test.PostStatusCode
				body = test.PostBody
			case http.MethodGet:
				statusCode = test.GetStatusCode
				body = test.GetBody
			case http.MethodPut:

				putBody, err := ioutil.ReadAll(r.Body)
				r.Body.Close()
				assert.NoError(t, err, test.Description)

				putEntry := &v1alpha1.UICatalogEntry{}
				json.Unmarshal(putBody, putEntry)
				assert.NoError(t, err, test.Description)
				assert.NotNil(t, test.GetBody, "invalid test: %s", test.Description)

				assert.Equal(t, test.GetBody.GetResourceVersion(), putEntry.GetResourceVersion(),
					"UICatalogEntry in PUT must have same ResourceVersion as existing entry %s",
					test.Description)
				assert.True(t, equality.Semantic.DeepEqual(test.InputEntry, putEntry),
					"testing '%s': UICatalogEntry in PUT should be updated version - %s",
					test.Description, diff.ObjectGoPrintSideBySide(test.InputEntry, putEntry))

				statusCode = test.PutStatusCode
				body = test.PutBody
			}
			if body != nil {
				rawResp, err := json.Marshal(body)
				assert.NoError(t, err, test.Description)
				w.Header().Set("Content-Type", "application/json")
				w.Write(rawResp)
			} else {
				w.WriteHeader(statusCode)
			}
		})

		ts := httptest.NewServer(testHandler)
		defer ts.Close()
		test.InputEntry.TypeMeta.Kind = v1alpha1.UICatalogEntryKind
		test.InputEntry.TypeMeta.APIVersion = v1alpha1.UICatalogEntryCRDAPIVersion

		actualEntry, err := mockClient(t, ts).UpdateEntry(test.InputEntry)
		assert.Equal(t, test.ExpectedError, err, "testing: '%s'", test.Description)

		assert.True(t,
			equality.Semantic.DeepEqual(test.ExpectedEntry, actualEntry),
			"testing '%s': unexpected result - %s", test.Description,
			diff.ObjectDiff(test.ExpectedEntry, actualEntry),
		)
	}
}
