#!/usr/bin/env bash

# install dependencies
go install -mod=vendor ./vendor/github.com/golang/mock/mockgen
go install -mod=vendor ./vendor/github.com/maxbrunsfeld/counterfeiter/v6

# generate fakes and mocks
go generate -mod=vendor ./pkg/...
