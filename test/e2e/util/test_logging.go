package util

import (
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
