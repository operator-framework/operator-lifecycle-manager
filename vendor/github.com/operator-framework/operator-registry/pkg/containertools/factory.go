package containertools

import (
	"os/exec"
)

type CommandFactory interface {
	BuildCommand(o BuildOptions) (*exec.Cmd, error)
}
