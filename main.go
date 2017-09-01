package main

import (
	"fmt"
	"github.com/coreos-inc/alm/alm"
	"net/http"
)

func doesShitWork() {
	fmt.Println("testing...")
	testApp := &alm.AppType{
		DisplayName: "TestAppType",
		Description: "This is a test app type",
		Keywords:    []string{"mock", "dev", "alm"},
		Maintainers: []alm.Maintainer{{
			Name:  "testbot",
			Email: "testbot@coreos.com",
		}},
		Links: []alm.AppLink{{
			Name: "ALM Homepage",
			URL:  "https://github.com/coreos-inc/alm",
		}},
		Icon: alm.Icon{
			Data:      "dGhpcyBpcyBhIHRlc3Q=",
			MediaType: "image/gif",
		},
	}
	mock := alm.MockALM{Name: "MainMock"}
	_, err := mock.RegisterAppType(testApp)
	fmt.Println("Error? ", err)
}

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hi there, I love %s!", r.URL.Path[1:])
	doesShitWork()
}

func main() {
	http.HandleFunc("/healthz", handler)
	http.ListenAndServe(":8080", nil)
}
