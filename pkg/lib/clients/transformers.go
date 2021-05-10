package clients

import (
	"k8s.io/client-go/rest"
)

func SetWarningHandler(wh rest.WarningHandler) ConfigTransformer {
	return ConfigTransformerFunc(func(config *rest.Config) *rest.Config {
		config.WarningHandler = wh
		return config
	})
}
