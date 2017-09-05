package alm

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestRegisterAppType(t *testing.T) {
	testApp := &AppType{
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
	mock := MockALM{Name: "MainMock"}
	rsrc, err := mock.RegisterAppType(testApp)
	assert.NoError(t, err)
	assert.Equal(t, AppTypeCRDName, rsrc.Kind)
}
