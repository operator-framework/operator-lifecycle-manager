package deploy

import (
	"list"

	ocpManifests "operatorframework.io/deploy/schemas/ocp:manifests"
	kubeManifests "operatorframework.io/deploy/schemas/kube:manifests"
)

// config that is commonly used to produce release variants is exposed via tags
// for example: `cue -t debug=true generate`
#config: {
	version: string @tag(version)
	debug: bool @tag(debug,type=bool)
	olm: {
		imageRef: string @tag(olmRef)
		...
	}
	catalog: {
		imageRef: *olm.imageRef | string @tag(catalogRef)
		...
	}
	packageserver: {
		imageRef: *olm.imageRef | string @tag(packageserverRef)
		...
	}
	...
}

kmanifests: kubeManifests.Manifests
kmanifests: _config: kubeManifests.#DefaultKubeConfig & #config

omanifests: ocpManifests.Manifests
omanifests: _config: ocpManifests.#DefaultOCPConfig & #config

files: list.FlattenN([omanifests.files, kmanifests.files], 1)