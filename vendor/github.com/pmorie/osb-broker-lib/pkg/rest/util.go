package rest

import (
	"encoding/json"
	"io/ioutil"
	"net/http"

	osb "github.com/pmorie/go-open-service-broker-client/v2"
)

func getBrokerAPIVersionFromRequest(r *http.Request) string {
	return r.Header.Get(osb.APIVersionHeader)
}

func unmarshalRequestBody(request *http.Request, obj interface{}) error {
	body, err := ioutil.ReadAll(request.Body)
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, obj)
	if err != nil {
		return err
	}

	return nil
}
