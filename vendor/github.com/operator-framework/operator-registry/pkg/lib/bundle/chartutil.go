/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Adapted from https://github.com/helm/helm/blob/b49db9e6e6fb1913a125b8cf9b31f81199c6753c/pkg/chartutil/chartfile.go

package bundle

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

// Maintainer describes a Chart maintainer.
type Maintainer struct {
	// Name is a user name or organization name
	Name string `json:"name,omitempty"`
	// Email is an optional email address to contact the named maintainer
	Email string `json:"email,omitempty"`
	// URL is an optional URL to an address for the named maintainer
	URL string `json:"url,omitempty"`
}

// Dependency describes a chart upon which another chart depends.
//
// Dependencies can be used to express developer intent, or to capture the state
// of a chart.
type Dependency struct {
	// Name is the name of the dependency.
	//
	// This must mach the name in the dependency's Chart.yaml.
	Name string `json:"name"`
	// Version is the version (range) of this chart.
	//
	// A lock file will always produce a single version, while a dependency
	// may contain a semantic version range.
	Version string `json:"version,omitempty"`
	// The URL to the repository.
	//
	// Appending `index.yaml` to this string should result in a URL that can be
	// used to fetch the repository index.
	Repository string `json:"repository"`
	// A yaml path that resolves to a boolean, used for enabling/disabling charts (e.g. subchart1.enabled )
	Condition string `json:"condition,omitempty"`
	// Tags can be used to group charts for enabling/disabling together
	Tags []string `json:"tags,omitempty"`
	// Enabled bool determines if chart should be loaded
	Enabled bool `json:"enabled,omitempty"`
	// ImportValues holds the mapping of source values to parent key to be imported. Each item can be a
	// string or pair of child/parent sublist items.
	ImportValues []interface{} `json:"import-values,omitempty"`
	// Alias usable alias to be used for the chart
	Alias string `json:"alias,omitempty"`
}

// Metadata for a Chart file. This models the structure of a Chart.yaml file.
type Metadata struct {
	// The name of the chart
	Name string `json:"name,omitempty"`
	// The URL to a relevant project page, git repo, or contact person
	Home string `json:"home,omitempty"`
	// Source is the URL to the source code of this chart
	Sources []string `json:"sources,omitempty"`
	// A SemVer 2 conformant version string of the chart
	Version string `json:"version,omitempty"`
	// A one-sentence description of the chart
	Description string `json:"description,omitempty"`
	// A list of string keywords
	Keywords []string `json:"keywords,omitempty"`
	// A list of name and URL/email address combinations for the maintainer(s)
	Maintainers []*Maintainer `json:"maintainers,omitempty"`
	// The URL to an icon file.
	Icon string `json:"icon,omitempty"`
	// The API Version of this chart.
	APIVersion string `json:"apiVersion,omitempty"`
	// The condition to check to enable chart
	Condition string `json:"condition,omitempty"`
	// The tags to check to enable chart
	Tags string `json:"tags,omitempty"`
	// The version of the application enclosed inside of this chart.
	AppVersion string `json:"appVersion,omitempty"`
	// Whether or not this chart is deprecated
	Deprecated bool `json:"deprecated,omitempty"`
	// Annotations are additional mappings uninterpreted by Helm,
	// made available for inspection by other applications.
	Annotations map[string]string `json:"annotations,omitempty"`
	// KubeVersion is a SemVer constraint specifying the version of Kubernetes required.
	KubeVersion string `json:"kubeVersion,omitempty"`
	// Dependencies are a list of dependencies for a chart.
	Dependencies []*Dependency `json:"dependencies,omitempty"`
	// Specifies the chart type: application or library
	Type string `json:"type,omitempty"`
}

// IsChartDir validates a chart directory.
//
// Checks for a valid Chart.yaml.
func IsChartDir(dirName string) (bool, error) {
	const ChartfileName = "Chart.yaml"

	if fi, err := os.Stat(dirName); err != nil {
		return false, err
	} else if !fi.IsDir() {
		return false, errors.Errorf("%q is not a directory", dirName)
	}

	chartYaml := filepath.Join(dirName, ChartfileName)
	if _, err := os.Stat(chartYaml); os.IsNotExist(err) {
		return false, errors.Errorf("no %s exists in directory %q", ChartfileName, dirName)
	}

	chartYamlContent, err := ioutil.ReadFile(chartYaml)
	if err != nil {
		return false, errors.Errorf("cannot read %s in directory %q", ChartfileName, dirName)
	}

	chartContent := new(Metadata)
	if err := yaml.Unmarshal(chartYamlContent, &chartContent); err != nil {
		return false, err
	}
	if chartContent == nil {
		return false, errors.Errorf("chart metadata (%s) missing", ChartfileName)
	}
	if chartContent.Name == "" {
		return false, errors.Errorf("invalid chart (%s): name must not be empty", ChartfileName)
	}

	return true, nil
}
