package bundle

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildBundleImage(t *testing.T) {
	setup("")
	defer cleanup()

	tests := []struct {
		directory    string
		imageTag     string
		imageBuilder string
		commandStr   string
		errorMsg     string
	}{
		{
			operatorDir,
			"test",
			"docker",
			"docker build -f test-operator/Dockerfile -t test .",
			"",
		},
		{
			operatorDir,
			"test",
			"podman",
			"podman bud --format=docker -f test-operator/Dockerfile -t test .",
			"",
		},
		{
			operatorDir,
			"test",
			"buildah",
			"buildah build -f test-operator/Dockerfile -t test .",
			"",
		},
		{
			operatorDir,
			"test",
			"hello",
			"",
			"hello is not supported image builder",
		},
	}

	for _, item := range tests {
		var cmd *exec.Cmd
		cmd, err := buildBundleImage(item.directory, item.imageTag, item.imageBuilder)
		if item.errorMsg == "" {
			require.Equal(t, item.commandStr, cmd.String())
		} else {
			require.Equal(t, item.errorMsg, err.Error())
		}
	}
}
