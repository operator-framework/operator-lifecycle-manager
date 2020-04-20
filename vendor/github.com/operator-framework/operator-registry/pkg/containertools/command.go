//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . CommandRunner
package containertools

import (
	"fmt"
	"os/exec"

	"github.com/sirupsen/logrus"
)

// CommandRunner defines methods to shell out to common container tools
type CommandRunner interface {
	GetToolName() string
	Pull(image string) error
	Build(dockerfile, tag string) error
	Save(image, tarFile string) error
	Inspect(image string) ([]byte, error)
}

// ContainerCommandRunner is configured to select a container cli tool and
// execute commands with that tooling.
type ContainerCommandRunner struct {
	logger        *logrus.Entry
	containerTool ContainerTool
}

// NewCommandRunner takes the containerTool as an input string and returns a
// CommandRunner to run commands with that cli tool
func NewCommandRunner(containerTool ContainerTool, logger *logrus.Entry) CommandRunner {
	r := ContainerCommandRunner{
		logger: logger,
		containerTool: containerTool,
	}
	return &r
}

// GetToolName returns the container tool this command runner is using
func (r *ContainerCommandRunner) GetToolName() string {
	return r.containerTool.String()
}

// Pull takes a container image path hosted on a container registry and runs the
// pull command to download it onto the local environment
func (r *ContainerCommandRunner) Pull(image string) error {
	args := []string{"pull", image}

	command := exec.Command(r.containerTool.String(), args...)

	r.logger.Infof("running %s", command.String())

	out, err := command.CombinedOutput()
	if err != nil {
		r.logger.Errorf(string(out))
		return fmt.Errorf("error pulling image: %s. %v", string(out), err)
	}

	return nil
}

// Build takes a dockerfile and a tag and builds a container image
func (r *ContainerCommandRunner) Build(dockerfile, tag string) error {
	args := []string{"build", "-f", dockerfile}

	if tag != "" {
		args = append(args, "-t", tag)
	}

	args = append(args, ".")

	command := exec.Command(r.containerTool.String(), args...)

	r.logger.Infof("running %s build", r.containerTool)
	r.logger.Infof("%s", command.Args)

	out, err := command.CombinedOutput()
	if err != nil {
		r.logger.Errorf(string(out))
		return fmt.Errorf("error building image: %s. %v", string(out), err)
	}

	return nil
}

// Save takes a local container image and runs the save commmand to convert the
// image into a specified tarball and push it to the local directory
func (r *ContainerCommandRunner) Save(image, tarFile string) error {
	args := []string{"save", image, "-o", tarFile}

	command := exec.Command(r.containerTool.String(), args...)

	r.logger.Infof("running %s save", r.containerTool)
	r.logger.Debugf("%s", command.Args)

	out, err := command.CombinedOutput()
	if err != nil {
		r.logger.Errorf(string(out))
		return fmt.Errorf("error saving image: %s. %v", string(out), err)
	}

	return nil
}

// Inspect runs the 'inspect' command to get image metadata of a local container
// image and returns a byte array of the command's output
func (r *ContainerCommandRunner) Inspect(image string) ([]byte, error) {
	args := []string{"inspect", image}

	command := exec.Command(r.containerTool.String(), args...)

	r.logger.Infof("running %s inspect", r.containerTool)
	r.logger.Debugf("%s", command.Args)

	out, err := command.Output()
	if err != nil {
		r.logger.Errorf(string(out))
		return nil, err
	}

	return out, err
}
