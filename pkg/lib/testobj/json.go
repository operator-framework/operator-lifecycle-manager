package testobj

import (
	"encoding/json"
	"fmt"
)

// Marshal returns the raw json of the given object and panics if it fails.
func Marshal(obj interface{}) []byte {
	raw, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Sprintf("error marshaling json: %v", raw))
	}

	return raw
}
