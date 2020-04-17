//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . LabelReader
package containertools

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
)

type LabelReader interface {
	GetLabelsFromImage(string) (map[string]string, error)
}

type ImageLabelReader struct {
	Logger *logrus.Entry
	Cmd    CommandRunner
}

func NewLabelReader(containerTool ContainerTool, logger *logrus.Entry) LabelReader {
	cmd := NewCommandRunner(containerTool, logger)

	return ImageLabelReader{
		Logger: logger,
		Cmd:    cmd,
	}
}

type DockerImageData struct {
	Config DockerConfig `json:"Config"`
}

type DockerConfig struct {
	Labels map[string]string `json:"Labels"`
}

type PodmanImageData struct {
	Labels map[string]string `json:"Labels"`
}

// GetLabelsFromImage takes a container image path as input, pulls that image
// to the local environment and then inspects it for labels
func (r ImageLabelReader) GetLabelsFromImage(image string) (map[string]string, error) {
	err := r.Cmd.Pull(image)
	if err != nil {
		return nil, err
	}

	r.Logger.Info("Getting label data from previous image")

	imageData, err := r.Cmd.Inspect(image)
	if err != nil {
		return nil, err
	}

	// parse output of inspect to get labels
	switch containerTool := r.Cmd.GetToolName(); containerTool {
	case "docker":
		var data []DockerImageData
		err := json.Unmarshal(imageData, &data)
		if err != nil {
			return nil, err
		}
		return data[0].Config.Labels, nil
	case "podman":
		var data []PodmanImageData
		err := json.Unmarshal(imageData, &data)
		if err != nil {
			return nil, err
		}
		return data[0].Labels, nil
	}

	return nil, fmt.Errorf("Unable to parse label data from container")
}
