package containertools

import (
	"fmt"
	"os/exec"
)

type StubCommandFactory struct {
	name string
}

func (s *StubCommandFactory) BuildCommand(o BuildOptions) (*exec.Cmd, error) {
	return nil, fmt.Errorf(`"build" is not supported by tool %q`, s.name)
}
