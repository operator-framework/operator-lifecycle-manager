package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/otiai10/copy"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "cpb [output directory]",
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
