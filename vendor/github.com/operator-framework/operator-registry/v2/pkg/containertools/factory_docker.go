package containertools

import (
	"fmt"
	"os/exec"
)

type DockerCommandFactory struct{}

func (d *DockerCommandFactory) BuildCommand(o BuildOptions) (*exec.Cmd, error) {
	args := []string{"build"}

	if o.format != "" && o.format != "docker" {
		return nil, fmt.Errorf(`format %q invalid for "docker build"`, o.format)
	}

	if o.dockerfile != "" {
		args = append(args, "-f", o.dockerfile)
	}

	for _, tag := range o.tags {
		args = append(args, "-t", tag)
	}

	if o.context == "" {
		return nil, fmt.Errorf("context not provided")
	}
	args = append(args, o.context)

	return exec.Command("docker", args...), nil
}
