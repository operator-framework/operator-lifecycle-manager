#!/usr/bin/env bash

# install dependencies
go install -mod=vendor ./vendor/github.com/golang/mock/mockgen
go install -mod=vendor ./vendor/github.com/maxbrunsfeld/counterfeiter/v6

# generate fakes and mocks
go generate -mod=vendor ./pkg/...
go run github.com/maxbrunsfeld/counterfeiter/v6 -o ./pkg/fakes/client-go/listers/fake_v1_service_account_lister.go ./vendor/k8s.io/client-go/listers/core/v1 ServiceAccountLister 
go run github.com/maxbrunsfeld/counterfeiter/v6 -o ./pkg/fakes/client-go/listers/fake_v1_service_account_namespace_lister.go ./vendor/k8s.io/client-go/listers/core/v1 ServiceAccountNamespaceLister
