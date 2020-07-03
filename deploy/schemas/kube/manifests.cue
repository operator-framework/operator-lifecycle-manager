package manifests

import (
    "strings"
)

// this file contains the defintions of the manifest _files_
// the other files contian definitions of the objects that go into the files

// this is the list of representations of manifests that will turn into files
Manifests: { 
    _config: *#DefaultKubeConfig | {...}
    _stream: [#ObjectManifest, #TypeManifest]
    files: [for s in _stream { s & {config: _config}} ]
}

#UpstreamManifestFile: #ManifestFile & {
    _meta: folder: strings.Join(["..", "kube"], "/")
    ...
}

#ObjectManifest: #UpstreamManifestFile & {
    _meta: name: "manifests"
}

#TypeManifest: #UpstreamManifestFile & {
    _meta: name: "crds"
}

#ObjectManifest: _stream: [
    #DeployNamespace, 
    #OperatorNamespace, 
    #OLMServiceAccount, 
    #OLMClusterRole, 
    #OLMClusterRoleBinding,
    #OLMDeployment,
    #CatalogDeployment,
    #EditAggregatedClusterRole,
    #ViewAggregatedClusterRole,
    #GlobalOperatorGroup,
    #OLMOperatorGroup,
    #PackageServerCSV,
    #OperatorHubCatalogSource
]

#TypeManifest: _stream: [for c in crds {c}]