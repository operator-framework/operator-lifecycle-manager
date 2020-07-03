package manifests

import (
    "strings"
)

// this file contains the defintions of the manifest _files_
// the other files contian definitions of the objects that go into the files

Manifests: { 
    _config: *#DefaultOCPConfig | {...}
    _stream:  [
        #TypeManifest,
        #NamespaceManifest, 
        #RbacManifest, 
        #ServiceManifest, 
        #DeploymentManifest, 
        #AggregatedRbacManifest, 
        #OperatorGroupManifest,
        #PackageServerManifest,
        #ClusterOperatorManifest,
        #MonitoringManifest,
        #ImageReferencesManifest,
    ]
    files: [for s in _stream { s & {config: _config}} ]
}

#OCPManifestFile: #ManifestFile & {
    _meta: {
        folder: strings.Join(["..", "manifests"], "/")
        file_prefix: string
    }
}

#OLMFilePrefix: "0000_50_olm_"

#TypeManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "00_"
    _meta: name: "crds"
    _stream: [for c in crds {c}]
}

#NamespaceManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "00_"
    _meta: name: "namespace"
    _stream: [#DeployNamespace, #OperatorNamespace]
}

#RbacManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "01_"
    _meta: name: "serviceaccount"
    _stream: [#OLMServiceAccount, #OLMClusterRole, #OLMClusterRoleBinding]
}

#ServiceManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "02_"
    _meta: name: "services"
    _stream: [#OLMMetricService, #CatalogMetricService]
}

#DeploymentManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "03_"
    _meta: name: "deployments"
    _stream: [#OCPOLMDeployment, #OCPCatalogDeployment]
}

#AggregatedRbacManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "04_"
    _meta: name: "aggregated"
    _stream: [#EditAggregatedClusterRole, #ViewAggregatedClusterRole]
}

#OperatorGroupManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "05_"
    _meta: name: "operatorgroup"
    _stream: [#GlobalOperatorGroup, #OLMOperatorGroup]
}

#PackageServerManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "06_"
    _meta: name: "packageserver"
    _stream: [#PackageServerCSV]
}

#ClusterOperatorManifest: #OCPManifestFile & {
    _meta: file_prefix: #OLMFilePrefix
    _meta: order_prefix: "07_"
    _meta: name: "clusteroperator"
    _stream: [#OLMClusterOperator, #CatalogClusterOperator, #PackageServerClusterOperator]
}

#MonitoringManifest: #OCPManifestFile & {
    // monitoring will install / start after olm
    _meta: file_prefix: "0000_90_olm_"
    _meta: order_prefix: "00_"
    _meta: name: "monitoring"
    _stream: [#MetricsRole, #MetricsRoleBinding, #OLMServiceMonitor, #CatalogServiceMonitor]
}

#ImageReferencesManifest: #OCPManifestFile & {
    _meta: name: "image-references"
    _meta: file_prefix: ""
    _meta: order_prefix: ""
    _meta: suffix: ""
    _stream: [#ImageReferences]
}