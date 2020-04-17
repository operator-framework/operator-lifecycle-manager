package registry

import (
	"fmt"

	"github.com/blang/semver"
)

// BundleGraphLoader generates updated graphs by adding bundles to them, updating
// the graph implicitly via semantic version of each bundle
type BundleGraphLoader struct {
}

// AddBundleToGraph takes a bundle and an existing graph and updates the graph to insert the new bundle
// into each channel it is included in
func (g *BundleGraphLoader) AddBundleToGraph(bundle *Bundle, graph *Package, newDefaultChannel string, skippatch bool) (*Package, error) {
	bundleVersion, err := bundle.Version()
	if err != nil {
		return nil, fmt.Errorf("Unable to extract bundle version from bundle %s, can't insert in semver mode", bundle.BundleImage)
	}

	versionToAdd, err := semver.Make(bundleVersion)
	if err != nil {
		return nil, fmt.Errorf("Bundle version %s is not valid", bundleVersion)
	}

	newBundleKey := BundleKey{
		CsvName:    bundle.Name,
		Version:    versionToAdd.String(),
		BundlePath: bundle.BundleImage,
	}

	// initialize the graph if it started empty
	if graph.Name == "" {
		graph.Name = bundle.Package
	}
	if newDefaultChannel != "" {
		graph.DefaultChannel = newDefaultChannel
	}

	// generate the DAG for each channel the new bundle is being insert into
	for _, channel := range bundle.Channels {
		replaces := make(map[BundleKey]struct{}, 0)

		// If the channel doesn't exist yet, initialize it
		if !graph.HasChannel(channel) {
			// create the channel and add a single node
			newChannelGraph := Channel{
				Head: newBundleKey,
				Nodes: map[BundleKey]map[BundleKey]struct{}{
					newBundleKey: nil,
				},
			}
			if graph.Channels == nil {
				graph.Channels = make(map[string]Channel, 1)
			}
			graph.Channels[channel] = newChannelGraph
			continue
		}

		// find the version(s) it should sit between
		channelGraph := graph.Channels[channel]
		if channelGraph.Nodes == nil {
			channelGraph.Nodes = make(map[BundleKey]map[BundleKey]struct{}, 1)
		}

		lowestAhead := BundleKey{}
		greatestBehind := BundleKey{}
		skipPatchCandidates := []BundleKey{}

		// Iterate over existing nodes and compare the new node's version to find the
		// lowest version above it and highest version below it (to insert between these nodes)
		for node := range channelGraph.Nodes {
			nodeVersion, err := semver.Make(node.Version)
			if err != nil {
				return nil, fmt.Errorf("Unable to parse existing bundle version stored in index %s %s %s",
					node.CsvName, node.Version, node.BundlePath)
			}

			switch comparison := nodeVersion.Compare(versionToAdd); comparison {
			case 0:
				return nil, fmt.Errorf("Bundle version %s already added to index", bundleVersion)
			case 1:
				if lowestAhead.IsEmpty() {
					lowestAhead = node
				} else {
					lowestAheadSemver, _ := semver.Make(lowestAhead.Version)
					if nodeVersion.LT(lowestAheadSemver) {
						lowestAhead = node
					}
				}
			case -1:
				if greatestBehind.IsEmpty() {
					greatestBehind = node
				} else {
					greatestBehindSemver, _ := semver.Make(greatestBehind.Version)
					if nodeVersion.GT(greatestBehindSemver) {
						greatestBehind = node
					}
				}
			}

			// if skippatch mode is enabled, check each node to determine if z-updates should
			// be replaced as well. Keep track of them to delete those nodes from the graph itself,
			// just be aware of them for replacements
			if skippatch {
				if isSkipPatchCandidate(versionToAdd, nodeVersion) {
					skipPatchCandidates = append(skipPatchCandidates, node)
					replaces[node] = struct{}{}
				}
			}
		}

		// If we found a node behind the one we're adding, make the new node replace it
		if !greatestBehind.IsEmpty() {
			replaces[greatestBehind] = struct{}{}
		}

		// If we found a node ahead of the one we're adding, make the lowest to replace
		// the new node. If we didn't find a node semantically ahead, the new node is
		// the new channel head
		if !lowestAhead.IsEmpty() {
			channelGraph.Nodes[lowestAhead] = map[BundleKey]struct{}{
				newBundleKey: struct{}{},
			}
		} else {
			channelGraph.Head = newBundleKey
		}

		if skippatch {
			// Remove the nodes that are now being skipped by a new patch version update
			for _, candidate := range skipPatchCandidates {
				delete(channelGraph.Nodes, candidate)
			}
		}

		// add the node and update the graph
		channelGraph.Nodes[newBundleKey] = replaces
		graph.Channels[channel] = channelGraph
	}

	return graph, nil
}

func isSkipPatchCandidate(version, toCompare semver.Version) bool {
	return (version.Major == toCompare.Major) && (version.Minor == toCompare.Minor) && (version.Patch > toCompare.Patch)
}
