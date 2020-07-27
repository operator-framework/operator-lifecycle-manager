package main

import "strings"

const (
	mediatypeLabel = "operators.operatorframework.io.bundle.mediatype.v1"
	manifestsLabel = "operators.operatorframework.io.bundle.manifests.v1"
	metadataLabel = "operators.operatorframework.io.bundle.metadata.v1"
	packageLabel = "operators.operatorframework.io.bundle.package.v1"
	channelsLabel = "operators.operatorframework.io.bundle.channels.v1"
	defaultChannelLabel = "operators.operatorframework.io.bundle.channel.default.v1"
	mediatypeRegistry = "registry+v1"
	defaultImageDir = "temp-image-dir"
)

func generateBundleLabels(packageName, defaultChannel string, channels []string) map[string]string{
	labels := make(map[string]string)
	labels[mediatypeLabel] = mediatypeRegistry
	labels[packageLabel] = packageName
	labels[manifestsLabel] = "manifests/"
	labels[metadataLabel] = "metadata/"
	labels[defaultChannelLabel] = defaultChannel
	if len(defaultChannel) != 0 && len(channels) == 0 {
		channels = []string{defaultChannel}
	}
	labels[channelsLabel] = strings.Join(channels,",")
	return labels
}
