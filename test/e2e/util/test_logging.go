package util

import (
	"encoding/json"
	"fmt"
	"strings"

	g "github.com/onsi/ginkgo/v2"
)

func Logf(f string, v ...interface{}) {
	if !strings.HasSuffix(f, "\n") {
		f += "\n"
	}
	_, _ = fmt.Fprintf(g.GinkgoWriter, f, v...)
}

func ObjectToPrettyJsonString(obj interface{}) string {
	data, _ := json.MarshalIndent(obj, "", " ")
	return string(data)
}

func ObjectToJsonString(obj interface{}) string {
	data, _ := json.MarshalIndent(obj, "", " ")
	return string(data)
}
