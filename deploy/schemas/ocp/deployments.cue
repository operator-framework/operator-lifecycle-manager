package manifests

#OCPOLMDeployment: #OLMDeployment & {
    _config: {...}
    _containers: "olm-operator": {
        _args: {
            writeStatusName: ["--writeStatusName", "operator-lifecycle-manager"]
            writePackageServerStatusName: ["--writePackageServerStatusName", "operator-lifecycle-manager-packageserver"]
            tlsCert: ["--tls-cert", "/var/run/secrets/serving-cert/tls.crt"]
            tlsKey: ["--tls-key", "/var/run/secrets/serving-cert/tls.key"]
            ...
        }

        // replaced at build time
        _env: {
            RELEASE_VERSION: value: "0.0.1-snapshot"
            ...
        }

        _volumeMounts: "serving-cert": {
            mountPath: "/var/run/secrets/serving-cert"
            ...
        } 
        ...
    }
    _volumes: {
        "serving-cert": secret: secretName: "olm-operator-serving-cert"
        ...
    }
}


#OCPCatalogDeployment: #CatalogDeployment & {
    _config: {...}
    _containers: "catalog-operator": {
        _args: {
            writeStatusName: ["--writeStatusNameCatalog", "operator-lifecycle-manager-catalog"]
            tlsCert: ["--tls-cert", "/var/run/secrets/serving-cert/tls.crt"]
            tlsKey: ["--tls-key", "/var/run/secrets/serving-cert/tls.key"]
            ...
        }

        // replaced at build time
        _env: {
            RELEASE_VERSION: value: "0.0.1-snapshot"
            ...
        }

        _volumeMounts: "serving-cert": {
            mountPath: "/var/run/secrets/serving-cert"
            ...
        } 
        ...
    }
    _volumes: {
        "serving-cert": secret: secretName: "catalog-operator-serving-cert"
        ...
    }
}