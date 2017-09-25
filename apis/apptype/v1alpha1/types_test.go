package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegisterAppType(t *testing.T) {
	testApp := &AppTypeSpec{
		DisplayName: "TestAppType",
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
	}

	rsrc := NewAppTypeResource(testApp)

	assert.Equal(t, AppTypeCRDName, rsrc.Kind)
}
