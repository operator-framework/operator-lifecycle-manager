package manifests

// kube-flavored config
#DefaultKubeConfig: #DefaultConfig & {
    deployNamespace: "olm"
    operatorNamespace: "operators"
    ...
}