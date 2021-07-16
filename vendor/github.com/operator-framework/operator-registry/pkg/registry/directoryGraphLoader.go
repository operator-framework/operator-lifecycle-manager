package registry

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/blang/semver"
	"github.com/onsi/gomega/gstruct/errors"
)

type DirGraphLoader struct {
	PackageDir           string
	CsvNameAndReplaceMap map[string]csvReplaces
	SortedCSVs           csvs // only contains bundles with version field which will be considered for skip range.
}

type csvReplaces struct {
	replaces  []string
	skipRange semver.Range
}

type csv struct {
	name    string
	version semver.Version
}

type csvs []csv

func (c csvs) Len() int           { return len(c) }
func (c csvs) Less(i, j int) bool { return c[i].version.LT(c[j].version) }
func (c csvs) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

// NewPackageGraphLoaderFromDir takes the root directory of the package in the file system.
func NewPackageGraphLoaderFromDir(packageDir string) (*DirGraphLoader, error) {
	_, err := ioutil.ReadDir(packageDir)
	if err != nil {
		return nil, fmt.Errorf("error reading from %s directory, %v", packageDir, err)
	}

	loader := DirGraphLoader{
		PackageDir: packageDir,
	}

	return &loader, nil
}

// Generate returns Package graph by parsing through package directory assuming all bundles in the package exist.
func (g *DirGraphLoader) Generate() (*Package, error) {
	err := g.loadBundleCsvPathMap()
	if err != nil {
		return nil, fmt.Errorf("error geting CSVs from bundles in the package directory, %v", err)
	}

	pkg, err := g.parsePackageYAMLFile()
	if err != nil {
		return nil, fmt.Errorf("error parsing package.yaml file in the package root directory, %v", err)
	}

	for chName, ch := range pkg.Channels {
		pkg.Channels[chName] = Channel{
			Head:  ch.Head,
			Nodes: *g.getChannelNodes(ch.Head.CsvName),
		}
	}
	return pkg, nil
}

// loadBundleCsvPathMap loads the CsvNameAndReplaceMap and SortedCSVs in the Package Struct.
func (g *DirGraphLoader) loadBundleCsvPathMap() error {
	bundleDirs, err := ioutil.ReadDir(g.PackageDir)
	if err != nil {
		return fmt.Errorf("error reading from %s directory, %v", g.PackageDir, err)
	}
	CsvNameAndReplaceMap := make(map[string]csvReplaces)
	for _, bundlePath := range bundleDirs {
		if bundlePath.IsDir() {
			csvStruct, err := ReadCSVFromBundleDirectory(filepath.Join(g.PackageDir, bundlePath.Name()))
			if err != nil {
				return err
			}

			// Best effort to get Skips, Replace, and SkipRange
			srString := csvStruct.GetSkipRange()
			sr, _ := semver.ParseRange(srString)

			var replaceStrings []string
			rs, err := csvStruct.GetSkips()
			if err == nil && rs != nil {
				replaceStrings = rs
			}

			r, err := csvStruct.GetReplaces()
			if err == nil && r != "" {
				replaceStrings = append(replaceStrings, r)
			}

			CsvNameAndReplaceMap[csvStruct.GetName()] = csvReplaces{
				replaces:  replaceStrings,
				skipRange: sr,
			}

			version, err := csvStruct.GetVersion()
			if err == nil && version != "" {
				v, err := semver.Parse(version)
				if err == nil {
					g.SortedCSVs = append(g.SortedCSVs, csv{name: csvStruct.GetName(), version: v})
				}
			}
		}
	}

	g.CsvNameAndReplaceMap = CsvNameAndReplaceMap
	sort.Sort(g.SortedCSVs)
	return nil
}

// getChannelNodes follows the head of the channel csv through all replaces to fill the nodes until the tail of the
// channel which will not replace anything.
func (g *DirGraphLoader) getChannelNodes(channelHeadCsv string) *map[BundleKey]map[BundleKey]struct{} {
	nodes := make(map[BundleKey]map[BundleKey]struct{})
	remainingCSVsInChannel := make(map[BundleKey]struct{})

	remainingCSVsInChannel[BundleKey{CsvName: channelHeadCsv}] = struct{}{}
	for _, csv := range g.CsvNameAndReplaceMap[channelHeadCsv].replaces {
		remainingCSVsInChannel[BundleKey{CsvName: csv}] = struct{}{}
	}

	// Iterate through remainingCSVsInChannel and add replaces of each encountered CSVs if not already in nodes.
	// Loop only exit after all remaining csvs are visited/deleted.
	for len(remainingCSVsInChannel) > 0 {
		for bk, _ := range remainingCSVsInChannel {
			if _, ok := nodes[BundleKey{CsvName: bk.CsvName}]; !ok {
				nodes[BundleKey{CsvName: bk.CsvName}] = func() map[BundleKey]struct{} {
					subNode := make(map[BundleKey]struct{})
					for _, csv := range g.CsvNameAndReplaceMap[bk.CsvName].replaces {
						subNode[BundleKey{CsvName: csv}] = struct{}{}
					}
					return subNode
				}()
				for _, csv := range g.CsvNameAndReplaceMap[bk.CsvName].replaces {
					if _, ok := nodes[BundleKey{CsvName: csv}]; !ok {
						remainingCSVsInChannel[BundleKey{CsvName: csv}] = struct{}{}
					}
				}
			}
			delete(remainingCSVsInChannel, bk)
		}
	}
	return &nodes
}

// parsePackageYAMLFile parses the *.package.yaml file and fills the information in Package including name,
// defaultchannel, and head of all Channels. It returns parsing error if any.
func (g *DirGraphLoader) parsePackageYAMLFile() (*Package, error) {
	files, err := ioutil.ReadDir(g.PackageDir)
	if err != nil {
		return nil, fmt.Errorf("error reading bundle parent directory, %v", err)
	}

	var ymlFiles []string
	for _, f := range files {
		if !f.IsDir() {
			ymlFiles = append(ymlFiles, f.Name())
		}
	}

	errs := errors.AggregateError{}

	for _, ymlFile := range ymlFiles {
		ymlFile = path.Join(g.PackageDir, ymlFile)
		ymlReader, err := os.Open(ymlFile)
		if err != nil {
			errs = append(errs, fmt.Errorf("error opening %s file, %v", ymlFile, err))
			continue
		}

		pkgManifest, err := DecodePackageManifest(ymlReader)
		if err != nil {
			errs = append(errs, fmt.Errorf("error parsing %s as package.yaml file, %v", ymlFile, err))
			continue
		}
		return convertFromPackageManifest(*pkgManifest), nil
	}

	return nil, fmt.Errorf("valid PackageManifest YAML file not found in %s, %s", g.PackageDir, errs.Error())
}

func convertFromPackageManifest(pkgManifest PackageManifest) *Package {
	pkgChannels := make(map[string]Channel)
	for _, channel := range pkgManifest.Channels {
		pkgChannels[channel.Name] = Channel{
			Head: BundleKey{
				CsvName: channel.CurrentCSVName,
			},
		}
	}

	return &Package{
		Name:           pkgManifest.PackageName,
		DefaultChannel: pkgManifest.GetDefaultChannel(),
		Channels:       pkgChannels,
	}

}
