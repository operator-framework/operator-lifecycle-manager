package registry

import (
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectoryLoader(t *testing.T) {
	_, err := NewInMemoryFromDirectory("../../catalog_resources")
	require.NoError(t, err)
}

func TestDirectoryLoaderHiddenDirs(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	err = os.Mkdir(path.Join(tmpdir, ".hidden_dir"), 0755)
	require.NoError(t, err)

	dirinfo, err := os.Open("../../catalog_resources")
	require.NoError(t, err)
	defer dirinfo.Close()

	dirnames, err := dirinfo.Readdirnames(0)
	require.NoError(t, err)

	for _, filename := range dirnames {
		oldfile, err := os.Open(path.Join("../../catalog_resources", filename))
		require.NoError(t, err)
		defer oldfile.Close()

		newfile, err := os.Create(path.Join(tmpdir, filename))
		require.NoError(t, err)
		defer newfile.Close()

		_, err = io.Copy(newfile, oldfile)
		require.NoError(t, err)

		if strings.HasSuffix(filename, ".clusterserviceversion.yaml") {
			err = os.Symlink(path.Join(tmpdir, filename), path.Join(tmpdir, ".hidden_dir", filename))
			require.NoError(t, err)
		}
	}
	_, err = NewInMemoryFromDirectory(tmpdir)
	require.NoError(t, err)
}
