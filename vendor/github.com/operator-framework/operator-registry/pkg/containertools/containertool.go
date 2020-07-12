package containertools

type ContainerTool int

const (
	NoneTool ContainerTool = iota
	PodmanTool
	DockerTool
)

func (t ContainerTool) String() (s string) {
	switch t {
	case NoneTool:
		s = "none"
	case PodmanTool:
		s = "podman"
	case DockerTool:
		s = "docker"
	}
	return
}

func (t ContainerTool) CommandFactory() CommandFactory {
	switch t {
	case PodmanTool:
		return &PodmanCommandFactory{}
	case DockerTool:
		return &DockerCommandFactory{}
	}
	return &StubCommandFactory{}
}

func NewContainerTool(s string, defaultTool ContainerTool) (t ContainerTool) {
	switch s {
	case "podman":
		t = PodmanTool
	case "docker":
		t = DockerTool
	case "none":
		t = NoneTool
	default:
		t = defaultTool
	}
	return
}

// NewCommandContainerTool returns a tool that can be used in `exec` statements.
func NewCommandContainerTool(s string) (t ContainerTool) {
	switch s {
	case "docker":
		t = DockerTool
	default:
		t = PodmanTool
	}
	return
}
