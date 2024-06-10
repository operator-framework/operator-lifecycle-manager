package semver

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/property"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"
)

func (t Template) Render(ctx context.Context) (*declcfg.DeclarativeConfig, error) {
	var out declcfg.DeclarativeConfig

	sv, err := readFile(t.Data)
	if err != nil {
		return nil, fmt.Errorf("render: unable to read file: %v", err)
	}

	var cfgs []declcfg.DeclarativeConfig

	bundleDict := make(map[string]struct{})
	buildBundleList(&sv.Candidate.Bundles, &bundleDict)
	buildBundleList(&sv.Fast.Bundles, &bundleDict)
	buildBundleList(&sv.Stable.Bundles, &bundleDict)

	for b := range bundleDict {
		r := action.Render{
			AllowedRefMask: action.RefBundleImage,
			Refs:           []string{b},
			Registry:       t.Registry,
		}
		c, err := r.Run(ctx)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, *c)
	}
	out = *combineConfigs(cfgs)

	if len(out.Bundles) == 0 {
		return nil, fmt.Errorf("render: no bundles specified or no bundles could be rendered")
	}

	channelBundleVersions, err := sv.getVersionsFromStandardChannels(&out)
	if err != nil {
		return nil, fmt.Errorf("render: unable to post-process bundle info: %v", err)
	}

	channels := sv.generateChannels(channelBundleVersions)
	out.Channels = channels
	out.Packages[0].DefaultChannel = sv.defaultChannel

	return &out, nil
}

func buildBundleList(bundles *[]semverTemplateBundleEntry, dict *map[string]struct{}) {
	for _, b := range *bundles {
		if _, ok := (*dict)[b.Image]; !ok {
			(*dict)[b.Image] = struct{}{}
		}
	}
}

func readFile(reader io.Reader) (*semverTemplate, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	sv := semverTemplate{}
	if err := yaml.UnmarshalStrict(data, &sv); err != nil {
		return nil, err
	}

	if sv.Schema != schema {
		return nil, fmt.Errorf("readFile: input file has unknown schema, should be %q", schema)
	}

	// if no generate option is selected, default to GenerateMinorChannels
	if !sv.GenerateMajorChannels && !sv.GenerateMinorChannels {
		sv.GenerateMinorChannels = true
	}

	// for default channel preference,
	// if un-set, default to align to the selected generate option
	// if set, error out if we mismatch the two
	switch sv.DefaultChannelTypePreference {
	case defaultStreamType:
		if sv.GenerateMinorChannels {
			sv.DefaultChannelTypePreference = minorStreamType
		} else if sv.GenerateMajorChannels {
			sv.DefaultChannelTypePreference = majorStreamType
		}
	case minorStreamType:
		if !sv.GenerateMinorChannels {
			return nil, fmt.Errorf("schema attribute mismatch: DefaultChannelTypePreference set to 'minor' doesn't make sense if not generating minor-version channels")
		}
	case majorStreamType:
		if !sv.GenerateMajorChannels {
			return nil, fmt.Errorf("schema attribute mismatch: DefaultChannelTypePreference set to 'major' doesn't make sense if not generating major-version channels")
		}
	default:
		return nil, fmt.Errorf("unknown DefaultChannelTypePreference: %q\nValid values are 'major' or 'minor'", sv.DefaultChannelTypePreference)
	}

	return &sv, nil
}

func (sv *semverTemplate) getVersionsFromStandardChannels(cfg *declcfg.DeclarativeConfig) (*bundleVersions, error) {
	versions := bundleVersions{}

	bdm, err := sv.getVersionsFromChannel(sv.Candidate.Bundles, cfg)
	if err != nil {
		return nil, err
	}
	if err = validateVersions(&bdm); err != nil {
		return nil, err
	}
	versions[candidateChannelArchetype] = bdm

	bdm, err = sv.getVersionsFromChannel(sv.Fast.Bundles, cfg)
	if err != nil {
		return nil, err
	}
	if err = validateVersions(&bdm); err != nil {
		return nil, err
	}
	versions[fastChannelArchetype] = bdm

	bdm, err = sv.getVersionsFromChannel(sv.Stable.Bundles, cfg)
	if err != nil {
		return nil, err
	}
	if err = validateVersions(&bdm); err != nil {
		return nil, err
	}
	versions[stableChannelArchetype] = bdm

	return &versions, nil
}

func (sv *semverTemplate) getVersionsFromChannel(semverBundles []semverTemplateBundleEntry, cfg *declcfg.DeclarativeConfig) (map[string]semver.Version, error) {
	entries := make(map[string]semver.Version)

	// we iterate over the channel bundles from the template, to:
	// - identify if any required bundles for the channel are missing/not rendered/otherwise unavailable
	// - maintain the channel-bundle relationship as we map from un-rendered semver template bundles to rendered bundles in `entries` which is accumulated by the caller
	//   in a per-channel structure to which we can safely refer when generating/linking channels
	for _, semverBundle := range semverBundles {
		// test if the bundle specified in the template is present in the successfully-rendered bundles
		index := 0
		for index < len(cfg.Bundles) {
			if cfg.Bundles[index].Image == semverBundle.Image {
				break
			}
			index++
		}
		if index == len(cfg.Bundles) {
			return nil, fmt.Errorf("supplied bundle image name %q not found in rendered bundle images", semverBundle.Image)
		}
		b := cfg.Bundles[index]

		props, err := property.Parse(b.Properties)
		if err != nil {
			return nil, fmt.Errorf("parse properties for bundle %q: %v", b.Name, err)
		}
		if len(props.Packages) != 1 {
			return nil, fmt.Errorf("bundle %q has multiple %q properties, expected exactly 1", b.Name, property.TypePackage)
		}
		v, err := semver.Parse(props.Packages[0].Version)
		if err != nil {
			return nil, fmt.Errorf("bundle %q has invalid version %q: %v", b.Name, props.Packages[0].Version, err)
		}

		// package name detection
		if sv.pkg != "" {
			// if we have a known package name, then ensure all subsequent packages match
			if props.Packages[0].PackageName != sv.pkg {
				return nil, fmt.Errorf("bundle %q does not belong to this package: %q", props.Packages[0].PackageName, sv.pkg)
			}
		} else {
			// else cache the first
			p := newPackage(props.Packages[0].PackageName)
			cfg.Packages = append(cfg.Packages, *p)
			sv.pkg = props.Packages[0].PackageName
		}

		if _, ok := entries[b.Name]; ok {
			return nil, fmt.Errorf("duplicate bundle name %q", b.Name)
		}

		entries[b.Name] = v
	}

	return entries, nil
}

// generates an unlinked channel for each channel as per the input template config (major || minor), then link up the edges of the set of channels so that:
// - for minor version increase, the new edge replaces the previous
// - (for major channels) iterating to a new minor version channel (traversing between Y-streams) creates a 'replaces' edge between the predecessor and successor bundles
// - within the same minor version (Y-stream), the head of the channel should have a 'skips' encompassing all lesser Y.Z versions of the bundle enumerated in the template.
// along the way, uses a highwaterChannel marker to identify the "most stable" channel head to be used as the default channel for the generated package

func (sv *semverTemplate) generateChannels(semverChannels *bundleVersions) []declcfg.Channel {
	outChannels := []declcfg.Channel{}

	// sort the channel archetypes in ascending order so we can traverse the bundles in order of
	// their source channel's priority
	var archetypesByPriority []channelArchetype
	for k := range channelPriorities {
		archetypesByPriority = append(archetypesByPriority, k)
	}
	sort.Sort(byChannelPriority(archetypesByPriority))

	// set to the least-priority channel
	hwc := highwaterChannel{archetype: archetypesByPriority[0], version: semver.Version{Major: 0, Minor: 0}}

	unlinkedChannels := make(map[string]*declcfg.Channel)

	for _, archetype := range archetypesByPriority {
		bundles := (*semverChannels)[archetype]
		// skip channel if empty
		if len(bundles) == 0 {
			continue
		}

		// sort the bundle names according to their semver, so we can walk in ascending order
		bundleNamesByVersion := []string{}
		for b := range bundles {
			bundleNamesByVersion = append(bundleNamesByVersion, b)
		}
		sort.Slice(bundleNamesByVersion, func(i, j int) bool {
			return bundles[bundleNamesByVersion[i]].LT(bundles[bundleNamesByVersion[j]])
		})

		// for each bundle (by version):
		//   for each of Major/Minor setting (since they're independent)
		//     retrieve the existing channel object, or create a channel (by criteria major/minor) if one doesn't exist
		//     add a new edge entry based on the bundle name
		//     save the channel name --> channel archetype mapping
		//     test the channel object for 'more stable' than previous best
		for _, bundleName := range bundleNamesByVersion {
			// a dodge to avoid duplicating channel processing body; accumulate a map of the channels which need creating from the bundle
			// we need to associate by kind so we can partition the resulting entries
			channelNameKeys := make(map[streamType]string)
			if sv.GenerateMajorChannels {
				channelNameKeys[majorStreamType] = channelNameFromMajor(archetype, bundles[bundleName])
			}
			if sv.GenerateMinorChannels {
				channelNameKeys[minorStreamType] = channelNameFromMinor(archetype, bundles[bundleName])
			}

			for cKey, cName := range channelNameKeys {
				ch, ok := unlinkedChannels[cName]
				if !ok {
					ch = newChannel(sv.pkg, cName)

					unlinkedChannels[cName] = ch

					hwcCandidate := highwaterChannel{archetype: archetype, kind: cKey, version: bundles[bundleName], name: cName}
					if hwcCandidate.gt(&hwc, sv.DefaultChannelTypePreference) {
						hwc = hwcCandidate
					}
				}
				ch.Entries = append(ch.Entries, declcfg.ChannelEntry{Name: bundleName})
			}
		}
	}

	// save off the name of the high-water-mark channel for the default for this package
	sv.defaultChannel = hwc.name

	outChannels = append(outChannels, sv.linkChannels(unlinkedChannels, semverChannels)...)

	return outChannels
}

func (sv *semverTemplate) linkChannels(unlinkedChannels map[string]*declcfg.Channel, harvestedVersions *bundleVersions) []declcfg.Channel {
	channels := []declcfg.Channel{}

	// bundle --> version lookup
	bundleVersions := make(map[string]semver.Version)
	for _, vs := range *harvestedVersions {
		for b, v := range vs {
			if _, ok := bundleVersions[b]; !ok {
				bundleVersions[b] = v
			}
		}
	}

	for _, channel := range unlinkedChannels {
		entries := &channel.Entries
		sort.Slice(*entries, func(i, j int) bool {
			return bundleVersions[(*entries)[i].Name].LT(bundleVersions[(*entries)[j].Name])
		})

		// "inchworm" through the sorted entries, iterating curEdge but extending yProbe to the next Y-transition
		// then catch up curEdge to yProbe as 'skips', and repeat until we reach the end of the entries
		// finally, because the inchworm will always fail to pick up the last Y-transition, we test for it and link it up as a 'replaces'
		curEdge, yProbe := 0, 0
		zmaxQueue := ""
		entryCount := len(*entries)

		for curEdge < entryCount {
			for yProbe < entryCount {
				curVersion := bundleVersions[(*entries)[curEdge].Name]
				yProbeVersion := bundleVersions[(*entries)[yProbe].Name]
				if getMinorVersion(yProbeVersion).EQ(getMinorVersion(curVersion)) {
					yProbe += 1
				} else {
					break
				}
			}
			// if yProbe crossed a threshold, the previous entry is the last of the previous Y-stream
			preChangeIndex := yProbe - 1

			if curEdge != yProbe {
				if zmaxQueue != "" {
					// add skips edge to allow skipping over y iterations within an x stream
					(*entries)[preChangeIndex].Skips = append((*entries)[preChangeIndex].Skips, zmaxQueue)
					(*entries)[preChangeIndex].Replaces = zmaxQueue
				}
				zmaxQueue = (*entries)[preChangeIndex].Name
			}
			for curEdge < preChangeIndex {
				// add skips edges to y-1 from z < y
				(*entries)[preChangeIndex].Skips = append((*entries)[preChangeIndex].Skips, (*entries)[curEdge].Name)
				curEdge += 1
			}
			curEdge += 1
			yProbe = curEdge + 1
		}
		// since probe will always fail to pick up a y-change in the last item, test for it
		if entryCount > 1 {
			penultimateEntry := &(*entries)[len(*entries)-2]
			ultimateEntry := &(*entries)[len(*entries)-1]
			penultimateVersion := bundleVersions[penultimateEntry.Name]
			ultimateVersion := bundleVersions[ultimateEntry.Name]
			if ultimateVersion.Minor != penultimateVersion.Minor {
				ultimateEntry.Replaces = penultimateEntry.Name
			}
		}
		channels = append(channels, *channel)
	}

	return channels
}

func channelNameFromMinor(prefix channelArchetype, version semver.Version) string {
	return fmt.Sprintf("%s-v%d.%d", prefix, version.Major, version.Minor)
}

func channelNameFromMajor(prefix channelArchetype, version semver.Version) string {
	return fmt.Sprintf("%s-v%d", prefix, version.Major)
}

func newPackage(name string) *declcfg.Package {
	return &declcfg.Package{
		Schema:         "olm.package",
		Name:           name,
		DefaultChannel: "",
	}
}

func newChannel(pkgName string, chName string) *declcfg.Channel {
	return &declcfg.Channel{
		Schema:  "olm.channel",
		Name:    string(chName),
		Package: pkgName,
		Entries: []declcfg.ChannelEntry{},
	}
}

func combineConfigs(cfgs []declcfg.DeclarativeConfig) *declcfg.DeclarativeConfig {
	out := &declcfg.DeclarativeConfig{}
	for _, in := range cfgs {
		out.Merge(&in)
	}
	return out
}

func getMinorVersion(v semver.Version) semver.Version {
	return semver.Version{
		Major: v.Major,
		Minor: v.Minor,
	}
}

func getMajorVersion(v semver.Version) semver.Version {
	return semver.Version{
		Major: v.Major,
	}
}

func withoutBuildMetadataConflict(versions *map[string]semver.Version) error {
	errs := []error{}

	// using the stringified semver because the semver package generates deterministic representations,
	// and because the semver.Version contains slice fields which make it unsuitable as a map key
	//      stringified-semver.Version ==> incidence count
	seen := make(map[string]int)
	for b := range *versions {
		stripped := stripBuildMetadata((*versions)[b])
		if _, ok := seen[stripped]; !ok {
			seen[stripped] = 1
		} else {
			seen[stripped] = seen[stripped] + 1
			errs = append(errs, fmt.Errorf("bundle version %q cannot be compared to %q", (*versions)[b].String(), stripped))
		}
	}

	if len(errs) != 0 {
		return fmt.Errorf("encountered bundle versions which differ only by build metadata, which cannot be ordered: %v", errors.NewAggregate(errs))
	}

	return nil
}

func validateVersions(versions *map[string]semver.Version) error {
	// short-circuit if empty, since that is not an error
	if len(*versions) == 0 {
		return nil
	}
	return withoutBuildMetadataConflict(versions)
}

// strips out the build metadata from a semver.Version and then stringifies it to make it suitable for collision detection
func stripBuildMetadata(v semver.Version) string {
	v.Build = nil
	return v.String()
}

// prefer (in descending order of preference):
// - higher-rank archetype,
// - semver version,
// - a channel type matching the set preference, or
// - a 'better' (higher value) channel type
func (h *highwaterChannel) gt(ih *highwaterChannel, pref streamType) bool {
	if channelPriorities[h.archetype] != channelPriorities[ih.archetype] {
		return channelPriorities[h.archetype] > channelPriorities[ih.archetype]
	}
	if h.version.NE(ih.version) {
		return h.version.GT(ih.version)
	}
	if h.kind != ih.kind {
		if h.kind == pref {
			return true
		}
		if ih.kind == pref {
			return false
		}
		return h.kind.gt((*ih).kind)
	}
	return false
}

func (t streamType) gt(in streamType) bool {
	return streamTypePriorities[t] > streamTypePriorities[in]
}
