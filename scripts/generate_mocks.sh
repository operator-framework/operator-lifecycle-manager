#!/usr/bin/env bash

# install tools
go install ./vendor/github.com/golang/mock/mockgen
go install ./vendor/github.com/maxbrunsfeld/counterfeiter

# generate fakes and mocks
go generate ./pkg/...
mockgen -source ./pkg/lib/operatorclient/client.go -destination ./pkg/lib/operatorclient/mock_client.go -package operatorclient
