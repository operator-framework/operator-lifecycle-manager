package containertools

import (
	"fmt"
	"os/exec"
)

const (
	// Podman cli tool
	Podman = "podman"
	// Docker cli tool
	Docker = "docker"
)

// CommandRunner is configured to select a container cli tool and execute commands with that
// tooling.
type CommandRunner struct {
	containerTool string
}

// NewCommandRunner takes the containerTool as an input string and returns a CommandRunner to
// run commands with that cli tool
func NewCommandRunner(containerTool string) *CommandRunner {
	r := &CommandRunner{}

	switch containerTool {
	case Podman:
		r.containerTool = Podman
	case Docker:
		r.containerTool = Docker
	default:
		r.containerTool = Podman
	}

	return r
}

// Pull takes a container image path hosted on a container registry and runs the pull command to
// download it onto the local environment
func (r *CommandRunner) Pull(image string) error {
	args := []string{"pull", image}

	command := exec.Command(r.containerTool, args...)

	out, err := command.Output()
	if err != nil {
		return fmt.Errorf("error with %s %s: %s", command.Args, string(out), err)
	}

	return nil
}

// Save takes a local container image and runs the save commmand to convert the image into a specified
// tarball and push it to the local directory
func (r *CommandRunner) Save(image, tarFile string) error {
	args := []string{"save", image, "-o", tarFile}

	command := exec.Command(r.containerTool, args...)

	out, err := command.Output()
	if err != nil {
		return fmt.Errorf("error with %s %s: %s", command.Args, string(out), err)
	}

	return nil
}
