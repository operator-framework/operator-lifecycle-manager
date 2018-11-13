#!/usr/bin/env bash

# install tools
go install ./vendor/github.com/golang/mock/mockgen
go install ./vendor/github.com/maxbrunsfeld/counterfeiter

# generate fakes and mocks
go generate ./pkg/...
counterfeiter -o ./pkg/fakes/client-go/listers/fake_v1_service_account_lister.go ./vendor/k8s.io/client-go/listers/core/v1 ServiceAccountLister 
counterfeiter -o ./pkg/fakes/client-go/listers/fake_v1_service_account_namespace_lister.go ./vendor/k8s.io/client-go/listers/core/v1 ServiceAccountNamespaceLister
mockgen -source ./pkg/lib/operatorclient/client.go -destination ./pkg/lib/operatorclient/mock_client.go -package operatorclient
