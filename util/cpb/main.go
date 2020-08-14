package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/alicebob/sqlittle"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/reconciler"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

func main() {
	rootCmd.AddCommand(copyBundleCmd)
	rootCmd.AddCommand(copyIndexCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "cpb",
	Short: "Copy bundle or index contents",
	Long: `Utility for copying the contents out of data-only images which may not include standard copying utils.`,
}

var copyBundleCmd = &cobra.Command{
	Use:   "bundle [output directory]",
	Short: "Copy bundle content",
	Long: `Copy bundle metadata and manifests into an output directory.
Copy traverses the filesystem from the current directory down
until it finds an annotations.yaml representing the bundle's metadata.
From there, it copies the annotations.yaml file and manifests into the
specified output directory`,
	Run: func(cmd *cobra.Command, args []string) {
		dest := "./bundle"
		if len(args) > 1 {
			// Get the destination position argument
			dest = args[0]
		}

		if err := copyBundle(dest); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

var copyIndexCmd = &cobra.Command{
	Use:   "index [output directory]",
	Short: "Copy index content",
	Long: `Copy index content to an output directory. Will look in 
standard locations (/bundles.db, /database/index.db) before walking
the entire filesystem to find the content.`,
	Run: func(cmd *cobra.Command, args []string) {
		dest := "/mounted/index.db"
		if len(args) > 0 {
			// Get the destination position argument
			dest = args[0]
		}

		if err := copyIndex(dest); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

func copyBundle(dest string) error {
	// Find the manifest directory
	m, err := getMetadata()
	if err != nil {
		return fmt.Errorf("error finding metadata directory: %s", err)
	}

	fmt.Printf("%v\n", m)

	return m.copy(dest)
}

type metadata struct {
	annotationsFile string
	manifestDir     string
}

func (m *metadata) copy(dest string) error {
	// Copy the annotations file
	path := filepath.Join(dest, "metadata/annotations.yaml")
	if err := copy.Copy(m.annotationsFile, path); err != nil {
		return err
	}

	// Copy manifest dir
	path = filepath.Join(dest, "manifests")
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		return err
	}
	if err := copy.Copy(m.manifestDir, path); err != nil {
		return err
	}

	return nil
}

func getMetadata() (m *metadata, err error) {
	// Create default metadata
	m = &metadata{
		annotationsFile: "/metadata/annotations.yaml",
		manifestDir:     "/manifests",
	}

	// Traverse the filesystem looking for metadata
	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("prevent panic by handling failure accessing a path %q: %v\n", path, err)
			return nil
		}
		if info.IsDir() {
			fmt.Printf("skipping a dir without errors: %+v \n", info.Name())
			return nil
		}
		if info.Name() != bundle.AnnotationsFile {
			return nil
		}
		m.annotationsFile = path

		// Unmarshal metadata
		content, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("couldn't get content of annotations.yaml file: %s", path)
		}

		annotations := bundle.AnnotationMetadata{}
		if err := yaml.Unmarshal(content, &annotations); err != nil {
			return err
		}

		if annotations.Annotations == nil {
			return fmt.Errorf("annotations.yaml file unmarshalling failed: %s", path)
		}

		if manifestDir, ok := annotations.Annotations[bundle.ManifestsLabel]; ok {
			m.manifestDir = manifestDir
		}

		// Skip the remainder of files in the directory
		return filepath.SkipDir
	})

	if err != nil {
		m = nil
	}

	return
}

// copyIndex searches for the index in an image and then copies it to dest
// it will create intermediate directories for dest if they do not exist
func copyIndex(dest string) (err error) {
	path := getIndexPath()
	if path == "" {
		err = fmt.Errorf("could not find index data in image")
		return
	}

	dir := filepath.Dir(dest)
	if err = os.MkdirAll(dir, os.ModePerm); err != nil {
		return
	}

	in, err := os.Open(path)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()

	hash := sha256.New()
	hashAndCopy := io.MultiWriter(hash, out)

	if _, err = io.Copy(hashAndCopy, in); err != nil {
		return
	}
	if err = out.Sync(); err != nil {
		return
	}

	status := reconciler.CopyIndexResult{Digest: fmt.Sprintf("%x", hash.Sum(nil))}
	b, err := json.Marshal(status)
	if err != nil {
		return
	}
	return ioutil.WriteFile(reconciler.CopyIndexStatusLocation, b, os.ModePerm)
}

// getIndexPath searches standard paths for a db file and then falls back to walk the fs.
// it is possible to get a false positive if there is more than one sqlite file in the image,
// but in practice this is unlikely.
func getIndexPath() (found string) {
	standardPaths := []string{"/database/index.db", "/bundles.db"}
	for _, p := range standardPaths{
		_, err := os.Stat(p)
		if os.IsNotExist(err) {
			fmt.Println(p, "does not exist")
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			fmt.Println("err opening: ", err)
			continue
		}
		if err := f.Close(); err != nil {
			continue
		}
		if db, err := sqlittle.Open(p); err == nil {
			fmt.Println("found", p)
			db.Close()
			found = p
			return
		}
	}

	_ = filepath.Walk("/", func(path string, info os.FileInfo, err error) error {
		if info == nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			fmt.Println("err opening: ", err)
			return nil
		}
		if err := f.Close(); err != nil {
			return nil
		}
		if db, err := sqlittle.Open(path); err == nil {
			db.Close()
			fmt.Println("found", path)
			found = path
			return io.EOF
		}
		return nil
	})
	return
}