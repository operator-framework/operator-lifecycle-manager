package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/operator-framework/operator-registry/pkg/registry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func PackageManifestToAPIPackage(manifest *registry.PackageManifest) *Package {
	channels := []*Channel{}
	for _, c := range manifest.Channels {
		channels = append(channels, PackageChannelToAPIChannel(&c))
	}
	return &Package{
		Name:               manifest.PackageName,
		DefaultChannelName: manifest.DefaultChannelName,
		Channels:           channels,
	}
}

func PackageChannelToAPIChannel(channel *registry.PackageChannel) *Channel {
	return &Channel{
		Name:    channel.Name,
		CsvName: channel.CurrentCSVName,
	}
}

func ChannelEntryToAPIChannelEntry(entry *registry.ChannelEntry) *ChannelEntry {
	return &ChannelEntry{
		PackageName: entry.PackageName,
		ChannelName: entry.ChannelName,
		BundleName:  entry.BundleName,
		Replaces:    entry.Replaces,
	}
}

// Bundle strings are appended json objects, we need to split them apart
// e.g. {"my":"obj"}{"csv":"data"}{"crd":"too"}
func BundleStringToObjectStrings(bundleString string) ([]string, error) {
	objs := []string{}
	dec := json.NewDecoder(strings.NewReader(bundleString))

	for dec.More() {
		var m json.RawMessage
		err := dec.Decode(&m)
		if err != nil {
			return nil, err
		}
		objs = append(objs, string(m))
	}
	return objs, nil
}

func BundleStringToAPIBundle(bundleString string, entry *registry.ChannelEntry) (*Bundle, error) {
	objs, err := BundleStringToObjectStrings(bundleString)
	if err != nil {
		return nil, err
	}
	out := &Bundle{
		Object: objs,
	}
	for _, o := range objs {
		dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(o), 10)
		unst := &unstructured.Unstructured{}
		if err := dec.Decode(unst); err != nil {
			return nil, err
		}
		if unst.GetKind() == "ClusterServiceVersion" {
			out.CsvName = unst.GetName()
			out.CsvJson = o
			break
		}
	}
	if out.CsvName == "" {
		return nil, fmt.Errorf("no csv in bundle")
	}
	out.ChannelName = entry.ChannelName
	out.PackageName = entry.PackageName
	return out, nil
}
