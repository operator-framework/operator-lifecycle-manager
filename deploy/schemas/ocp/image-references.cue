package manifests

#ImageReferences: {
    _config: {...}
    kind: "ImageStream"
    apiVersion: "image.openshift.io/v1"
    spec: tags: [
    {
        name: "operator-lifecycle-manager"
        from: {
            kind: "DockerImage"
            name: _config.olm.imageRef
        }
    },
    {   name: "operator-registry"
        from: {
            kind: "DockerImage"
            name: _config.registryImage
        }
    }
    ]
}
