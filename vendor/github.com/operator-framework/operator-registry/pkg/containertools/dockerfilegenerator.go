//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . DockerfileGenerator
package containertools

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

const (
	baseImage                = "scratch"
	defaultBinarySourceImage = "quay.io/operator-framework/upstream-registry-builder"
	DefaultDbLocation        = "./index.db"
	DbLocationLabel          = "operators.operatorframework.io.index.database.v1"
)

// DockerfileGenerator defines functions to generate index dockerfiles
type DockerfileGenerator interface {
	GenerateIndexDockerfile(string, string) string
}

// IndexDockerfileGenerator struct implementation of DockerfileGenerator interface
type IndexDockerfileGenerator struct {
	Logger *logrus.Entry
}

// NewDockerfileGenerator is a constructor that returns a DockerfileGenerator
func NewDockerfileGenerator(containerTool string, logger *logrus.Entry) DockerfileGenerator {
	return &IndexDockerfileGenerator{
		Logger: logger,
	}
}

// GenerateIndexDockerfile builds a string representation of a dockerfile to use when building
// an operator-registry index image
func (g *IndexDockerfileGenerator) GenerateIndexDockerfile(binarySourceImage, databaseFolder string) string {
	var dockerfile string

	if binarySourceImage == "" {
		binarySourceImage = defaultBinarySourceImage
	}

	g.Logger.Info("Generating dockerfile")

	// Where to collect the binary
	dockerfile += fmt.Sprintf("FROM %s AS builder\n", binarySourceImage)

	// From
	dockerfile += fmt.Sprintf("\nFROM %s\n", baseImage)

	// Labels
	dockerfile += fmt.Sprintf("LABEL %s=%s\n", DbLocationLabel, DefaultDbLocation)

	// Content
	dockerfile += fmt.Sprintf("COPY %s ./\n", databaseFolder)
	dockerfile += fmt.Sprintf("COPY --from=builder /bin/opm /opm\n")
	dockerfile += fmt.Sprintf("COPY --from=builder /bin/grpc_health_probe /bin/grpc_health_probe\n")
	dockerfile += fmt.Sprintf("EXPOSE 50051\n")
	dockerfile += fmt.Sprintf("ENTRYPOINT [\"/opm\"]\n")
	dockerfile += fmt.Sprintf("CMD [\"registry\", \"serve\", \"--database\", \"index.db\"]\n")

	return dockerfile
}
