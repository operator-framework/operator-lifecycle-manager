//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate
package registry

import (
	"github.com/sirupsen/logrus"
)

//counterfeiter:generate . RegistryAdder
type RegistryAdder interface {
	AddToRegistry(AddToRegistryRequest) error
}

func NewRegistryAdder(logger *logrus.Entry) RegistryAdder {
	return RegistryUpdater{
		Logger: logger,
	}
}

//counterfeiter:generate . RegistryDeleter
type RegistryDeleter interface {
	DeleteFromRegistry(DeleteFromRegistryRequest) error
}

func NewRegistryDeleter(logger *logrus.Entry) RegistryDeleter {
	return RegistryUpdater{
		Logger: logger,
	}
}

type RegistryStrandedPruner interface {
	PruneStrandedFromRegistry(PruneStrandedFromRegistryRequest) error
}

func NewRegistryStrandedPruner(logger *logrus.Entry) RegistryStrandedPruner {
	return RegistryUpdater{
		Logger: logger,
	}
}

type RegistryPruner interface {
	PruneFromRegistry(PruneFromRegistryRequest) error
}

func NewRegistryPruner(logger *logrus.Entry) RegistryPruner {
	return RegistryUpdater{
		Logger: logger,
	}
}

type RegistryDeprecator interface {
	DeprecateFromRegistry(DeprecateFromRegistryRequest) error
}

func NewRegistryDeprecator(logger *logrus.Entry) RegistryDeprecator {
	return RegistryUpdater{
		Logger: logger,
	}
}
