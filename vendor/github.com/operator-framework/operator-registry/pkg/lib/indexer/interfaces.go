//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate
package indexer

import (
	"github.com/operator-framework/operator-registry/pkg/containertools"
	"github.com/operator-framework/operator-registry/pkg/lib/registry"
	"github.com/sirupsen/logrus"
)

// IndexAdder allows the creation of index container images from scratch or
// based on previous index images
//
//counterfeiter:generate . IndexAdder
type IndexAdder interface {
	AddToIndex(AddToIndexRequest) error
}

// NewIndexAdder is a constructor that returns an IndexAdder
func NewIndexAdder(buildTool, pullTool containertools.ContainerTool, logger *logrus.Entry) IndexAdder {
	return ImageIndexer{
		DockerfileGenerator: containertools.NewDockerfileGenerator(logger),
		CommandRunner:       containertools.NewCommandRunner(buildTool, logger),
		LabelReader:         containertools.NewLabelReader(pullTool, logger),
		RegistryAdder:       registry.NewRegistryAdder(logger),
		BuildTool:           buildTool,
		PullTool:            pullTool,
		Logger:              logger,
	}
}

// IndexDeleter takes indexes and deletes all references to an operator
// from them
//
//counterfeiter:generate . IndexDeleter
type IndexDeleter interface {
	DeleteFromIndex(DeleteFromIndexRequest) error
}

// NewIndexDeleter is a constructor that returns an IndexDeleter
func NewIndexDeleter(buildTool, pullTool containertools.ContainerTool, logger *logrus.Entry) IndexDeleter {
	return ImageIndexer{
		DockerfileGenerator: containertools.NewDockerfileGenerator(logger),
		CommandRunner:       containertools.NewCommandRunner(buildTool, logger),
		LabelReader:         containertools.NewLabelReader(pullTool, logger),
		RegistryDeleter:     registry.NewRegistryDeleter(logger),
		BuildTool:           buildTool,
		PullTool:            pullTool,
		Logger:              logger,
	}
}

//counterfeiter:generate . IndexExporter
type IndexExporter interface {
	ExportFromIndex(ExportFromIndexRequest) error
}

// NewIndexExporter is a constructor that returns an IndexExporter
func NewIndexExporter(containerTool containertools.ContainerTool, logger *logrus.Entry) IndexExporter {
	return ImageIndexer{
		DockerfileGenerator: containertools.NewDockerfileGenerator(logger),
		CommandRunner:       containertools.NewCommandRunner(containerTool, logger),
		LabelReader:         containertools.NewLabelReader(containerTool, logger),
		BuildTool:           containerTool,
		PullTool:            containerTool,
		Logger:              logger,
	}
}

// IndexStrandedPruner prunes operators out of an index
type IndexStrandedPruner interface {
	PruneStrandedFromIndex(PruneStrandedFromIndexRequest) error
}

func NewIndexStrandedPruner(containerTool containertools.ContainerTool, logger *logrus.Entry) IndexStrandedPruner {
	return ImageIndexer{
		DockerfileGenerator:    containertools.NewDockerfileGenerator(logger),
		CommandRunner:          containertools.NewCommandRunner(containerTool, logger),
		LabelReader:            containertools.NewLabelReader(containerTool, logger),
		RegistryStrandedPruner: registry.NewRegistryStrandedPruner(logger),
		BuildTool:              containerTool,
		PullTool:               containerTool,
		Logger:                 logger,
	}
}

// IndexPruner prunes operators out of an index
type IndexPruner interface {
	PruneFromIndex(PruneFromIndexRequest) error
}

func NewIndexPruner(containerTool containertools.ContainerTool, logger *logrus.Entry) IndexPruner {
	return ImageIndexer{
		DockerfileGenerator: containertools.NewDockerfileGenerator(logger),
		CommandRunner:       containertools.NewCommandRunner(containerTool, logger),
		LabelReader:         containertools.NewLabelReader(containerTool, logger),
		RegistryPruner:      registry.NewRegistryPruner(logger),
		BuildTool:           containerTool,
		PullTool:            containerTool,
		Logger:              logger,
	}
}

// IndexDeprecator prunes operators out of an index
type IndexDeprecator interface {
	DeprecateFromIndex(DeprecateFromIndexRequest) error
}

func NewIndexDeprecator(buildTool, pullTool containertools.ContainerTool, logger *logrus.Entry) IndexDeprecator {
	return ImageIndexer{
		DockerfileGenerator: containertools.NewDockerfileGenerator(logger),
		CommandRunner:       containertools.NewCommandRunner(buildTool, logger),
		LabelReader:         containertools.NewLabelReader(pullTool, logger),
		RegistryDeprecator:  registry.NewRegistryDeprecator(logger),
		BuildTool:           buildTool,
		PullTool:            pullTool,
		Logger:              logger,
	}
}
