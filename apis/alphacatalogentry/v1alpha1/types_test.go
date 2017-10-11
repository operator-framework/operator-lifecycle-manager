package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterAlphaCatalogEntry(t *testing.T) {
	testApp := &AlphaCatalogEntrySpec{
		DisplayName: "TestAlphaCatalogEntry",
		Description: "This is a test app type",
		Keywords:    []string{"mock", "dev", "alm"},
		Maintainers: []Maintainer{{
			Name:  "testbot",
			Email: "testbot@coreos.com",
		}},
		Links: []AppLink{{
			Name: "ALM Homepage",
			URL:  "https://github.com/coreos-inc/alm",
		}},
		Icon: Icon{
			Data:      "dGhpcyBpcyBhIHRlc3Q=",
			MediaType: "image/gif",
		},
		Version: "myversion",
	}

	rsrc := NewAlphaCatalogEntryResource(testApp)

	assert.Equal(t, AlphaCatalogEntryCRDName, rsrc.Kind)
}
