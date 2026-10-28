package containertools

import (
	"fmt"
	"os/exec"
)

type PodmanCommandFactory struct{}

func (p *PodmanCommandFactory) BuildCommand(o BuildOptions) (*exec.Cmd, error) {
	args := []string{"build"}

	if o.format != "" {
		args = append(args, "--format", o.format)
	} else {
		args = append(args, "--format", "docker")
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

	return exec.Command("podman", args...), nil
}
